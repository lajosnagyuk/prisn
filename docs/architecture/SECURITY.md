# Security Model

**Trust but verify. Guard the guardrails.**

## Threat Model

prisn is designed for **non-adversarial environments**:
- Trusted users
- Internal networks
- Home labs
- Small teams

We do NOT defend against:
- Malicious users with valid credentials
- Nation-state attackers
- Determined privilege escalation attempts

We DO defend against:
- Accidental resource exhaustion
- Runaway processes
- Bugs in user code
- Cross-namespace pollution
- Accidental self-destruction

## Security Layers

```
┌─────────────────────────────────────────────────────────────┐
│                    Authentication                           │
│              (Who are you?)                                  │
├─────────────────────────────────────────────────────────────┤
│                    Authorization                            │
│              (What can you do?)                             │
├─────────────────────────────────────────────────────────────┤
│                    Namespace Isolation                      │
│              (Stay in your lane)                            │
├─────────────────────────────────────────────────────────────┤
│                    Process Isolation                        │
│              (Your code can't escape)                       │
├─────────────────────────────────────────────────────────────┤
│                    Resource Limits                          │
│              (Don't hog everything)                         │
└─────────────────────────────────────────────────────────────┘
```

## Authentication

### Default: Token-Based

```bash
# Initialize cluster, generates admin token
prisn cluster init
# Output: Cluster initialized. Admin token: prisn_xxxxxx

# Use token
export PRISN_TOKEN=prisn_xxxxxx
prisn get deploy

# Or in config
# ~/.prisn/config.yaml
clusters:
  - name: default
    endpoint: localhost:9000
    token: prisn_xxxxxx
```

### Token Types

| Type | Purpose | Permissions |
|------|---------|-------------|
| Admin | Full cluster access | Everything |
| Namespace | Single namespace | All operations in namespace |
| Read-only | Monitoring | Read all, write nothing |
| Deploy | CI/CD | Create/update deployments only |

### Creating Tokens

```bash
# Create namespace-scoped token
prisn token create --namespace production --name ci-deploy
# Output: prisn_ns_prod_xxxxxx

# Create read-only token
prisn token create --read-only --name monitoring
# Output: prisn_ro_xxxxxx

# List tokens
prisn token list

# Revoke token
prisn token revoke prisn_xxxxxx
```

### mTLS (Optional)

For production environments:

```yaml
# /etc/prisn/config.yaml
tls:
  enabled: true
  certFile: /etc/prisn/tls/server.crt
  keyFile: /etc/prisn/tls/server.key
  caFile: /etc/prisn/tls/ca.crt
  clientAuth: require  # require client certificates
```

```bash
# Connect with client cert
prisn --cert client.crt --key client.key get deploy
```

## Authorization

### Namespace-Based Access Control

Tokens are scoped to namespaces:

```
Token: prisn_ns_prod_abc123
Scope: namespace=production
Permissions: [create, read, update, delete]

Can:
  - prisn -n production deploy app.py ✓
  - prisn -n production get deploy ✓
  - prisn -n production delete deploy my-app ✓

Cannot:
  - prisn -n staging deploy app.py ✗
  - prisn get nodes ✗
  - prisn cluster status ✗
```

### Role-Based (Future)

For more complex environments:

```yaml
apiVersion: prisn.io/v1
kind: Role
metadata:
  name: developer
  namespace: production
spec:
  rules:
    - resources: [deployments, jobs, cronjobs]
      verbs: [get, list, create, update]
    - resources: [logs]
      verbs: [get]
    # No delete, no secrets access
```

## Namespace Isolation

### What Namespaces Isolate

- **Deployments**: my-app in ns-a is different from my-app in ns-b
- **Jobs**: Job IDs are unique per namespace
- **Secrets**: Cannot access secrets from other namespaces
- **Runners**: Can be namespace-scoped or cluster-wide
- **Resource quotas**: Each namespace can have limits

### What Namespaces DON'T Isolate

- **Nodes**: All namespaces share the same nodes
- **Network**: By default, workloads can reach each other
- **Metrics**: Cluster-wide metrics include all namespaces

### Namespace Quotas

```yaml
apiVersion: prisn.io/v1
kind: Namespace
metadata:
  name: production
spec:
  quota:
    maxDeployments: 100
    maxJobs: 1000
    maxReplicas: 500
    resources:
      cpu: 100       # cores
      memory: 256Gi
```

## Process Isolation

See [EXECUTION-MODEL.md](./EXECUTION-MODEL.md) for detailed isolation mechanisms.

### Summary by Level

| Level | Name | What It Does |
|-------|------|--------------|
| 0 | None | Just fork/exec, full trust |
| 1 | Basic | Process groups, ulimits, cwd isolation |
| 2 | Strong | Namespaces/jails, filesystem isolation |
| 3 | Paranoid | + seccomp/pledge, network deny |

### Default: Level 1

Safe enough for trusted code, fast enough for most uses.

```yaml
# Override per-deployment
apiVersion: prisn.io/v1
kind: Deployment
spec:
  isolation: 2  # strong
```

## Resource Limits

### Per-Execution Defaults

```yaml
# /etc/prisn/config.yaml
defaults:
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 1000m      # 1 core max
      memory: 512Mi   # 512MB max
    timeout: 1h       # kill after 1 hour
    maxProcesses: 50  # no fork bombs
```

### Per-Deployment Override

```yaml
apiVersion: prisn.io/v1
kind: Deployment
spec:
  resources:
    requests:
      cpu: 500m
      memory: 1Gi
    limits:
      cpu: 4
      memory: 8Gi
```

### Node-Level Limits

