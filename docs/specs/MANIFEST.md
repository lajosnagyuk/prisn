# Manifest Specification

**How to describe what you want to run**

## Overview

prisn manifests define deployments, jobs, cron jobs, runners, and other resources. They use a familiar YAML format inspired by Kubernetes but simplified.

## Quick Reference

```yaml
apiVersion: prisn.io/v1
kind: Deployment | Job | CronJob | Runner | Secret | Namespace | Ingress | NetworkPolicy
metadata:
  name: string           # Required
  namespace: string      # Optional, default: "default"
  labels: {}             # Optional
  annotations: {}        # Optional
spec:
  # ... kind-specific fields
```

## Deployment

A long-running service or worker.

```yaml
apiVersion: prisn.io/v1
kind: Deployment
metadata:
  name: my-api
  namespace: production
  labels:
    app: my-api
    tier: backend
spec:
  # Runtime (required: one of runtime or runner)
  runtime: python3       # Built-in runtime
  # OR
  runner: my-custom      # Custom runner name

  # Source (required: one of inline, file, url)
  source:
    inline: |
      from http.server import HTTPServer, BaseHTTPRequestHandler
      class Handler(BaseHTTPRequestHandler):
          def do_GET(self):
              self.send_response(200)
              self.end_headers()
              self.wfile.write(b"Hello from prisn!")
      HTTPServer(("", 8080), Handler).serve_forever()
    # OR
    file: ./api.py        # Local file path
    # OR
    url: https://example.com/api.py

  # Replicas
  replicas: 3            # Exact count
  # OR
  replicas:
    min: 2
    max: 10
    target:
      cpu: 70            # Auto-scale to maintain 70% CPU

  # Networking
  port: 8080             # Single port
  # OR
  ports:
    - name: http
      port: 8080
      protocol: tcp      # tcp (default) or udp
    - name: metrics
      port: 9090

  # Health checks
  healthCheck:
    http:
      path: /health
      port: 8080
    interval: 10s
    timeout: 5s
    initialDelay: 5s
    failureThreshold: 3
    successThreshold: 1

  # Resources
  resources:
    requests:
      cpu: 100m          # 0.1 cores
      memory: 128Mi
    limits:
      cpu: 1             # 1 core
      memory: 512Mi

  # Environment
  env:
    - name: LOG_LEVEL
      value: info
    - name: DATABASE_URL
      secretRef:
        name: db-secrets
        key: url
    - name: CONFIG
      configRef:
        name: app-config
        key: config.json

  # Volumes
  volumes:
    - name: data
      persistent:
        size: 10Gi
    - name: cache
      ephemeral:
        size: 1Gi
    - name: secrets
      secret:
        secretName: app-secrets
  mounts:
    - name: data
      path: /prisn/data
    - name: cache
      path: /tmp/cache
    - name: secrets
      path: /prisn/secrets
      readOnly: true

  # Placement
  placement:
    nodeSelector:
      os: linux
      arch: amd64
      gpu: "true"
    affinity:
      preferredDuringScheduling:
        - weight: 100
          preference:
            matchLabels:
              zone: us-east-1a
    antiAffinity:
      requiredDuringScheduling:
        - deploymentName: other-app
    topologySpreadConstraints:
      - maxSkew: 1
        topologyKey: zone

  # Isolation
  isolation: 1           # 0-3, default: 1

  # Networking policy
  network:
    enabled: true        # Default
    egress:
      - allow: 10.0.0.0/8
      - deny: 0.0.0.0/0

  # Ingress
  expose:
    type: ingress        # none, internal, nodePort, ingress
    host: api.example.com
    path: /
    tls:
      enabled: true
      secretName: api-tls

  # Lifecycle
  lifecycle:
    preStop:
      exec:
        command: ["python", "-c", "import app; app.shutdown()"]
    terminationGracePeriod: 30s

  # Update strategy
  updateStrategy:
    type: rolling        # rolling, recreate
    maxUnavailable: 1
    maxSurge: 1
```

## Job

A one-time execution.

