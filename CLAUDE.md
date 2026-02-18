# prisn Development Guide

**"Just run my shit" - now with dependencies that actually work**

## What is prisn?

prisn is a single-binary serverless platform that runs scripts anywhere. Dependencies are automatically detected and installed. You don't need to understand containers, Kubernetes, or distributed systems.

```bash
prisn run script.py          # Deps auto-installed from requirements.txt
prisn deploy api.py --port 8080  # Long-running service
prisn scale api 5            # Scale to 5 replicas
```

## User Documentation

| Document | Purpose |
|----------|---------|
| **[README.md](README.md)** | Project overview and quick start |
| [docs/QUICKSTART.md](docs/QUICKSTART.md) | Get running in 5 minutes |
| [docs/CLI-REFERENCE.md](docs/CLI-REFERENCE.md) | All commands and flags |
| [docs/CONFIGURATION.md](docs/CONFIGURATION.md) | prisn.toml reference |
| [docs/SECRETS.md](docs/SECRETS.md) | Environment variables and secrets |
| [docs/RBAC.md](docs/RBAC.md) | Token management and access control |
| [docs/examples/EXAMPLES.md](docs/examples/EXAMPLES.md) | Real-world patterns |

## Developer Documentation

| Document | Purpose |
|----------|---------|
| **[docs/RED-TEAM-SYNTHESIS.md](docs/RED-TEAM-SYNTHESIS.md)** | What we changed after red teaming |
| [docs/architecture/OVERVIEW.md](docs/architecture/OVERVIEW.md) | High-level architecture |
| [docs/architecture/EXECUTION-MODEL.md](docs/architecture/EXECUTION-MODEL.md) | How code runs (fork-based) |
| [docs/SYSTEM-CONTRACT.md](docs/SYSTEM-CONTRACT.md) | Definition of done |

## Post-Red-Team Changes

The original architecture was red-teamed by 4 agents. Critical issues found:

### 1. Dependencies Were Missing (FIXED)
The #1 problem with serverless. Now implemented:
- `pkg/deps/` - Detects dependencies from requirements.txt, pyproject.toml, or import scanning
- `pkg/venv/` - Creates and caches virtual environments per dependency hash

### 2. Warm Pools Cause State Corruption (FIXED)
Original design: warm interpreter pools that reset state between runs.
Problem: Python can't truly reset state (C extensions, globals, sys.path pollution).

New design: Fork-based execution:
```
1. Start interpreter with venv
2. fork() before each execution
3. Child runs script
4. Child exits (all state gone)
5. Parent remains warm for next fork
```

### 3. Dangerous CLI Syntax (FIXED)
Removed:
- `*1.5` - shell escaping nightmare
- `-1` - looks like a flag
- `00` - too close to `0`, one typo = delete everything

New syntax:
```bash
prisn scale app 5              # Absolute (safe)
prisn scale app 0              # Pause (safe)
prisn scale app --add 2        # Relative (explicit flag)
prisn scale app --multiply 1.5 # Multiplicative (explicit flag)
prisn scale app --auto         # Auto-scale (shows calculation)
```

### 4. Raft Complexity (DEFERRED)
Single-node first, clustering later. Most users don't need HA.

## Project Structure

```
prisn/
├── CLAUDE.md                         # This file
├── cmd/prisn/main.go                 # Entry point
├── docs/
│   ├── RED-TEAM-SYNTHESIS.md         # What we changed and why
│   ├── architecture/                 # Design docs
│   └── SYSTEM-CONTRACT.md            # Definition of done
├── pkg/
│   ├── cli/root.go                   # CLI commands
│   ├── deps/detect.go                # Dependency detection
│   ├── venv/manager.go               # Virtual environment management
│   ├── runner/runner.go              # Fork-based execution
│   └── executor/                     # Platform-specific isolation
└── go.mod
```

## How Dependencies Work

```
prisn run script.py
       │
       ▼
┌─────────────────────────────┐
│ 1. Dependency Detection     │
│    - Check requirements.txt │
│    - Check pyproject.toml   │
│    - Scan imports           │
└─────────────────────────────┘
       │
       ▼
┌─────────────────────────────┐
│ 2. Environment Setup        │
│    - Hash dependencies      │
│    - Check cache            │
│    - Create venv if miss    │
│    - pip install deps       │
└─────────────────────────────┘
       │
       ▼
┌─────────────────────────────┐
│ 3. Execution                │
│    - fork()                 │
│    - Child runs script      │
│    - Capture output         │
│    - Exit code returned     │
└─────────────────────────────┘
```

## Building and Running

```bash
# Download dependencies
go mod tidy

# Build
go build -o prisn ./cmd/prisn

# Run a script
./prisn run examples/hello.py

# Run with verbose output
./prisn run examples/hello.py -v
```

## Implementation Status

### Done (v0.1.0 release)
- [x] Dependency detection (requirements.txt, pyproject.toml, imports)
- [x] Virtual environment management and caching
- [x] Fork-based execution (replaces warm pools)
- [x] Safe CLI syntax
- [x] SQLite state storage
- [x] HTTP API with full CRUD for deployments, jobs, secrets
- [x] Job scheduler and cron
- [x] RBAC with tokens (admin/developer/viewer roles)
- [x] Context management (multi-server)
- [x] Graceful shutdown and draining
- [x] TOML config parser (prisn.toml)
- [x] Pluggable runtimes (Python, Node, Bash, Ruby, Perl, Deno, Bun, Babashka)
- [x] Raft consensus clustering
- [x] Kubernetes operator (PrisnApp, PrisnJob, PrisnCronJob CRDs)
- [x] Helm chart (charts/prisn-operator/)
- [x] Resource sandboxing (cgroups v2 on Linux)
- [x] Layer-based environment composition

### Deferred (post-release backlog)
- [ ] Networking (service discovery, load balancing)
- [ ] Events/audit log persistence (currently faked from SQLite)
- [ ] Windows support (stub-only, returns "not supported")
- [ ] FreeBSD support (stub-only)
- [ ] Consolidate TOML libraries (BurntSushi/toml + pelletier/go-toml - pick one)
- [ ] prisn.io domain + install script
- [ ] Config validator doesn't know about pluggable runtimes (only python/node/bash)
- [ ] API version consistency (some docs say v1, code uses v1alpha1)
- [ ] Architecture docs partially aspirational (gRPC described but HTTP in practice)

## Coding Standards

- **Go**: Use standard library where possible. No unnecessary dependencies.
- **Errors**: Wrap with context: `fmt.Errorf("failed to X: %w", err)`
- **Logging**: Use structured logging
- **Testing**: Unit tests for all packages

## The Promise We Can Keep

> prisn: Your script runs. Dependencies install automatically.
> You don't need to understand containers, Kubernetes, or distributed systems.
> It just works.

## Repository

- **Module**: `github.com/lajosnagyuk/prisn`
- **License**: MIT
- **Go**: 1.24+
- **Platforms**: Linux (full), macOS/Darwin (full), Windows/FreeBSD (stubs)

---

*Last updated: 2026-02-18 - MIT open source release prep*
