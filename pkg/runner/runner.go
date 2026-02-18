//go:build !windows

// Package runner executes scripts in isolated environments.
//
// Unlike the warm pool approach in the original design, this uses
// fork-based isolation to prevent state corruption between executions.
//
// This package now uses the pluggable runtime system from pkg/runtime,
// which supports any interpreter (Python, Node, Bash, Babashka, Perl, etc.)
// through configurable runtime definitions.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/cgroup"
	"github.com/lajosnagyuk/prisn/pkg/deps"
	"github.com/lajosnagyuk/prisn/pkg/log"
	prisnruntime "github.com/lajosnagyuk/prisn/pkg/runtime"
	"github.com/lajosnagyuk/prisn/pkg/venv"
)

// Runner executes scripts with proper isolation.
type Runner struct {
	// VenvManager handles virtual environment creation (legacy)
	VenvManager *venv.Manager

	// DepDetector detects dependencies (legacy)
	DepDetector *deps.Detector

	// RuntimeRunner is the new pluggable runtime system
	RuntimeRunner *prisnruntime.ScriptRunner

	// WorkDir is the base directory for script workspaces
	WorkDir string

	// DefaultTimeout is the default execution timeout
	DefaultTimeout time.Duration

	// UsePluggableRuntime enables the new runtime system (default: true)
	UsePluggableRuntime bool

	// cgroupManager handles cgroup v2 resource limits (Linux only)
	cgroupManager *cgroup.Manager
}

// New creates a new runner.
func New() (*Runner, error) {
	vm, err := venv.NewManager()
	if err != nil {
		return nil, fmt.Errorf("failed to create venv manager: %w", err)
	}

	workDir := filepath.Join(os.TempDir(), "prisn-workspaces")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create work directory: %w", err)
	}

	// Initialize the pluggable runtime system
	runtimeRunner, err := prisnruntime.NewScriptRunner()
	if err != nil {
		// Fall back to legacy mode if runtime system fails
		log.Warn("failed to initialize pluggable runtime system: %v (using legacy mode)", err)
		return &Runner{
			VenvManager:         vm,
			DepDetector:         deps.NewDetector(),
			WorkDir:             workDir,
			DefaultTimeout:      1 * time.Hour,
			UsePluggableRuntime: false,
			cgroupManager:       cgroup.NewManager(),
		}, nil
	}

	return &Runner{
		VenvManager:         vm,
		DepDetector:         deps.NewDetector(),
		RuntimeRunner:       runtimeRunner,
		WorkDir:             workDir,
		DefaultTimeout:      1 * time.Hour,
		UsePluggableRuntime: true,
		cgroupManager:       cgroup.NewManager(),
	}, nil
}

// RunConfig specifies how to run a script.
type RunConfig struct {
	// Script is the path to the script file
	Script string

	// Args are command-line arguments to pass to the script
	Args []string

	// Env are additional environment variables
	Env map[string]string

	// Stdin provides input to the script
	Stdin io.Reader

	// Stdout receives standard output
	Stdout io.Writer

	// Stderr receives standard error
	Stderr io.Writer

	// Timeout is the maximum execution time (0 = use default)
	Timeout time.Duration

	// WorkDir is the working directory (default: script's directory)
	WorkDir string

	// Resources are resource limits
	Resources ResourceConfig
}

// ResourceConfig specifies resource limits.
type ResourceConfig struct {
	// MaxMemoryMB is the memory limit in megabytes (0 = unlimited)
	MaxMemoryMB int

	// MaxCPUPercent is the CPU limit as percentage (0 = unlimited)
	MaxCPUPercent int

	// MaxProcesses is the maximum number of child processes
	MaxProcesses int
}

