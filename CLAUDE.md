# easylb

Simple hostname-based load balancer for easyrun.

## Structure

```
cmd/easylb/main.go     - CLI entrypoint
internal/lb/
  route.go             - Route table and wildcard matching
  watcher.go           - Polls easyrun for task updates
  proxy.go             - Reverse proxy with round-robin
```

## Design

- Routes based on `urlprefix:` tags from easyrun jobs
- Only routes to tasks with `state == "running"`
- Health checks are handled by easyrun, not easylb
