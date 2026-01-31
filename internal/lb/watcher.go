package lb

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"
)

// Task represents an easyrun task (subset of fields we need)
type Task struct {
	ID      string         `json:"id"`
	JobID   string         `json:"job_id"`
	JobName string         `json:"job_name"`
	State   string         `json:"state"`
	Ports   map[string]int `json:"ports"`
}

// Job represents an easyrun job (subset of fields we need)
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

// Watcher watches easyrun for task changes and updates the route table
type Watcher struct {
	leaderAddr string
	routeTable *RouteTable
	client     *http.Client
	interval   time.Duration
}

// NewWatcher creates a new watcher
func NewWatcher(leaderAddr string, routeTable *RouteTable) *Watcher {
	return &Watcher{
		leaderAddr: leaderAddr,
		routeTable: routeTable,
		client:     &http.Client{Timeout: 10 * time.Second},
		interval:   5 * time.Second,
	}
}

// Run starts watching for changes
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Initial sync
	w.sync()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sync()
		}
	}
}

func (w *Watcher) sync() {
	// Fetch agents (for endpoints)
	agents, err := w.fetchAgents()
	if err != nil {
		log.Printf("Failed to fetch agents: %v", err)
		return
	}

	// Fetch jobs (for tags)
	jobs, err := w.fetchJobs()
	if err != nil {
		log.Printf("Failed to fetch jobs: %v", err)
		return
	}

	// Fetch cluster status (tasks per agent)
	status, err := w.fetchClusterStatus()
	if err != nil {
		log.Printf("Failed to fetch cluster status: %v", err)
		return
	}

	// Build lookups
	agentByID := make(map[string]*Agent)
	for _, agent := range agents {
		agentByID[agent.ID] = agent
	}

	jobByID := make(map[string]*Job)
	for _, job := range jobs {
		jobByID[job.ID] = job
	}

	// Build routes from running tasks with urlprefix tags
	routes := make(map[string]*Route)

	for agentID, tasks := range status {
		agent := agentByID[agentID]
		if agent == nil {
			continue
		}

		// Extract host from agent endpoint (e.g., "http://192.168.1.10:8080" -> "192.168.1.10")
		agentHost := extractHost(agent.Endpoint)
		if agentHost == "" {
			continue
		}

		for _, task := range tasks {
			// Only route to running tasks
			if task.State != "running" {
				continue
			}

			job := jobByID[task.JobID]
			if job == nil {
				continue
			}

			// Check for urlprefix tag
			for _, tagValue := range job.Tags {
				pattern, ok := ParseURLPrefix(tagValue)
				if !ok {
					continue
				}

				// Get the port (use "http" port or first available)
				port := 0
				if p, ok := task.Ports["http"]; ok {
					port = p
				} else {
					for _, p := range task.Ports {
						port = p
						break
					}
				}

				if port == 0 {
					continue
				}

				// Add backend to route using agent's IP
				backend := &Backend{
					Address: fmt.Sprintf("%s:%d", agentHost, port),
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

func (w *Watcher) fetchAgents() ([]*Agent, error) {
	resp, err := w.client.Get(w.leaderAddr + "/agents")
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
	resp, err := w.client.Get(w.leaderAddr + "/jobs")
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

func (w *Watcher) fetchClusterStatus() (map[string][]*Task, error) {
	resp, err := w.client.Get(w.leaderAddr + "/status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var status map[string][]*Task
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, err
	}
	return status, nil
}

// extractHost extracts the hostname/IP from a URL (e.g., "http://192.168.1.10:8080" -> "192.168.1.10")
func extractHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
