# Raft Consensus Implementation

## Status: Complete

### Completed
- [x] Design document
- [x] Raft FSM core (`pkg/raft/fsm.go`, `pkg/raft/types.go`)
- [x] Log replication (`pkg/raft/log.go`)
- [x] Leader election (`pkg/raft/node.go`)
- [x] xxhash64 fingerprinting (`pkg/store/fingerprint.go`)
- [x] Local storage layer (`pkg/storage/cache.go`)
- [x] Hot/cold priming algorithm
- [x] Compression (LZ4) for warm storage
- [x] Server integration (`pkg/api/cluster.go`)
- [x] CLI integration (`pkg/cli/cluster.go`)
- [x] Node discovery (`pkg/raft/discovery.go`)
- [x] HTTP transport (`pkg/raft/transport.go`)

## Architecture

### Design Goals

1. **Fast hot path**: Script execution NEVER waits for Raft consensus (~60ms)
2. **Strong consistency for metadata**: Deployment configs, schedules, assignments
3. **Eventual consistency for data**: Deployment files replicated asynchronously
4. **Local-first execution**: Nodes execute from local storage

### What Goes Through Raft (Cold Path)

- Cluster membership changes (node join/leave)
- Deployment creation/deletion/update metadata
- Version activation (which version is "current")
- Scheduler assignments (which node runs which cron)
- Scaling decisions (replica count changes)

### What Bypasses Raft (Hot Path)

- Script execution (runs from local storage)
- Log streaming
- Health checks
- Metrics collection
- File reads from local deployment cache

### Data Flow

```
                                    Raft Consensus
                                   ┌─────────────┐
                                   │   Leader    │
                                   │  (metadata) │
                                   └──────┬──────┘
                                          │
              ┌───────────────────────────┼───────────────────────────┐
              │                           │                           │
              ▼                           ▼                           ▼
       ┌─────────────┐             ┌─────────────┐             ┌─────────────┐
       │   Node A    │             │   Node B    │             │   Node C    │
       │             │◄───────────►│             │◄───────────►│             │
       │ Local Store │  async sync │ Local Store │  async sync │ Local Store │
       └──────┬──────┘             └──────┬──────┘             └──────┬──────┘
              │                           │                           │
              ▼                           ▼                           ▼
       ┌─────────────┐             ┌─────────────┐             ┌─────────────┐
       │  Executor   │             │  Executor   │             │  Executor   │
       │ (hot path)  │             │ (hot path)  │             │ (hot path)  │
       └─────────────┘             └─────────────┘             └─────────────┘
```

## Storage Layout

### Per-Node Local Storage

```
~/.prisn/
├── raft/
│   ├── log/              # Raft log (append-only)
│   ├── snapshots/        # Raft snapshots
│   └── state.json        # Current term, voted for
├── deployments/          # Local deployment cache
│   ├── api/
│   │   ├── v-a1b2c3d4/   # Version directory
│   │   │   ├── .meta     # xxhash64, size, file count
│   │   │   └── data.lz4  # Compressed deployment (cold)
│   │   │   └── files/    # Extracted files (hot)
│   │   └── v-e5f6g7h8/
│   └── worker/
├── state.db              # SQLite (local state)
└── node.json             # Node identity
```

## Hot/Cold Priming Algorithm

### States

- **HOT**: Files extracted locally, ready for immediate execution
- **WARM**: Compressed archive exists locally, needs extraction (~10ms)
- **COLD**: Not on this node, needs fetch from peer (~100ms+)

### Priming Strategy

1. **Proactive priming**: When deployment assigned to node, prime it HOT
2. **Predictive priming**: Keep recently-used deployments HOT
3. **LRU eviction**: When storage pressure, evict coldest first
4. **Peer fetch**: Request from nearest node with HOT copy

### Temperature Scoring

```
score = (last_access_time * 0.4) + (access_frequency * 0.3) + (assigned_here * 0.3)
```

- Deployments assigned to this node: always HOT
- Recently accessed: stay HOT for 1 hour
- Frequently accessed: stay WARM
- Others: COLD (compressed or evicted)

## Fingerprinting

### xxhash64 vs SHA256

| Metric | SHA256 | xxhash64 |
|--------|--------|----------|
| Speed | ~400 MB/s | ~10 GB/s |
| Size | 32 bytes | 8 bytes |
| Collisions | Cryptographic | Good enough |

We use xxhash64 because:
- Not doing cryptographic verification
- Just detecting changes
- 25x faster
- Smaller storage overhead

### Fingerprint Format

```
<xxhash64>-<file_count>-<total_size>
Example: a1b2c3d4e5f6g7h8-42-1048576
```

## Compression

### LZ4 for Transfers

- Speed: ~400 MB/s compression, ~2 GB/s decompression
- Ratio: ~2-3x for typical source code
- Streaming: Can decompress on-the-fly

### When to Compress

- **Always**: Network transfers between nodes
- **Warm storage**: Keep compressed archive on disk
- **Never**: Hot storage (extracted for execution)

## Raft Implementation Details

### Log Entry Types

```go
type LogEntryType uint8

const (
    LogNoop LogEntryType = iota
    LogDeploymentCreate
    LogDeploymentDelete
    LogDeploymentActivate
    LogSchedulerAssign
    LogNodeJoin
    LogNodeLeave
    LogScaleChange
)
```

### State Machine

```go
type ClusterState struct {
    Deployments map[string]*DeploymentMeta
    Nodes       map[string]*NodeInfo
    Assignments map[string][]string  // deployment -> nodes
    Schedules   map[string]*Schedule
}
```

### Election Timeouts

- Election timeout: 150-300ms (randomized)
- Heartbeat interval: 50ms
- Log replication: batched, async for followers

## API Changes

### New Endpoints

```
POST   /v1/cluster/join      # Join cluster
DELETE /v1/cluster/leave     # Leave cluster
GET    /v1/cluster/status    # Cluster health
GET    /v1/cluster/nodes     # List nodes
POST   /v1/cluster/prime     # Request deployment priming
```

### CLI Changes

```bash
prisn cluster status          # Show cluster state
prisn cluster nodes           # List nodes
prisn cluster join <addr>     # Join existing cluster
prisn cluster leave           # Gracefully leave
```

## Implementation Order

1. **Raft core** (`pkg/raft/`)
   - Log storage
   - State machine interface
   - Leader election
   - Log replication

2. **Fingerprinting** (`pkg/store/`)
   - Replace SHA256 with xxhash64
   - Update version ID format

3. **Local storage** (`pkg/storage/`)
   - Hot/cold/warm states
   - LZ4 compression
   - Priming scheduler

4. **Integration**
   - Server uses Raft for metadata ops
   - Executor uses local storage
   - CLI cluster commands
