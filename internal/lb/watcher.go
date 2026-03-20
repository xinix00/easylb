package lb

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"easylib"
)

// Watcher watches local easyrun agent for task changes
type Watcher struct {
	agentAddr  string
	routeTable *RouteTable
	client     *easylib.Client
	interval   time.Duration
	tagFilter  string // e.g., "lb:easyflor" means only jobs with tag lb=easyflor

	// Cached state for incremental updates
	agentHosts map[string]string                        // agentID → hostname
	jobs       map[string]*easylib.Job                  // jobName → job
	relevant   map[string]struct{}                      // job names that contribute routes
	tasks      map[string]map[string][]*easylib.Task    // jobName → agentID → tasks
}

// NewWatcher creates a new watcher
func NewWatcher(agentAddr string, routeTable *RouteTable, tagFilter string, apiKey string) *Watcher {
	return &Watcher{
		agentAddr:  agentAddr,
		routeTable: routeTable,
		client:     easylib.NewClient(apiKey),
		interval:   5 * time.Second,
		tagFilter:  tagFilter,
	}
}

// Run connects to the SSE event stream and syncs on state changes.
// On disconnect, retries after a short delay.
func (w *Watcher) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		err := w.watchSSE(ctx)
		if ctx.Err() != nil {
			return
		}
		log.Printf("SSE disconnected: %v, reconnecting in %v", err, w.interval)
		select {
		case <-time.After(w.interval):
		case <-ctx.Done():
			return
		}
	}
}

// watchSSE connects to the agent's SSE stream and triggers debounced
// sync on any state change. Does a full sync on connect to seed routes.
func (w *Watcher) watchSSE(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", w.agentAddr+"/v1/events", nil)
	if err != nil {
		return err
	}
	if w.client.APIKey != "" {
		req.Header.Set("X-API-Key", w.client.APIKey)
	}

	resp, err := (&http.Client{}).Do(req) // no timeout — SSE is long-lived
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &url.Error{Op: "GET", URL: w.agentAddr + "/v1/events", Err: http.ErrNotSupported}
	}

	log.Printf("SSE connected to %s/v1/events, seeding routes", w.agentAddr)
	w.sync() // seed routes — SSE stream already open, events buffered

	lineCh := make(chan string)
	go func() {
		defer close(lineCh)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
	}()

	debounce := time.NewTimer(0)
	if !debounce.Stop() {
		<-debounce.C
	}
	pending := make(map[string]struct{})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lineCh:
			if !ok {
				return nil // stream closed
			}
			if strings.HasPrefix(line, "data:") {
				job := easylib.ParseJobFromSSE(line)
				if job == "" {
					continue
				}
				_, isRelevant := w.relevant[job]
				_, isKnown := w.jobs[job]
				if !isRelevant && isKnown {
					continue // known irrelevant job, skip
				}
				if len(pending) == 0 {
					debounce.Reset(500 * time.Millisecond)
				}
				pending[job] = struct{}{}
			}
		case <-debounce.C:
			needFullSync := false
			for job := range pending {
				if _, known := w.jobs[job]; !known {
					needFullSync = true
					break
				}
			}
			if needFullSync {
				w.sync() // new job appeared, need full sync
			} else {
				for job := range pending {
					w.syncJob(job)
				}
			}
			pending = make(map[string]struct{})
		}
	}
}

