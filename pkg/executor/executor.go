// Package executor handles process execution with platform-specific isolation.
//
// This is the heart of prisn's "run code without containers" capability.
// See docs/architecture/EXECUTION-MODEL.md for details.
package executor

import (
	"context"
	"io"
	"time"
)

// IsolationLevel determines how strongly a process is isolated.
type IsolationLevel int

const (
	// IsolationNone provides no isolation beyond a separate process.
	// Use for fully trusted code in development.
	IsolationNone IsolationLevel = 0

	// IsolationBasic provides process groups, ulimits, and working directory isolation.
	// Default level - safe for trusted code that might have bugs.
	IsolationBasic IsolationLevel = 1

	// IsolationStrong provides platform-native isolation (namespaces/jails/sandbox).
	// Use for multi-tenant or less-trusted code.
	IsolationStrong IsolationLevel = 2

	// IsolationParanoid adds syscall filtering and network denial.
	// Use for running user-submitted code.
	IsolationParanoid IsolationLevel = 3
)

// Config specifies how to execute a script.
type Config struct {
	// Command is the interpreter path (e.g., "/usr/bin/python3")
	Command string

	// Args are the interpreter arguments (e.g., ["-u", "script.py"])
	Args []string

	// Env are environment variables
	Env map[string]string

	// WorkDir is the working directory
	WorkDir string

	// Stdin provides input to the process
	Stdin io.Reader

	// Stdout receives standard output
	Stdout io.Writer

	// Stderr receives standard error
	Stderr io.Writer

	// Isolation level
	Isolation IsolationLevel

	// Resource limits
	Resources ResourceLimits

	// Timeout after which the process is killed
	Timeout time.Duration
}

// ResourceLimits specifies resource constraints.
type ResourceLimits struct {
	// MaxMemoryBytes is the memory limit (0 = unlimited)
	MaxMemoryBytes int64

	// MaxCPUMillis is the CPU limit in millicores (0 = unlimited)
	// 1000 = 1 CPU core
	MaxCPUMillis int64

	// MaxProcesses is the maximum number of child processes
	MaxProcesses int

	// MaxOpenFiles is the maximum number of open file descriptors
	MaxOpenFiles int

	// MaxDiskBytes is the maximum disk usage for the workspace
	MaxDiskBytes int64
}

// Result contains the outcome of an execution.
type Result struct {
	// ExitCode is the process exit code
	ExitCode int

	// Error is set if execution failed before getting an exit code
	Error error

	// StartTime is when execution began
	StartTime time.Time

	// EndTime is when execution completed
	EndTime time.Time

	// CPUTime is the total CPU time used
	CPUTime time.Duration

	// MaxMemoryBytes is the peak memory usage
	MaxMemoryBytes int64

	// Killed is true if the process was killed (timeout, OOM, etc.)
	Killed bool

	// KillReason explains why the process was killed
	KillReason string
}

// Executor runs scripts with isolation.
type Executor interface {
	// Execute runs a script with the given configuration.
	// The context can be used for cancellation.
	Execute(ctx context.Context, cfg Config) Result

	// Capabilities returns what this executor can do.
	Capabilities() Capabilities

	// Platform returns the platform identifier (linux, freebsd, darwin, etc.)
	Platform() string
}

// Capabilities describes what an executor supports.
type Capabilities struct {
	// MaxIsolationLevel is the highest isolation level supported
	MaxIsolationLevel IsolationLevel

	// SupportsNamespaces indicates Linux namespace support
	SupportsNamespaces bool

	// SupportsCgroups indicates Linux cgroup v2 support
	SupportsCgroups bool

	// SupportsSeccomp indicates Linux seccomp support
	SupportsSeccomp bool

	// SupportsJails indicates FreeBSD jail support
	SupportsJails bool

	// SupportsRctl indicates FreeBSD rctl support
	SupportsRctl bool

	// SupportsSandbox indicates macOS sandbox-exec support
	SupportsSandbox bool

	// SupportsOverlayFS indicates overlayfs support for filesystem isolation
	SupportsOverlayFS bool
}

// New returns the appropriate executor for the current platform.
func New() Executor {
	return newPlatformExecutor()
}
