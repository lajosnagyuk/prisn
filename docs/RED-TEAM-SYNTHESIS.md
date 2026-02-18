# Red Team Synthesis: What We're Actually Building

**Date**: 2026-01-21
**Status**: DECISIONS MADE

## The Brutal Truth

Four agents tore the architecture apart. Here's what they found:

### Critical Failures

| Issue | Severity | Impact |
|-------|----------|--------|
| **No dependency management** | FATAL | Scripts with `import pandas` fail immediately |
| **Shell escaping (`*1.5`)** | CRITICAL | Will glob in bash, destroy user's command |
| **`0` vs `00` footgun** | CRITICAL | Single character = delete everything |
| **Warm pool state corruption** | HIGH | Scripts pollute each other's state |
| **Raft complexity** | HIGH | "Simple" cluster requires distributed systems PhD |
| **Cross-platform inconsistency** | MEDIUM | Same script behaves differently on Linux vs macOS |

### The Core Contradiction

We promised: **"Just run my shit"**

But we designed: **"Write YAML manifests and understand process isolation"**

## Decisions Made

### Decision 1: Dependencies Are First-Class

**Old approach**: Ignore dependencies, assume scripts are self-contained.

**New approach**: Auto-detect and install dependencies.

```bash
# User runs:
prisn run script.py

# prisn:
# 1. Scans for requirements.txt / pyproject.toml
# 2. Parses imports from script
# 3. Creates/caches virtual environment
# 4. Runs in isolated venv
```

**Implementation**: Per-deployment isolated environments, hashed by dependencies.

### Decision 2: Kill the Dangerous Syntax

**Removed**:
- `prisn scale app *1.5` - shell escapes, use `x1.5` or `--multiply 1.5`
- `prisn scale app -1` - looks like flag, use `--add -1` or `sub:1`
- `prisn scale app 00` - too dangerous, use `prisn delete deploy app`

**Kept (but safer)**:
- `prisn scale app 5` - absolute, unambiguous
- `prisn scale app 0` - pause (scale to 0)
- `prisn scale app enough` - auto-scale, but show calculation first

### Decision 3: Fork, Don't Pool

**Old approach**: Warm interpreter pools with state reset.

**New approach**: Fork-based isolation.

```
1. Start interpreter, load common imports
2. fork() before each execution
3. Child runs script
4. Child exits (all state gone)
5. Parent remains warm for next fork
```

This is slower (~100ms vs ~50ms) but **correct**.

### Decision 4: Single-Node First, Cluster Later

**Old approach**: Raft from day one.

**New approach**:
- v0.1: Single-node, SQLite, dead simple
- v0.2: Optional external state (Postgres, etcd)
- v0.3: Built-in clustering (if still needed)

Users who need HA can use external Postgres. Most users don't need HA.

### Decision 5: Be Honest About Platform Support

**Tier 1 (Full support)**: Linux amd64/arm64
- Process isolation via namespaces/cgroups
- Full resource limits
- Network isolation

**Tier 2 (Basic support)**: macOS, FreeBSD
- Process groups, ulimits
- Best-effort resource limits
- Warning on platform-specific code

**Tier 3 (CLI only)**: Windows
- Can deploy TO Linux nodes
- Cannot BE a worker node

### Decision 6: Config Format = TOML (Not YAML)

YAML is error-prone. TOML is simpler.

```toml
# prisn.toml - deployment config
name = "my-api"
runtime = "python3.11"
source = "api.py"
port = 8080
replicas = 3

[resources]
cpu = "500m"
memory = "512Mi"

[env]
DATABASE_URL = { secret = "db-creds", key = "url" }
```

CLI still uses flags for simple cases. TOML for complex deployments.

## What We're Actually Building

### The Honest Tagline

> **prisn**: Run scripts anywhere. Dependencies handled. Complexity hidden.

### The Actual UX

