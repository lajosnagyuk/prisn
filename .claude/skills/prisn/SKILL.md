---
name: prisn
description: >
  Use this skill when the user asks questions about the prisn serverless platform,
  including "can prisn do X", "how do I do Y with prisn", "is X possible",
  "how would I deploy", "how does prisn handle", or any question about prisn capabilities,
  architecture, CLI commands, Kubernetes operator, configuration, or deployment modes.
  Also use when the user asks about running scripts, deploying services, scheduling jobs,
  managing secrets, RBAC tokens, scaling, or clustering with prisn.
user_invocable: true
version: 0.1.0
---

# prisn - "Just run my shit"

You are an expert on the prisn serverless platform. Answer questions about what prisn can do, how to accomplish tasks, and guide users through setup and usage. Be direct, practical, and give concrete examples.

## What prisn Is

prisn is a single-binary serverless platform that runs scripts anywhere. Dependencies are automatically detected and installed. No containers, no Kubernetes knowledge, no distributed systems expertise needed.

```
prisn run script.py              # Deps auto-installed from requirements.txt
prisn deploy api.py --port 8080  # Long-running service
prisn scale api 5                # Scale to 5 replicas
```

## Deployment Modes

prisn runs in 4 modes. Always recommend the simplest mode that fits the user's needs.

### 1. Local Mode (default)
Just run scripts. No server needed.
```bash
prisn run script.py
prisn deploy api.py --port 8080
```

### 2. Server Mode (teams, on-prem)
Central server, multiple clients.
```bash
# Server
prisn server --bind :7331

# Client
prisn context add prod --server prod.example.com:7331
prisn -c prod deploy api.py --port 8080
```

### 3. Clustered Mode (HA, production)
Raft consensus across 3+ nodes.
```bash
# Bootstrap
prisn server --cluster --raft-addr :7332 --bootstrap
# Join
prisn server --cluster --raft-addr :7332 --peers node1:7332
```

### 4. Kubernetes Operator
CRDs for PrisnApp, PrisnJob, PrisnCronJob.
```bash
# Install via Helm
helm install prisn-operator ./charts/prisn-operator \
  --namespace prisn-system --create-namespace

# Or via kustomize
kubectl apply -k operator/config/default
```

## CLI Commands Reference

### Running code
| Command | Purpose | Key flags |
|---------|---------|-----------|
| `prisn run <script>` | Execute once and exit | `-e`, `--timeout`, `--detach`, `--runtime` |
| `prisn dev <script>` | Watch and re-run on changes | `-e`, `--clear`, `--debounce` |
| `prisn deploy <script>` | Deploy as service/job/cron | `--port`, `--replicas`, `--schedule`, `--name`, `--wait` |
| `prisn apply -f <file>` | Deploy from config file | `--dry-run`, `--prune` |

### Managing deployments
| Command | Purpose | Key flags |
|---------|---------|-----------|
| `prisn scale <name> <n>` | Change replicas | Supports: `5`, `+2`, `-1`, `x1.5`, `150%`, `auto` |
| `prisn delete <type> <name>` | Delete a resource | `--force`, `-f <file>` |
| `prisn status [name]` | Show deployment status | `-o json` |
| `prisn logs <name>` | View logs | `-f`, `--tail`, `--since` |
| `prisn describe <type> <name>` | Detailed resource info | `-o yaml` |
| `prisn exec <name> -- <cmd>` | Run command in deployment | `-it` for interactive |
| `prisn diff` | Compare local vs deployed | `-f <file>` |
| `prisn top` | Live resource usage TUI | `--sort`, `--interval` |
| `prisn history <name>` | Deployment revision history | |
| `prisn rollback <name>` | Revert to previous version | `--to-revision`, `--dry-run` |
| `prisn trigger <name>` | Manually trigger a job | `--wait`, `-e` |

### Secrets and environment
| Command | Purpose |
|---------|---------|
| `prisn env set <name> KEY=val` | Set env vars on deployment |
| `prisn env get <name>` | Show env vars |
| `prisn secret create <name>` | Create secret (`--from-literal`, `--from-file`, `--from-env-file`) |
| `prisn secret list` | List secrets |
| `prisn secret get <name>` | Show secret keys (add `--show-values` for values) |
| `prisn secret delete <name>` | Delete secret |

### RBAC tokens
| Command | Purpose |
|---------|---------|
| `prisn token create` | Create token (`--name`, `--role`, `--namespace`, `--expires-in`) |
| `prisn token list` | List tokens |
| `prisn token revoke <id>` | Revoke token |
| `prisn token delete <id>` | Delete token |

Roles: `admin` (full access), `developer` (manage deploys/jobs/secrets), `viewer` (read-only)

### Contexts and infrastructure
| Command | Purpose |
|---------|---------|
| `prisn context add <name>` | Add server context (`--server`, `--token`) |
| `prisn context use <name>` | Switch context |
| `prisn server` | Start prisn server (`--addr`, `--data-dir`, `--api-key`) |
| `prisn cluster status` | Show cluster status |
| `prisn runtime list` | List available runtimes |
| `prisn doctor` | Diagnose environment |
| `prisn check` | Validate config files |
| `prisn init` | Generate prisn.toml from detected scripts |

## Configuration (prisn.toml)