// RunResult contains the outcome of a script execution.
type RunResult struct {
	// ExitCode is the script's exit code
	ExitCode int

	// Stdout is the captured standard output (if not streamed)
	Stdout string

	// Stderr is the captured standard error (if not streamed)
	Stderr string

	// Duration is how long the script ran
	Duration time.Duration

	// StartTime is when execution started
	StartTime time.Time

	// EndTime is when execution ended
	EndTime time.Time

	// Killed indicates if the script was killed (timeout, OOM, etc.)
	Killed bool

	// KillReason explains why the script was killed
	KillReason string

	// EnvCached indicates if the environment was cached
	EnvCached bool

	// EnvSetupDuration is how long environment setup took
	EnvSetupDuration time.Duration
}

// Run executes a script with full dependency handling.
func (r *Runner) Run(ctx context.Context, cfg RunConfig) (*RunResult, error) {
	// Use pluggable runtime system if available
	if r.UsePluggableRuntime && r.RuntimeRunner != nil {
		return r.runWithPluggableRuntime(ctx, cfg)
	}

	// Legacy mode follows...
	return r.runLegacy(ctx, cfg)
}

// runWithPluggableRuntime uses the new pkg/runtime system.
func (r *Runner) runWithPluggableRuntime(ctx context.Context, cfg RunConfig) (*RunResult, error) {
	rtCfg := prisnruntime.RunConfig{
		Script:  cfg.Script,
		Args:    cfg.Args,
		Env:     cfg.Env,
		WorkDir: cfg.WorkDir,
		Stdin:   cfg.Stdin,
		Stdout:  cfg.Stdout,
		Stderr:  cfg.Stderr,
		Timeout: cfg.Timeout,
	}

	result, err := r.RuntimeRunner.Run(ctx, rtCfg)
	if err != nil {
		return nil, err
	}

	return convertRuntimeResult(result), nil
}

// convertRuntimeResult converts a prisnruntime.RunResult to runner.RunResult.
func convertRuntimeResult(rr *prisnruntime.RunResult) *RunResult {
	return &RunResult{
		ExitCode:         rr.ExitCode,
		Stdout:           rr.Stdout,
		Stderr:           rr.Stderr,
		Duration:         rr.Duration,
		StartTime:        rr.StartTime,
		EndTime:          rr.EndTime,
		Killed:           rr.Killed,
		KillReason:       rr.KillReason,
		EnvCached:        rr.EnvCached,
		EnvSetupDuration: rr.EnvSetupDuration,
	}
}

// runLegacy uses the original deps/venv system for Python/Node/Bash.
func (r *Runner) runLegacy(ctx context.Context, cfg RunConfig) (*RunResult, error) {
	result := &RunResult{
		StartTime: time.Now(),
	}

	// Validate script path to prevent path traversal attacks
	if err := validateScriptPath(cfg.Script); err != nil {
		return nil, err
	}

	// Set defaults
	if cfg.Timeout == 0 {
		cfg.Timeout = r.DefaultTimeout
	}
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	if cfg.WorkDir == "" {
		cfg.WorkDir = filepath.Dir(cfg.Script)
	}

	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	// Detect dependencies
	envStart := time.Now()
	manifest, err := r.DepDetector.Detect(cfg.Script)
	if err != nil {
		return nil, fmt.Errorf("failed to detect dependencies: %w", err)
	}

	// Prepare environment
	envInfo, err := r.VenvManager.Prepare(ctx, manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare environment: %w", err)
	}
	result.EnvCached = envInfo.Cached
	result.EnvSetupDuration = time.Since(envStart)

	// Build command
	cmd, err := r.buildCommand(ctx, cfg, manifest, envInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to build command: %w", err)
	}

	// Set up process group for cleanup
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}

	// Capture output if not streaming
	var stdoutBuf, stderrBuf bytes.Buffer
	if cfg.Stdout == os.Stdout {
		cmd.Stdout = io.MultiWriter(cfg.Stdout, &stdoutBuf)
	} else {
		cmd.Stdout = cfg.Stdout
	}
	if cfg.Stderr == os.Stderr {
		cmd.Stderr = io.MultiWriter(cfg.Stderr, &stderrBuf)
	} else {
		cmd.Stderr = cfg.Stderr
	}
	cmd.Stdin = cfg.Stdin

	// Start execution
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start script: %w", err)
	}

	// Apply resource limits (using cgroups on Linux, or best-effort elsewhere)
	var cgroupName string
	if cfg.Resources.MaxMemoryMB > 0 || cfg.Resources.MaxCPUPercent > 0 || cfg.Resources.MaxProcesses > 0 {
		cgroupName = r.applyResourceLimits(cmd.Process.Pid, cfg.Resources)
	}
	defer func() {
		if cgroupName != "" {
			r.cgroupManager.Remove(cgroupName)
		}
	}()

	// Wait for completion
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(result.StartTime)

		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				result.ExitCode = exitErr.ExitCode()
			} else {
				return nil, err
			}
		}

	case <-ctx.Done():
		// Timeout or cancellation
		result.Killed = true
		result.KillReason = "timeout"

		// Kill entire process group
		if cmd.Process != nil {
			syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}

		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(result.StartTime)
		result.ExitCode = -1
	}

	result.Stdout = stdoutBuf.String()
	result.Stderr = stderrBuf.String()

	return result, nil
}

