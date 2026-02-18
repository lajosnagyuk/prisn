# Networking

**Service discovery, load balancing, and ingress**

## Overview

prisn provides networking for:
- **Services**: Long-running deployments that accept connections
- **Discovery**: Finding services by name
- **Load Balancing**: Distributing traffic across replicas
- **Ingress**: Exposing services externally

## Service Discovery

### Internal DNS

Every deployment with a port gets a DNS name:

```
{deployment}.{namespace}.prisn.local
```

Examples:
```
api.default.prisn.local
worker.production.prisn.local
```

From inside any workload:
```python
import requests
response = requests.get("http://api.default.prisn.local:8080/health")
```

### DNS Resolution

prisn runs a built-in DNS server on each node:

```
┌─────────────────────────────────────────────────────────────┐
│                         Node                                │
│  ┌────────────────────────────────────────────────────────┐ │
│  │                   prisn DNS                             │ │
│  │  *.prisn.local -> lookup in cluster state              │ │
│  │  other -> forward to system resolver                   │ │
│  └────────────────────────────────────────────────────────┘ │
│                            ▲                                │
│                            │                                │
│  ┌─────────┐  ┌─────────┐  │  ┌─────────┐                  │
│  │ Process │  │ Process │  │  │ Process │                  │
│  │ (my-app)│  │ (worker)│──┘  │ (cron)  │                  │
│  └─────────┘  └─────────┘     └─────────┘                  │
└─────────────────────────────────────────────────────────────┘
```

Workloads have `/etc/resolv.conf` configured to use prisn DNS.

### Service Records

When you deploy with a port:

```bash
prisn deploy api.py --port 8080 --replicas 3
```

prisn creates:
```
A record:    api.default.prisn.local -> [10.0.0.5, 10.0.0.6, 10.0.0.7]
SRV record:  _http._tcp.api.default.prisn.local -> port 8080
```

The A record returns all healthy instance IPs (round-robin).

## Load Balancing

### L4 Load Balancing (Default)

TCP-level load balancing. Each connection goes to one instance.

```
Client ──TCP──> prisn proxy ──TCP──> Instance 1
                             ──TCP──> Instance 2
                             ──TCP──> Instance 3
```

Algorithm: Round-robin with health awareness.

### L7 Load Balancing (HTTP)

For HTTP services, prisn can do smarter routing:

```yaml
apiVersion: prisn.io/v1
kind: Deployment
spec:
  port: 8080
  protocol: http  # enables L7 features
  loadBalancing:
    algorithm: least-connections  # or: round-robin, random, ip-hash
    healthCheck:
      path: /health
      interval: 10s
      timeout: 5s
      threshold: 3
```

### Session Affinity

Stick clients to the same instance:

```yaml
spec:
  loadBalancing:
    sessionAffinity:
      enabled: true
      type: cookie  # or: ip-hash, header
      ttl: 1h
```

## Built-in Proxy

Each node runs a lightweight reverse proxy:

```
┌─────────────────────────────────────────────────────────────┐
│                        prisn Proxy                          │
│                                                             │
│  Listeners:                                                 │
│    :80   -> HTTP router                                     │
│    :443  -> HTTPS router (TLS termination)                  │
│    :9090 -> TCP router (arbitrary ports)                    │
│                                                             │
│  Routes:                                                    │
│    api.example.com -> deployment/api (port 8080)            │
│    *.example.com/app/* -> deployment/app (port 3000)        │
│    tcp:5432 -> deployment/postgres (port 5432)              │
└─────────────────────────────────────────────────────────────┘
```

### Configuration

```yaml
# /etc/prisn/proxy.yaml
proxy:
  http:
    port: 80
    enabled: true
  https:
    port: 443
    enabled: true
    cert: /etc/prisn/tls/cert.pem
    key: /etc/prisn/tls/key.pem
  tcp:
    enabled: true
    portRange: 9000-9999  # available for TCP services
```

## Ingress (External Access)

### Simple: Port Exposure

```yaml
apiVersion: prisn.io/v1
kind: Deployment
spec:
  port: 8080
  expose:
    type: nodePort
    nodePort: 30080  # accessible on any node at :30080
```

### HTTP Ingress

```yaml
apiVersion: prisn.io/v1
kind: Deployment
spec:
  port: 8080
  expose:
    type: ingress
    host: api.example.com
    path: /
    tls:
      enabled: true
      cert: my-cert  # secret name
```

Or standalone:

```yaml
apiVersion: prisn.io/v1
kind: Ingress
metadata:
  name: api-ingress
spec:
  rules:
    - host: api.example.com
      paths:
        - path: /
          deployment: api
          port: 8080
        - path: /admin
          deployment: admin-ui
          port: 3000
  tls:
    - hosts: [api.example.com]
      secretName: api-tls
```

