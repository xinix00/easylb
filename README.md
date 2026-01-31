# easylb

Simple hostname-based load balancer for easyrun.

## Features

- Routes traffic based on `urlprefix:` tags from easyrun jobs
- Wildcard support (`*.domain.com`)
- Round-robin load balancing
- Only routes to running tasks

## Usage

```bash
# Route all jobs with urlprefix tags
./easylb -listen :80 -agent http://127.0.0.1:8080

# Only route jobs with specific tag (e.g., lb=easyflor)
./easylb -listen :80 -agent http://127.0.0.1:8080 -tag lb:easyflor
```

Runs on each node, queries the local easyrun agent.

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
  urlprefix: "urlprefix:*.easyflor.eu"
```

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
