package lb

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Task represents an easyrun task
type Task struct {
	ID      string         `json:"id"`
	JobName string         `json:"job_name"`
	State   string         `json:"state"`
	Ports   map[string]int `json:"ports"`
}

// Job represents an easyrun job
type Job struct {
	ID   string            `json:"id"`
	Name string            `json:"name"`
	Tags map[string]string `json:"tags"`
}

// Agent represents an easyrun agent
type Agent struct {
	ID       string `json:"id"`
	Endpoint string `json:"endpoint"`
}

// Watcher watches local easyrun agent for task changes
type Watcher struct {
	agentAddr  string
	routeTable *RouteTable
	client     *http.Client
	interval   time.Duration
	tagFilter  string // e.g., "lb:easyflor" means only jobs with tag lb=easyflor

	// Cached state for incremental updates
	agentHosts map[string]string              // agentID → hostname
	jobs       map[string]*Job                // jobName → job
	relevant   map[string]struct{}            // job names that contribute routes
	tasks      map[string]map[string][]*Task  // jobName → agentID → tasks
}

// NewWatcher creates a new watcher
func NewWatcher(agentAddr string, routeTable *RouteTable, tagFilter string) *Watcher {
	return &Watcher{
		agentAddr:  agentAddr,
		routeTable: routeTable,
		client:     &http.Client{Timeout: 10 * time.Second},
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

	resp, err := (&http.Client{}).Do(req) // no timeout — SSE is long-lived
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &url.Error{Op: "GET", URL: w.agentAddr + "/v1/events", Err: http.ErrNotSupported}
	}

	w.sync() // seed routes on (re)connect
	log.Printf("SSE connected to %s/v1/events", w.agentAddr)

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
				job := parseJobFromData(line)
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
	agents, err := w.fetchAgents()
	if err != nil {
		log.Printf("Failed to fetch agents: %v", err)
		return
	}

	jobs, err := w.fetchJobs()
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
	w.jobs = make(map[string]*Job)
	w.relevant = make(map[string]struct{})
	for _, job := range jobs {
		w.jobs[job.Name] = job
		if w.jobMatchesFilter(job) && jobHasURLPrefix(job) {
			w.relevant[job.Name] = struct{}{}
		}
	}

	// Fetch tasks only for relevant jobs (much leaner than fetching all tasks)
	w.tasks = make(map[string]map[string][]*Task)
	for jobName := range w.relevant {
		w.syncJob(jobName)
	}

	w.buildRoutes()
}

// syncJob fetches only the tasks for a single job and rebuilds routes from cache.
func (w *Watcher) syncJob(jobName string) {
	resp, err := w.client.Get(fmt.Sprintf("%s/v1/jobs/%s/status", w.agentAddr, jobName))
	if err != nil {
		log.Printf("Failed to fetch job status for %s: %v", jobName, err)
		w.sync()
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		w.sync()
		return
	}

	var status struct {
		Agents       []*Agent            `json:"agents"`
		TasksByAgent map[string][]*Task  `json:"tasks_by_agent"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
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
	w.tasks[jobName] = make(map[string][]*Task)
	for agentID, tasks := range status.TasksByAgent {
		w.tasks[jobName][agentID] = tasks
	}

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
	log.Printf("Updated routes: %d patterns", len(routes))
}

// parseJobFromData extracts the job name from an SSE data line.
func parseJobFromData(line string) string {
	data := strings.TrimPrefix(line, "data:")
	data = strings.TrimSpace(data)
	var ev struct {
		Job string `json:"job"`
	}
	json.Unmarshal([]byte(data), &ev)
	return ev.Job
}

// taskPort returns the named port (from job's "port" tag) or first available.
func taskPort(task *Task, portName string) int {
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

// jobHasURLPrefix checks if a job has the easylb-urlprefix tag.
func jobHasURLPrefix(job *Job) bool {
	return job.Tags["easylb-urlprefix"] != ""
}

func (w *Watcher) fetchAgents() ([]*Agent, error) {
	resp, err := w.client.Get(w.agentAddr + "/v1/agents")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var agents []*Agent
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		return nil, err
	}
	return agents, nil
}

func (w *Watcher) fetchJobs() ([]*Job, error) {
	resp, err := w.client.Get(w.agentAddr + "/v1/jobs")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var jobs []*Job
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return nil, err
	}
	return jobs, nil
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
func (w *Watcher) jobMatchesFilter(job *Job) bool {
	if w.tagFilter == "" {
		return true // no filter = match all
	}
	key, value := parseTagFilter(w.tagFilter)
	return job.Tags[key] == value
}
