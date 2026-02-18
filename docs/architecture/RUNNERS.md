# Runner System

**Any language, any interpreter**

## What is a Runner?

A runner is prisn's abstraction for "something that can execute code." It defines:

- What command to run
- How to pass the script
- What file extensions it handles
- Environment setup
- Warm pool configuration

## Built-in Runners

prisn ships with these runners out of the box:

### python3

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: python3
spec:
  detect:
    extensions: [".py"]
    shebangs: ["#!/usr/bin/env python", "#!/usr/bin/python"]
  command: ["python3"]
  args: ["{script}"]
  env:
    PYTHONUNBUFFERED: "1"
    PYTHONDONTWRITEBYTECODE: "1"
  pool:
    minIdle: 1
    maxIdle: 5
    maxAge: 1h
    recycleAfter: 100
  capabilities:
    network: true
    filesystem: true
```

### node

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: node
spec:
  detect:
    extensions: [".js", ".mjs"]
    shebangs: ["#!/usr/bin/env node"]
  command: ["node"]
  args: ["{script}"]
  env:
    NODE_ENV: "production"
  pool:
    minIdle: 1
    maxIdle: 5
    maxAge: 1h
    recycleAfter: 100
```

### bash

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: bash
spec:
  detect:
    extensions: [".sh", ".bash"]
    shebangs: ["#!/bin/bash", "#!/usr/bin/env bash"]
  command: ["bash"]
  args: ["-e", "{script}"]  # -e: exit on error
  pool:
    minIdle: 2
    maxIdle: 10
    maxAge: 30m
    recycleAfter: 50
```

### sh

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: sh
spec:
  detect:
    extensions: []  # fallback for unknown shell scripts
    shebangs: ["#!/bin/sh", "#!/usr/bin/env sh"]
  command: ["sh"]
  args: ["-e", "{script}"]
  pool:
    minIdle: 1
    maxIdle: 5
```

## Creating Custom Runners

### Example: Ruby

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: ruby
  namespace: default  # or cluster-wide with "prisn-system"
spec:
  detect:
    extensions: [".rb"]
    shebangs: ["#!/usr/bin/env ruby", "#!/usr/bin/ruby"]

  # The interpreter command
  command: ["ruby"]

  # How to invoke the script
  # {script} is replaced with the script path
  args: ["{script}"]

  # Environment variables
  env:
    RUBYOPT: "-W0"  # silence warnings

  # Working directory (relative to workspace)
  workdir: "."

  # Pool configuration
  pool:
    minIdle: 0      # don't keep any warm by default
    maxIdle: 3
    maxAge: 30m
    recycleAfter: 50

  # What this runner can do
  capabilities:
    network: true
    filesystem: true
    subprocess: true

  # Requirements for nodes to run this
  requirements:
    commands: ["ruby"]  # node must have ruby installed
```

### Example: Lua

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: lua
spec:
  detect:
    extensions: [".lua"]
  command: ["lua"]
  args: ["{script}"]
  pool:
    minIdle: 0
    maxIdle: 2
```

### Example: PHP

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: php
spec:
  detect:
    extensions: [".php"]
  command: ["php"]
  args: ["{script}"]
  env:
    PHP_INI_SCAN_DIR: ""  # ignore global php.ini
  pool:
    minIdle: 0
    maxIdle: 3
```

### Example: Perl

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: perl
spec:
  detect:
    extensions: [".pl", ".pm"]
    shebangs: ["#!/usr/bin/perl", "#!/usr/bin/env perl"]
  command: ["perl"]
  args: ["{script}"]
```

### Example: Deno

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: deno
spec:
  detect:
    extensions: [".ts", ".tsx"]
  command: ["deno"]
  args: ["run", "--allow-net", "--allow-read", "{script}"]
  pool:
    minIdle: 1
    maxIdle: 3
```

### Example: Compiled Binary

For pre-compiled binaries (Go, Rust, C, etc.):

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: my-go-app
spec:
  detect:
    extensions: []  # no auto-detection
  command: ["/path/to/my-app"]
  args: []  # binary takes no script argument
  # No pool - binary is the application itself
  pool:
    minIdle: 0
    maxIdle: 0
```

Then deploy:
```bash
prisn deploy --runner my-go-app
```

