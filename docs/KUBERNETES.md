# Kubernetes Deployment

prisn provides a native Kubernetes operator for running scripts and services in any K8s cluster.

## Quick Start

### Install the Operator

```bash
# Clone and install CRDs + operator
git clone https://github.com/lajosnagyuk/prisn.git
kubectl apply -k prisn/operator/config/default

# Verify installation
kubectl get pods -n prisn-system
kubectl get crds | grep prisn
```

The operator image defaults to `ghcr.io/lajosnagyuk/prisn-operator:latest`. To use
a custom image (e.g., from a private registry), override after install:

```bash
kubectl -n prisn-system set image deployment/prisn-operator \
  manager=your-registry.example.com/prisn-operator:v0.1.0
```

### Deploy Your First App

```yaml
# hello.yaml
apiVersion: prisn.io/v1alpha1
kind: PrisnApp
metadata:
  name: hello
spec:
  runtime: python3
  replicas: 2
  port: 8080
  source:
    inline: |
      from http.server import HTTPServer, BaseHTTPRequestHandler
      class Handler(BaseHTTPRequestHandler):
          def do_GET(self):
              self.send_response(200)
              self.end_headers()
              self.wfile.write(b"Hello from prisn!")
      HTTPServer(('', 8080), Handler).serve_forever()
  healthCheck:
    path: /
```

```bash
kubectl apply -f hello.yaml
kubectl get prisnapps
kubectl get pods -l prisn.io/app=hello
```

## Custom Resource Definitions

### PrisnApp

Long-running services (HTTP APIs, workers, background processes).

```yaml
apiVersion: prisn.io/v1alpha1
kind: PrisnApp
metadata:
  name: my-api
spec:
  # Runtime: python, python3, node, bash
  runtime: python3

  # Number of replicas
  replicas: 3

  # Port for HTTP services (creates Service + optional Ingress)
  port: 8080

  # Source code (choose one)
  source:
    # Option 1: Inline code
    inline: |
      print("Hello!")

    # Option 2: From ConfigMap
    configMapRef:
      name: my-source

    # Option 3: From Secret (for private code)
    secretRef:
      name: my-private-source

    # Option 4: From Git repository
    git:
      url: https://github.com/example/repo.git
      ref: main
      path: src/
      secretRef:
        name: git-credentials  # Optional

    # Entrypoint script name
    entrypoint: main.py

  # Environment variables
  env:
    - name: LOG_LEVEL
      value: info
    - name: API_KEY
      valueFrom:
        secretKeyRef:
          name: api-secrets
          key: key

  # Environment from ConfigMap/Secret
  envFrom:
    - configMapRef:
        name: app-config
    - secretRef:
        name: app-secrets

  # Resource limits
  resources:
    cpu: "500m"
    memory: "256Mi"
    requests:
      cpu: "100m"
      memory: "128Mi"

  # Health check (required for port-based services)
  healthCheck:
    path: /health
    initialDelaySeconds: 5
    periodSeconds: 10
    failureThreshold: 3

  # Persistent storage
  storage:
    - name: data
      mountPath: /data
      size: 10Gi
      storageClassName: standard
      accessMode: ReadWriteMany

  # Ingress configuration
  ingress:
    enabled: true
    host: api.example.com
    path: /
    tls: true
    ingressClassName: nginx
    annotations:
      nginx.ingress.kubernetes.io/ssl-redirect: "true"

  # Pod scheduling
  nodeSelector:
    node-type: compute
  tolerations:
    - key: dedicated
      value: prisn
      effect: NoSchedule

  # Pause without deleting
  suspend: false
```

### PrisnJob

One-time job execution.

```yaml
apiVersion: prisn.io/v1alpha1
kind: PrisnJob
metadata:
  name: data-migration
spec:
  runtime: python3
  source:
    inline: |
      print("Running migration...")
      # migration logic
      print("Done!")
    entrypoint: script

  # Arguments passed to script
  args:
    - "--dry-run"
    - "--verbose"

  # Maximum execution time
  timeout: 30m

  # Retry count before marking failed
  backoffLimit: 3

  # Auto-delete after completion (seconds)
  ttlSecondsAfterFinished: 3600

  # Resources
  resources:
    cpu: "1"
    memory: "512Mi"

  # Environment
  env:
    - name: DATABASE_URL
      valueFrom:
        secretKeyRef:
          name: db-creds
          key: url
```

### PrisnCronJob

Scheduled job execution.

```yaml
apiVersion: prisn.io/v1alpha1
kind: PrisnCronJob
metadata:
  name: daily-backup
spec:
  # Cron schedule (or @hourly, @daily, @weekly)
  schedule: "0 3 * * *"

  # Concurrency policy: Allow, Forbid, Replace
  concurrencyPolicy: Forbid

  # Deadline for starting missed jobs
  startingDeadlineSeconds: 3600

  # History limits
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 1

  # Pause scheduling
  suspend: false

  # Job template (same as PrisnJob spec)
  jobTemplate:
    runtime: bash
    source:
      inline: |
        #!/bin/bash
        pg_dump $DATABASE_URL | gzip > backup.sql.gz
        aws s3 cp backup.sql.gz s3://backups/$(date +%Y%m%d).sql.gz
      entrypoint: script
    timeout: 1h
    resources:
      cpu: "500m"
      memory: "256Mi"
```