```yaml
apiVersion: prisn.io/v1
kind: Job
metadata:
  name: backup-20240115
  namespace: default
spec:
  runtime: bash

  source:
    inline: |
      #!/bin/bash
      pg_dump $DATABASE_URL > /prisn/data/backup.sql
      aws s3 cp /prisn/data/backup.sql s3://backups/

  # Timeout
  timeout: 1h

  # Retries
  retries: 3
  retryDelay: 30s

  # Resources
  resources:
    limits:
      cpu: 2
      memory: 4Gi

  # Environment
  env:
    - name: DATABASE_URL
      secretRef:
        name: db-secrets
        key: url

  # Volumes
  volumes:
    - name: data
      ephemeral:
        size: 50Gi
  mounts:
    - name: data
      path: /prisn/data

  # Placement
  placement:
    nodeSelector:
      os: linux
```

## CronJob

A scheduled job.

```yaml
apiVersion: prisn.io/v1
kind: CronJob
metadata:
  name: hourly-backup
  namespace: default
spec:
  # Schedule (required: one of schedule or every)
  schedule: "0 * * * *"    # Standard cron syntax
  # OR
  every: hour              # Human-friendly: minute, hour, day, week
  at: "00"                 # Optional: specific time (for every: hour -> "30" means :30)

  # Timezone
  timezone: America/New_York   # Default: UTC

  # Concurrency
  concurrencyPolicy: forbid    # allow, forbid, replace
  # allow: Multiple jobs can run concurrently
  # forbid: Skip if previous still running
  # replace: Kill previous, start new

  # Job template (same as Job spec)
  jobTemplate:
    spec:
      runtime: bash
      source:
        file: ./backup.sh
      timeout: 30m
      resources:
        limits:
          cpu: 1
          memory: 1Gi

  # History
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 5

  # Suspend
  suspend: false
```

## Runner

A custom runtime definition.

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: lua
  namespace: default       # Or "prisn-system" for cluster-wide
spec:
  # Detection
  detect:
    extensions: [".lua"]
    shebangs: ["#!/usr/bin/env lua", "#!/usr/bin/lua"]

  # Execution
  command: ["lua"]
  args: ["{script}"]
  env:
    LUA_PATH: "/prisn/lib/?.lua"

  # Script injection method
  injection:
    method: file           # file, stdin, arg

  # Pool
  pool:
    minIdle: 0
    maxIdle: 5
    maxAge: 1h
    recycleAfter: 100

  # Capabilities
  capabilities:
    network: true
    filesystem: true
    subprocess: true

  # Requirements
  requirements:
    commands: ["lua"]
    os: ["linux", "freebsd"]

  # Default resources
  resources:
    requests:
      cpu: 50m
      memory: 64Mi
    limits:
      cpu: 500m
      memory: 256Mi
```

## Secret

Sensitive data storage.

```yaml
apiVersion: prisn.io/v1
kind: Secret
metadata:
  name: db-secrets
  namespace: default
spec:
  type: opaque           # opaque (default), tls

  # For opaque type
  data:
    url: cG9zdGdyZXM6Ly8uLi4=        # Base64 encoded
    password: aHVudGVyMg==

  # OR stringData (plaintext, will be encoded)
  stringData:
    url: postgres://user:pass@host/db
    password: hunter2

  # For TLS type
  # data:
  #   tls.crt: <base64 cert>
  #   tls.key: <base64 key>
```

## Namespace

Logical grouping and isolation.

```yaml
apiVersion: prisn.io/v1
kind: Namespace
metadata:
  name: production
  labels:
    environment: production
spec:
  # Resource quotas
  quota:
    maxDeployments: 100
    maxJobs: 1000
    maxCronJobs: 50
    maxReplicas: 500
    resources:
      cpu: 100         # Total cores
      memory: 256Gi    # Total memory

  # Default resource limits for deployments
  defaults:
    resources:
      requests:
        cpu: 100m
        memory: 128Mi
      limits:
        cpu: 1
        memory: 512Mi
```

## Ingress

External access configuration.

```yaml
apiVersion: prisn.io/v1
kind: Ingress
metadata:
  name: api-ingress
  namespace: default
