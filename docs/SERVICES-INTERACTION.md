# Service Interaction in prisn

How deployments interact, how to build systems with multiple services, and how to trigger jobs programmatically.

## Run vs Deploy

| Command | Creates Deployment? | Retriggerable? | Use Case |
|---------|---------------------|----------------|----------|
| `prisn run script.py` | No | No (re-run manually) | One-off scripts, testing |
| `prisn run --detach script.py` | No (creates execution) | No | Background one-off |
| `prisn deploy script.py --name job` | Yes | Yes | Retriggerable jobs |
| `prisn deploy api.py --port 8080` | Yes | N/A (always running) | Long-running services |
| `prisn deploy task.py --schedule "* * * * *"` | Yes | Automatic | Scheduled tasks |

## Retriggering Jobs

### CLI

```bash
# Create a job deployment (no port, no schedule = job type)
prisn deploy backup.py --name backup-job

# Trigger it manually
prisn trigger backup-job

# Or run directly via the API
curl -X POST http://localhost:7331/api/v1/executions \
  -H "Content-Type: application/json" \
  -d '{"deployment_id": "backup-job"}'
```

### API Endpoints

```bash
# List all deployments
GET /api/v1/deployments

# Get specific deployment
GET /api/v1/deployments/{name}

# Create/trigger execution from deployment
POST /api/v1/executions
{
  "deployment_id": "backup-job"
}

# Or run arbitrary code
POST /api/v1/executions
{
  "source": "print('hello')",
  "runtime": "python3"
}

# List execution history
GET /api/v1/executions

# Get execution logs
GET /api/v1/executions/{id}/logs
```

## Environment Variables

Every deployment gets these environment variables automatically:

```bash
PORT=8080                    # For services (the exposed port)
PRISN_DEPLOYMENT=myapp       # Deployment name
PRISN_NAMESPACE=default      # Namespace
```

Plus any custom env vars you specify:
```bash
prisn deploy api.py --port 8080 -e DATABASE_URL=postgres://... -e API_KEY=secret
```

## Building Multi-Service Systems

### Architecture Pattern: Hub and Spoke

```
                    ┌─────────────────┐
                    │  prisn server   │
                    │  (coordinator)  │
                    └────────┬────────┘
                             │
        ┌────────────────────┼────────────────────┐
        │                    │                    │
        ▼                    ▼                    ▼
┌───────────────┐  ┌───────────────┐  ┌───────────────┐
│   api-service │  │ worker-service│  │  cron-jobs    │
│   port: 8080  │  │   port: 8081  │  │   scheduled   │
└───────────────┘  └───────────────┘  └───────────────┘
```

### Example: Data Pipeline

```bash
# 1. Deploy an API service that receives webhooks
prisn deploy webhook-receiver.py --port 8080 --name webhook-api \
  -e QUEUE_SERVICE=http://localhost:8081

# 2. Deploy a worker that processes the queue
prisn deploy queue-worker.py --port 8081 --name queue-worker \
  -e DATABASE_URL=postgres://...

# 3. Deploy a scheduled cleanup job
prisn deploy cleanup.py --schedule "0 3 * * *" --name nightly-cleanup \
  -e DATABASE_URL=postgres://...

# 4. Deploy a job that can be triggered manually
prisn deploy full-reindex.py --name reindex-job \
  -e ELASTICSEARCH_URL=http://localhost:9200
```

### Service Communication

Services communicate via HTTP on their exposed ports:

```python
# webhook-receiver.py
import os
import requests
from http.server import HTTPServer, BaseHTTPRequestHandler

QUEUE_SERVICE = os.environ.get('QUEUE_SERVICE', 'http://localhost:8081')

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        # Receive webhook, forward to queue
        data = self.rfile.read(int(self.headers['Content-Length']))
        requests.post(f"{QUEUE_SERVICE}/enqueue", data=data)
        self.send_response(202)
        self.end_headers()

HTTPServer(('', int(os.environ['PORT'])), Handler).serve_forever()
```

### Triggering Jobs from Services

```python
# Inside a service, trigger another deployment
import requests

PRISN_API = "http://localhost:7331"

def trigger_reindex():
    """Trigger the reindex job via prisn API"""
    resp = requests.post(f"{PRISN_API}/api/v1/executions", json={
        "deployment_id": "reindex-job"
    })
    return resp.json()["id"]  # Returns execution ID

def check_execution(exec_id):
    """Check if execution completed"""
    resp = requests.get(f"{PRISN_API}/api/v1/executions/{exec_id}")
    return resp.json()["status"]  # "Running", "Completed", "Failed"
```

## Service Discovery

Currently prisn uses explicit configuration (environment variables) rather than automatic service discovery. Services find each other via:

