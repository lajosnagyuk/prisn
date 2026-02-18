# Execution Model

**The hard part: running arbitrary code without containers**

## The Problem

Containers (Docker, containerd) provide:
- Filesystem isolation
- Process isolation
- Resource limits
- Reproducible environments

But containers aren't available everywhere:
- FreeBSD has jails, not Docker
- Some Linux systems don't have container runtimes
- macOS containers are slow and awkward
- Embedded systems often can't run containers

prisn must work everywhere. So we build our own execution model.

## Design: Tiered Isolation

Use the best isolation available on each platform:

| Platform | Filesystem | Process | Resources | Network |
|----------|-----------|---------|-----------|---------|
| Linux | overlayfs + pivot_root | namespaces | cgroups v2 | netns |
| FreeBSD | nullfs + chroot | jail | rctl | vnet |
| macOS | sandbox-exec | process | ulimit | - |
| Generic | chroot or none | process group | ulimit | - |

The executor detects platform capabilities at startup and uses the strongest available.

## Isolation Levels

Configurable per-runner or per-deployment:

### Level 0: None
Just fork/exec. Trust the code completely.
- Use case: Development, fully trusted internal tools

### Level 1: Basic (Default)
Process groups + ulimits + working directory isolation.
- Process can't escape its cwd
- Resource limits prevent runaway
- Use case: Trusted code that might have bugs

### Level 2: Strong
Platform-native isolation (namespaces/jails/sandbox).
- Isolated filesystem view
- Can't see other processes
- Network restrictions available
- Use case: Multi-tenant, untrusted code

### Level 3: Paranoid
Maximum isolation + seccomp/pledge + network deny.
- Syscall filtering
- No network by default
- Read-only root filesystem
- Use case: Running user-submitted code

## Executor Architecture

```
┌────────────────────────────────────────────────────────────┐
│                        Executor                            │
│  ┌──────────────────────────────────────────────────────┐  │
│  │                  Platform Detector                    │  │
│  │    Probes: cgroups? namespaces? jails? sandbox?      │  │
│  └──────────────────────────────────────────────────────┘  │
│                            │                               │
│            ┌───────────────┼───────────────┐               │
│            ▼               ▼               ▼               │
│  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐       │
│  │ Linux        │ │ FreeBSD      │ │ Generic      │       │
│  │ Executor     │ │ Executor     │ │ Executor     │       │
│  │              │ │              │ │              │       │
│  │ - clone()    │ │ - jail()     │ │ - fork()     │       │
│  │ - cgroups    │ │ - rctl       │ │ - ulimit     │       │
│  │ - seccomp    │ │ - capsicum   │ │ - chroot     │       │
│  └──────────────┘ └──────────────┘ └──────────────┘       │
└────────────────────────────────────────────────────────────┘
```

## Linux Executor (Detailed)

The Linux executor provides container-like isolation without a container runtime.

### Process Isolation (Namespaces)

```go
// Clone flags for strong isolation
flags := syscall.CLONE_NEWPID |  // New PID namespace (process is PID 1)
         syscall.CLONE_NEWNS |   // New mount namespace
         syscall.CLONE_NEWUTS |  // New hostname
         syscall.CLONE_NEWIPC    // New IPC namespace

// Optional network isolation
if config.NetworkIsolation {
    flags |= syscall.CLONE_NEWNET
}

// Optional user namespace (for rootless)
if !isRoot {
    flags |= syscall.CLONE_NEWUSER
}
```

### Filesystem Isolation (Overlayfs)

```
┌─────────────────────────────────────────┐
│           Process View                   │
│  /                                       │
│  ├── usr/     (read-only from base)     │
│  ├── lib/     (read-only from base)     │
│  ├── bin/     (read-only from base)     │
│  ├── prisn/                             │
│  │   ├── workspace/  (read-write)       │
│  │   ├── script.py   (injected)         │
│  │   └── data/       (persistent mount) │
│  └── tmp/     (read-write, ephemeral)   │
└─────────────────────────────────────────┘

Implemented as:
  overlay mount:
    lower = /prisn/bases/python3 (immutable base image)
    upper = /prisn/workspaces/{execution-id} (per-run scratch)
    work  = /prisn/work/{execution-id}
    merged = /prisn/roots/{execution-id}
```

### Resource Limits (cgroups v2)

