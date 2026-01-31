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

// Task represents an easyrun task
type Task struct {
	ID      string         `json:"id"`
	JobID   string         `json:"job_id"`
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
}

// NewWatcher creates a new watcher
func NewWatcher(agentAddr string, routeTable *RouteTable) *Watcher {
	return &Watcher{
		agentAddr:  agentAddr,
		routeTable: routeTable,
		client:     &http.Client{Timeout: 10 * time.Second},
		interval:   5 * time.Second,
	}
}

// Run starts watching for changes
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

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

	status, err := w.fetchClusterStatus()
	if err != nil {
		log.Printf("Failed to fetch cluster status: %v", err)
		return
	}

	agentByID := make(map[string]*Agent)
	for _, agent := range agents {
		agentByID[agent.ID] = agent
	}

	jobByID := make(map[string]*Job)
	for _, job := range jobs {
		jobByID[job.ID] = job
	}

	routes := make(map[string]*Route)

	for agentID, tasks := range status {
		agent := agentByID[agentID]
		if agent == nil {
			continue
		}

		agentHost := extractHost(agent.Endpoint)
		if agentHost == "" {
			continue
		}

		for _, task := range tasks {
			if task.State != "running" {
				continue
			}

			job := jobByID[task.JobID]
			if job == nil {
				continue
			}

			for _, tagValue := range job.Tags {
				pattern, ok := ParseURLPrefix(tagValue)
				if !ok {
					continue
				}

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
	resp, err := w.client.Get(w.agentAddr + "/agents")
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
	resp, err := w.client.Get(w.agentAddr + "/jobs")
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
	resp, err := w.client.Get(w.agentAddr + "/status")
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

func extractHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
