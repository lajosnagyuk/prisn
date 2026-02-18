# Contributing to prisn

Thanks for your interest in contributing to prisn. This document covers the basics you need to get started.

## Getting Started

### Prerequisites

- Go 1.24+
- Python 3.8+ (for testing script execution)
- Docker (for building operator images)
- kubectl + a Kubernetes cluster (for operator development)

### Clone and Build

```bash
git clone https://github.com/lajosnagyuk/prisn.git
cd prisn

# Build the CLI
go build -o prisn ./cmd/prisn

# Run tests
go test ./...

# Build the operator
cd operator && go build -o ../bin/prisn-operator ./cmd/
```

### Project Layout

```
prisn/
├── cmd/prisn/              # CLI entry point
├── pkg/
│   ├── cli/                # CLI commands (cobra)
│   ├── api/                # HTTP API server
│   ├── runner/             # Process supervisor, fork-based execution
│   ├── runtime/            # Script runner, dependency detection, environments
│   ├── deps/               # Dependency detection (requirements.txt, imports, etc.)
│   ├── venv/               # Virtual environment management and caching
│   ├── layer/              # Layer-based environment composition
│   ├── sandbox/            # Resource isolation (cgroups, ulimits)
│   ├── raft/               # Raft consensus for clustering
│   ├── manifest/           # YAML/TOML manifest parsing
│   └── supervisor/         # Process lifecycle management
├── operator/
│   ├── api/v1alpha1/       # CRD type definitions
│   ├── controllers/        # Reconcilers (PrisnApp, PrisnJob, PrisnCronJob)
│   ├── cmd/                # Operator entry point
│   └── config/             # Kustomize manifests, CRDs, RBAC
├── charts/prisn-operator/  # Helm chart
└── docs/                   # Documentation
```

## Making Changes

### Before You Start

1. Check [existing issues](https://github.com/lajosnagyuk/prisn/issues) to avoid duplicating work
2. For larger changes, open an issue first to discuss the approach
3. For bug fixes and small improvements, go ahead and open a PR

### Development Workflow

```bash
# Create a feature branch
git checkout -b feature/your-change

# Make changes, run tests
go test ./...

# Build and smoke test
go build -o prisn ./cmd/prisn
./prisn run examples/hello.py

# Commit with a descriptive message
git commit -m "Add support for X"

# Push and open a PR
git push origin feature/your-change
```

### Running Tests

```bash
# All tests
go test ./...

# Specific package
go test ./pkg/runner/...
go test ./pkg/raft/...

# With verbose output
go test -v ./pkg/runtime/...

# Race detector
go test -race ./...
```

### Testing the Operator

```bash
# Build operator image
cd operator
docker build -t prisn-operator:dev -f Dockerfile ..

# Load into local cluster (kind, minikube, etc.)
kind load docker-image prisn-operator:dev

# Install CRDs and deploy
kubectl apply -k config/default

# Override image to use your local build
kubectl -n prisn-system set image deployment/prisn-operator \
  manager=prisn-operator:dev

# Test with a sample resource
kubectl apply -f config/samples/prisnapp_sample.yaml
```

### Testing the Helm Chart

```bash
# Lint
helm lint charts/prisn-operator

# Template dry-run
helm template prisn-operator charts/prisn-operator --namespace prisn-system

# Install
helm install prisn-operator charts/prisn-operator \
  --namespace prisn-system --create-namespace \
  --set image.repository=prisn-operator \
  --set image.tag=dev
```

## Code Guidelines

### Go

- Use the standard library where possible. Avoid pulling in dependencies for things Go already does.
- Wrap errors with context: `fmt.Errorf("failed to create venv: %w", err)`
- Use structured logging via controller-runtime's logger (operator) or the standard `log/slog` package (CLI).
- Keep functions short. If a function needs a comment explaining what it does, it might need to be split up or renamed.
- No blank error handling. If you're ignoring an error, comment why.

### Tests

- Write table-driven tests where it makes sense.
- Test behavior, not implementation. If refactoring breaks your test but not the behavior, the test was too coupled.
- Use `t.TempDir()` for filesystem tests, not hardcoded paths.
- Raft tests are inherently timing-sensitive. If you touch `pkg/raft/`, run the tests several times to check for flakiness.

### Kubernetes Operator

- Follow controller-runtime patterns: Reconcile should be idempotent.
- Use `controllerutil.SetControllerReference` so owned resources get garbage collected.
- Update the status subresource to reflect actual state, not desired state.
- Keep CRD changes backward compatible. Don't remove fields, add new ones as optional.
- If you change CRD types in `operator/api/v1alpha1/`, regenerate the CRD YAML and update `charts/prisn-operator/crds/`.

### Commits

- Write short, imperative commit messages: "Add X", "Fix Y", not "Added X" or "This commit fixes Y"
- One logical change per commit. Don't mix a bug fix with a refactor.
- If a commit fixes a bug, describe what was wrong and why the fix is correct.

## What to Work On

Good first contributions:

- **Documentation**: Fix typos, clarify confusing sections, add examples
- **Tests**: Increase coverage, especially for `pkg/runtime/` and `pkg/deps/`
- **Bug fixes**: Check the issue tracker
- **Platform stubs**: `pkg/sandbox/` has stubs for Windows and FreeBSD that need real implementations
- **Runtime support**: Add new runtimes (Ruby, Lua, etc.) to the registry

Bigger items (open an issue first):

- Service mesh / inter-service networking in server mode
- Event-driven triggers
- Custom runtime images for the K8s operator
- Multi-arch operator image builds
- OCI chart publishing for Helm

## Pull Request Checklist

- [ ] Tests pass (`go test ./...`)
- [ ] Code builds (`go build ./...`)
- [ ] Commit messages are clear
- [ ] Docs updated if behavior changed
- [ ] No secrets, credentials, or personal info in the diff
- [ ] CRD YAML regenerated if types changed
- [ ] Helm chart updated if kustomize config changed (keep them in sync)

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