```bash
# Simple script (stdlib only)
prisn run script.py
# Runs immediately in isolated process

# Script with dependencies
prisn run ml_script.py
# Detects requirements.txt, builds venv, caches, runs
# First run: ~5s (building env)
# Subsequent: ~200ms (cached env)

# Long-running service
prisn deploy api.py --port 8080
# Same dependency handling, plus service discovery

# Scale (safe syntax only)
prisn scale api 5              # exact
prisn scale api --add 2        # relative (safe flag)
prisn scale api --multiply 1.5 # multiplicative (safe flag)
prisn scale api --auto         # auto-scale, shows calculation

# Delete (explicit, with confirmation)
prisn delete deploy api
# "Are you sure? Type 'api' to confirm: "
```

### The Execution Model

```
┌─────────────────────────────────────────────────────────────┐
│                     User: prisn run script.py               │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│ 1. Dependency Resolution                                    │
│    - Scan for requirements.txt / pyproject.toml             │
│    - Parse imports from script                              │
│    - Hash dependencies -> cache key                         │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. Environment Setup                                        │
│    - If cache hit: use existing venv                        │
│    - If cache miss: create venv, pip install, cache         │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Execution (Fork-based)                                   │
│    - Warm interpreter with venv activated                   │
│    - fork()                                                 │
│    - Child: exec script, capture output                     │
│    - Parent: wait, return result                            │
└─────────────────────────────────────────────────────────────┘
```

## Implementation Plan

### Phase 1: Core Runner (Week 1-2)

1. **Dependency detection**
   - Parse `requirements.txt`
   - Parse `pyproject.toml`
   - Scan Python imports
   - Hash -> cache key

2. **Environment management**
   - Create venvs per dependency hash
   - Cache in `~/.prisn/envs/`
   - Garbage collect old envs

3. **Fork-based execution**
   - Pre-fork interpreter with venv
   - Fork per execution
   - Capture stdout/stderr
   - Resource limits via rlimit

### Phase 2: CLI (Week 2-3)

4. **Safe CLI syntax**
   - Remove dangerous operators
   - Add `--add`, `--multiply`, `--auto` flags
   - Confirmation for destructive ops
   - Show namespace in all output

5. **Progressive complexity**
   - `prisn run script.py` (simple)
   - `prisn run script.py --env KEY=val` (with env)
   - `prisn deploy api.py --port 8080` (service)
   - `prisn apply -f prisn.toml` (full config)

### Phase 3: Single-Node Server (Week 3-4)

6. **State storage**
   - SQLite for deployments, jobs, cron
   - File-based log storage
   - Prometheus metrics

7. **Job scheduling**
   - In-process scheduler
   - Cron parser
   - Retry logic

8. **Networking**
   - Built-in reverse proxy
   - Health checks
   - Simple DNS (mDNS or hosts file)

### Phase 4: Production Hardening (Week 4-5)

9. **Resource isolation**
   - Linux: cgroups v2
   - macOS: ulimit (with warnings)
   - Process groups for cleanup

10. **Observability**
    - Structured logging
    - Metrics endpoint
    - Event streaming

### Phase 5: Optional Clustering (Future)

11. **External state backend**
    - Postgres adapter
    - etcd adapter

12. **Multi-node (if needed)**
    - Raft for coordination
    - But most users won't need this

## File Changes

### New Files
- `pkg/deps/` - Dependency detection and resolution
- `pkg/venv/` - Virtual environment management
- `pkg/fork/` - Fork-based execution
- `pkg/config/` - TOML config parsing

### Modified Files
- `pkg/cli/root.go` - Remove dangerous syntax
- `pkg/executor/` - Replace warm pools with fork
- `docs/` - Update all docs with new model

### Removed Concepts
- Warm interpreter pools (replaced by fork)
- Complex scaling syntax (`*`, `-N`)
- `00` for delete
- Raft (for now)

## Success Criteria

1. `prisn run script.py` works with dependencies
2. Cold start < 5s (with dependency install)
3. Warm start < 200ms (cached env)
4. No state corruption between executions
5. Clear error messages with fix suggestions
6. Single-node works perfectly before any clustering

## The Promise We Can Keep

> **prisn**: Your script runs. Dependencies install automatically.
> You don't need to understand containers, Kubernetes, or distributed systems.
> It just works.
