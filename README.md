# prisn

**Just run my shit.**

One binary. Any language. Dependencies handled. Complexity hidden.

```bash
prisn run script.py              # Run once (deps auto-installed)
prisn deploy api.py --port 8080  # Deploy as service
prisn scale api 5                # Scale to 5 replicas
```

## Install

```bash
# From source
go install github.com/lajosnagyuk/prisn/cmd/prisn@latest

# Or download from releases
# https://github.com/lajosnagyuk/prisn/releases
```

## Quick Start

### Run a script

```bash
# Python (dependencies auto-detected from requirements.txt)
prisn run backup.py

# With arguments
prisn run process.py -- --input data.csv

# With environment variables
prisn run script.py -e API_KEY=secret -e DEBUG=true
```

### Deploy a service

```bash
# Deploy a web service
prisn deploy api.py --port 8080

# Check status
prisn status api

# View logs
prisn logs api -f

# Scale up
prisn scale api 3
```

### Schedule a job

```bash
# Run every hour using deploy with --schedule
prisn deploy backup.sh --name backup --schedule "0 * * * *"

# Or use a config file
cat > prisn.toml << 'EOF'
[job.backup]
source = "backup.sh"
schedule = "0 * * * *"

[job.backup.env]
S3_BUCKET = "my-backups"
EOF

prisn apply -f prisn.toml
```

## Configuration

prisn works without config files, but for anything beyond a single script:

**`prisn.toml`**
```toml
[service.api]
source = "api.py"
port = 8080
replicas = 3

[service.api.env]
LOG_LEVEL = "info"
DATABASE_URL = { secret = "db-creds", key = "url" }

[job.backup]
source = "backup.sh"
schedule = "0 3 * * *"
```

```bash
prisn apply -f prisn.toml  # Deploy everything from config
prisn status               # Check all services
```

## Deployment Modes

prisn runs anywhere - single machine, server cluster, or Kubernetes.

### 1. Local Mode (default)

```bash
# Just run it
prisn run script.py
prisn deploy api.py --port 8080
```

### 2. Server Mode (on-prem, teams)

```bash
# Start server
prisn server --bind :7331 --db /var/lib/prisn/state.db

# Connect from anywhere
prisn context add prod --server prod.example.com:7331
prisn -c prod deploy api.py --port 8080
```

### 3. Clustered Mode (HA, production)

```bash
# Node 1 (bootstrap)
prisn server --cluster --raft-addr :7332 --bootstrap

# Node 2
prisn server --cluster --raft-addr :7332 --peers node1:7332

# Node 3
prisn server --cluster --raft-addr :7332 --peers node1:7332,node2:7332
```

Raft consensus ensures state is replicated across all nodes. Leader handles writes, followers serve reads.

### 4. Kubernetes Operator

For Kubernetes-native deployment, use the prisn operator:

```bash
# Install CRDs and operator
kubectl apply -k github.com/lajosnagyuk/prisn/operator/config/default

# Deploy an app
kubectl apply -f - << 'EOF'
apiVersion: prisn.io/v1alpha1
kind: PrisnApp
metadata:
  name: hello-api
spec:
  runtime: python3
  replicas: 3
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
EOF

# Check status
kubectl get prisnapps
kubectl get pods -l prisn.io/app=hello-api
```

See [Kubernetes Deployment](docs/KUBERNETES.md) for full details.

### 5. Kubernetes Script Runner (helmfile/bootstrapping)

prisn works as a general-purpose "just get it done" layer in Kubernetes. Instead of
building bespoke operators or init containers for every bit of glue logic, throw
Python (or any language) at prisn and let it handle the rest.

**Use case**: Your helmfile needs custom logic between chart installs - generating
RBAC configs, seeding databases, calling external APIs, running migrations. Instead
of a purpose-built tool for each, write the script and let prisn run it:

```bash
# Install the operator
kubectl apply -k github.com/lajosnagyuk/prisn/operator/config/default

# Run a one-shot script as a K8s Job
kubectl apply -f - << 'EOF'
apiVersion: prisn.io/v1alpha1
kind: PrisnJob
metadata:
  name: setup-rbac
spec:
  runtime: python3
  source:
    configMap: bootstrap-scripts
    key: setup_rbac.py
  env:
    - name: CLUSTER_NAME
      value: "production"
    - name: ADMIN_EMAIL
      valueFrom:
        secretKeyRef:
          name: platform-secrets
          key: admin-email
EOF
```

The script itself is plain Python - no SDK, no framework:

```python
# setup_rbac.py - stored in a ConfigMap
import json, os, urllib.request

cluster = os.environ["CLUSTER_NAME"]
admin = os.environ["ADMIN_EMAIL"]

# Generate role bindings based on your logic
roles = {
    "admin": {"namespaces": ["*"], "verbs": ["*"]},
    "developer": {"namespaces": [f"{cluster}-dev", f"{cluster}-staging"], "verbs": ["get", "list", "create"]},
}

for role_name, perms in roles.items():
    print(f"Configuring {role_name}: {perms['namespaces']}")
    # ... apply via kubernetes API, write files, call external IdP, whatever you need
```

**In a helmfile workflow:**

```yaml
# helmfile.yaml
releases:
  - name: prisn-operator
    chart: oci://ghcr.io/lajosnagyuk/prisn/charts/prisn-operator

  - name: bootstrap
    chart: ./charts/bootstrap
    needs: [prisn-operator]
    # This chart just contains PrisnJob CRs that point at your scripts
    # No custom Docker images. No bespoke controllers. Just Python.

  - name: platform
    chart: ./charts/platform
    needs: [bootstrap]
```

This replaces the pattern of building a "thing-doer" container image for every piece
of glue logic. Write your Python/Node/Bash, store it in a ConfigMap or git repo,
and prisn handles dependencies, isolation, and execution.

## Features

| Feature | Local | Server | Cluster | K8s Operator |
|---------|-------|--------|---------|--------------|
| Run scripts | Yes | Yes | Yes | Yes |
| Deploy services | Yes | Yes | Yes | Yes |
| Cron jobs | Yes | Yes | Yes | Yes (CronJob) |
| Auto-scaling | - | - | - | Yes (HPA) |
| Health checks | - | Yes | Yes | Yes |
| Log aggregation | - | Yes | Yes | K8s native |
| HA/Failover | - | - | Yes | K8s native |
| Git source | - | - | - | Yes |
| Persistent storage | - | - | - | Yes (PVC) |
| Ingress/TLS | - | - | - | Yes |

## How It Works

1. **Dependency detection**: Scans requirements.txt, package.json, or imports
2. **Environment setup**: Creates isolated venv/node_modules, cached by hash
3. **Execution**: Fork-based isolation (no warm pool state corruption)
4. **Scaling**: Process supervision with restart policies

No containers required. No Kubernetes knowledge needed. Just your code.

## Examples

### Python API

```python
# api.py
from flask import Flask
app = Flask(__name__)

@app.route('/health')
def health():
    return {'status': 'ok'}

@app.route('/')
def hello():
    return 'Hello from prisn!'

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=8080)
```

```bash
prisn deploy api.py --port 8080
```

### Node.js Worker

```javascript
// worker.js
const queue = require('./queue');

async function processJobs() {
  while (true) {
    const job = await queue.pop();
    console.log('Processing:', job);
    // ... process job
  }
}

processJobs();
```

```bash
prisn deploy worker.js --replicas 5
```

### Bash Script

```bash
#!/bin/bash
# backup.sh
pg_dump $DATABASE_URL | gzip > backup-$(date +%Y%m%d).sql.gz
aws s3 cp backup-*.sql.gz s3://my-backups/
```

```bash
prisn deploy backup.sh --name backup --schedule "0 3 * * *" -e DATABASE_URL=$DB -e AWS_ACCESS_KEY_ID=$KEY
```

## Documentation

- **[Quickstart](docs/QUICKSTART.md)** - Get running in 5 minutes
- **[CLI Reference](docs/CLI.md)** - All commands and flags
- **[Configuration](docs/CONFIGURATION.md)** - prisn.toml format
- **[Server Mode](docs/SERVER.md)** - Running prisn server
- **[Clustering](docs/CLUSTERING.md)** - High availability setup
- **[Kubernetes](docs/KUBERNETES.md)** - K8s operator deployment
- **[Examples](docs/examples/)** - Real-world use cases

## Architecture

```
                       CLI Commands
                            |
                            v
        +-------------------+-------------------+
        |                   |                   |
        v                   v                   v
   Local Mode          Server Mode         K8s Operator
   (direct exec)       (HTTP API)          (CRDs)
        |                   |                   |
        v                   v                   v
   +----------+        +----------+        +----------+
   | Runner   |        | Raft     |        | K8s API  |
   | (fork)   |        | Cluster  |        | Server   |
   +----------+        +----------+        +----------+
        |                   |                   |
        v                   v                   v
   Process             Process             Deployment
   Supervisor          Supervisor          + Service
```

## Building from Source

```bash
# Clone
git clone https://github.com/lajosnagyuk/prisn.git
cd prisn

# Build CLI
go build -o prisn ./cmd/prisn

# Build operator
cd operator && make build

# Run tests
go test ./...
```

## Contributing

Contributions welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md).

## License

MIT - see [LICENSE](LICENSE)