```go
// Create cgroup for execution
cgroupPath := fmt.Sprintf("/sys/fs/cgroup/prisn/%s", executionID)

// Set limits
writeFile(cgroupPath+"/memory.max", fmt.Sprintf("%d", memoryBytes))
writeFile(cgroupPath+"/cpu.max", fmt.Sprintf("%d 100000", cpuMicros))
writeFile(cgroupPath+"/pids.max", fmt.Sprintf("%d", maxProcesses))

// Move process into cgroup
writeFile(cgroupPath+"/cgroup.procs", fmt.Sprintf("%d", pid))
```

### Syscall Filtering (seccomp)

For Level 3 isolation, restrict dangerous syscalls:

```go
// Deny list approach - block known dangerous calls
blocked := []string{
    "reboot",
    "kexec_load",
    "init_module",
    "delete_module",
    "mount",
    "umount2",
    "pivot_root",
    "ptrace",
}

// Or allow list approach - only permit known safe calls
allowed := []string{
    "read", "write", "open", "close", "stat", "fstat",
    "mmap", "mprotect", "munmap", "brk",
    "nanosleep", "clock_gettime",
    "socket", "connect", "send", "recv",  // if network allowed
    // ... comprehensive list
}
```

## FreeBSD Executor

FreeBSD's jails provide excellent isolation, better than Linux in some ways.

### Jail Setup

```go
// Create jail
jailParams := jail.Params{
    Path:     "/prisn/roots/" + executionID,
    Name:     executionID,
    Hostname: executionID[:12],
    IP4:      jail.IP4Inherit,  // or specific IPs
}

// Resource limits via rctl
rctlRules := []string{
    fmt.Sprintf("jail:%s:memoryuse:deny=%d", jailName, memoryBytes),
    fmt.Sprintf("jail:%s:cputime:deny=%d", jailName, cpuSeconds),
    fmt.Sprintf("jail:%s:maxproc:deny=%d", jailName, maxProcesses),
}
```

### Capsicum (Optional)

For even stronger isolation, use capability mode:

```go
// Enter capability mode - no new file descriptors to global namespace
cap_enter()

// Pre-open allowed file descriptors before entering capability mode
fd_script := open(scriptPath, O_RDONLY)
fd_stdout := open(logPath, O_WRONLY|O_CREATE)
```

## Generic Executor

For platforms without advanced isolation, we still provide safety:

### Process Groups

```go
// Create new process group
cmd.SysProcAttr = &syscall.SysProcAttr{
    Setpgid: true,
    Pgid:    0,  // New group, this process as leader
}

// On timeout or kill, terminate entire group
syscall.Kill(-pgid, syscall.SIGKILL)
```

### Resource Limits (ulimit)

```go
// Set resource limits before exec
syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{
    Cur: memoryBytes,
    Max: memoryBytes,
})
syscall.Setrlimit(syscall.RLIMIT_CPU, &syscall.Rlimit{
    Cur: cpuSeconds,
    Max: cpuSeconds,
})
syscall.Setrlimit(syscall.RLIMIT_NPROC, &syscall.Rlimit{
    Cur: maxProcesses,
    Max: maxProcesses,
})
```

### Chroot (If Available)

```go
if canChroot {
    syscall.Chroot(workspacePath)
    syscall.Chdir("/")
}
```

## Script Injection

How does the script get to the runner?

### Method 1: File Copy (Default)

```
1. Create workspace directory
2. Write script to workspace/script.{ext}
3. Write dependencies to workspace/ (if any)
4. Execute: interpreter workspace/script.{ext}
```

### Method 2: Stdin Pipe

For interpreters that support reading from stdin:

```
1. Open pipe to interpreter's stdin
2. Write script content
3. Close stdin
4. Interpreter executes

Example: python3 - < script.py
         node -e "$(cat script.js)"
```

### Method 3: Warm Pool Injection

For frequently-used interpreters with slow startup:

```
1. Start interpreter with prisn shim loaded
2. Shim waits on Unix socket for commands
3. On execution request:
   a. Send script path to shim
   b. Shim exec()s the script in its process
   c. Or shim eval()s the script (if safe)
4. After execution, shim resets state and waits again
```

## Warm Pool Architecture

Cold starts hurt. Warm pools help.

```
┌─────────────────────────────────────────────────────────────┐
│                    Runner Pool: python3                     │
│                                                             │
│  Configuration:                                             │
│    min_idle: 2                                              │
│    max_idle: 10                                             │
│    max_age: 1h                                              │
│    recycle_after: 100 executions                            │
│                                                             │
│  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐          │
│  │ Worker  │ │ Worker  │ │ Worker  │ │ Worker  │          │
│  │  #1     │ │  #2     │ │  #3     │ │  #4     │          │
│  │         │ │         │ │         │ │         │          │
│  │ idle    │ │ running │ │ idle    │ │ running │          │
│  │ age: 5m │ │ exec:47 │ │ age: 2m │ │ exec:12 │          │
│  └─────────┘ └─────────┘ └─────────┘ └─────────┘          │
│       ▲                       ▲                            │
│       │                       │                            │
│       └───── idle queue ──────┘                            │
└─────────────────────────────────────────────────────────────┘
```

