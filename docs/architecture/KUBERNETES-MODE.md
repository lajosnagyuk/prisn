# Kubernetes Mode

prisn can operate in three modes:
- **Local**: Run scripts directly on the local machine
- **Remote**: Connect to a prisn API server
- **Kubernetes**: Create CRDs in a Kubernetes cluster

This document covers Kubernetes mode, where the CLI creates PrisnApp/PrisnJob/PrisnCronJob CRDs that are managed by the prisn operator.

## How It Works

```
┌─────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  prisn CLI  │────>│  Kubernetes API │────>│ prisn Operator  │
│             │     │                 │     │                 │
│  Creates:   │     │  Stores CRDs    │     │  Reconciles:    │
│  - CRDs     │     │                 │     │  - Deployments  │
│  - ConfigMaps│     │                 │     │  - Services     │
└─────────────┘     └─────────────────┘     │  - ConfigMaps   │
                                            └─────────────────┘
```

1. CLI reads the script source and creates a ConfigMap with the code
2. CLI creates a PrisnApp/PrisnJob/PrisnCronJob CRD referencing the ConfigMap
3. The operator watches for CRDs and creates the underlying Kubernetes resources
4. Workloads run in pods with proper isolation

## Setting Up Kubernetes Mode

### 1. Install the Operator

```bash
# Install CRDs
kubectl apply -f operator/config/crd/bases/

# Deploy the operator
kubectl apply -f operator/config/manager/
```

### 2. Create a Kubernetes Context

```bash
# Use the current kubeconfig context
prisn context add k8s --mode kubernetes --namespace default

# Or specify a particular kubeconfig context
prisn context add prod-k8s --mode kubernetes --kube-context prod-cluster --namespace production

# Switch to the context
prisn context use k8s
```

### 3. Deploy Applications

```bash
# Deploy a web service
prisn deploy api.py --port 8080 --replicas 3

# Deploy a background worker
prisn deploy worker.py --replicas 2

# Deploy a cron job
prisn deploy backup.py --schedule "0 2 * * *"
```

## CLI Commands in Kubernetes Mode

All standard prisn commands work in Kubernetes mode:

| Command | What It Does |
|---------|--------------|
| `prisn deploy <source>` | Creates PrisnApp or PrisnCronJob CRD |
| `prisn scale <name> <n>` | Updates PrisnApp replicas |
| `prisn get apps` | Lists PrisnApps in the namespace |
| `prisn delete <name>` | Deletes the PrisnApp/CronJob |
| `prisn logs <name>` | Streams pod logs |

## How Source Code Is Handled

When you deploy a script, the CLI:

1. Reads the source file(s)
2. Creates a ConfigMap named `<app-name>-code` containing the script
3. Creates a PrisnApp that references this ConfigMap

```yaml
# ConfigMap created by CLI
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-api-code
  labels:
    app.kubernetes.io/managed-by: prisn
data:
  main.py: |
    from flask import Flask
    app = Flask(__name__)

    @app.route('/health')
    def health():
        return 'OK'
```

```yaml
# PrisnApp CRD created by CLI
apiVersion: prisn.io/v1alpha1
kind: PrisnApp
metadata:
  name: my-api
spec:
  source:
    configMapRef:
      name: my-api-code
    entrypoint: main.py
  runtime: python3
  port: 8080
  replicas: 3
```

The operator then creates:
- A Deployment with pods running the script
- A Service exposing the port
- Optionally, an Ingress for external access

## Context Resolution

Context mode is determined by the `mode` field:

```toml
# ~/.prisn/config.toml
current_context = "k8s"

[contexts.k8s]
name = "k8s"
mode = "kubernetes"
kube_context = "minikube"  # optional: uses current kubeconfig context if empty
namespace = "default"

[contexts.prod]
name = "prod"
mode = "remote"
server = "prod.example.com:7331"
namespace = "production"
token = "sk_live_..."
```

Resolution priority:
1. `--context` flag
2. `PRISN_CONTEXT` environment variable
3. `current_context` in config file
4. Default to local mode

## Environment Variables

| Variable | Description |
|----------|-------------|
| `KUBECONFIG` | Path to kubeconfig file (default: `~/.kube/config`) |
| `PRISN_CONTEXT` | Override the current context |
| `PRISN_NAMESPACE` | Override the namespace |

## Architecture Comparison

| Feature | Local Mode | Remote Mode | Kubernetes Mode |
|---------|------------|-------------|-----------------|
| Isolation | Process namespaces | Pods on server | Pods in cluster |
| Scaling | Single instance | Via API server | Via operator |
| State | Local SQLite | Server SQLite | CRDs in etcd |
| Dependencies | Local venvs | Server venvs | Container image |
| Networking | localhost | Server network | K8s service mesh |

## Operator CRDs

The operator manages three custom resources:

### PrisnApp

Long-running services (HTTP APIs, workers, etc.)

```yaml
apiVersion: prisn.io/v1alpha1
kind: PrisnApp
spec:
  source:
    inline: |      # or configMapRef, secretRef, git
      print("Hello")
  runtime: python3  # python3, node, bash
  port: 8080
  replicas: 3
  env:
    - name: ENV
      value: production
  resources:
    cpu: "500m"
    memory: "256Mi"
  storage:
    - name: data
      mountPath: /data
      size: 1Gi
      storageClassName: standard
```

### PrisnJob

One-time script execution.

```yaml
apiVersion: prisn.io/v1alpha1
kind: PrisnJob
spec:
  source:
    configMapRef:
      name: backup-script
  runtime: bash
  timeout: 1h
  backoffLimit: 3
```

### PrisnCronJob

Scheduled script execution.

```yaml
apiVersion: prisn.io/v1alpha1
kind: PrisnCronJob
spec:
  schedule: "0 2 * * *"  # 2 AM daily
  jobTemplate:
    source:
      configMapRef:
        name: nightly-report
    runtime: python3
  concurrencyPolicy: Forbid
```

## Cold Start Performance

The operator uses several optimizations to reduce cold start time:

1. **Startup Probes**: Check every 1 second during startup (instead of waiting for initial delay)
2. **Python Flags**: Uses `python3 -u -B` (unbuffered output, no bytecode compilation)
3. **Slim Base Images**: Uses `python:3.11-slim` instead of full images

Typical cold start times:
- Image cached: 6-8 seconds
- Image not cached: 20-30 seconds (depends on image pull speed)

## Storage

For apps that need persistent storage, use the `storage` field:

```bash
# Deploy with persistent storage
prisn deploy app.py --storage data=/data:1Gi
```

For multi-replica apps, the storage class must support ReadWriteMany (RWX):
- Azure: `azurefile`
- AWS: `efs`
- On-prem: NFS-based storage classes

## Troubleshooting

### Check Operator Logs

```bash
kubectl logs -n prisn-system deployment/prisn-operator-controller-manager
```

### Check PrisnApp Status

```bash
kubectl describe prisnapp my-api
```

### Check Pod Events

```bash
kubectl get events --field-selector involvedObject.name=my-api
```

### Common Issues

1. **"context not found"**: Run `prisn context add` with the correct mode
2. **"failed to create K8s client"**: Check KUBECONFIG and cluster connectivity
3. **"CRD not found"**: Install the CRDs with `kubectl apply -f operator/config/crd/bases/`
4. **Pods stuck in Pending**: Check node resources and storage class availability
