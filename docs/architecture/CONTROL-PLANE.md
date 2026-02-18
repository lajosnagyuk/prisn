# Control Plane Architecture

**How prisn coordinates a cluster**

## Overview

The control plane is the brain of prisn. It:
- Stores all cluster state (deployments, jobs, nodes)
- Elects a leader for coordination
- Schedules workloads to nodes
- Serves the API

Every prisn binary can be a control plane node. Run 1 for development, 3 for production.

## Consensus: Raft

We use Raft for distributed consensus. Specifically, hashicorp/raft with BoltDB backend.

### Why Raft?

- Battle-tested (HashiCorp uses it in Consul, Nomad, Vault)
- Pure Go, no external dependencies
- Understandable (important for debugging)
- Supports dynamic membership (add/remove nodes)

### What Goes Through Raft?

All state mutations:
- Creating/updating/deleting deployments, jobs, runners
- Node join/leave
- Scheduling decisions
- Secret storage

Reads can be:
- **Consistent**: Route to leader, always fresh (default for writes)
- **Stale**: Read from any node, may be slightly behind (default for reads)

### Raft Configuration

```go
config := raft.DefaultConfig()
config.LocalID = raft.ServerID(nodeID)

// Tuning for prisn's use case
config.HeartbeatTimeout = 1000 * time.Millisecond
config.ElectionTimeout = 1000 * time.Millisecond
config.CommitTimeout = 50 * time.Millisecond
config.MaxAppendEntries = 64
config.SnapshotInterval = 120 * time.Second
config.SnapshotThreshold = 8192
```

## State Storage

### Finite State Machine (FSM)

Raft replicates a log of commands. The FSM applies those commands to build state.

```go
type FSM struct {
    db *sql.DB  // SQLite
}

func (f *FSM) Apply(log *raft.Log) interface{} {
    var cmd Command
    json.Unmarshal(log.Data, &cmd)

    switch cmd.Type {
    case "CreateDeployment":
        return f.createDeployment(cmd.Payload)
    case "UpdateDeployment":
        return f.updateDeployment(cmd.Payload)
    case "DeleteDeployment":
        return f.deleteDeployment(cmd.Payload)
    case "CreateJob":
        return f.createJob(cmd.Payload)
    // ... etc
    }
}
```

### Why SQLite?

- Single file, easy to snapshot
- SQL queries for complex lookups
- Excellent Go support (modernc.org/sqlite - pure Go, no CGO)
- Fast enough for our scale

### Schema

```sql
-- Nodes
CREATE TABLE nodes (
    id TEXT PRIMARY KEY,
    address TEXT NOT NULL,
    capabilities TEXT NOT NULL,  -- JSON
    labels TEXT,                 -- JSON
    status TEXT NOT NULL,
    last_heartbeat INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);

-- Namespaces
CREATE TABLE namespaces (
    name TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL
);

-- Runners
CREATE TABLE runners (
    namespace TEXT NOT NULL,
    name TEXT NOT NULL,
    spec TEXT NOT NULL,  -- JSON
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (namespace, name)
);

-- Deployments
CREATE TABLE deployments (
    namespace TEXT NOT NULL,
    name TEXT NOT NULL,
    spec TEXT NOT NULL,         -- JSON
    status TEXT NOT NULL,       -- JSON
    generation INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (namespace, name)
);

-- Jobs
CREATE TABLE jobs (
    namespace TEXT NOT NULL,
    id TEXT NOT NULL,
    spec TEXT NOT NULL,    -- JSON
    status TEXT NOT NULL,  -- JSON
    created_at INTEGER NOT NULL,
    completed_at INTEGER,
    PRIMARY KEY (namespace, id)
);

-- CronJobs
CREATE TABLE cronjobs (
    namespace TEXT NOT NULL,
    name TEXT NOT NULL,
    spec TEXT NOT NULL,    -- JSON
    status TEXT NOT NULL,  -- JSON
    next_run INTEGER,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (namespace, name)
);

-- Instances (running copies of deployments)
CREATE TABLE instances (
    deployment_namespace TEXT NOT NULL,
    deployment_name TEXT NOT NULL,
    id TEXT NOT NULL,
    node_id TEXT NOT NULL,
    status TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (deployment_namespace, deployment_name, id)
);

-- Secrets
CREATE TABLE secrets (
    namespace TEXT NOT NULL,
    name TEXT NOT NULL,
    data TEXT NOT NULL,  -- Encrypted JSON
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (namespace, name)
);

-- Events (audit log)
CREATE TABLE events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp INTEGER NOT NULL,
    namespace TEXT,
    resource_type TEXT NOT NULL,
    resource_name TEXT NOT NULL,
    action TEXT NOT NULL,
    message TEXT,
    actor TEXT
);

-- Indexes
CREATE INDEX idx_instances_node ON instances(node_id);
CREATE INDEX idx_jobs_status ON jobs(namespace, status);
CREATE INDEX idx_events_timestamp ON events(timestamp);
CREATE INDEX idx_nodes_status ON nodes(status);
```