### Pool Lifecycle

```
1. On startup:
   - Spawn min_idle workers
   - Each worker: start interpreter, load shim, wait

2. On execution request:
   - Try to acquire idle worker (non-blocking)
   - If none available:
     - If under max, spawn new worker
     - Else, wait for one to become idle
   - Send script to worker
   - Mark worker as busy

3. On execution complete:
   - Increment worker's execution count
   - If count >= recycle_after or error occurred:
     - Terminate worker
     - Spawn replacement
   - Else:
     - Reset worker state
     - Return to idle queue

4. Background:
   - Cull workers older than max_age
   - Maintain min_idle workers
   - Report pool metrics
```

### The prisn Shim

A small program loaded into warm interpreters:

```python
# /prisn/shim/python_shim.py
import os
import sys
import socket
import importlib.util

SOCK_PATH = os.environ['PRISN_SHIM_SOCKET']

def main():
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.connect(SOCK_PATH)

    while True:
        # Read command: "EXEC /path/to/script.py\n"
        cmd = sock.makefile().readline().strip()

        if cmd.startswith('EXEC '):
            script_path = cmd[5:]
            try:
                # Clear previous state
                sys.modules.clear()
                sys.path = [os.path.dirname(script_path)]

                # Execute script
                spec = importlib.util.spec_from_file_location("__main__", script_path)
                module = importlib.util.module_from_spec(spec)
                spec.loader.exec_module(module)

                sock.send(b"OK\n")
            except Exception as e:
                sock.send(f"ERR {e}\n".encode())

        elif cmd == 'PING':
            sock.send(b"PONG\n")

        elif cmd == 'EXIT':
            break

if __name__ == '__main__':
    main()
```

## Execution Flow (Complete)

```
User: prisn run script.py

┌─────────────────────────────────────────────────────────────┐
│ 1. CLI                                                      │
│    - Parse command                                          │
│    - Read script.py                                         │
│    - Detect language (python from extension)                │
│    - Build Job manifest                                     │
│    - POST /api/v1/namespaces/default/jobs                   │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. API Server (Control Plane)                               │
│    - Validate manifest                                      │
│    - Assign job ID                                          │
│    - Write to Raft log                                      │
│    - Scheduler picks target node                            │
│    - Dispatch to worker                                     │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Worker Node                                              │
│    - Receive job spec                                       │
│    - Find runner: python3                                   │
│    - Acquire from pool (or spawn new)                       │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. Executor                                                 │
│    - Create execution context                               │
│    - Set up isolation (namespace/jail/process group)        │
│    - Create workspace                                       │
│    - Write script to workspace                              │
│    - Apply resource limits                                  │
│    - Start execution                                        │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 5. Runtime                                                  │
│    - Script executes                                        │
│    - stdout/stderr captured                                 │
│    - Metrics collected (CPU, memory, duration)              │
│    - Exit code captured                                     │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 6. Cleanup                                                  │
│    - Release cgroup/jail                                    │
│    - Delete workspace (unless persistent)                   │
│    - Return runner to pool (or terminate)                   │
│    - Report completion to control plane                     │
│    - Stream logs to CLI                                     │
└─────────────────────────────────────────────────────────────┘
```

## Performance Targets

| Metric | Target | Notes |
|--------|--------|-------|
| Cold start (new runner) | < 2s | Interpreter startup |
| Warm start (pooled) | < 50ms | Script copy + exec |
| Job dispatch latency | < 10ms | API to worker |
| Execution overhead | < 5% | vs. running directly |
| Pool acquire time | < 1ms | From idle queue |

## Configuration

```yaml
# /etc/prisn/executor.yaml
executor:
  # Default isolation level (0-3)
  defaultIsolation: 1

  # Workspace location
  workspaceRoot: /var/lib/prisn/workspaces

  # Base images for overlayfs (Linux)
  bases:
    python3: /var/lib/prisn/bases/python3
    node: /var/lib/prisn/bases/node
    bash: /  # Use host filesystem

  # Platform-specific
  linux:
    useCgroups: true
    useNamespaces: true
    useSeccomp: true
    cgroupRoot: /sys/fs/cgroup/prisn

  freebsd:
    useJails: true
    useRctl: true
```