### TLS Termination

prisn terminates TLS at the proxy:

```
Client ──HTTPS──> prisn proxy ──HTTP──> Instance
                   (TLS term)
```

Certificates from:
- Manual: Create secret with cert/key
- Auto: Let's Encrypt (if configured)

```yaml
# /etc/prisn/config.yaml
tls:
  acme:
    enabled: true
    email: admin@example.com
    staging: false  # use production LE
```

## Kubernetes Mode Networking

When running in Kubernetes, prisn uses native k8s networking:

### Services

prisn creates Kubernetes Services:

```yaml
# Generated by prisn
apiVersion: v1
kind: Service
metadata:
  name: my-app
  namespace: default
  labels:
    prisn.io/deployment: my-app
spec:
  selector:
    prisn.io/deployment: my-app
  ports:
    - port: 8080
      targetPort: 8080
```

### Ingress

prisn creates Kubernetes Ingress:

```yaml
# Generated by prisn
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-app
  annotations:
    # Works with any ingress controller
spec:
  rules:
    - host: my-app.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-app
                port:
                  number: 8080
```

## Standalone Mode Networking

### Node Discovery

Nodes find each other via:
1. **Explicit**: `prisn cluster join <ip>:<port>`
2. **mDNS**: Auto-discovery on local network
3. **DNS**: SRV records for `_prisn._tcp.example.com`

### Mesh Networking

All nodes can route to all other nodes:

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Node 1    │◄───►│   Node 2    │◄───►│   Node 3    │
│ 10.0.0.1    │     │ 10.0.0.2    │     │ 10.0.0.3    │
│             │     │             │     │             │
│ Instance A  │     │ Instance B  │     │ Instance C  │
└─────────────┘     └─────────────┘     └─────────────┘

Request to service "my-app" from any node:
1. DNS resolves my-app.default.prisn.local
2. Returns: [10.0.0.1, 10.0.0.2, 10.0.0.3]
3. Client connects to one (or proxy load balances)
```

### VPN/Overlay (Optional)

For nodes across networks:

```yaml
# /etc/prisn/config.yaml
network:
  overlay:
    enabled: true
    type: wireguard  # or: vxlan, geneve
    subnet: 10.100.0.0/16
```

Each node gets an overlay IP. All prisn traffic uses overlay.

## Port Management

### Automatic Port Allocation

```bash
prisn deploy api.py --port auto
# prisn picks an available port, prints it
# Service accessible at api.default.prisn.local:<port>
```

### Named Ports

```yaml
spec:
  ports:
    - name: http
      port: 8080
    - name: metrics
      port: 9090
    - name: grpc
      port: 50051
```

Access:
```
http://api.default.prisn.local:8080
http://api.default.prisn.local:9090/metrics
```

## Health Checks

### TCP (Default)

```yaml
spec:
  healthCheck:
    tcp:
      port: 8080
    interval: 10s
    timeout: 5s
    failureThreshold: 3
```

### HTTP

```yaml
spec:
  healthCheck:
    http:
      path: /health
      port: 8080
      expectedStatus: 200
    interval: 10s
```

### Command

```yaml
spec:
  healthCheck:
    exec:
      command: ["python", "-c", "import app; app.healthcheck()"]
    interval: 30s
```

### Health-Based Routing

Unhealthy instances are removed from:
- DNS responses
- Load balancer pools
- Service endpoints

After recovery (passing `successThreshold` checks), they're re-added.

## Network Policies (Optional)

Restrict which services can talk to which:

```yaml
apiVersion: prisn.io/v1
kind: NetworkPolicy
metadata:
  name: db-access
  namespace: default
spec:
  target:
    deployment: postgres
  ingress:
    - from:
        - deployment: api
        - deployment: worker
      ports:
        - 5432
    # Only api and worker can reach postgres
```

## Debugging

### Check service DNS

```bash
# From inside a workload
nslookup api.default.prisn.local

# From CLI
prisn dns lookup api.default.prisn.local
```

### Check endpoints

```bash
prisn get endpoints api
# NAME   ENDPOINTS
# api    10.0.0.5:8080, 10.0.0.6:8080, 10.0.0.7:8080
```

### Trace request

```bash
prisn proxy trace api.default.prisn.local:8080
# Resolves to: 10.0.0.5:8080, 10.0.0.6:8080, 10.0.0.7:8080
# Selected: 10.0.0.5:8080 (round-robin)
# Response: 200 OK (45ms)
```

### Proxy stats

```bash
prisn proxy stats
# Service            Requests   Errors   Latency (p99)
# api.default        15234      12       45ms
# worker.default     8921       0        12ms
```