// buildCommand creates the exec.Cmd for a script.
func (r *Runner) buildCommand(ctx context.Context, cfg RunConfig, manifest *deps.Manifest, env *venv.EnvInfo) (*exec.Cmd, error) {
	var args []string
	var interpreter string

	switch manifest.Runtime {
	case "python3", "python":
		interpreter = env.PythonPath
		if interpreter == "" {
			interpreter = "python3"
		}
		// Use unbuffered output
		args = append(args, "-u", cfg.Script)
		args = append(args, cfg.Args...)

	case "node":
		interpreter = env.NodePath
		if interpreter == "" {
			interpreter = "node"
		}
		args = append(args, cfg.Script)
		args = append(args, cfg.Args...)

	case "bash":
		interpreter = "bash"
		args = append(args, "-e", cfg.Script) // -e: exit on error
		args = append(args, cfg.Args...)

	default:
		return nil, fmt.Errorf("unsupported runtime: %s", manifest.Runtime)
	}

	cmd := exec.CommandContext(ctx, interpreter, args...)
	cmd.Dir = cfg.WorkDir

	// Build environment
	cmd.Env = os.Environ()

	// Add venv to PATH
	if env.BinDir != "" {
		for i, e := range cmd.Env {
			if strings.HasPrefix(e, "PATH=") {
				cmd.Env[i] = "PATH=" + env.BinDir + ":" + e[5:]
				break
			}
		}
	}

	// Add VIRTUAL_ENV for Python
	if env.Path != "" && strings.HasPrefix(manifest.Runtime, "python") {
		cmd.Env = append(cmd.Env, "VIRTUAL_ENV="+env.Path)
	}

	// Add NODE_PATH for Node.js
	if manifest.Runtime == "node" && env.Path != "" {
		cmd.Env = append(cmd.Env, "NODE_PATH="+filepath.Join(env.Path, "node_modules"))
	}

	// Add user environment variables
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	return cmd, nil
}

