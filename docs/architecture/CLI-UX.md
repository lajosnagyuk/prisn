# CLI User Experience

**The interface that makes you smile**

## Philosophy

1. **Infer intent, default to absolutes**
2. **One command should do one thing well**
3. **Progressive disclosure: simple by default, powerful when needed**
4. **Errors should explain how to fix themselves**

## Command Structure

```
prisn [global-flags] <command> [command-flags] [arguments]
```

### Global Flags

```
--namespace, -n    Target namespace (default: "default")
--context, -c      Cluster context (for multi-cluster)
--output, -o       Output format: text, json, yaml, wide
--verbose, -v      Verbose output
--quiet, -q        Suppress non-essential output
```

## Core Commands

### `prisn run` - Execute Once

Run a script and exit. Like `ssh server 'command'` but smarter.

```bash
# Run a local script
prisn run script.py
prisn run backup.sh
prisn run task.js

# Run inline code
prisn run -e "print('hello')" --runtime python
prisn run -e "echo hello" --runtime bash

# Run from URL
prisn run https://example.com/script.py

# Run with arguments
prisn run script.py -- --input data.csv --output results.json

# Run with environment variables
prisn run script.py -e KEY=value -e SECRET=hunter2

# Run with secret from cluster
prisn run script.py --secret db-creds

# Run with resource limits
prisn run heavy.py --cpu 2 --memory 4Gi --timeout 1h

# Run on specific node type
prisn run train.py --node-selector gpu=true

# Watch the output (default: stream until complete)
prisn run script.py

# Run in background, don't wait
prisn run script.py --detach
# Later: prisn logs job/abc123
```

### `prisn deploy` - Run Forever

Deploy a long-running service or worker.

```bash
# Deploy a script as a service
prisn deploy api.py --port 8080
prisn deploy worker.js --replicas 3

# Deploy with a name
prisn deploy api.py --name my-api

# Deploy from directory (looks for main.py, index.js, etc.)
prisn deploy ./my-app/

# Deploy with auto-scaling
prisn deploy api.py --replicas 2:10 --target-cpu 70

# Update an existing deployment
prisn deploy api.py --name my-api  # redeploys with new code

# Deploy with persistent storage
prisn deploy stateful.py --volume data:/prisn/data

# Deploy with health check
prisn deploy api.py --health-check http://:8080/health

# Deploy with environment
prisn deploy api.py -e DATABASE_URL=postgres://...
```

### `prisn cron` - Schedule It

Create a scheduled job.

```bash
# Every hour
prisn cron backup.sh "0 * * * *"

# Every day at 3am
prisn cron cleanup.py "0 3 * * *"

# Every Monday
prisn cron report.py "0 9 * * 1"

# With a name
prisn cron backup.sh "0 * * * *" --name hourly-backup

# Human-readable schedules
prisn cron backup.sh --every hour
prisn cron backup.sh --every day --at 3:00
prisn cron backup.sh --every monday --at 9:00
prisn cron backup.sh --every "5 minutes"
```

### `prisn scale` - Change Replicas

The magic syntax.

```bash
# Absolute: set to exactly N
prisn scale my-app --replicas 5
prisn scale my-app -r 5
prisn scale my-app 5          # shorthand

# Relative: add or subtract
prisn scale my-app +2         # add 2
prisn scale my-app -1         # remove 1

# Multiplicative: scale by factor
prisn scale my-app *2         # double
prisn scale my-app *1.5       # increase by 50%
prisn scale my-app /2         # halve

# Semantic
prisn scale my-app 0          # pause (scale to 0, keep config)
prisn scale my-app 00         # disarm (delete deployment)
prisn scale my-app min        # scale to 1
prisn scale my-app max        # scale to maximum allowed
prisn scale my-app enough     # auto-calculate based on load

# What "enough" does:
# 1. Check current CPU/memory across instances
# 2. Calculate: needed = ceil(current_load / 0.7)  # 70% target
# 3. Add 20% margin: final = ceil(needed * 1.2)
# 4. Respect min/max bounds
# 5. Apply
```

### `prisn get` - List Resources

```bash
# List deployments
prisn get deployments
prisn get deploy            # alias
prisn get d                 # shorter alias

# List jobs
prisn get jobs
prisn get job               # alias
prisn get j                 # shorter alias

# List cron jobs
prisn get cronjobs
prisn get cron              # alias

# List runners
prisn get runners
prisn get runner

# List nodes
prisn get nodes
prisn get node

# List everything
prisn get all

# With namespace
prisn get deploy -n production

# With wide output
prisn get deploy -o wide

# As JSON/YAML
prisn get deploy my-api -o json
prisn get deploy my-api -o yaml

# Watch (live updates)
prisn get deploy --watch
prisn get deploy -w
```

