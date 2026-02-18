# Configuration

**prisn.toml reference**

## Overview

`prisn.toml` defines your deployments, jobs, and configuration in one file. It's simpler than YAML manifests and perfect for most use cases.

```toml
# prisn.toml

[service.api]
source = "api.py"
port = 8080
replicas = 3

[job.backup]
source = "backup.sh"
schedule = "0 3 * * *"
```

```bash
prisn apply           # Deploy everything
prisn apply --dry-run # Preview changes
```

## Services

Long-running processes (web servers, workers, etc.)

```toml
[service.api]
source = "api.py"           # Script to run (required)
port = 8080                 # Port to expose
replicas = 3                # Number of instances

# Optional fields
runtime = "python3"         # Override auto-detection
args = ["--workers", "4"]   # Arguments to script
timeout = "30s"             # Request timeout
```

### Environment Variables

```toml
[service.api]
source = "api.py"
port = 8080

[service.api.env]
LOG_LEVEL = "info"
DEBUG = "false"
API_URL = "https://api.example.com"
```

### Secret References

Don't put secrets in prisn.toml. Reference them:

```toml
[service.api]
source = "api.py"

[service.api.env]
LOG_LEVEL = "info"
# Reference a secret
DATABASE_URL = { secret = "db-creds", key = "url" }
API_KEY = { secret = "api-keys", key = "production" }
```

Create secrets separately:

```bash
prisn secret create db-creds --from-literal url=postgres://user:pass@host/db
prisn secret create api-keys --from-literal production=sk_live_xxx
```

### Environment Files

Load from a `.env` file:

```toml
[service.api]
source = "api.py"
env_file = ".env"          # Load from file

[service.api.env]
# These override .env values
LOG_LEVEL = "debug"
```

**.env**
```
DATABASE_URL=postgres://localhost/dev
REDIS_URL=redis://localhost:6379
API_KEY=dev-key-123
```

### Resources

```toml
[service.api]
source = "api.py"

[service.api.resources]
cpu = "500m"               # 0.5 cores (or "1", "2")
memory = "256Mi"           # (or "1Gi", "512M")
```

### Health Checks

```toml
[service.api]
source = "api.py"
port = 8080

[service.api.health]
path = "/health"           # HTTP health check
interval = "10s"
timeout = "5s"
```

### Auto-scaling

```toml
[service.api]
source = "api.py"

[service.api.scale]
min = 2
max = 10
target_cpu = 70            # Scale up when CPU > 70%
```

## Jobs

One-time or scheduled executions.

### Cron Jobs

```toml
[job.backup]
source = "backup.sh"
schedule = "0 3 * * *"     # Cron syntax: daily at 3am

[job.backup.env]
S3_BUCKET = "my-backups"
```

### Human-Readable Schedules

```toml
[job.hourly-report]
source = "report.py"
every = "hour"             # minute, hour, day, week

[job.daily-cleanup]
source = "cleanup.sh"
every = "day"
at = "03:00"               # Time (for day/week)
```

### Job Options

```toml
[job.backup]
source = "backup.sh"
schedule = "0 * * * *"
timeout = "30m"            # Max run time
retries = 3                # Retry on failure
concurrency = "forbid"     # Don't overlap: allow, forbid, replace
```

## Multiple Environments

Use different config files:

```
prisn.toml          # Development defaults
prisn.prod.toml     # Production overrides
.env                # Dev secrets (gitignored)
.env.prod           # Prod secrets (gitignored)
```

**prisn.prod.toml**
```toml
[service.api]
source = "api.py"
port = 8080
replicas = 5
env_file = ".env.prod"

[service.api.env]
LOG_LEVEL = "warn"
```

```bash
prisn apply -f prisn.prod.toml
```

## Variable Substitution

Use environment variables in config:

```toml
[service.api]
source = "api.py"
replicas = "${REPLICAS:-2}"    # Default: 2

[service.api.env]
VERSION = "${GIT_SHA}"
```

```bash
REPLICAS=5 GIT_SHA=$(git rev-parse HEAD) prisn apply
```

## Full Example

```toml
# prisn.toml - Complete application

[service.api]
source = "src/api.py"
port = 8080
replicas = 3
env_file = ".env"

[service.api.env]
LOG_LEVEL = "info"
DATABASE_URL = { secret = "db-creds", key = "url" }

[service.api.resources]
cpu = "500m"
memory = "512Mi"

[service.api.health]
path = "/health"
interval = "10s"

[service.api.scale]
min = 2
max = 10
target_cpu = 70

[service.worker]
source = "src/worker.py"
replicas = 5

[service.worker.env]
QUEUE = "high-priority"
REDIS_URL = { secret = "redis-creds", key = "url" }

[job.backup]
source = "scripts/backup.sh"
schedule = "0 3 * * *"
timeout = "1h"

[job.backup.env]
S3_BUCKET = "backups"

[job.cleanup]
source = "scripts/cleanup.py"
every = "day"
at = "04:00"
```

## YAML Alternative

For complex deployments, use YAML manifests. See [MANIFEST.md](specs/MANIFEST.md).

```bash
# TOML (simple)
prisn apply

# YAML (complex)
prisn apply -f deployment.yaml
```

## Validation

Check your config without applying:

```bash
prisn check                  # Validate prisn.toml
prisn apply --dry-run        # Preview what would change
prisn diff                   # Show diff from current state
```