// applyResourceLimits applies resource limits to a process.
// On Linux, uses cgroups v2 for THROTTLING (not killing).
// Returns the cgroup name for cleanup, or empty string if not using cgroups.
func (r *Runner) applyResourceLimits(pid int, res ResourceConfig) string {
	hasLimits := res.MaxMemoryMB > 0 || res.MaxCPUPercent > 0 || res.MaxProcesses > 0
	if !hasLimits {
		return ""
	}

	// Log what was requested
	var limitStrs []string
	if res.MaxMemoryMB > 0 {
		limitStrs = append(limitStrs, fmt.Sprintf("memory=%dMB", res.MaxMemoryMB))
	}
	if res.MaxCPUPercent > 0 {
		limitStrs = append(limitStrs, fmt.Sprintf("cpu=%d%%", res.MaxCPUPercent))
	}
	if res.MaxProcesses > 0 {
		limitStrs = append(limitStrs, fmt.Sprintf("procs=%d", res.MaxProcesses))
	}

	// Try to use cgroups v2 on Linux
	if r.cgroupManager != nil && r.cgroupManager.Available() {
		limits := cgroup.Limits{
			CPUPercent:   res.MaxCPUPercent,
			MemoryBytes:  int64(res.MaxMemoryMB) * 1024 * 1024,
			MaxProcesses: res.MaxProcesses,
			// Use 90% as soft limit - starts throttling before hitting hard limit
			MemorySoftPercent: 90,
		}

		cgroupName, err := r.cgroupManager.Create(pid, limits)
		if err != nil {
			log.Warn("cgroup creation failed, limits not enforced: %v", err)
			return ""
		}

		log.VInfo("Resource limits applied via cgroups (%s)", strings.Join(limitStrs, ", "))
		return cgroupName
	}

	// Fall back to warning on non-Linux or when cgroups unavailable
	if goruntime.GOOS == "linux" {
		log.Warn("cgroups v2 not available, resource limits not enforced (pid=%d, %s)", pid, strings.Join(limitStrs, ", "))
		log.Debug("Ensure /sys/fs/cgroup is mounted with cgroups v2 and user has write access")
	} else {
		log.VInfo("resource limits requested but not available on %s (pid=%d, %s)", goruntime.GOOS, pid, strings.Join(limitStrs, ", "))
	}
	return ""
}

// QuickRun is a convenience method for simple script execution.
func (r *Runner) QuickRun(ctx context.Context, script string, args ...string) (*RunResult, error) {
	return r.Run(ctx, RunConfig{
		Script: script,
		Args:   args,
	})
}

// RunInline executes inline code (not a file).
func (r *Runner) RunInline(ctx context.Context, code, runtimeID string) (*RunResult, error) {
	// Use pluggable runtime system if available
	if r.UsePluggableRuntime && r.RuntimeRunner != nil {
		result, err := r.RuntimeRunner.RunInline(ctx, code, runtimeID)
		if err != nil {
			return nil, err
		}
		return convertRuntimeResult(result), nil
	}

	// Legacy mode: create temporary file
	var ext string
	switch runtimeID {
	case "python3", "python":
		ext = ".py"
	case "node":
		ext = ".js"
	case "bash":
		ext = ".sh"
	default:
		return nil, fmt.Errorf("unsupported runtime: %s", runtimeID)
	}

	tmpFile, err := os.CreateTemp(r.WorkDir, "prisn-inline-*"+ext)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(code); err != nil {
		return nil, fmt.Errorf("failed to write code: %w", err)
	}
	tmpFile.Close()

	return r.QuickRun(ctx, tmpFile.Name())
}

// validateScriptPath validates that a script path is safe to execute.
// It prevents path traversal attacks and validates that the file exists.
func validateScriptPath(path string) error {
	if path == "" {
		return fmt.Errorf("script path is empty")
	}

	// Resolve to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid script path: %w", err)
	}

	// Check for path traversal patterns
	if strings.Contains(path, "..") {
		// Clean the path and check if it escapes
		cleanPath := filepath.Clean(absPath)
		if cleanPath != absPath {
			return fmt.Errorf("path traversal detected in script path")
		}
	}

	// Verify the file exists and is readable
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("script not found: %s", path)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied reading script: %s", path)
		}
		return fmt.Errorf("cannot access script: %w", err)
	}

	// Must be a regular file
	if info.IsDir() {
		return fmt.Errorf("script path is a directory: %s", path)
	}

	// Block obviously dangerous paths
	dangerousPaths := []string{
		"/etc/passwd", "/etc/shadow", "/etc/hosts",
		"/proc/", "/sys/", "/dev/",
	}
	for _, dangerous := range dangerousPaths {
		if strings.HasPrefix(absPath, dangerous) || absPath == dangerous[:len(dangerous)-1] {
			return fmt.Errorf("access denied: %s", path)
		}
	}

	return nil
}
