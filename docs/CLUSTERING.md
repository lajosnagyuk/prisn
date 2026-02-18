# Clustering (High Availability)

prisn uses Raft consensus for high availability clustering. State is replicated across all nodes, and the cluster continues operating as long as a majority of nodes are healthy.

## Overview

```
                    +-----------+
                    |  Client   |
                    +-----+-----+
                          |
            +-------------+-------------+
            |             |             |
      +-----v-----+ +-----v-----+ +-----v-----+
      |  Node 1   | |  Node 2   | |  Node 3   |
      |  (Leader) | | (Follower)| | (Follower)|
      +-----------+ +-----------+ +-----------+
            |             ^             ^
            |    Raft     |    Raft     |
            +-------------+-------------+
              Replication   Replication
```

- **Leader**: Handles all writes, replicates to followers
- **Followers**: Serve reads, vote in elections
- **Quorum**: Majority required for commits (2 of 3, 3 of 5, etc.)

## Quick Start

### 3-Node Cluster

```bash
# Node 1 - Bootstrap the cluster
prisn server \
  --bind :7331 \
  --cluster \
  --raft-addr :7332 \
  --bootstrap

# Node 2 - Join the cluster
prisn server \
  --bind :7331 \
  --cluster \
  --raft-addr :7332 \
  --peers node1:7332

# Node 3 - Join the cluster
prisn server \
  --bind :7331 \
  --cluster \
  --raft-addr :7332 \
  --peers node1:7332,node2:7332
```

### Verify Cluster

```bash
# Check cluster status
curl http://node1:7331/api/v1/cluster/status

# Response:
{
  "leader": "node1",
  "nodes": [
    {"id": "node1", "address": "node1:7332", "state": "leader"},
    {"id": "node2", "address": "node2:7332", "state": "follower"},
    {"id": "node3", "address": "node3:7332", "state": "follower"}
  ],
  "healthy": true
}
```

## Configuration

### Command Line

```bash
prisn server \
  --bind :7331 \                  # HTTP API port
  --cluster \                     # Enable clustering
  --raft-addr :7332 \             # Raft consensus port
  --peers node1:7332,node2:7332 \ # Initial peers to join
  --bootstrap \                   # Bootstrap new cluster (first node only)
  --node-id mynode \              # Node identifier (default: hostname)
  --data-dir /var/lib/prisn       # Data directory for Raft logs
```

### Environment Variables

```bash
export PRISN_CLUSTER=true
export PRISN_RAFT_ADDR=:7332
export PRISN_PEERS=node1:7332,node2:7332
export PRISN_NODE_ID=node1
export PRISN_DATA_DIR=/var/lib/prisn

prisn server --bind :7331
```

## Cluster Operations

### Add a Node

```bash
# On new node
prisn server \
  --bind :7331 \
  --cluster \
  --raft-addr :7332 \
  --peers node1:7332

# The new node will automatically join and sync
```

### Remove a Node

```bash
# From any healthy node
curl -X DELETE http://node1:7331/api/v1/cluster/nodes/node3
```

### Force Leader Election

```bash
# Useful when leader is stuck
curl -X POST http://node2:7331/api/v1/cluster/election
```

## Deployment Patterns

### Docker Compose

```yaml
version: '3.8'
services:
  prisn1:
    image: ghcr.io/prisn/prisn:latest
    hostname: prisn1
    command: >
      server --bind :7331 --cluster --raft-addr :7332 --bootstrap
    ports:
      - "7331:7331"
      - "7332:7332"
    volumes:
      - prisn1-data:/var/lib/prisn
    networks:
      - prisn-net

  prisn2:
    image: ghcr.io/prisn/prisn:latest
    hostname: prisn2
    command: >
      server --bind :7331 --cluster --raft-addr :7332 --peers prisn1:7332
    ports:
      - "7333:7331"
      - "7334:7332"
    volumes:
      - prisn2-data:/var/lib/prisn
    networks:
      - prisn-net
    depends_on:
      - prisn1

  prisn3:
    image: ghcr.io/prisn/prisn:latest
    hostname: prisn3
    command: >
      server --bind :7331 --cluster --raft-addr :7332 --peers prisn1:7332,prisn2:7332
    ports:
      - "7335:7331"
      - "7336:7332"
    volumes:
      - prisn3-data:/var/lib/prisn
    networks:
      - prisn-net
    depends_on:
      - prisn1
      - prisn2

networks:
  prisn-net:

volumes:
  prisn1-data:
  prisn2-data:
  prisn3-data:
```