Output:
```
$ prisn get deploy
NAME       REPLICAS   STATUS    AGE    RUNTIME
my-api     3/3        Running   2d     python3
worker     5/5        Running   1h     node
backup     0/0        Paused    7d     bash

$ prisn get deploy -o wide
NAME       REPLICAS   STATUS    AGE    RUNTIME   CPU     MEMORY    RESTARTS
my-api     3/3        Running   2d     python3   150m    384Mi     0
worker     5/5        Running   1h     node      2100m   1.2Gi     2
backup     0/0        Paused    7d     bash      0       0         0
```

### `prisn describe` - Detailed Info

```bash
prisn describe deploy my-api
prisn describe job abc123
prisn describe node node-1
```

Output:
```
$ prisn describe deploy my-api
Name:         my-api
Namespace:    default
Runtime:      python3
Replicas:     3/3 running
Created:      2 days ago
Updated:      3 hours ago

Source:
  Type:  File
  Hash:  sha256:abc123...

Resources:
  Requests:  cpu=100m, memory=128Mi
  Limits:    cpu=500m, memory=512Mi

Port: 8080
Health Check: http://:8080/health (every 10s)

Instances:
  ID          NODE      STATUS    AGE    RESTARTS
  my-api-1    node-1    Running   2d     0
  my-api-2    node-2    Running   2d     0
  my-api-3    node-3    Running   3h     1

Events:
  3h ago   Scaled    Increased replicas from 2 to 3
  3h ago   Killed    Instance my-api-2 killed (node failure)
  2d ago   Created   Deployment created
```

### `prisn logs` - View Logs

```bash
# Logs from a deployment (all instances)
prisn logs my-api

# Logs from specific instance
prisn logs my-api-1

# Logs from a job
prisn logs job/abc123

# Follow (stream new logs)
prisn logs my-api -f
prisn logs my-api --follow

# Last N lines
prisn logs my-api --tail 100
prisn logs my-api -n 100

# Since time
prisn logs my-api --since 1h
prisn logs my-api --since "2024-01-01 00:00"

# With timestamps
prisn logs my-api --timestamps
prisn logs my-api -t

# Filter by log level (if structured)
prisn logs my-api --level error
```

### `prisn exec` - Run in Context

Execute a command in a running instance's context.

```bash
# Interactive shell
prisn exec my-api-1 -- /bin/sh

# Run a command
prisn exec my-api-1 -- python -c "import sys; print(sys.version)"

# Run in any instance of deployment
prisn exec my-api -- python manage.py migrate
```

### `prisn delete` - Remove Things

```bash
# Delete a deployment
prisn delete deploy my-api

# Delete a job
prisn delete job abc123

# Delete a cron job
prisn delete cron hourly-backup

# Delete multiple
prisn delete deploy my-api worker backup

# Delete with confirmation skip
prisn delete deploy my-api --yes

# Equivalent to scale 00
prisn scale my-api 00
```

### `prisn runner` - Manage Runtimes

```bash
# List available runners
prisn runner list

# Show runner details
prisn runner show python3

# Create custom runner
prisn runner create my-lua -f runner.yaml

# Create from inline spec
prisn runner create my-ruby \
  --command /usr/bin/ruby \
  --args "{script}" \
  --extensions .rb

# Delete runner
prisn runner delete my-lua
```

### `prisn cluster` - Cluster Management

```bash
# Initialize a new cluster
prisn cluster init

# Initialize with specific address
prisn cluster init --advertise 192.168.1.10:9001

# Join an existing cluster (as worker)
prisn cluster join 192.168.1.10:9001

# Join as control plane node
prisn cluster join 192.168.1.10:9001 --control-plane

# Show cluster status
prisn cluster status

# List nodes
prisn cluster nodes
prisn get nodes  # same thing

# Remove a node
prisn cluster remove node-123

# Drain a node (evacuate workloads, then remove)
prisn cluster drain node-123

# Show cluster info
prisn cluster info
```

## Smart Defaults

### Language Detection

```bash
prisn run script.py      # -> python3
prisn run script.js      # -> node
prisn run script.sh      # -> bash
prisn run script.rb      # -> ruby (if runner exists)
prisn run script.lua     # -> lua (if runner exists)
```

### Port Detection

```bash
# If your script listens on a port, prisn detects it
prisn deploy api.py
# -> Detects port 8080 from code, exposes it

# Or specify explicitly
prisn deploy api.py --port 8080
```

