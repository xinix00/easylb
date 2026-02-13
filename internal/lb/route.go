package lb

import (
	"strings"
	"sync"
	"sync/atomic"
)

// Backend represents a single backend server
type Backend struct {
	Address string // host:port
	Healthy bool
}

// Route represents a routing rule
type Route struct {
	Pattern  string     // e.g., "*.easyflor.eu" or "api.easyflor.eu"
	Backends []*Backend
	next     uint64 // round-robin counter
}

// RouteTable manages all routes
type RouteTable struct {
	mu     sync.RWMutex
	routes map[string]*Route // pattern -> route
}

// NewRouteTable creates a new route table
func NewRouteTable() *RouteTable {
	return &RouteTable{
		routes: make(map[string]*Route),
	}
}

// Update replaces all routes atomically
func (rt *RouteTable) Update(routes map[string]*Route) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.routes = routes
}

// Match finds a route for the given host
func (rt *RouteTable) Match(host string) *Route {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	// Strip port if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Exact match first
	if route, ok := rt.routes[host]; ok {
		return route
	}

	// Wildcard match (*.domain.com)
	for pattern, route := range rt.routes {
		if matchWildcard(pattern, host) {
			return route
		}
	}

	return nil
}

// GetHealthyBackend returns a healthy backend using round-robin
func (r *Route) GetHealthyBackend() *Backend {
	n := len(r.Backends)
	if n == 0 {
		return nil
	}

	// Round-robin through backends, skipping unhealthy ones
	start := atomic.AddUint64(&r.next, 1)
	for i := 0; i < n; i++ {
		idx := (int(start) + i) % n
		if r.Backends[idx].Healthy {
			return r.Backends[idx]
		}
	}
	return nil
}

// matchWildcard checks if host matches pattern like *.domain.com
func matchWildcard(pattern, host string) bool {
	if !strings.HasPrefix(pattern, "*.") {
		return false
	}

	suffix := pattern[1:] // ".domain.com"
	return strings.HasSuffix(host, suffix) && strings.Count(host, ".") == strings.Count(suffix, ".")
}

