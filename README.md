# easylb

Simple hostname-based load balancer for easyrun.

## Features

- Routes traffic based on `urlprefix:` tags from easyrun jobs
- Wildcard support (`*.domain.com`)
- Round-robin load balancing
- Only routes to running tasks

## Usage

```bash
./easylb -listen :80 -leader http://127.0.0.1:8080
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

1. Polls easyrun leader every 5 seconds for task updates
2. Builds route table from jobs with `urlprefix:` tags
3. Only includes tasks in `running` state
4. Round-robins requests across healthy backends
