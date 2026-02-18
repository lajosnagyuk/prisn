# System Contract

**What must be true before prisn ships**

This document defines the non-negotiable requirements for prisn v1.0. Every feature must meet these criteria before being marked complete.

## Core Invariants

### 1. Single Binary

- [ ] One `prisn` binary does everything
- [ ] No external runtime dependencies (no Docker, no etcd, no Redis)
- [ ] Works on Linux amd64/arm64, FreeBSD amd64, macOS amd64/arm64
- [ ] Static binary where possible (musl on Linux, static on FreeBSD)
- [ ] Binary size < 50MB (compressed < 20MB)

### 2. Zero-Config Startup

```bash
# This must work on a fresh machine:
prisn cluster init
# Output: Cluster initialized. Admin token: prisn_xxx
# Cluster is now ready to accept deployments
```

- [ ] No config files required for basic operation
- [ ] No manual port configuration
- [ ] No certificate generation steps
- [ ] No database setup

### 3. Execution Performance

- [ ] Cached environment (existing venv) < 200ms
- [ ] Cold start (new venv creation) < 5s
- [x] API response time < 10ms (p99)
- [ ] Scheduling latency < 50ms

Note: Fork-based execution prioritizes correctness over speed. Each execution gets clean state.

### 4. Correct Behavior Under Failure

**Single-node (v1)**:
- [x] Process crash: prisn restarts and recovers state from SQLite
- [x] Service crash: Supervisor restarts with exponential backoff
- [x] Graceful shutdown: Running services stopped cleanly (drain + timeout)

**Clustering (v2, deferred)**:
- [ ] Node failure: Workloads rescheduled within 60s
- [ ] Leader failure: New leader elected within 5s
- [ ] Network partition: No split-brain (Raft guarantees)

### 5. Resource Isolation

- [ ] Memory limits enforced (OOM killer for limit breach)
- [ ] CPU limits enforced (throttling, not killing)
- [ ] Disk space limits enforced (workspace quotas)
- [ ] Process count limits enforced (no fork bombs)

## Implementation Status

### Core Infrastructure (DONE)

- [x] Single binary (`prisn`) for CLI, server, and runner
- [x] Dependency detection (requirements.txt, pyproject.toml, import scan)
- [x] Virtual environment management with caching
- [x] Fork-based execution (replaced warm pools)
- [x] SQLite state storage
- [x] REST API server with CRUD for deployments
- [x] Supervisor for service lifecycle (start/stop/restart)
- [x] Safe CLI syntax (removed `*1.5`, `-1`, `00`)
- [x] Multi-context CLI (local, kubernetes)
- [x] Kubernetes operator mode (CRD-based)

### CLI UX (DONE)

- [x] Shell completion (bash/zsh/fish/powershell)
- [x] Output format flag (-o json/yaml/text)
- [x] Health status in `prisn status` output
- [x] `prisn doctor` command for system diagnostics
- [x] Dry-run mode for delete command
- [x] Logs command (basic, streaming pending)
- [x] Improved error messages with suggestions

### In Progress

- [x] Scheduler for cron jobs
- [x] Health checks (via /health endpoint)
- [ ] Log aggregation (basic implementation done, streaming pending)
- [x] Metrics endpoint (/metrics Prometheus format)

### Deferred to v2

- [ ] Multi-node clustering (Raft)
- [ ] External state backends (Postgres, etcd)
- [ ] Network isolation
- [ ] Service mesh features

## Feature Requirements

### Server Management (v1 - Single Node)

| Feature | Requirement | Test |
|---------|-------------|------|
| Start | Single command starts server | `prisn server start` succeeds |
| Stop | Graceful shutdown | `prisn server stop` stops all services |
| Status | Clear server health view | `prisn status` shows running services |
| Recover | State recovers from SQLite | Restart server, services resume |

### Cluster Management (v2 - Deferred)

| Feature | Requirement | Test |
|---------|-------------|------|
| Init | Single command creates working cluster | `prisn cluster init` succeeds |
| Join | Single command joins cluster | `prisn cluster join <addr>` succeeds |
| HA | 3-node cluster survives 1 failure | Kill any node, cluster continues |
| Leave | Graceful node removal | `prisn cluster drain` evacuates workloads |
| Status | Clear cluster health view | `prisn cluster status` shows all nodes |

### Deployments

| Feature | Requirement | Test |
|---------|-------------|------|
| Create | Deploy from file in one command | `prisn deploy app.py` creates deployment |
| Scale | Safe scale syntaxes work | `5`, `0`, `--add 2`, `--multiply 1.5`, `--auto` |
| Update | Zero-downtime update | `prisn deploy app.py` with running instances |
| Delete | Clean removal | `prisn delete deploy app` removes all resources |
| Logs | Stream logs in real-time | `prisn logs app -f` shows live output |
| Describe | Complete status information | `prisn describe deploy app` shows instances, events |

### Jobs

| Feature | Requirement | Test |
|---------|-------------|------|
| Run | Execute and wait | `prisn run script.py` runs, streams output, exits |
| Detach | Run in background | `prisn run script.py --detach` returns job ID |
| Status | Check job status | `prisn get job <id>` shows status |
| Logs | View job output | `prisn logs job/<id>` shows output |
| Timeout | Enforce timeout | Job killed after `--timeout` |

### CronJobs

| Feature | Requirement | Test |
|---------|-------------|------|
| Create | Standard cron syntax | `prisn cron script.py "0 * * * *"` |
| Create | Human syntax | `prisn cron script.py --every hour` |
| Execute | Runs on schedule | Job created at scheduled time |
| History | View past runs | `prisn get jobs --cron cron-name` |
| Suspend | Pause without delete | `prisn scale cron-name 0` |