### Name Inference

```bash
prisn deploy api.py
# -> Creates deployment named "api"

prisn deploy ./my-service/
# -> Creates deployment named "my-service"
```

### Replica Defaults

```bash
prisn deploy api.py
# -> 1 replica by default

prisn deploy worker.py --replicas 3
# -> 3 replicas
```

## Error Messages

Errors explain what went wrong AND how to fix it:

```
$ prisn deploy api.py --runtime go
Error: Runtime 'go' is not a script runtime

Go programs need to be compiled first. For Go support:
  1. Compile: go build -o app
  2. Create a bash runner: prisn runner create go-app --command ./app
  3. Deploy: prisn deploy --runner go-app

Or use one of the built-in runtimes: python, node, bash
```

```
$ prisn scale my-api enough
Error: Cannot calculate 'enough' - no metrics available

The deployment has no running instances, so there's no load data.
Try one of these instead:
  prisn scale my-api 1      # start with 1 instance
  prisn scale my-api min    # start with minimum
```

```
$ prisn run heavy.py
Error: No node can satisfy requirements

Your script requires: python3, linux/amd64, 4Gi memory
Available nodes:
  node-1: python3, linux/amd64, 2Gi memory (insufficient memory)
  node-2: python3, linux/arm64 (wrong architecture)
  node-3: node only (missing python3)

Options:
  1. Reduce requirements: prisn run heavy.py --memory 2Gi
  2. Add a capable node: prisn cluster join ... (on a bigger machine)
  3. Install python3 on node-3: apt install python3
```

## Interactive Mode

For exploration and learning:

```bash
$ prisn
prisn> help
Available commands: run, deploy, scale, get, logs, ...

prisn> get deploy
NAME       REPLICAS   STATUS    AGE
my-api     3/3        Running   2d

prisn> scale my-api +1
Scaled my-api to 4 replicas

prisn> logs my-api -f
[my-api-1] Starting server on :8080
[my-api-2] Starting server on :8080
...
^C

prisn> exit
```

## Configuration

```bash
# Set default namespace
prisn config set namespace production

# Set default cluster
prisn config set cluster prod-cluster

# View config
prisn config show

# Use specific config file
prisn --config ~/.prisn/work.yaml get deploy
```

## Tab Completion

```bash
# Install completions
prisn completion bash >> ~/.bashrc
prisn completion zsh >> ~/.zshrc
prisn completion fish >> ~/.config/fish/completions/prisn.fish

# Then:
prisn sca<tab>           -> prisn scale
prisn scale my<tab>      -> prisn scale my-api
prisn scale my-api <tab> -> shows: +N -N *N /N 0 00 min max enough
```

## Output Formats

```bash
# Default: human-readable
prisn get deploy

# Wide: more columns
prisn get deploy -o wide

# JSON: for scripting
prisn get deploy my-api -o json

# YAML: for manifests
prisn get deploy my-api -o yaml

# Name only: for piping
prisn get deploy -o name
# my-api
# worker

# Custom columns
prisn get deploy -o custom-columns=NAME:.name,CPU:.status.cpu
```

## Manifest Files

For complex deployments, use manifest files:

```yaml
# my-app.yaml
apiVersion: prisn.io/v1
kind: Deployment
metadata:
  name: my-api
  namespace: production
spec:
  runtime: python3
  source:
    file: ./api.py
    # or: inline: |
    #       print("hello")
    # or: url: https://example.com/api.py
  replicas: 3
  port: 8080
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 512Mi
  env:
    - name: DATABASE_URL
      value: postgres://...
    - name: SECRET_KEY
      secretRef:
        name: app-secrets
        key: secret-key
  healthCheck:
    http:
      path: /health
      port: 8080
    interval: 10s
    timeout: 5s
  placement:
    nodeSelector:
      os: linux
```

```bash
# Apply manifest
prisn apply -f my-app.yaml

# Apply directory of manifests
prisn apply -f ./manifests/

# Delete from manifest
prisn delete -f my-app.yaml

# Diff before applying
prisn diff -f my-app.yaml
```

## Aliases (Built-in)

```
d, deploy    = deployments
j, job       = jobs
c, cron      = cronjobs
r, runner    = runners
n, node      = nodes
ns           = namespaces
```

## Environment Variables

```bash
PRISN_NAMESPACE      # Default namespace
PRISN_CONTEXT        # Default cluster context
PRISN_CONFIG         # Config file path
PRISN_OUTPUT         # Default output format
PRISN_NO_COLOR       # Disable colors
PRISN_DEBUG          # Enable debug logging
```
