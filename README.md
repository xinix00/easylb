# hoplb

Simple hostname-based load balancer for hop with Prometheus metrics.

## Features

- Routes traffic based on `hoplb-urlprefix` tags from hop jobs
- Wildcard support (`*.domain.com`)
- Round-robin load balancing
- Only routes to running tasks
- **Prometheus metrics** - Request counts, latency percentiles, status codes
- **Admin endpoints** - Separate port for /health and /metrics (security)

## Usage

```bash
# Start with defaults (HTTP on :80, admin on :9091)
./hoplb -listen :80 -admin-listen :9091 -agent http://127.0.0.1:8080

# Only route jobs with specific tag (e.g., lb=haas)
./hoplb -listen :80 -admin-listen :9091 -agent http://127.0.0.1:8080 -tag lb:haas

# Check health
curl http://localhost:9091/health

# Check metrics
curl http://localhost:9091/metrics
```

**Port Strategy:**
- `-listen` - HTTP traffic (user requests)
- `-admin-listen` - Admin endpoints (/health, /metrics) - **keep internal only!**

### Tag Filtering

Use `-tag key:value` to filter which jobs this instance handles:

```bash
# Instance 1: handles haas jobs
./hoplb -listen :80 -tag lb:haas

# Instance 2: handles staging jobs
./hoplb -listen :8080 -tag lb:staging
```

Jobs need the matching tag:
```yaml
tags:
  lb: "haas"
  hoplb-urlprefix: "*.haas.eu"
  hoplb-port: "http"  # optional: which port from task.Ports to use
```

## Tags

Add tags to your hop job:

```yaml
tags:
  hoplb-urlprefix: "app.example.com"
  # or wildcard
  hoplb-urlprefix: "*.example.com"
  # optional: select which named port to use
  hoplb-port: "http"
```

## Prometheus Metrics

hoplb exposes HTTP traffic metrics on the admin port (`-admin-listen`).

### Exposed Metrics

**Request Counters:**
```prometheus
# Total requests per domain/backend/status code
hoplb_requests_total{domain="api.example.com",backend="10.0.1.5:8080",code="200"} 15234
hoplb_requests_total{domain="api.example.com",backend="10.0.1.5:8080",code="500"} 12
```

**Latency Percentiles:**
```prometheus
# Request duration percentiles (p50, p90, p95, p99)
hoplb_request_duration_seconds{domain="api.example.com",backend="10.0.1.5:8080",quantile="0.50"} 0.023
hoplb_request_duration_seconds{domain="api.example.com",backend="10.0.1.5:8080",quantile="0.90"} 0.089
hoplb_request_duration_seconds{domain="api.example.com",backend="10.0.1.5:8080",quantile="0.95"} 0.145
hoplb_request_duration_seconds{domain="api.example.com",backend="10.0.1.5:8080",quantile="0.99"} 0.234

# Sample count and sum (for rate calculations)
hoplb_request_duration_seconds_count{domain="api.example.com",backend="10.0.1.5:8080"} 15234
hoplb_request_duration_seconds_sum{domain="api.example.com",backend="10.0.1.5:8080"} 350.234
```

### Prometheus Configuration

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'hoplb'
    scrape_interval: 10s
    static_configs:
      - targets: ['hoplb:9091']  # Admin port, not HTTP traffic port
```

### Alerting Examples

```yaml
# alerts.yml
groups:
  - name: hoplb
    rules:
      # High error rate (5xx responses)
      - alert: HopLBHighErrorRate
        expr: |
          sum(rate(hoplb_requests_total{code=~"5.."}[5m])) by (domain)
          /
          sum(rate(hoplb_requests_total[5m])) by (domain)
          > 0.05
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Domain {{ $labels.domain }} has high error rate ({{ $value | humanizePercentage }})"

      # High latency (p95 > 500ms)
      - alert: HopLBHighLatency
        expr: hoplb_request_duration_seconds{quantile="0.95"} > 0.5
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Domain {{ $labels.domain }} backend {{ $labels.backend }} has high p95 latency ({{ $value }}s)"

      # No requests (service down?)
      - alert: HopLBNoTraffic
        expr: rate(hoplb_requests_total[5m]) == 0
        for: 10m
        labels:
          severity: info
        annotations:
          summary: "Domain {{ $labels.domain }} has no traffic"
```

### Example Queries

```promql
# Request rate per domain
sum(rate(hoplb_requests_total[5m])) by (domain)

# Error rate (4xx + 5xx)
sum(rate(hoplb_requests_total{code=~"[45].."}[5m])) by (domain)

# Success rate (2xx)
sum(rate(hoplb_requests_total{code=~"2.."}[5m])) by (domain)

# p99 latency across all backends
max(hoplb_request_duration_seconds{quantile="0.99"})

# Backend distribution (which backend gets most traffic)
sum(rate(hoplb_requests_total[5m])) by (backend)
```

## How it works

1. Connects to local hop agent via SSE (`/v1/events`) for real-time updates
2. On state changes, fetches agents, jobs, and tasks from cluster (via agent proxy to leader)
3. Builds route table from jobs with `hoplb-urlprefix` tags
4. Only includes tasks in `running` state
5. Round-robins requests across backends
6. **Tracks every request** - domain, backend, status code, latency
7. **Exposes metrics** on admin port in Prometheus format

## Architecture

```
User Request
     │
     ▼
┌────────────┐ :80        ┌─────────────┐
│  hoplb    │────────────│  Backends   │ (tasks)
│  (HTTP)    │ round-robin│ 10.0.1.x:80 │
└─────┬──────┘            └─────────────┘
      │
      │ metrics
      ▼
┌────────────┐ :9091
│  hoplb    │────────────► Prometheus
│  (admin)   │ /metrics
└────────────┘
```

**2 HTTP Servers:**
1. **Traffic server** (`:80`) - Proxies user requests, tracks metrics
2. **Admin server** (`:9091`) - Exposes /health and /metrics

**Why separate ports?**
- Security: admin endpoints should be internal-only
- Isolation: metrics scraping doesn't affect user traffic
- Firewall: block admin port from public internet
