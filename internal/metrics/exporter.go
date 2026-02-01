package metrics

import (
	"fmt"
	"net/http"
	"strings"
)

// Exporter exposes metrics in Prometheus format
type Exporter struct {
	metrics *Metrics
}

// NewExporter creates a new Prometheus exporter
func NewExporter(m *Metrics) *Exporter {
	return &Exporter{metrics: m}
}

// ServeHTTP handles /metrics requests
func (e *Exporter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	var b strings.Builder

	// Request counters per domain/backend/status
	b.WriteString("# HELP easylb_requests_total Total HTTP requests\n")
	b.WriteString("# TYPE easylb_requests_total counter\n")

	counts := e.metrics.RequestCounts()
	for _, domain := range e.metrics.AllDomains() {
		backends := counts[domain]
		for _, backend := range e.metrics.AllBackends(domain) {
			codes := backends[backend]
			for code, count := range codes {
				fmt.Fprintf(&b, "easylb_requests_total{domain=%q,backend=%q,code=\"%d\"} %d\n",
					domain, backend, code, count)
			}
		}
	}
	b.WriteString("\n")

	// Request duration percentiles
	b.WriteString("# HELP easylb_request_duration_seconds Request duration percentiles\n")
	b.WriteString("# TYPE easylb_request_duration_seconds summary\n")

	percentiles := []float64{0.5, 0.9, 0.95, 0.99}
	for _, domain := range e.metrics.AllDomains() {
		for _, backend := range e.metrics.AllBackends(domain) {
			sampleCount := e.metrics.SampleCount(domain, backend)
			if sampleCount == 0 {
				continue
			}

			for _, p := range percentiles {
				value := e.metrics.Percentile(domain, backend, p)
				fmt.Fprintf(&b, "easylb_request_duration_seconds{domain=%q,backend=%q,quantile=\"%.2f\"} %.6f\n",
					domain, backend, p, value)
			}

			// Add _count and _sum for summary type
			fmt.Fprintf(&b, "easylb_request_duration_seconds_count{domain=%q,backend=%q} %d\n",
				domain, backend, sampleCount)

			// Calculate sum from percentile (approximate)
			sum := e.metrics.Percentile(domain, backend, 0.5) * float64(sampleCount)
			fmt.Fprintf(&b, "easylb_request_duration_seconds_sum{domain=%q,backend=%q} %.6f\n",
				domain, backend, sum)
		}
	}

	w.Write([]byte(b.String()))
}