### Example: Docker Container

For environments where containers ARE available and you want them:

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: docker-python
spec:
  detect:
    extensions: []  # explicit only
  type: container  # special type
  image: python:3.11-slim
  command: ["python"]
  args: ["{script}"]
  requirements:
    commands: ["docker"]
```

## Runner Spec Reference

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: string           # Required. Unique name.
  namespace: string      # Optional. Default: "default"
  labels: {}             # Optional. Key-value labels.
spec:
  # Detection (how prisn knows to use this runner)
  detect:
    extensions: [string]   # File extensions (e.g., [".py"])
    shebangs: [string]     # Shebang patterns (e.g., ["#!/usr/bin/env python"])
    mimeTypes: [string]    # MIME types (e.g., ["text/x-python"])

  # Execution
  type: string             # "process" (default) or "container"
  command: [string]        # Required. The interpreter command.
  args: [string]           # Arguments. Use {script} for script path.
  env: {}                  # Environment variables.
  workdir: string          # Working directory.

  # For container type
  image: string            # Container image.
  pull: string             # "always", "ifNotPresent", "never"

  # Script injection
  injection:
    method: string         # "file" (default), "stdin", "arg"
    # file: write to {script}, pass path in args
    # stdin: pipe script to stdin
    # arg: embed script directly in args

  # Initialization (run once when starting interpreter)
  init:
    script: string         # Inline init script
    file: string           # Or path to init file

  # Pool configuration
  pool:
    minIdle: int           # Minimum warm instances (default: 0)
    maxIdle: int           # Maximum warm instances (default: 5)
    maxAge: duration       # Recycle after this age (default: 1h)
    recycleAfter: int      # Recycle after N executions (default: 100)
    warmupScript: string   # Script to run on pool member startup

  # Capabilities (what the runner allows)
  capabilities:
    network: bool          # Can make network connections (default: true)
    filesystem: bool       # Can access filesystem (default: true)
    subprocess: bool       # Can spawn subprocesses (default: true)
    ipc: bool              # Can use IPC (default: false)

  # Node requirements
  requirements:
    commands: [string]     # Required commands on node
    os: [string]           # Required OS (e.g., ["linux", "freebsd"])
    arch: [string]         # Required arch (e.g., ["amd64", "arm64"])
    labels: {}             # Required node labels

  # Resource defaults (can be overridden per-deployment)
  resources:
    requests:
      cpu: string          # e.g., "100m"
      memory: string       # e.g., "128Mi"
    limits:
      cpu: string
      memory: string
```

## How Runners Work

### Lifecycle

```
1. Runner Definition Created
   └─> prisn validates spec
   └─> prisn checks which nodes can support it

2. Deployment Created with runtime: <runner-name>
   └─> Scheduler finds nodes with runner capability
   └─> Instances assigned to nodes

3. Execution Starts
   └─> Worker checks pool for idle runner
   └─> If none: spawn new interpreter
   └─> Inject script
   └─> Execute

4. Execution Completes
   └─> Collect exit code, output
   └─> If pool.recycleAfter not reached: return to pool
   └─> Else: terminate and spawn fresh
```

### Pool Management

```
Runner Pool: python3
┌─────────────────────────────────────────────────────────────┐
│ Config: minIdle=2, maxIdle=5, recycleAfter=100              │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐          │
│  │ Instance│ │ Instance│ │ Instance│ │ Instance│          │
│  │ #1      │ │ #2      │ │ #3      │ │ #4      │          │
│  │         │ │         │ │         │ │         │          │
│  │ IDLE    │ │ BUSY    │ │ IDLE    │ │ BUSY    │          │
│  │ exec:45 │ │ exec:89 │ │ exec:12 │ │ exec:3  │          │
│  │ age:20m │ │ age:45m │ │ age:5m  │ │ age:1m  │          │
│  └─────────┘ └─────────┘ └─────────┘ └─────────┘          │
│                                                             │
│  Queue: [req-1, req-2]  (waiting for idle instance)        │
└─────────────────────────────────────────────────────────────┘

Pool Manager Actions:
- If queue not empty and idle > 0: assign oldest idle to oldest request
- If queue not empty and total < maxIdle: spawn new instance
- If idle > minIdle and oldest idle age > maxAge: terminate oldest
- If any instance exec count >= recycleAfter: terminate after current request
```