### Runners

| Feature | Requirement | Test |
|---------|-------------|------|
| Built-in | python3, node, bash work | `prisn run script.py/js/sh` |
| Custom | Custom runner creation | Create Lua runner, `prisn run script.lua` |
| Detection | Auto-detect from extension | `.py` -> python3, `.js` -> node |
| Fork | Fork-based execution (no state pollution) | Each run starts with clean state |
| Isolation | Isolation levels work | Level 2 prevents filesystem escape |

### Networking

| Feature | Requirement | Test |
|---------|-------------|------|
| Discovery | DNS resolution works | `nslookup app.ns.prisn.local` |
| Load balancing | Traffic distributed | Curl multiple times, see different instances |
| Health checks | Unhealthy removed | Kill instance, traffic stops going there |
| Ingress | External access works | Access via domain name from outside |

### Security

| Feature | Requirement | Test |
|---------|-------------|------|
| Auth | Token auth works | Request without token fails |
| Namespace | Isolation enforced | Token for ns-a can't access ns-b |
| Secrets | Encrypted at rest | Inspect database, secrets unreadable |
| Guardrails | Can't kill prisn | Script attempting to kill prisn fails |
| Resource limits | Enforced | Memory bomb script gets OOM killed |

### Observability

| Feature | Requirement | Test |
|---------|-------------|------|
| Metrics | Prometheus endpoint | `/metrics` returns valid Prometheus format |
| Logs | Aggregated, queryable | `prisn logs --since 1h` works |
| Events | Audit trail | `prisn events` shows all actions |
| Health | Node health visible | `prisn get nodes` shows health |

## Quality Requirements

### Code Quality

- [ ] No panics in production code paths
- [ ] All errors have context (wrapped with `fmt.Errorf`)
- [ ] No data races (pass `-race` flag in tests)
- [ ] Code coverage > 70% for core packages
- [ ] All public APIs documented

### Performance

- [ ] 1000 concurrent deployments supported
- [ ] 10000 jobs/minute throughput
- [ ] Control plane memory < 500MB (at 1000 deployments)
- [ ] Worker memory < 100MB (base, excluding workloads)

### Reliability

- [ ] No data loss on clean shutdown
- [ ] Recovery from unclean shutdown within 30s
- [ ] Raft log compaction works (disk doesn't grow unbounded)
- [ ] Graceful degradation under load (reject vs crash)

### Usability

- [x] All errors suggest remediation (improved error messages)
- [x] Tab completion for all commands (bash/zsh/fish/powershell)
- [x] Help text for all commands and flags
- [ ] Examples in all documentation

## Testing Requirements

### Unit Tests

Every package must have tests covering:
- Happy path
- Error conditions
- Edge cases
- Concurrent access (where applicable)

### Integration Tests

- [ ] Single-node cluster lifecycle
- [ ] 3-node cluster lifecycle
- [ ] Node failure and recovery
- [ ] Deployment lifecycle (create, scale, update, delete)
- [ ] Job execution (success, failure, timeout)
- [ ] CronJob scheduling
- [ ] Runner pool management
- [ ] Network connectivity
- [ ] Secret management

### End-to-End Tests

- [ ] Fresh install to running deployment in < 5 minutes
- [ ] Multi-language deployment (Python, Node, Bash)
- [ ] Cross-platform deployment (Linux -> FreeBSD, if applicable)
- [ ] Cluster upgrade (rolling update of prisn itself)

### Chaos Tests

- [ ] Random node kills (cluster recovers)
- [ ] Network partitions (no split-brain)
- [ ] Disk full (graceful failure)
- [ ] Memory pressure (OOM handling)

## Documentation Requirements

### Architecture Docs

- [x] OVERVIEW.md - High-level architecture
- [x] CONTROL-PLANE.md - Clustering and state
- [x] EXECUTION-MODEL.md - How code runs
- [x] RUNNERS.md - Runner system
- [x] CLI-UX.md - CLI design
- [x] SECURITY.md - Security model
- [x] NETWORKING.md - Networking

### Spec Docs

- [ ] API.md - REST/gRPC API specification
- [ ] MANIFEST.md - Deployment manifest format
- [ ] CRD.md - Kubernetes CRD definitions

### User Docs

- [ ] Getting Started guide
- [ ] Examples for common use cases
- [ ] Troubleshooting guide
- [ ] FAQ

## Release Checklist

Before any release:

1. [ ] All tests pass
2. [ ] No critical/high security issues
3. [ ] Documentation is current
4. [ ] CHANGELOG updated
5. [ ] Version bumped appropriately
6. [ ] Binaries built for all platforms
7. [ ] Smoke test on each platform
8. [ ] Release notes written

## Definition of Done

A feature is DONE when:

1. **Code**: Implemented, reviewed, merged
2. **Tests**: Unit + integration tests passing
3. **Docs**: Usage documented with examples
4. **Errors**: Helpful error messages
5. **Metrics**: Relevant metrics exposed
6. **Logs**: Appropriate logging
7. **Security**: Security implications considered

## Non-Goals (Explicit)

Things we are NOT building:

- Container orchestration (use Kubernetes)
- General-purpose VM management
- Database hosting
- Message queue
- Service mesh (beyond basic load balancing)
- Multi-region clustering (single region only)
- Windows support (maybe later)
- GUI (CLI is the interface)

## Versioning

- **v0.x**: Breaking changes allowed
- **v1.0**: Stable API, no breaking changes without deprecation
- **v1.x**: Bug fixes, non-breaking features
- **v2.0**: Next major, breaking changes allowed

## Sign-Off

This contract is agreed upon by the project maintainers. Any deviation requires explicit discussion and approval.

---

*Last updated: 2026-01-26*