```toml
[service.api]
source = "api.py"
port = 8080
replicas = 3
runtime = "python3"        # Optional, auto-detected
env_file = ".env"          # Load .env file

[service.api.env]
LOG_LEVEL = "info"
DATABASE_URL = { secret = "db-creds", key = "url" }  # Secret reference

[service.api.resources]
cpu = "500m"
memory = "256Mi"

[service.api.health]
path = "/health"
interval = "10s"

[service.api.scale]
min = 2
max = 10
target_cpu = 70

[job.backup]
source = "backup.sh"
schedule = "0 3 * * *"     # Cron syntax
timeout = "1h"
retries = 3
concurrency = "forbid"     # allow, forbid, replace

[job.backup.env]
S3_BUCKET = "my-backups"
```

## Kubernetes CRDs

### PrisnApp (long-running services)
```yaml
apiVersion: prisn.io/v1alpha1
kind: PrisnApp
metadata:
  name: my-api
spec:
  runtime: python3          # python, python3, node, bash
  replicas: 3
  port: 8080
  source:
    inline: |               # Or: configMapRef, secretRef, git
      from http.server import HTTPServer, BaseHTTPRequestHandler
      # ... your code
    entrypoint: main.py     # When using configMapRef/git
  env:
    - name: LOG_LEVEL
      value: info
    - name: API_KEY
      valueFrom:
        secretKeyRef:
          name: api-secrets
          key: key
  resources:
    cpu: "500m"
    memory: "256Mi"
  healthCheck:
    path: /health
  storage:
    - name: data
      mountPath: /data
      size: 10Gi
  ingress:
    enabled: true
    host: api.example.com
    tls: true
  suspend: false            # Pause without deleting
```

### PrisnJob (one-shot execution)
```yaml
apiVersion: prisn.io/v1alpha1
kind: PrisnJob
metadata:
  name: migrate-db
spec:
  runtime: python3
  source:
    configMap: migration-scripts
    key: migrate.py
  timeout: 30m
  backoffLimit: 3
  ttlSecondsAfterFinished: 3600
```

### PrisnCronJob (scheduled)
```yaml
apiVersion: prisn.io/v1alpha1
kind: PrisnCronJob
metadata:
  name: daily-backup
spec:
  schedule: "0 3 * * *"
  concurrencyPolicy: Forbid
  jobTemplate:
    runtime: bash
    source:
      inline: |
        pg_dump $DATABASE_URL | gzip > backup.sql.gz
    timeout: 1h
```

### Source options for K8s
- **inline**: Code directly in the YAML
- **configMapRef**: Code stored in a ConfigMap (multiple files supported)
- **secretRef**: Private code in a Secret
- **git**: Clone from repository (supports private repos via secretRef)

## Architecture

- **Dependency detection**: Scans requirements.txt, pyproject.toml, package.json, or imports
- **Environment setup**: Creates isolated venv/node_modules, cached by dependency hash
- **Execution**: Fork-based isolation (no warm pool state corruption)
  - fork() before each execution
  - Child runs script, exits (all state gone)
  - Parent remains warm for next fork
- **Isolation levels**: None (L0), Basic/ulimits (L1), Namespaces/jails (L2), Seccomp/paranoid (L3)
- **Platforms**: Linux (cgroups+namespaces), FreeBSD (jails+rctl), macOS (sandbox-exec), Generic (ulimits)
- **State**: Embedded SQLite, replicated via Raft in cluster mode
- **Runtimes**: Python, Node.js, Bash built-in. Custom runtimes via config.

## Kubernetes Script Runner Pattern

prisn works as a "just get it done" layer in K8s. Instead of building bespoke operators or init containers for glue logic, write Python/Bash and let prisn run it:

```yaml
# In a helmfile workflow
releases:
  - name: prisn-operator
    chart: ./charts/prisn-operator

  - name: bootstrap
    chart: ./charts/bootstrap    # Contains PrisnJob CRs
    needs: [prisn-operator]

  - name: platform
    chart: ./charts/platform
    needs: [bootstrap]
```

Store scripts in ConfigMaps, point PrisnJob at them. No custom Docker images needed.

## How to Answer Questions

When the user asks "can prisn do X":
1. Check if it maps to an existing command or feature above
2. If yes, show the exact command/config with a concrete example
3. If partially, explain what works today and what's deferred
4. If no, say so clearly

When the user asks "how would I do X":
1. Determine the simplest deployment mode that fits
2. Show the CLI command or config needed
3. If K8s, show the CRD YAML
4. Include any relevant flags or options

**Deferred/not-yet-implemented features** (be honest about these):
- Service mesh / inter-service networking in non-K8s modes
- Windows support (stubs exist, not functional)
- FreeBSD support (stubs exist, not functional)
- Event-driven triggers
- Built-in CI/CD pipeline
- Multi-region clustering
- GPU workloads

**Working and tested:**
- Local script execution with dependency detection
- Server mode with HTTP API
- RBAC with tokens (admin/developer/viewer)
- Secrets and environment management
- Kubernetes operator (PrisnApp, PrisnJob, PrisnCronJob)
- Helm chart for operator deployment
- Raft clustering
- Fork-based execution with isolation
- Cron scheduling
- Scale operations (absolute, relative, multiplicative, percentage, auto)
- ConfigMap and inline source in K8s
- Persistent storage, ingress, health checks in K8s
- Suspend/resume for PrisnApps

For detailed documentation, refer to:
- `docs/CLI-REFERENCE.md` - All commands and flags
- `docs/CONFIGURATION.md` - prisn.toml format
- `docs/SECRETS.md` - Environment variables and secrets
- `docs/RBAC.md` - Token management and access control
- `docs/KUBERNETES.md` - K8s operator deployment
- `docs/architecture/OVERVIEW.md` - System architecture
- `docs/architecture/EXECUTION-MODEL.md` - How code runs