### Script Injection Methods

**Method 1: File (Default)**
```
1. Create workspace: /prisn/workspaces/{exec-id}/
2. Write script: /prisn/workspaces/{exec-id}/script.py
3. Execute: python3 /prisn/workspaces/{exec-id}/script.py
```

**Method 2: Stdin**
```yaml
spec:
  injection:
    method: stdin
  command: ["python3"]
  args: ["-"]
```
```
1. Start: python3 -
2. Write script to stdin
3. Close stdin
4. Python executes from stdin
```

**Method 3: Arg**
```yaml
spec:
  injection:
    method: arg
  command: ["python3"]
  args: ["-c", "{script_content}"]
```
```
1. Embed script in command: python3 -c "print('hello')"
2. Execute
```

## Node Capability Discovery

When a node starts, it probes for available runtimes:

```go
func probeCapabilities() Capabilities {
    caps := Capabilities{}

    // Check for built-in runtimes
    if _, err := exec.LookPath("python3"); err == nil {
        version := getVersion("python3", "--version")
        caps.Runtimes = append(caps.Runtimes, Runtime{
            Name:    "python3",
            Version: version,
            Path:    "/usr/bin/python3",
        })
    }

    if _, err := exec.LookPath("node"); err == nil {
        version := getVersion("node", "--version")
        caps.Runtimes = append(caps.Runtimes, Runtime{
            Name:    "node",
            Version: version,
            Path:    "/usr/bin/node",
        })
    }

    // ... bash, sh, etc

    // Check for custom runners in config
    for _, runner := range config.AdditionalRunners {
        if canRun(runner) {
            caps.Runtimes = append(caps.Runtimes, runner)
        }
    }

    return caps
}
```

Nodes report capabilities to control plane. Scheduler uses this for placement.

## Using Runners

### Automatic Detection

```bash
# prisn detects .py -> python3
prisn run script.py

# prisn detects .js -> node
prisn run app.js

# prisn detects shebang #!/usr/bin/env ruby -> ruby
prisn run script.rb
```

### Explicit Runtime

```bash
# Force specific runtime
prisn run script --runtime python3
prisn run script --runtime deno

# Use custom runner
prisn run script --runner my-custom-runner
```

### In Manifests

```yaml
apiVersion: prisn.io/v1
kind: Deployment
metadata:
  name: my-app
spec:
  runtime: python3    # use built-in runner
  # or
  runner: my-custom   # use custom runner
  source:
    inline: |
      print("Hello")
```

## Runner Isolation

Each runner can have different isolation requirements:

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: untrusted-python
spec:
  command: ["python3"]
  args: ["{script}"]

  # Maximum isolation
  isolation:
    level: 3  # paranoid

    filesystem:
      readOnly: true
      allowedPaths:
        - /prisn/workspace  # read-write
        - /usr/lib          # read-only

    network:
      enabled: false

    syscalls:
      mode: allowlist
      allowed:
        - read
        - write
        - open
        - close
        # ... minimal set

    resources:
      maxMemory: 256Mi
      maxCpu: 100m
      maxProcesses: 10
      maxFileSize: 10Mi
```

## Examples: Complex Runners

### Python with Dependencies

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: python-ml
spec:
  detect:
    extensions: []  # explicit only
  command: ["python3"]
  args: ["{script}"]
  env:
    PYTHONPATH: "/prisn/lib/python-ml"
  init:
    script: |
      import sys
      sys.path.insert(0, '/prisn/lib/python-ml')
  # Assume node has ML libs pre-installed at /prisn/lib/python-ml
  requirements:
    labels:
      ml-libs: "true"
```

### Node with TypeScript

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: tsx
spec:
  detect:
    extensions: [".ts", ".tsx"]
  command: ["npx"]
  args: ["tsx", "{script}"]
  requirements:
    commands: ["npx"]
```

### Jupyter-style Notebook Cells

```yaml
apiVersion: prisn.io/v1
kind: Runner
metadata:
  name: notebook
spec:
  detect:
    extensions: [".ipynb"]
  command: ["python3"]
  args: ["/prisn/shim/notebook_runner.py", "{script}"]
  # notebook_runner.py extracts and runs cells
```