spec:
  rules:
    - host: api.example.com
      paths:
        - path: /
          pathType: prefix     # prefix, exact
          deployment: api
          port: 8080
        - path: /admin
          deployment: admin
          port: 3000
    - host: "*.example.com"    # Wildcard
      paths:
        - path: /
          deployment: default-app
          port: 8080

  tls:
    - hosts:
        - api.example.com
        - "*.example.com"
      secretName: example-tls

  # Or auto-generate with ACME
  # tls:
  #   - hosts: [api.example.com]
  #     acme: true
```

## NetworkPolicy

Network access rules.

```yaml
apiVersion: prisn.io/v1
kind: NetworkPolicy
metadata:
  name: db-access
  namespace: default
spec:
  # Target deployment(s)
  target:
    deployment: postgres
    # OR
    # selector:
    #   matchLabels:
    #     tier: database

  # Ingress rules (who can connect IN)
  ingress:
    - from:
        - deployment: api
        - deployment: worker
        - namespace: monitoring
      ports:
        - port: 5432
          protocol: tcp

  # Egress rules (who can connect OUT)
  egress:
    - to:
        - cidr: 10.0.0.0/8
      ports:
        - port: 443
```

## Field Reference

### Duration

Durations use Go-style format:
- `5s` - 5 seconds
- `10m` - 10 minutes
- `1h` - 1 hour
- `1h30m` - 1 hour 30 minutes

### Resources

CPU:
- `100m` - 100 millicores (0.1 cores)
- `1` - 1 core
- `2.5` - 2.5 cores

Memory:
- `128Mi` - 128 mebibytes
- `1Gi` - 1 gibibyte
- `512M` - 512 megabytes (decimal)

### Cron Syntax

Standard 5-field cron:
```
┌───────────── minute (0-59)
│ ┌───────────── hour (0-23)
│ │ ┌───────────── day of month (1-31)
│ │ │ ┌───────────── month (1-12)
│ │ │ │ ┌───────────── day of week (0-6, Sun=0)
│ │ │ │ │
* * * * *
```

Special values:
- `*` - any value
- `*/5` - every 5
- `1,15` - 1 and 15
- `1-5` - 1 through 5

Examples:
- `0 * * * *` - every hour at :00
- `*/15 * * * *` - every 15 minutes
- `0 3 * * *` - daily at 3:00 AM
- `0 9 * * 1` - Mondays at 9:00 AM
- `0 0 1 * *` - first of month at midnight

## Scaling Syntax in Manifests

The replicas field supports the same syntax as the CLI:

```yaml
# Absolute
replicas: 5

# Range (for auto-scaling)
replicas:
  min: 2
  max: 10
  target:
    cpu: 70

# Semantic (at creation time)
replicas: min     # Resolved to 1
replicas: max     # Resolved based on resources
replicas: enough  # Only valid at runtime with existing instances
```

## Applying Manifests

```bash
# Apply single file
prisn apply -f deployment.yaml

# Apply multiple files
prisn apply -f deployment.yaml -f service.yaml

# Apply directory
prisn apply -f ./manifests/

# Apply from stdin
cat deployment.yaml | prisn apply -f -

# Dry run (show what would change)
prisn apply -f deployment.yaml --dry-run

# Diff (show changes)
prisn diff -f deployment.yaml

# Delete from manifest
prisn delete -f deployment.yaml

# Get as manifest
prisn get deploy my-app -o yaml > my-app.yaml
```

## Multi-Document YAML

Multiple resources in one file:

```yaml
apiVersion: prisn.io/v1
kind: Secret
metadata:
  name: db-secrets
spec:
  stringData:
    url: postgres://...
---
apiVersion: prisn.io/v1
kind: Deployment
metadata:
  name: api
spec:
  runtime: python3
  source:
    file: ./api.py
  env:
    - name: DATABASE_URL
      secretRef:
        name: db-secrets
        key: url
```

## Templating (Optional)

prisn supports simple variable substitution:

```yaml
apiVersion: prisn.io/v1
kind: Deployment
metadata:
  name: api
  namespace: ${NAMESPACE}
spec:
  replicas: ${REPLICAS:-3}    # Default: 3
  env:
    - name: VERSION
      value: ${VERSION}
```

```bash
NAMESPACE=production REPLICAS=5 VERSION=1.2.3 prisn apply -f deployment.yaml
```

For complex templating, use external tools (envsubst, ytt, helm template).
