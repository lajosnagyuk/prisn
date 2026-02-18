# prisn Architecture Overview

**Version**: 0.1.0-draft
**Status**: Design Phase

## What is prisn?

prisn is a "just run my shit" serverless platform. One binary. Any language. Kubernetes or bare metal. Sleep at night.

```
prisn run backup.py                     # runs once
prisn run backup.py --cron "0 3 * * *"  # runs at 3am daily
prisn deploy api.py --port 8080         # runs forever, load balanced
prisn deploy worker.js --replicas 3     # runs 3 copies
```

## Core Design Principles

### 1. Single Binary, Zero Dependencies

One `prisn` binary does everything:
- Control plane (stores state, coordinates cluster)
- Worker (executes your code)
- CLI (deploys and manages)
- Kubernetes operator (when running in k8s)

No etcd. No Redis. No container runtime required. Just `prisn`.

### 2. Any Language, Any Platform

First-class support for Python, Bash, and JavaScript. But anything with an interpreter works:

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: lua
spec:
  command: ["/usr/bin/lua"]
  args: ["{script}"]
  extensions: [".lua"]
```

Platform support: Linux (amd64, arm64), FreeBSD, macOS. Windows is possible but not prioritized.

### 3. Non-Adversarial Security

We trust users. But we're not stupid:
- Process isolation (can't see other processes)
- Resource limits (can't OOM the host)
- No self-harm (can't kill prisn, can't nuke other namespaces)
- Namespace separation (your stuff is your stuff)

### 4. Heterogeneous Clusters

A cluster can contain:
- Linux boxes with Python 3.11
- FreeBSD servers with custom Lua builds
- Raspberry Pis with Node.js
- Kubernetes pods

Deployments specify what they need. prisn figures out where to run them.

## Architecture Layers

```
┌─────────────────────────────────────────────────────────────┐
│                         CLI / API                           │
│              (prisn deploy, prisn run, REST/gRPC)           │
├─────────────────────────────────────────────────────────────┤
│                      Control Plane                          │
│         (Raft consensus, state storage, scheduling)         │
├─────────────────────────────────────────────────────────────┤
│                     Worker Nodes                            │
│              (Runner pools, process execution)              │
├─────────────────────────────────────────────────────────────┤
│                   Platform Adapters                         │
│        (Linux cgroups, FreeBSD jails, k8s pods)             │
└─────────────────────────────────────────────────────────────┘
```

## Key Components

### Control Plane

- **Raft Consensus**: hashicorp/raft for leader election and log replication
- **State Store**: Embedded SQLite (replicated via Raft)
- **Scheduler**: Matches deployments to capable nodes
- **API Server**: gRPC (internal) + REST (external)

One node can be a control plane. Three is recommended for HA. All control plane nodes can also be workers.

### Worker

- **Runner Manager**: Maintains warm interpreter pools
- **Executor**: Spawns and monitors processes
- **Resource Controller**: Enforces CPU/memory limits
- **Heartbeat**: Reports health and capacity to control plane

### Runners

A runner is a warm interpreter ready to execute code:

```
┌─────────────────────────────────────────┐
│             Runner: python3             │
│  ┌─────────┐ ┌─────────┐ ┌─────────┐   │
│  │ Process │ │ Process │ │ Process │   │  ← Warm pool
│  │  idle   │ │  idle   │ │ running │   │
│  └─────────┘ └─────────┘ └─────────┘   │
└─────────────────────────────────────────┘
```

When code arrives:
1. Grab an idle process from the pool
2. Inject the script
3. Execute
4. Return process to pool (or kill if tainted)

### Kubernetes Mode

When deployed to Kubernetes, prisn becomes an operator:
- CRDs for Deployment, Job, CronJob, Runner
- Control plane runs as a StatefulSet
- Workers can be prisn pods OR external nodes joining the cluster
- Uses k8s Services/Ingress for networking

### Standalone Mode

When running outside Kubernetes:
- Control plane is 1-3 prisn processes with `--control-plane`
- Workers join with `prisn join <control-plane-addr>`
- Built-in reverse proxy for services
- mDNS for local discovery, DNS for production

## Data Model

```
Cluster
├── Node
│   ├── capabilities (os, arch, runtimes, resources)
│   ├── labels
│   └── status
├── Namespace
│   ├── Runner (interpreter definition)
│   ├── Deployment
│   │   ├── spec (code, runtime, resources, placement)
│   │   ├── scaling (replicas, auto-scale config)
│   │   └── instances
│   ├── Job (one-time execution)
│   ├── CronJob (scheduled execution)
│   └── Secret (env vars, files)
└── Event (audit log)
```

## Execution Lifecycle

```
User: prisn deploy app.py --replicas 2

1. CLI parses command, builds Deployment manifest
2. API receives Deployment, validates
3. Scheduler finds 2 nodes with:
   - Python runtime available
   - Sufficient resources
   - Matching any placement constraints
4. Scheduler assigns instances to nodes
5. Workers receive instance specs
6. Workers:
   a. Acquire runner from pool (or start new one)
   b. Copy script to runner's workspace
   c. Execute script
   d. Monitor process, collect metrics
   e. Report status to control plane
7. Control plane updates Deployment status
8. If a node dies, scheduler reassigns instances
```

## Networking

### Services (Long-running deployments)

```yaml
apiVersion: prisn.io/v1
kind: Deployment
metadata:
  name: api
spec:
  runtime: python3
  source:
    inline: |
      from http.server import HTTPServer, BaseHTTPRequestHandler
      # ... your code
  port: 8080
  replicas: 3
```

prisn provides:
- **Load balancing**: Round-robin across instances
- **Service discovery**: `api.namespace.prisn.local`
- **Health checks**: TCP or HTTP
- **Ingress**: Optional external exposure

### Jobs (One-time execution)

No networking. Runs, outputs to stdout/stderr, exits.

## Storage

Scripts can access:
- **Ephemeral workspace**: `/prisn/workspace` (per-execution, fast)
- **Persistent volume**: `/prisn/data` (per-deployment, optional)
- **Shared volume**: `/prisn/shared/<name>` (across deployments, optional)

Persistent and shared volumes require a storage backend (local disk, NFS, S3-compatible).

## Metrics

Every component emits Prometheus metrics:
- `prisn_deployments_total`
- `prisn_instances_running`
- `prisn_execution_duration_seconds`
- `prisn_runner_pool_size`
- `prisn_node_cpu_usage`
- `prisn_node_memory_usage`

Built-in `/metrics` endpoint on every node.

## What's Next

- [CONTROL-PLANE.md](./CONTROL-PLANE.md) - Clustering and state management
- [EXECUTION-MODEL.md](./EXECUTION-MODEL.md) - How code actually runs
- [RUNNERS.md](./RUNNERS.md) - The runner system
- [CLI-UX.md](./CLI-UX.md) - CLI design philosophy
- [SECURITY.md](./SECURITY.md) - Isolation and guardrails
- [KUBERNETES-MODE.md](./KUBERNETES-MODE.md) - Running prisn with Kubernetes operator
