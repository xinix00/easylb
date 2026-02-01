package metrics

import (
	"sort"
	"sync"
	"time"
)

// Metrics tracks HTTP request statistics for Prometheus
type Metrics struct {
	mu sync.RWMutex

	// Request counters: domain -> backend -> status code -> count
	requests map[string]map[string]map[int]int64

	// Latency samples: domain -> backend -> []duration (for percentiles)
	latencySamples map[string]map[string][]float64

	// Max samples to keep per domain/backend (rolling window)
	maxSamples int
}

// New creates a new metrics collector
func New() *Metrics {
	return &Metrics{
		requests:       make(map[string]map[string]map[int]int64),
		latencySamples: make(map[string]map[string][]float64),
		maxSamples:     10000, // Keep last 10k samples for percentiles
	}
}

// RecordRequest records a request with its status code and duration
func (m *Metrics) RecordRequest(domain, backend string, statusCode int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Initialize nested maps if needed
	if m.requests[domain] == nil {
		m.requests[domain] = make(map[string]map[int]int64)
	}
	if m.requests[domain][backend] == nil {
		m.requests[domain][backend] = make(map[int]int64)
	}

	// Increment counter
	m.requests[domain][backend][statusCode]++

	// Record latency sample
	if m.latencySamples[domain] == nil {
		m.latencySamples[domain] = make(map[string][]float64)
	}

	samples := m.latencySamples[domain][backend]
	samples = append(samples, duration.Seconds())

	// Keep only recent samples (rolling window)
	if len(samples) > m.maxSamples {
		samples = samples[len(samples)-m.maxSamples:]
	}

	m.latencySamples[domain][backend] = samples
}

// RequestCounts returns all request counts
// Returns: domain -> backend -> status code -> count
func (m *Metrics) RequestCounts() map[string]map[string]map[int]int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Deep copy to avoid race conditions
	result := make(map[string]map[string]map[int]int64)
	for domain, backends := range m.requests {
		result[domain] = make(map[string]map[int]int64)
		for backend, codes := range backends {
			result[domain][backend] = make(map[int]int64)
			for code, count := range codes {
				result[domain][backend][code] = count
			}
		}
	}
	return result
}

// Percentile calculates the given percentile (0.0-1.0) from samples
func (m *Metrics) Percentile(domain, backend string, p float64) float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	samples, ok := m.latencySamples[domain][backend]
	if !ok || len(samples) == 0 {
		return 0
	}

	// Copy and sort
	sorted := make([]float64, len(samples))
	copy(sorted, samples)
	sort.Float64s(sorted)

	// Calculate percentile index
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}

	return sorted[idx]
}

// AllDomains returns all tracked domains
func (m *Metrics) AllDomains() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	domains := make([]string, 0, len(m.requests))
	for domain := range m.requests {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	return domains
}

// AllBackends returns all backends for a domain
func (m *Metrics) AllBackends(domain string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	backends := make([]string, 0)
	if domainMap, ok := m.requests[domain]; ok {
		for backend := range domainMap {
			backends = append(backends, backend)
		}
	}
	sort.Strings(backends)
	return backends
}

// SampleCount returns the number of latency samples for a domain/backend
func (m *Metrics) SampleCount(domain, backend string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if samples, ok := m.latencySamples[domain][backend]; ok {
		return len(samples)
	}
	return 0
}