### Kubernetes StatefulSet

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: prisn
spec:
  serviceName: prisn
  replicas: 3
  selector:
    matchLabels:
      app: prisn
  template:
    metadata:
      labels:
        app: prisn
    spec:
      containers:
      - name: prisn
        image: ghcr.io/prisn/prisn:latest
        args:
        - server
        - --bind=:7331
        - --cluster
        - --raft-addr=:7332
        - --peers=prisn-0.prisn:7332,prisn-1.prisn:7332,prisn-2.prisn:7332
        ports:
        - containerPort: 7331
          name: http
        - containerPort: 7332
          name: raft
        volumeMounts:
        - name: data
          mountPath: /var/lib/prisn
  volumeClaimTemplates:
  - metadata:
      name: data
    spec:
      accessModes: ["ReadWriteOnce"]
      resources:
        requests:
          storage: 10Gi
---
apiVersion: v1
kind: Service
metadata:
  name: prisn
spec:
  clusterIP: None
  selector:
    app: prisn
  ports:
  - port: 7331
    name: http
  - port: 7332
    name: raft
---
apiVersion: v1
kind: Service
metadata:
  name: prisn-api
spec:
  type: ClusterIP
  selector:
    app: prisn
  ports:
  - port: 7331
    targetPort: 7331
```

### systemd (Multi-Node)

On each node, create `/etc/systemd/system/prisn.service`:

```ini
[Unit]
Description=prisn Server (Clustered)
After=network.target

[Service]
Type=simple
User=prisn
Group=prisn
ExecStart=/usr/local/bin/prisn server \
  --bind :7331 \
  --cluster \
  --raft-addr :7332 \
  --peers node1:7332,node2:7332,node3:7332 \
  --data-dir /var/lib/prisn
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

## Failover Behavior

### Leader Failure

1. Followers detect leader timeout (default: 150ms)
2. Election timer triggers on followers
3. Candidate requests votes
4. Majority elects new leader
5. New leader starts accepting writes
6. Failed node rejoins as follower when recovered

### Network Partition

- **Majority partition**: Continues operating, elects new leader if needed
- **Minority partition**: Cannot commit writes, read-only or stale

### Split Brain Prevention

Raft guarantees at most one leader per term. A leader must maintain quorum to commit writes.

## Monitoring

### Metrics

```bash
curl http://localhost:7331/metrics | grep raft
```

Key metrics:
- `raft_state` - Node state (0=follower, 1=candidate, 2=leader)
- `raft_term` - Current Raft term
- `raft_commit_index` - Committed log index
- `raft_applied_index` - Applied log index
- `raft_peers` - Number of peers
- `raft_replication_lag_seconds` - Replication lag

### Health Checks

```bash
# Cluster health
curl http://localhost:7331/api/v1/cluster/health

# Individual node
curl http://localhost:7331/health
```

### Alerts

```yaml
# Prometheus alerting rules
groups:
- name: prisn-cluster
  rules:
  - alert: PrisnNoLeader
    expr: sum(raft_state == 2) == 0
    for: 30s
    labels:
      severity: critical
    annotations:
      summary: "No leader in prisn cluster"

  - alert: PrisnQuorumLost
    expr: count(up{job="prisn"}) < 2
    for: 1m
    labels:
      severity: critical
    annotations:
      summary: "prisn cluster lost quorum"

  - alert: PrisnReplicationLag
    expr: raft_commit_index - raft_applied_index > 100
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "High replication lag on {{ $labels.instance }}"
```

## Troubleshooting

### Cluster Won't Form

```bash
# Check connectivity between nodes
nc -zv node2 7332

# Check firewall
sudo ufw status

# Verify Raft addresses match
curl http://node1:7331/api/v1/cluster/status
```

### Split Brain

```bash
# Check which partition has quorum
for node in node1 node2 node3; do
  echo "$node:"
  curl -s http://$node:7331/api/v1/cluster/status | jq '.leader'
done
```

### Stale Reads

Reads from followers may be slightly behind leader. For strong consistency:

```bash
# Force read from leader
curl -H "X-Prisn-Consistency: strong" http://localhost:7331/api/v1/deployments
```

### Recovery from Total Failure

If all nodes fail simultaneously:

```bash
# On first node, force bootstrap
prisn server \
  --cluster \
  --raft-addr :7332 \
  --bootstrap \
  --force-new-cluster  # Dangerous: creates new cluster from local state

# Then add other nodes normally
```

## Best Practices

1. **Use odd number of nodes** (3, 5, 7) for clear majority
2. **Spread nodes across failure domains** (AZs, racks)
3. **Use dedicated disk for Raft logs** for performance
4. **Monitor replication lag** to detect issues early
5. **Test failover regularly** in non-production
6. **Keep cluster size small** (3-7 nodes) - more nodes = slower commits

## Cluster Sizing

| Nodes | Fault Tolerance | Quorum |
|-------|-----------------|--------|
| 3     | 1 failure       | 2      |
| 5     | 2 failures      | 3      |
| 7     | 3 failures      | 4      |

More nodes increases fault tolerance but adds latency (all writes must wait for quorum).