1. **Environment variables** - Pass service URLs at deploy time
2. **Consistent ports** - Use predictable ports (8080, 8081, etc.)
3. **API queries** - Query prisn API for deployment info

```python
# Query prisn API for service info
import requests

def get_service_port(name):
    resp = requests.get(f"http://localhost:7331/api/v1/deployments/{name}")
    return resp.json()["port"]
```

### Future: Automatic Discovery

A future version could support automatic service discovery:
```bash
# Hypothetical future syntax
prisn deploy api.py --name myapi --expose myapi.local:8080
# Other services could then reach it at http://myapi.local:8080
```

## Secrets Management

Store secrets and inject them into deployments:

```bash
# Create a secret
prisn secret create db-password --value "supersecret"

# Use in deployment
prisn deploy api.py --port 8080 --name api \
  --secret db-password:DATABASE_PASSWORD

# In your script
import os
db_password = os.environ['DATABASE_PASSWORD']
```

## Monitoring

```bash
# List all deployments and their status
prisn get apps

# Watch deployments in real-time
prisn get apps -w

# Check execution history
prisn get executions

# View logs
prisn logs myservice
prisn logs myservice -f  # Follow/stream

# Prometheus metrics
curl http://localhost:7331/metrics
```

## Complete Example: Event-Driven System

```bash
#!/bin/bash
# deploy-system.sh - Deploy an event-driven data processing system

PRISN_SERVER="localhost:7331"

# Core services
prisn deploy services/api-gateway.py \
  --port 8080 --name api-gateway \
  -e EVENT_PROCESSOR=http://localhost:8081

prisn deploy services/event-processor.py \
  --port 8081 --name event-processor \
  -e DATABASE_URL=$DATABASE_URL \
  -e NOTIFICATION_SERVICE=http://localhost:8082

prisn deploy services/notification-sender.py \
  --port 8082 --name notification-service \
  -e SMTP_HOST=$SMTP_HOST \
  -e SMTP_USER=$SMTP_USER \
  --secret smtp-password:SMTP_PASSWORD

# Scheduled jobs
prisn deploy jobs/daily-report.py \
  --schedule "0 9 * * *" --name daily-report \
  -e REPORT_RECIPIENTS=$REPORT_RECIPIENTS

prisn deploy jobs/cleanup-old-events.py \
  --schedule "0 2 * * *" --name event-cleanup \
  -e RETENTION_DAYS=30

# Manual trigger jobs
prisn deploy jobs/full-reprocess.py \
  --name reprocess-job \
  -e DATABASE_URL=$DATABASE_URL

echo "System deployed. Trigger reprocessing with:"
echo "  curl -X POST http://$PRISN_SERVER/api/v1/executions -d '{\"deployment_id\":\"reprocess-job\"}'"
```

## SDK/Client Libraries

For programmatic access, you can use the HTTP API directly or wrap it:

```python
# prisn_client.py - Simple Python client

import requests
from typing import Optional, Dict, Any

class PrisnClient:
    def __init__(self, server: str = "http://localhost:7331"):
        self.server = server

    def list_deployments(self) -> list:
        resp = requests.get(f"{self.server}/api/v1/deployments")
        resp.raise_for_status()
        return resp.json()["items"]

    def get_deployment(self, name: str) -> dict:
        resp = requests.get(f"{self.server}/api/v1/deployments/{name}")
        resp.raise_for_status()
        return resp.json()

    def trigger(self, deployment_id: str, env: Optional[Dict[str, str]] = None) -> str:
        """Trigger a deployment and return execution ID"""
        payload = {"deployment_id": deployment_id}
        if env:
            payload["env"] = env
        resp = requests.post(f"{self.server}/api/v1/executions", json=payload)
        resp.raise_for_status()
        return resp.json()["id"]

    def wait_for_execution(self, exec_id: str, timeout: int = 300) -> dict:
        """Wait for execution to complete"""
        import time
        deadline = time.time() + timeout
        while time.time() < deadline:
            resp = requests.get(f"{self.server}/api/v1/executions/{exec_id}")
            data = resp.json()
            if data["status"] in ("Completed", "Failed"):
                return data
            time.sleep(1)
        raise TimeoutError(f"Execution {exec_id} didn't complete in {timeout}s")

    def scale(self, name: str, replicas: int):
        resp = requests.post(
            f"{self.server}/api/v1/deployments/{name}/scale",
            json={"replicas": replicas}
        )
        resp.raise_for_status()

# Usage
client = PrisnClient()
exec_id = client.trigger("backup-job")
result = client.wait_for_execution(exec_id)
print(f"Backup completed: {result['status']}")
```
