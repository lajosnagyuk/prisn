# Server Mode

prisn server mode provides a centralized API for managing deployments across a team or organization.

## Quick Start

```bash
# Start server
prisn server --bind :7331 --db /var/lib/prisn/state.db

# In another terminal, add context and deploy
prisn context add local --server localhost:7331
prisn deploy api.py --port 8080
```

## Server Configuration

### Basic

```bash
prisn server \
  --bind :7331 \
  --db /var/lib/prisn/state.db
```

### All Options

```bash
prisn server \
  --bind :7331 \                    # HTTP API address
  --db /var/lib/prisn/state.db \    # SQLite database path
  --data-dir /var/lib/prisn \       # Data directory for venvs, logs
  --log-level info                  # Log level: debug, info, warn, error
```

### Environment Variables

```bash
export PRISN_BIND=:7331
export PRISN_DB=/var/lib/prisn/state.db
export PRISN_DATA_DIR=/var/lib/prisn
export PRISN_LOG_LEVEL=info

prisn server
```

## API Endpoints

### Deployments

```bash
# List all deployments
curl http://localhost:7331/api/v1/deployments

# Get specific deployment
curl http://localhost:7331/api/v1/deployments/my-api

# Create deployment
curl -X POST http://localhost:7331/api/v1/deployments \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-api",
    "source": "api.py",
    "runtime": "python3",
    "port": 8080,
    "replicas": 2
  }'

# Scale deployment
curl -X PATCH http://localhost:7331/api/v1/deployments/my-api \
  -H "Content-Type: application/json" \
  -d '{"replicas": 5}'

# Delete deployment
curl -X DELETE http://localhost:7331/api/v1/deployments/my-api
```

### Jobs

```bash
# List jobs
curl http://localhost:7331/api/v1/jobs

# Create job
curl -X POST http://localhost:7331/api/v1/jobs \
  -H "Content-Type: application/json" \
  -d '{
    "name": "backup",
    "source": "backup.sh",
    "runtime": "bash",
    "schedule": "0 3 * * *"
  }'
```

### Health

```bash
# Server health
curl http://localhost:7331/health

# Cluster status (if clustering enabled)
curl http://localhost:7331/api/v1/cluster/status
```

## CLI Context Management

### Add Context

```bash
# Local server
prisn context add local --server localhost:7331

# Remote server with token
prisn context add prod --server prod.example.com:7331 --token sk_prod_xxx

# With namespace
prisn context add staging --server staging.example.com:7331 --namespace staging
```

### Switch Context

```bash
# Set default context
prisn context use prod

# Use context for single command
prisn -c staging deploy api.py --port 8080
```

### List Contexts

```bash
prisn context list
prisn context current
```

## Running as a Service

### systemd

```ini
# /etc/systemd/system/prisn.service
[Unit]
Description=prisn Server
After=network.target

[Service]
Type=simple
User=prisn
Group=prisn
ExecStart=/usr/local/bin/prisn server --bind :7331 --db /var/lib/prisn/state.db
Restart=always
RestartSec=5
Environment=PRISN_LOG_LEVEL=info

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable prisn
sudo systemctl start prisn
sudo systemctl status prisn
```

### Docker

```dockerfile
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o prisn ./cmd/prisn

FROM alpine:3.19
RUN apk add --no-cache python3 py3-pip nodejs npm bash
COPY --from=builder /app/prisn /usr/local/bin/
EXPOSE 7331
VOLUME /var/lib/prisn
CMD ["prisn", "server", "--bind", ":7331", "--db", "/var/lib/prisn/state.db"]
```

```bash
docker build -t prisn-server .
docker run -d -p 7331:7331 -v prisn-data:/var/lib/prisn prisn-server
```

### Docker Compose

```yaml
version: '3.8'
services:
  prisn:
    image: ghcr.io/prisn/prisn:latest
    ports:
      - "7331:7331"
    volumes:
      - prisn-data:/var/lib/prisn
      - ./deployments:/deployments:ro
    environment:
      - PRISN_LOG_LEVEL=info
    restart: unless-stopped

volumes:
  prisn-data:
```

## Security

### TLS

```bash
# Generate certificates
openssl req -x509 -newkey rsa:4096 -keyout key.pem -out cert.pem -days 365 -nodes

# Start with TLS
prisn server --bind :7331 --tls-cert cert.pem --tls-key key.pem
```

### Authentication

```bash
# Generate API token
prisn token create --name deploy-bot --scope deploy,read

# Use in client
prisn context add prod --server prod.example.com:7331 --token sk_xxx
```

### Firewall

Restrict access to the API port:

```bash
# UFW
sudo ufw allow from 10.0.0.0/8 to any port 7331

# iptables
iptables -A INPUT -p tcp --dport 7331 -s 10.0.0.0/8 -j ACCEPT
iptables -A INPUT -p tcp --dport 7331 -j DROP
```

## Monitoring

### Metrics

prisn exposes Prometheus metrics at `/metrics`:

```bash
curl http://localhost:7331/metrics
```

Key metrics:
- `prisn_deployments_total` - Total deployments
- `prisn_deployments_running` - Currently running deployments
- `prisn_jobs_total` - Total jobs
- `prisn_jobs_running` - Currently running jobs
- `prisn_api_requests_total` - API request count
- `prisn_api_request_duration_seconds` - API request latency

### Health Checks

```bash
# Liveness probe
curl -f http://localhost:7331/health

# Readiness probe (checks dependencies)
curl -f http://localhost:7331/ready
```

## Backup and Recovery

### Backup

```bash
# Stop server (or use online backup)
systemctl stop prisn

# Backup database
cp /var/lib/prisn/state.db /backup/prisn-$(date +%Y%m%d).db

# Backup data directory
tar -czf /backup/prisn-data-$(date +%Y%m%d).tar.gz /var/lib/prisn

# Restart
systemctl start prisn
```

### Recovery

```bash
# Stop server
systemctl stop prisn

# Restore database
cp /backup/prisn-20240115.db /var/lib/prisn/state.db

# Restore data
tar -xzf /backup/prisn-data-20240115.tar.gz -C /

# Start server
systemctl start prisn
```

## Troubleshooting

### Server Won't Start

```bash
# Check logs
journalctl -u prisn -f

# Verify database permissions
ls -la /var/lib/prisn/
chown -R prisn:prisn /var/lib/prisn

# Check port availability
ss -tlnp | grep 7331
```

### Client Can't Connect

```bash
# Test connectivity
curl -v http://server:7331/health

# Check firewall
sudo ufw status

# Verify context
prisn context list
prisn context current -v
```

### High Memory Usage

```bash
# Check deployment count
curl http://localhost:7331/api/v1/deployments | jq 'length'

# Restart server to clear caches
systemctl restart prisn
```