## Source Options

### Inline Code

Best for simple scripts:

```yaml
source:
  inline: |
    from http.server import HTTPServer, BaseHTTPRequestHandler
    # ... your code
  entrypoint: script
```

### ConfigMap

For code stored in ConfigMaps:

```yaml
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: my-app-source
data:
  main.py: |
    print("Hello!")
  utils.py: |
    def helper():
        return "helped"
---
apiVersion: prisn.io/v1alpha1
kind: PrisnApp
metadata:
  name: my-app
spec:
  source:
    configMapRef:
      name: my-app-source
    entrypoint: main.py
```

### Git Repository

For code in git (supports private repos):

```yaml
# For private repos, create a secret:
---
apiVersion: v1
kind: Secret
metadata:
  name: git-creds
type: Opaque
stringData:
  username: my-user
  password: ghp_xxxxxxxxxxxx  # GitHub PAT
---
apiVersion: prisn.io/v1alpha1
kind: PrisnApp
metadata:
  name: my-app
spec:
  source:
    git:
      url: https://github.com/org/repo.git
      ref: main
      path: src/app
      secretRef:
        name: git-creds
    entrypoint: server.py
```

## Persistent Storage

### Simple Storage

```yaml
spec:
  storage:
    - name: data
      mountPath: /data
      size: 10Gi
```

### With Storage Class

```yaml
spec:
  storage:
    - name: data
      mountPath: /data
      size: 10Gi
      storageClassName: fast-ssd
      accessMode: ReadWriteMany  # Required for replicas > 1
```

### Using Existing PVC

```yaml
spec:
  storage:
    - name: data
      mountPath: /data
      existingClaim: my-existing-pvc
      subPath: app-data  # Optional: mount subdirectory
```

## Ingress and TLS

### Basic Ingress

```yaml
spec:
  port: 8080
  ingress:
    enabled: true
    host: api.example.com
```

### With TLS (cert-manager)

```yaml
spec:
  port: 8080
  ingress:
    enabled: true
    host: api.example.com
    tls: true
    annotations:
      cert-manager.io/cluster-issuer: letsencrypt-prod
```

### Custom Ingress Class

```yaml
spec:
  port: 8080
  ingress:
    enabled: true
    host: api.example.com
    ingressClassName: traefik
    annotations:
      traefik.ingress.kubernetes.io/router.middlewares: default-strip-prefix@kubernetescrd
```

## Operations

### Check Status

```bash
# List all prisn resources
kubectl get prisnapps,prisnjobs,prisncronjobs

# Describe specific app
kubectl describe prisnapp my-api

# Check events
kubectl get events --field-selector involvedObject.name=my-api
```

### View Logs

```bash
# Follow logs
kubectl logs -l prisn.io/app=my-api -f

# Logs from specific pod
kubectl logs my-api-app-xxxxx-yyy
```

### Scale

```bash
# Scale via kubectl
kubectl scale prisnapp my-api --replicas=5

# Or edit the resource
kubectl edit prisnapp my-api
```

### Suspend/Resume

```bash
# Suspend (scales to 0, preserves config)
kubectl patch prisnapp my-api --type=merge -p '{"spec":{"suspend":true}}'

# Resume
kubectl patch prisnapp my-api --type=merge -p '{"spec":{"suspend":false}}'
```

## Troubleshooting

### Pod Not Starting

```bash
# Check pod status
kubectl describe pod -l prisn.io/app=my-api

# Common issues:
# - ImagePullBackOff: Check image exists and pull secrets
# - CrashLoopBackOff: Check logs for script errors
# - Pending: Check resource requests vs available resources
```

### Health Check Failing

```bash
# Check probe configuration
kubectl get prisnapp my-api -o yaml | grep -A10 healthCheck

# Test endpoint manually
kubectl exec -it my-api-app-xxx -- curl localhost:8080/health
```

### Storage Issues

```bash
# Check PVC status
kubectl get pvc -l prisn.io/app=my-api

# Check events
kubectl describe pvc my-api-storage-data
```

## Uninstall

```bash
# Delete all prisn resources in namespace
kubectl delete prisnapps,prisnjobs,prisncronjobs --all

# Uninstall operator
kubectl delete -k github.com/lajosnagyuk/prisn/operator/config/default
```

## Examples

See the [samples directory](https://github.com/lajosnagyuk/prisn/tree/main/operator/config/samples) for complete examples:

- `prisnapp_sample.yaml` - HTTP servers, workers, ConfigMap sources
- `prisnjob_sample.yaml` - Migrations, backups, ETL jobs
- `prisncronjob_sample.yaml` - Scheduled tasks, health monitors
- `prisnapp_storage_sample.yaml` - Persistent storage patterns