## Scheduler

The scheduler assigns workloads to nodes.

### Scheduling Algorithm

```
Input: Deployment with requirements
Output: List of (node, instance) assignments

1. Filter nodes:
   - Status = Ready
   - Has required runtime
   - Matches OS/arch constraints
   - Matches label selectors
   - Has sufficient resources

2. Score remaining nodes:
   - Resource availability (more = better)
   - Existing instance spread (spread across nodes)
   - Affinity rules
   - Node priority

3. Select top N nodes (N = replica count)

4. Create instance assignments
```

### Scheduling Constraints

```yaml
apiVersion: prisn.io/v1
kind: Deployment
metadata:
  name: my-app
spec:
  # ... code, runtime, etc

  placement:
    # Hard requirements (must match)
    nodeSelector:
      os: linux
      arch: amd64
      gpu: "true"

    # Soft preferences (try to match)
    affinity:
      preferredDuringScheduling:
        - weight: 100
          preference:
            matchExpressions:
              - key: zone
                operator: In
                values: [us-east-1a, us-east-1b]

    # Spread across nodes/zones
    topologySpreadConstraints:
      - maxSkew: 1
        topologyKey: zone
        whenUnsatisfiable: ScheduleAnyway

    # Anti-affinity with other deployments
    antiAffinity:
      - deploymentName: other-app
        type: preferred
```

### Resource-Based Scheduling

Each node reports capacity:
```go
type NodeCapacity struct {
    CPU    int64  // millicores
    Memory int64  // bytes
    Disk   int64  // bytes
    Slots  int    // max concurrent executions
}
```

Each deployment requests resources:
```yaml
spec:
  resources:
    requests:
      cpu: 100m      # 100 millicores
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 512Mi
```

Scheduler tracks allocated vs available per node.

## Node Management

### Node Registration

```
Worker starts:
1. Generate node ID (or use provided)
2. Probe local capabilities:
   - OS, arch
   - Available interpreters
   - CPU, memory, disk
3. Connect to control plane
4. Send RegisterNode request
5. Control plane adds to cluster
6. Worker starts heartbeat loop
```

### Heartbeat

Workers send heartbeats every 5 seconds:
```go
type Heartbeat struct {
    NodeID     string
    Timestamp  time.Time
    Status     NodeStatus
    Capacity   NodeCapacity
    Allocated  NodeCapacity
    Instances  []InstanceStatus
}
```

### Node Failure Detection

Control plane monitors heartbeats:
- **Warning**: No heartbeat for 15 seconds
- **Unhealthy**: No heartbeat for 30 seconds
- **Dead**: No heartbeat for 60 seconds

On Dead:
1. Mark node as Dead
2. Reschedule all instances to healthy nodes
3. Remove node from scheduling pool

### Graceful Shutdown

```
Worker receives SIGTERM:
1. Stop accepting new work
2. Send DrainNode request to control plane
3. Control plane marks node as Draining
4. Control plane reschedules instances
5. Wait for instances to drain (max 30s)
6. Send LeaveNode request
7. Exit
```

## API Server

### gRPC (Internal)

Used for:
- Node registration and heartbeat
- Instance dispatch
- Log streaming
- Metrics collection

```protobuf
service ControlPlane {
    rpc RegisterNode(RegisterNodeRequest) returns (RegisterNodeResponse);
    rpc Heartbeat(stream HeartbeatRequest) returns (stream HeartbeatResponse);
    rpc DrainNode(DrainNodeRequest) returns (DrainNodeResponse);
    rpc LeaveNode(LeaveNodeRequest) returns (LeaveNodeResponse);

    rpc DispatchInstance(DispatchInstanceRequest) returns (DispatchInstanceResponse);
    rpc ReportInstanceStatus(ReportInstanceStatusRequest) returns (ReportInstanceStatusResponse);

    rpc StreamLogs(StreamLogsRequest) returns (stream LogEntry);
}
```

### REST (External)

Used by CLI and external integrations:

