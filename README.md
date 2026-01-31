# easylb

Simple hostname-based load balancer for easyrun.

## Features

- Routes traffic based on `urlprefix:` tags from easyrun jobs
- Wildcard support (`*.domain.com`)
- Round-robin load balancing
- Only routes to running tasks

## Usage

```bash
./easylb -listen :80 -agent http://127.0.0.1:8080
```

Runs on each node, queries the local easyrun agent.

## Tags

Add tags to your easyrun job:

```yaml
tags:
  urlprefix: "urlprefix:app.example.com"
  # or wildcard
  urlprefix: "urlprefix:*.example.com"
```

## How it works

1. Queries local easyrun agent every 5 seconds
2. Fetches all agents, jobs, and tasks from cluster
3. Builds route table from jobs with `urlprefix:` tags
4. Only includes tasks in `running` state
5. Round-robins requests across backends
