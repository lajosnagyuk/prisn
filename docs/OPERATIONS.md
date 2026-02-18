# prisn Operations Model

## The Promise

```bash
# Deploy anything
prisn deploy ./my-project/           # Folder of scripts
prisn deploy backup.py               # Single script
prisn deploy https://github.com/...  # Git repo

# See what's happening
prisn status                         # Overview
prisn get deploy                     # List deployments
prisn logs backup                    # View logs

# Manage
prisn scale api 5                    # Scale up
prisn rollback api                   # Undo last deploy
prisn delete api                     # Remove
```

## Deployment Model

### What Gets Deployed

A **deployment** is:
- A folder (or single file) containing scripts
- A runtime (python, node, bash)
- An entrypoint (main script to run)
- Configuration (env vars, resources, schedule)

```
my-project/
├── prisn.toml          # Optional config
├── requirements.txt    # Dependencies
├── main.py            # Entrypoint
├── lib/
│   ├── utils.py
│   └── db.py
└── scripts/
    ├── backup.py
    └── cleanup.sh
```

### How Deployment Works

```bash
prisn deploy ./my-project/
```

1. **Package**: Tar up the folder
2. **Version**: Hash the contents → `v-a1b2c3d4`
3. **Store**: Upload to shared storage (`/deployments/my-project/v-a1b2c3d4/`)
4. **Record**: Create deployment record in DB with version pointer
5. **Prepare**: Build venv if needed (cached by dep hash)
6. **Activate**: Start replicas / register cron

### Versioning

Every deploy creates a new version:

```
/deployments/
└── my-api/
    ├── v-a1b2c3d4/     # Version 1
    │   ├── main.py
    │   └── lib/
    ├── v-e5f6g7h8/     # Version 2 (current)
    │   ├── main.py
    │   └── lib/
    └── metadata.json    # Points to current version
```

```bash
prisn history my-api
VERSION      DEPLOYED            STATUS
v-e5f6g7h8   2024-01-21 10:30   active (current)
v-a1b2c3d4   2024-01-20 14:15   inactive
v-9z8y7x6w   2024-01-19 09:00   inactive

prisn rollback my-api              # Roll back to v-a1b2c3d4
prisn rollback my-api v-9z8y7x6w   # Roll back to specific
```

## Status & Observability

### Quick Status

```bash
$ prisn status

CLUSTER: 3 nodes, all healthy

DEPLOYMENTS: 5 active
  api          service   3/3 ready   :8080
  worker       service   2/2 ready
  backup       cronjob   next: 02:00
  cleanup      cronjob   next: 06:00
  report       job       completed 1h ago

RECENT RUNS: 12 in last hour
  11 completed, 1 running
```

### List Deployments

```bash
$ prisn ls

NAME       TYPE      REPLICAS   VERSION      AGE
api        service   3/3        v-e5f6g7h8   2d
worker     service   2/2        v-a1b2c3d4   5d
backup     cronjob   -          v-f1g2h3i4   1d
cleanup    cronjob   -          v-j5k6l7m8   3d
report     job       0/1        v-n9o0p1q2   1h
```

### Detailed View

```bash
$ prisn get api

Name:       api
Type:       service
Namespace:  default
Version:    v-e5f6g7h8 (deployed 2d ago)
Source:     ./api-server/
Entrypoint: main.py
Runtime:    python3.11
Port:       8080

Replicas:   3/3 ready
  api-0     Running   node-1   12h
  api-1     Running   node-2   12h
  api-2     Running   node-3   12h

Resources:
  CPU:      500m (limit)
  Memory:   512Mi (limit)

Recent Events:
  10:30  Scaled from 2 to 3 replicas
  10:25  Deployment updated to v-e5f6g7h8
  09:00  Health check passed
```

### Logs

```bash
$ prisn logs api              # All replicas, recent
$ prisn logs api -f           # Follow
$ prisn logs api --tail 100   # Last 100 lines
$ prisn logs api-0            # Specific replica
$ prisn logs backup --run latest   # Last cron run
```

## Execution Tracking

Every execution is tracked:

```bash
$ prisn get jobs

ID              DEPLOYMENT   STATUS      STARTED      DURATION
run-a1b2c3d4    backup       completed   02:00:15     45s
run-e5f6g7h8    report       completed   01:30:00     2m30s
run-i9j0k1l2    cleanup      running     06:00:00     -
run-m3n4o5p6    api-0        running     (service)    12h

$ prisn get jobs --limit 10   # Last 10 runs
```

## Shared Storage Layout

When using NFS/shared storage:

```
/prisn/                         # Mount point
├── deployments/                # Versioned source code
│   ├── api/
│   │   ├── v-a1b2c3d4/
│   │   ├── v-e5f6g7h8/
│   │   └── current -> v-e5f6g7h8
│   └── backup/
│       └── ...
├── envs/                       # Cached venvs (by dep hash)
│   ├── python3-a1b2c3d4e5f6/
│   └── python3-g7h8i9j0k1l2/
├── logs/                       # Execution logs
│   ├── api/
│   │   └── 2024-01-21/
│   └── backup/
│       └── 2024-01-21/
└── state/                      # Coordination
    ├── leader.lock             # Who's the leader
    └── nodes/                  # Node heartbeats
        ├── node-1.json
        └── node-2.json
```

## Multi-Node Coordination (NFS approach)

Without Raft, using shared filesystem:

1. **Leader Election**: Simple lock file
   ```go
   // Try to acquire /prisn/state/leader.lock
   // If acquired, you're the leader for 30s
   // Leader handles: scheduling, scaling decisions
   // All nodes handle: execution
   ```

2. **Work Distribution**:
   - Leader writes jobs to `/prisn/state/queue/`
   - Workers poll and claim jobs (atomic rename)
   - Or: use the DB with row-level locking

3. **Health**:
   - Nodes write heartbeat to `/prisn/state/nodes/<id>.json`
   - Leader prunes dead nodes

## Kubernetes Mode

For K8s, the operator handles everything:

```yaml
apiVersion: prisn.io/v1
kind: Script
metadata:
  name: backup
spec:
  source:
    git: https://github.com/myorg/scripts
    path: backup/
  runtime: python3.11
  schedule: "0 2 * * *"
  resources:
    cpu: 500m
    memory: 512Mi
---
apiVersion: prisn.io/v1
kind: Service
metadata:
  name: api
spec:
  source:
    configMap: api-source
  runtime: python3.11
  port: 8080
  replicas: 3
```

Worker pods are generic:
- Have all runtimes installed
- Mount shared storage (PVC)
- Receive work via gRPC from operator
- Or: operator creates Jobs/CronJobs dynamically