// sync does a full fetch of agents, jobs, and per-job task status for relevant jobs.
func (w *Watcher) sync() {
	agents, err := easylib.Fetch[[]easylib.Agent](w.client, w.agentAddr+"/v1/agents")
	if err != nil {
		log.Printf("Failed to fetch agents: %v", err)
		return
	}

	jobs, err := easylib.Fetch[[]easylib.Job](w.client, w.agentAddr+"/v1/jobs")
	if err != nil {
		log.Printf("Failed to fetch jobs: %v", err)
		return
	}

	// Cache agents
	w.agentHosts = make(map[string]string)
	for _, agent := range agents {
		if h := extractHost(agent.Endpoint); h != "" {
			w.agentHosts[agent.ID] = h
		}
	}

	// Cache jobs + determine relevant
	w.jobs = make(map[string]*easylib.Job)
	w.relevant = make(map[string]struct{})
	for i := range jobs {
		w.jobs[jobs[i].Name] = &jobs[i]
		if w.jobMatchesFilter(&jobs[i]) && jobs[i].Tags["easylb-urlprefix"] != "" {
			w.relevant[jobs[i].Name] = struct{}{}
		}
	}

	// Fetch tasks only for relevant jobs
	w.tasks = make(map[string]map[string][]*easylib.Task)
	for jobName := range w.relevant {
		w.syncJob(jobName)
	}

	w.buildRoutes()
}

// syncJob fetches only the tasks for a single job and rebuilds routes from cache.
func (w *Watcher) syncJob(jobName string) {
	status, err := easylib.Fetch[struct {
		Agents       []easylib.Agent            `json:"agents"`
		TasksByAgent map[string][]*easylib.Task  `json:"tasks_by_agent"`
	}](w.client, fmt.Sprintf("%s/v1/jobs/%s/status", w.agentAddr, jobName))
	if err != nil {
		log.Printf("Failed to fetch job status for %s: %v", jobName, err)
		w.sync()
		return
	}

	// Update agent hosts from response
	for _, agent := range status.Agents {
		if h := extractHost(agent.Endpoint); h != "" {
			w.agentHosts[agent.ID] = h
		}
	}

	// Replace cached tasks for this job
	w.tasks[jobName] = status.TasksByAgent
	w.buildRoutes()
}

// buildRoutes rebuilds the route table from cached state.
func (w *Watcher) buildRoutes() {
	routes := make(map[string]*Route, len(w.relevant))

	for jobName := range w.relevant {
		job := w.jobs[jobName]
		if job == nil {
			continue
		}

		pattern := job.Tags["easylb-urlprefix"]
		if pattern == "" {
			continue
		}

		portName := job.Tags["easylb-port"]
		for agentID, tasks := range w.tasks[jobName] {
			host := w.agentHosts[agentID]
			if host == "" {
				continue
			}

			for _, task := range tasks {
				if task.State != "running" {
					continue
				}

				port := taskPort(task, portName)
				if port == 0 {
					continue
				}

				backend := &Backend{
					Address: host + ":" + strconv.Itoa(port),
					Healthy: true,
				}

				if route, ok := routes[pattern]; ok {
					route.Backends = append(route.Backends, backend)
				} else {
					routes[pattern] = &Route{
						Pattern:  pattern,
						Backends: []*Backend{backend},
					}
				}
			}
		}
	}

	w.routeTable.Update(routes)
	for pattern, route := range routes {
		log.Printf("Route %s: %d backends", pattern, len(route.Backends))
		for _, b := range route.Backends {
			log.Printf("  backend: %s", b.Address)
		}
	}
	log.Printf("Updated routes: %d patterns", len(routes))
}

// taskPort returns the named port (from job's "port" tag) or first available.
func taskPort(task *easylib.Task, portName string) int {
	if portName != "" {
		if p, ok := task.Ports[portName]; ok {
			return p
		}
	}
	for _, p := range task.Ports {
		return p
	}
	return 0
}

func extractHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// parseTagFilter parses "key:value" into key and value
func parseTagFilter(filter string) (string, string) {
	for i, c := range filter {
		if c == ':' {
			return filter[:i], filter[i+1:]
		}
	}
	return filter, ""
}

// jobMatchesFilter checks if job has the required tag
func (w *Watcher) jobMatchesFilter(job *easylib.Job) bool {
	if w.tagFilter == "" {
		return true // no filter = match all
	}
	key, value := parseTagFilter(w.tagFilter)
	return job.Tags[key] == value
}
