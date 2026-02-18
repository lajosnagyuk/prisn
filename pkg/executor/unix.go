//go:build unix

package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// genericUnixExecutor implements Executor for Unix-like systems.
// Platform-specific isolation (cgroups, jails, sandbox) is handled
// based on detected capabilities.
type genericUnixExecutor struct {
	platform string
	caps     Capabilities
}

func (e *genericUnixExecutor) Platform() string {
	return e.platform
}

func (e *genericUnixExecutor) Capabilities() Capabilities {
	return e.caps
}

func (e *genericUnixExecutor) Execute(ctx context.Context, cfg Config) Result {
	result := Result{
		StartTime: time.Now(),
	}

	// Cap isolation level at what we support
	if cfg.Isolation > e.caps.MaxIsolationLevel {
		cfg.Isolation = e.caps.MaxIsolationLevel
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

	// Create new process group for cleanup
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}

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
			result.MaxMemoryBytes = int64(rusage.Maxrss)
			// Linux reports in KB, others in bytes - normalize
			if e.platform == "linux" {
				result.MaxMemoryBytes *= 1024
			}
		}
	}

	return result
}
