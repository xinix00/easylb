# easylb

Simple hostname-based load balancer for easyrun with Prometheus metrics.

## Features

- Routes traffic based on `easylb-urlprefix` tags from easyrun jobs
- Wildcard support (`*.domain.com`)
- Round-robin load balancing
- Only routes to running tasks
- **Prometheus metrics** - Request counts, latency percentiles, status codes
- **Admin endpoints** - Separate port for /health and /metrics (security)

## Usage

```bash
# Start with defaults (HTTP on :80, admin on :9091)
./easylb -listen :80 -admin-listen :9091 -agent http://127.0.0.1:8080

# Only route jobs with specific tag (e.g., lb=easyflor)
./easylb -listen :80 -admin-listen :9091 -agent http://127.0.0.1:8080 -tag lb:easyflor

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
# Instance 1: handles easyflor jobs
./easylb -listen :80 -tag lb:easyflor

# Instance 2: handles staging jobs
./easylb -listen :8080 -tag lb:staging
```

Jobs need the matching tag:
```yaml
tags:
  lb: "easyflor"
  easylb-urlprefix: "*.easyflor.eu"
  easylb-port: "http"  # optional: which port from task.Ports to use
```

## Tags

Add tags to your easyrun job:

```yaml
tags:
  easylb-urlprefix: "app.example.com"
  # or wildcard
  easylb-urlprefix: "*.example.com"
  # optional: select which named port to use
  easylb-port: "http"
```

## Prometheus Metrics

easylb exposes HTTP traffic metrics on the admin port (`-admin-listen`).

### Exposed Metrics

**Request Counters:**
```prometheus
# Total requests per domain/backend/status code
easylb_requests_total{domain="api.example.com",backend="10.0.1.5:8080",code="200"} 15234
easylb_requests_total{domain="api.example.com",backend="10.0.1.5:8080",code="500"} 12
```

**Latency Percentiles:**
```prometheus
# Request duration percentiles (p50, p90, p95, p99)
easylb_request_duration_seconds{domain="api.example.com",backend="10.0.1.5:8080",quantile="0.50"} 0.023
easylb_request_duration_seconds{domain="api.example.com",backend="10.0.1.5:8080",quantile="0.90"} 0.089
easylb_request_duration_seconds{domain="api.example.com",backend="10.0.1.5:8080",quantile="0.95"} 0.145
easylb_request_duration_seconds{domain="api.example.com",backend="10.0.1.5:8080",quantile="0.99"} 0.234

# Sample count and sum (for rate calculations)
easylb_request_duration_seconds_count{domain="api.example.com",backend="10.0.1.5:8080"} 15234
easylb_request_duration_seconds_sum{domain="api.example.com",backend="10.0.1.5:8080"} 350.234
```

### Prometheus Configuration

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'easylb'
    scrape_interval: 10s
    static_configs:
      - targets: ['easylb:9091']  # Admin port, not HTTP traffic port
```

### Alerting Examples

```yaml
# alerts.yml
groups:
  - name: easylb
    rules:
      # High error rate (5xx responses)
      - alert: EasyLBHighErrorRate
        expr: |
          sum(rate(easylb_requests_total{code=~"5.."}[5m])) by (domain)
          /
          sum(rate(easylb_requests_total[5m])) by (domain)
          > 0.05
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Domain {{ $labels.domain }} has high error rate ({{ $value | humanizePercentage }})"

      # High latency (p95 > 500ms)
      - alert: EasyLBHighLatency
        expr: easylb_request_duration_seconds{quantile="0.95"} > 0.5
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Domain {{ $labels.domain }} backend {{ $labels.backend }} has high p95 latency ({{ $value }}s)"

      # No requests (service down?)
      - alert: EasyLBNoTraffic
        expr: rate(easylb_requests_total[5m]) == 0
        for: 10m
        labels:
          severity: info
        annotations:
          summary: "Domain {{ $labels.domain }} has no traffic"
```

### Example Queries

```promql
# Request rate per domain
sum(rate(easylb_requests_total[5m])) by (domain)

# Error rate (4xx + 5xx)
sum(rate(easylb_requests_total{code=~"[45].."}[5m])) by (domain)

# Success rate (2xx)
sum(rate(easylb_requests_total{code=~"2.."}[5m])) by (domain)

# p99 latency across all backends
max(easylb_request_duration_seconds{quantile="0.99"})

# Backend distribution (which backend gets most traffic)
sum(rate(easylb_requests_total[5m])) by (backend)
```

## How it works

1. Connects to local easyrun agent via SSE (`/v1/events`) for real-time updates
2. On state changes, fetches agents, jobs, and tasks from cluster (via agent proxy to leader)
3. Builds route table from jobs with `easylb-urlprefix` tags
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
│  easylb    │────────────│  Backends   │ (tasks)
│  (HTTP)    │ round-robin│ 10.0.1.x:80 │
└─────┬──────┘            └─────────────┘
      │
      │ metrics
      ▼
┌────────────┐ :9091
│  easylb    │────────────► Prometheus
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
