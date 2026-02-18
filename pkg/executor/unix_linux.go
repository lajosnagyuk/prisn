//go:build linux

package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// linuxExecutor implements Executor with Linux-specific isolation.
type linuxExecutor struct {
	platform string
	caps     Capabilities
}

// newLinuxExecutor creates a Linux executor with namespace support.
func newLinuxExecutor(caps Capabilities) *linuxExecutor {
	return &linuxExecutor{
		platform: "linux",
		caps:     caps,
	}
}

func (e *linuxExecutor) Platform() string {
	return e.platform
}

func (e *linuxExecutor) Capabilities() Capabilities {
	return e.caps
}

func (e *linuxExecutor) Execute(ctx context.Context, cfg Config) Result {
	result := Result{
		StartTime: time.Now(),
	}

	// Cap isolation level at what we support
	actualIsolation := cfg.Isolation
	if actualIsolation > e.caps.MaxIsolationLevel {
		actualIsolation = e.caps.MaxIsolationLevel
	}

	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	cmd.Dir = cfg.WorkDir
	cmd.Stdin = cfg.Stdin
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Configure process attributes based on isolation level
	sysProcAttr := &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}

	switch actualIsolation {
	case IsolationNone:
		// No special isolation, just process group

	case IsolationBasic:
		// Process group + basic ulimits (handled by cgroups now)

	case IsolationStrong:
		// Use Linux namespaces for isolation
		// CLONE_NEWUSER allows unprivileged namespace creation
		// CLONE_NEWNS creates a new mount namespace
		// CLONE_NEWPID creates a new PID namespace
		// CLONE_NEWIPC creates a new IPC namespace
		if e.caps.SupportsNamespaces {
			sysProcAttr.Cloneflags = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID | syscall.CLONE_NEWIPC
			// Map current user to root in the namespace
			sysProcAttr.UidMappings = []syscall.SysProcIDMap{
				{ContainerID: 0, HostID: os.Getuid(), Size: 1},
			}
			sysProcAttr.GidMappings = []syscall.SysProcIDMap{
				{ContainerID: 0, HostID: os.Getgid(), Size: 1},
			}
		}

	case IsolationParanoid:
		// Maximum isolation: add network namespace
		// This completely isolates network access
		if e.caps.SupportsNamespaces {
			sysProcAttr.Cloneflags = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID | syscall.CLONE_NEWIPC | syscall.CLONE_NEWNET
			sysProcAttr.UidMappings = []syscall.SysProcIDMap{
				{ContainerID: 0, HostID: os.Getuid(), Size: 1},
			}
			sysProcAttr.GidMappings = []syscall.SysProcIDMap{
				{ContainerID: 0, HostID: os.Getgid(), Size: 1},
			}
		}
	}

	cmd.SysProcAttr = sysProcAttr

	// Start the process
	if err := cmd.Start(); err != nil {
		result.Error = fmt.Errorf("failed to start process: %w", err)
		result.EndTime = time.Now()
		return result
	}

	// Set up timeout
	var timer *time.Timer
	if cfg.Timeout > 0 {
		timer = time.AfterFunc(cfg.Timeout, func() {
			if cmd.Process != nil {
				// Kill the entire process group
				syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				result.Killed = true
				result.KillReason = "timeout"
			}
		})
		defer timer.Stop()
	}

	// Wait for completion
	err := cmd.Wait()
	result.EndTime = time.Now()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else if !result.Killed {
			result.Error = err
		}
	}

	// Collect resource usage
	if cmd.ProcessState != nil {
		if rusage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			result.CPUTime = time.Duration(rusage.Utime.Nano()+rusage.Stime.Nano()) * time.Nanosecond
			result.MaxMemoryBytes = int64(rusage.Maxrss) * 1024 // Linux reports in KB
		}
	}

	return result
}
