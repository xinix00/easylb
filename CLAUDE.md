# easylb

Simple hostname-based load balancer for easyrun.

## Structure

```
cmd/easylb/main.go     - CLI entrypoint
internal/lb/
  route.go             - Route table and wildcard matching
  watcher.go           - Connects to easyrun via SSE, syncs routes on events
  proxy.go             - Reverse proxy with round-robin
```

## Design

- Routes based on `easylb-urlprefix` tags from easyrun jobs
- Real-time route updates via SSE (`/v1/events`) from local easyrun agent
- Only routes to tasks with `state == "running"`
- Health checks are handled by easyrun, not easylb