```yaml
# /etc/prisn/node.yaml
limits:
  maxConcurrent: 20        # max simultaneous executions
  reservedCpu: 500m        # keep for system
  reservedMemory: 1Gi      # keep for system
  maxSingleCpu: 4          # no single job gets more than 4 cores
  maxSingleMemory: 16Gi    # no single job gets more than 16GB
```

## Guardrails: Preventing Self-Harm

### Cannot Kill prisn

User code cannot:
- Send signals to prisn processes
- Terminate the prisn daemon
- Modify prisn's files

Implementation:
```go
// In seccomp filter (Level 3)
// Block kill() calls to prisn's PID
// Block ptrace() entirely
// Block access to /var/lib/prisn, /etc/prisn
```

### Cannot Access Other Namespaces

User code cannot:
- Read/write files from other namespace workspaces
- Query other namespace's deployments
- Send signals to other namespace's processes

Implementation:
- Filesystem isolation: chroot/mount namespace
- API: Token scoped to namespace
- Process isolation: PID namespace (can't see other PIDs)

### Cannot Nuke the OS

User code cannot:
- Reboot the machine
- Kill system processes
- Mount filesystems
- Load kernel modules
- Fill up root filesystem (workspace is limited)

Implementation:
- Blocked syscalls: reboot, kexec_load, mount, etc.
- Workspace disk quotas
- Separate partition for workspaces (optional)

### Lifecycle Controls

Deployments cannot delete themselves in ways that bypass audit:

```bash
# From within a deployment's code:
# This is blocked - no reaching back to modify own deployment
requests.delete("http://prisn-api/deployments/my-app")  # 403 Forbidden

# This is the only way to stop yourself
sys.exit(0)  # Clean exit, prisn respawns
sys.exit(1)  # Error exit, prisn respawns with backoff
```

To actually delete a deployment, use the CLI/API with proper auth.

## Secrets Management

### Creating Secrets

```bash
# From literal
prisn secret create db-creds \
  --from-literal username=admin \
  --from-literal password=hunter2

# From file
prisn secret create tls-cert \
  --from-file cert.pem \
  --from-file key.pem

# From env file
prisn secret create app-config --from-env-file .env
```

### Using Secrets

```yaml
apiVersion: prisn.io/v1
kind: Deployment
spec:
  env:
    - name: DB_USER
      secretRef:
        name: db-creds
        key: username
    - name: DB_PASS
      secretRef:
        name: db-creds
        key: password
```

Or mount as file:
```yaml
spec:
  volumes:
    - name: certs
      secret:
        secretName: tls-cert
  mounts:
    - name: certs
      path: /prisn/certs
```

### Secret Storage

Secrets are:
- Encrypted at rest (AES-256-GCM)
- Replicated via Raft (encrypted)
- Never logged
- Never shown in `prisn describe` (redacted)

Encryption key:
- Generated on cluster init
- Stored in `/var/lib/prisn/keyring`
- Optionally backed by external KMS

### Secret Rotation

```bash
# Update a secret (deployments auto-restart)
prisn secret update db-creds --from-literal password=newpassword

# Update without restart
prisn secret update db-creds --from-literal password=newpassword --no-restart
```

## Audit Logging

Every action is logged:

```
2024-01-15T10:30:45Z INFO  action=create resource=deployment name=my-app namespace=default actor=admin@token:abc123
2024-01-15T10:30:46Z INFO  action=schedule deployment=my-app instance=my-app-1 node=node-2
2024-01-15T10:30:47Z INFO  action=start instance=my-app-1 node=node-2
2024-01-15T10:35:00Z INFO  action=scale deployment=my-app replicas=3 actor=admin@token:abc123
```

Query audit log:
```bash
prisn events --namespace default --since 1h
prisn events --resource deployment/my-app
prisn events --actor admin
```

## Network Security

### Internal Communication

Control plane <-> Worker: gRPC with mTLS (optional)

```yaml
# /etc/prisn/config.yaml
cluster:
  tls:
    enabled: true
    certFile: /etc/prisn/cluster.crt
    keyFile: /etc/prisn/cluster.key
    caFile: /etc/prisn/ca.crt
```

### Workload Network

By default, workloads have full network access.

To restrict:

```yaml
# Per-deployment
apiVersion: prisn.io/v1
kind: Deployment
spec:
  network:
    egress:
      - allow: 10.0.0.0/8      # internal only
      - deny: 0.0.0.0/0        # block internet

  # Or completely disable
  network:
    enabled: false
```

### Service Exposure

```yaml
spec:
  port: 8080
  expose:
    type: internal   # only reachable within cluster
    # or
    type: external   # reachable from outside
    # or
    type: none       # no networking at all
```

## Security Checklist

### Before Production

- [ ] Enable TLS for API
- [ ] Enable mTLS for cluster communication
- [ ] Set up token auth (don't use default admin token)
- [ ] Configure resource limits
- [ ] Set namespace quotas
- [ ] Review isolation levels for each runner
- [ ] Enable audit logging
- [ ] Backup encryption keys

### Operational

- [ ] Rotate tokens periodically
- [ ] Review audit logs
- [ ] Monitor resource usage
- [ ] Update prisn regularly
- [ ] Backup state regularly

## Known Limitations

1. **Not a security boundary**: If you need true multi-tenancy with untrusted users, use separate clusters or Kubernetes with proper network policies.

2. **Shared kernel**: All workloads share the host kernel. A kernel exploit could escape isolation.

3. **Side channels**: CPU cache timing, memory timing attacks are not mitigated.

4. **No SELinux/AppArmor integration**: Yet. Coming in future versions.

5. **Secrets in memory**: Secrets are decrypted in memory. Memory dumps could expose them.

For truly adversarial environments, consider:
- Running prisn in VMs with strong isolation
- Using Kubernetes with gVisor/Kata containers
- Separate clusters per trust domain