```
GET    /api/v1/namespaces
POST   /api/v1/namespaces
DELETE /api/v1/namespaces/{namespace}

GET    /api/v1/namespaces/{namespace}/deployments
POST   /api/v1/namespaces/{namespace}/deployments
GET    /api/v1/namespaces/{namespace}/deployments/{name}
PUT    /api/v1/namespaces/{namespace}/deployments/{name}
DELETE /api/v1/namespaces/{namespace}/deployments/{name}
POST   /api/v1/namespaces/{namespace}/deployments/{name}/scale

GET    /api/v1/namespaces/{namespace}/jobs
POST   /api/v1/namespaces/{namespace}/jobs
GET    /api/v1/namespaces/{namespace}/jobs/{id}
DELETE /api/v1/namespaces/{namespace}/jobs/{id}

GET    /api/v1/namespaces/{namespace}/cronjobs
POST   /api/v1/namespaces/{namespace}/cronjobs
GET    /api/v1/namespaces/{namespace}/cronjobs/{name}
PUT    /api/v1/namespaces/{namespace}/cronjobs/{name}
DELETE /api/v1/namespaces/{namespace}/cronjobs/{name}

GET    /api/v1/namespaces/{namespace}/runners
POST   /api/v1/namespaces/{namespace}/runners
GET    /api/v1/namespaces/{namespace}/runners/{name}
PUT    /api/v1/namespaces/{namespace}/runners/{name}
DELETE /api/v1/namespaces/{namespace}/runners/{name}

GET    /api/v1/nodes
GET    /api/v1/nodes/{id}
DELETE /api/v1/nodes/{id}

GET    /api/v1/namespaces/{namespace}/logs
GET    /api/v1/namespaces/{namespace}/deployments/{name}/logs
GET    /api/v1/namespaces/{namespace}/jobs/{id}/logs

GET    /metrics
GET    /health
GET    /ready
```

## High Availability

### 3-Node Cluster (Recommended)

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Node 1    │     │   Node 2    │     │   Node 3    │
│   Leader    │◄───►│  Follower   │◄───►│  Follower   │
│             │     │             │     │             │
│  API ✓      │     │  API ✓      │     │  API ✓      │
│  Scheduler  │     │  (standby)  │     │  (standby)  │
│  Workers ✓  │     │  Workers ✓  │     │  Workers ✓  │
└─────────────┘     └─────────────┘     └─────────────┘
```

- Any node can handle reads
- Writes forwarded to leader
- Leader failure: ~1-2 second election
- Survives 1 node failure

### Single Node (Development)

```
┌─────────────┐
│   Node 1    │
│   Leader    │
│             │
│  API ✓      │
│  Scheduler  │
│  Workers ✓  │
└─────────────┘
```

No redundancy, but simple. Good for local dev.

### Adding/Removing Nodes

```bash
# Add a new control plane node
prisn cluster join <existing-node>:9001 --control-plane

# Remove a control plane node
prisn cluster remove <node-id>

# Demote control plane to worker-only
prisn cluster demote <node-id>
```

## Kubernetes Mode

When running in Kubernetes, prisn uses a different architecture:

### StatefulSet for Control Plane

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: prisn-control-plane
spec:
  replicas: 3
  serviceName: prisn-control-plane
  selector:
    matchLabels:
      app: prisn-control-plane
  template:
    spec:
      containers:
        - name: prisn
          image: prisn/prisn:latest
          args: ["--control-plane", "--kubernetes"]
          ports:
            - containerPort: 9000  # API
            - containerPort: 9001  # Raft
            - containerPort: 9002  # gRPC
  volumeClaimTemplates:
    - metadata:
        name: data
      spec:
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 10Gi
```

### CRD-Based API

In Kubernetes mode, prisn watches CRDs instead of using REST:

```yaml
apiVersion: prisn.io/v1
kind: Deployment
metadata:
  name: my-app
  namespace: default
spec:
  runtime: python3
  source:
    inline: |
      print("Hello from prisn!")
  replicas: 3
```

Control plane reconciles CRDs to internal state. External nodes can still join the cluster.

### Hybrid: K8s + External Nodes

```
┌─────────────────────────────────────────────────────────────┐
│                     Kubernetes Cluster                       │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐         │
│  │ prisn CP 1  │  │ prisn CP 2  │  │ prisn CP 3  │         │
│  │  (Pod)      │  │  (Pod)      │  │  (Pod)      │         │
│  └─────────────┘  └─────────────┘  └─────────────┘         │
│                                                             │
│  ┌─────────────┐  ┌─────────────┐                          │
│  │ Worker Pod  │  │ Worker Pod  │                          │
│  └─────────────┘  └─────────────┘                          │
└─────────────────────────────────────────────────────────────┘
            │                   │
            ▼                   ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│ External Node   │  │ External Node   │  │ External Node   │
│ (GPU server)    │  │ (ARM device)    │  │ (FreeBSD box)   │
└─────────────────┘  └─────────────────┘  └─────────────────┘
```

External nodes connect to the K8s-hosted control plane. Deployments can target either K8s pods or external nodes.
