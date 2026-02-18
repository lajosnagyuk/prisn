//go:build !windows

package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/layer"
	"github.com/lajosnagyuk/prisn/pkg/sandbox"
)

// ScriptRunner executes scripts using the runtime registry.
type ScriptRunner struct {
	Registry   *Registry
	Detector   *Detector
	EnvManager *EnvManager

	// Layer system for fast environment composition
	LayerStore       *layer.Store
	LayerResolver    *layer.Resolver      // Python
	NodeResolver     *layer.NodeResolver  // Node.js
	ShellValidator   *layer.ShellValidator // Shell scripts

	// Sandbox for resource protection
	Sandbox        sandbox.Sandbox
	SandboxMonitor *sandbox.Monitor

	DefaultTimeout time.Duration

	// UseLayerSystem enables the new layer-based environment system
	UseLayerSystem bool

	// ValidateShellDeps enables shell script dependency validation
	ValidateShellDeps bool
}

// NewScriptRunner creates a script runner with the default registry.
func NewScriptRunner() (*ScriptRunner, error) {
	return NewScriptRunnerWithOptions(ScriptRunnerOptions{})
}

// ScriptRunnerOptions configures the script runner.
type ScriptRunnerOptions struct {
	// DataDir is the base directory for prisn data (~/.prisn)
	DataDir string

	// EnableLayers enables the layer-based environment system
	EnableLayers bool

	// EnableSandbox enables resource protection
	EnableSandbox bool

	// ValidateShellDeps enables shell script dependency validation
	ValidateShellDeps bool

	// SupervisorConfig reserves resources for prisn itself
	SupervisorConfig *sandbox.SupervisorConfig

	// DefaultTimeout is the default execution timeout
	DefaultTimeout time.Duration
}

// NewScriptRunnerWithOptions creates a script runner with custom options.
func NewScriptRunnerWithOptions(opts ScriptRunnerOptions) (*ScriptRunner, error) {
	registry := DefaultRegistry
	envManager, err := NewEnvManager(registry)
	if err != nil {
		return nil, err
	}

	// Set defaults
	if opts.DataDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			opts.DataDir = filepath.Join(os.TempDir(), "prisn")
		} else {
			opts.DataDir = filepath.Join(homeDir, ".prisn")
		}
	}
	if opts.DefaultTimeout == 0 {
		opts.DefaultTimeout = 1 * time.Hour
	}

	runner := &ScriptRunner{
		Registry:          registry,
		Detector:          NewDetector(),
		EnvManager:        envManager,
		DefaultTimeout:    opts.DefaultTimeout,
		UseLayerSystem:    opts.EnableLayers,
		ValidateShellDeps: opts.ValidateShellDeps,
	}

	// Initialize layer system if enabled
	if opts.EnableLayers {
		layerRoot := filepath.Join(opts.DataDir, "layers")
		store, err := layer.NewStore(layerRoot)
		if err != nil {
			// Fall back to venv-only mode
			runner.UseLayerSystem = false
		} else {
			runner.LayerStore = store
			runner.LayerResolver = layer.NewResolver(store)
			runner.NodeResolver = layer.NewNodeResolver(store)
		}
	}

	// Always initialize shell validator (lightweight)
	runner.ShellValidator = layer.NewShellValidator()

	// Initialize sandbox if enabled
	if opts.EnableSandbox {
		sb, err := sandbox.New(opts.DataDir)
		if err != nil {
			// Sandbox not available, continue without it
		} else {
			runner.Sandbox = sb

			// Reserve resources for supervisor
			if opts.SupervisorConfig != nil {
				if err := sb.ReserveSupervisor(*opts.SupervisorConfig); err != nil {
					// Not fatal, just log and continue
				}
			}

			// Create monitor with default escalation policy
			runner.SandboxMonitor = sandbox.NewMonitor(sb, sandbox.DefaultEscalationPolicy())
		}
	}

	return runner, nil
}

// RunConfig specifies how to run a script.
type RunConfig struct {
	Script      string
	Args        []string
	Env         map[string]string
	WorkDir     string
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Timeout     time.Duration
	RuntimeID   string // Override runtime detection

	// Resource limits (optional)
	Resources ResourceLimits
}

// ResourceLimits specifies resource constraints for execution.
type ResourceLimits struct {
	// MemoryMB is the maximum memory in megabytes (0 = unlimited)
	MemoryMB int

	// CPUPercent is the CPU limit as percentage (100 = 1 core, 0 = unlimited)
	CPUPercent int

	// MaxProcesses is the maximum number of child processes (0 = unlimited)
	MaxProcesses int

	// MaxOpenFiles is the maximum number of open files (0 = unlimited)
	MaxOpenFiles int
}

// RunResult contains the outcome of a script execution.
type RunResult struct {
	ExitCode         int
	Stdout           string
	Stderr           string
	Duration         time.Duration
	StartTime        time.Time
	EndTime          time.Time
	Killed           bool
	KillReason       string
	EnvCached        bool
	EnvSetupDuration time.Duration
	Runtime          string
}

// Run executes a script with full dependency handling.
func (r *ScriptRunner) Run(ctx context.Context, cfg RunConfig) (*RunResult, error) {
	result := &RunResult{
		StartTime: time.Now(),
	}

	// Validate script path
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
	var manifest *Manifest
	var err error

	if cfg.RuntimeID != "" {
		manifest, err = r.Detector.DetectWithRuntime(cfg.Script, cfg.RuntimeID)
	} else {
		manifest, err = r.Detector.Detect(cfg.Script)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to detect dependencies: %w", err)
	}
	result.Runtime = manifest.Runtime

	// Get runtime definition
	rt, ok := r.Registry.Get(manifest.Runtime)
	if !ok {
		return nil, fmt.Errorf("unknown runtime: %s", manifest.Runtime)
	}

	// Validate shell script dependencies if enabled
	if r.ValidateShellDeps && r.ShellValidator != nil && isShellRuntime(manifest.Runtime) {
		validation, err := r.ShellValidator.Validate(cfg.Script)
		if err == nil && validation.HasMissing() {
			return nil, fmt.Errorf("missing shell dependencies: %v\n\n%s",
				validation.MissingNames(), validation.FormatValidation())
		}
	}

	// Prepare environment (using layers if available, otherwise venv/npm)
	var env *Environment
	if r.UseLayerSystem && r.LayerResolver != nil && isPythonRuntime(manifest.Runtime) {
		env, err = r.prepareWithLayers(ctx, cfg, manifest)
	} else if r.UseLayerSystem && r.NodeResolver != nil && isNodeRuntime(manifest.Runtime) {
		env, err = r.prepareWithNodeLayers(ctx, cfg, manifest)
	} else {
		env, err = r.EnvManager.Prepare(ctx, manifest)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to prepare environment: %w", err)
	}
	result.EnvCached = env.Cached
	result.EnvSetupDuration = time.Since(envStart)

	// Build command
	cmd, err := r.buildCommand(ctx, cfg, rt, env)
	if err != nil {
		return nil, fmt.Errorf("failed to build command: %w", err)
	}

	// Set up process group for cleanup
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}

	// Set up sandbox context if available
	var sandboxCtx sandbox.Context
	if r.Sandbox != nil && hasResourceLimits(cfg.Resources) {
		limits := sandbox.Limits{
			MemorySoft:   int64(cfg.Resources.MemoryMB) * 1024 * 1024 * 80 / 100, // 80% of hard
			MemoryHard:   int64(cfg.Resources.MemoryMB) * 1024 * 1024,
			CPUQuota:     cfg.Resources.CPUPercent,
			MaxPIDs:      cfg.Resources.MaxProcesses,
			MaxOpenFiles: cfg.Resources.MaxOpenFiles,
			Timeout:      cfg.Timeout,
		}
		if limits.MaxPIDs == 0 {
			limits.MaxPIDs = 256 // Default fork bomb protection
		}

		sandboxCtx, err = r.Sandbox.Create(fmt.Sprintf("script-%d", time.Now().UnixNano()), limits)
		if err != nil {
			// Sandbox creation failed, continue without it
			sandboxCtx = nil
		} else {
			defer sandboxCtx.Cleanup()
			// Let sandbox wrap the command (sets env vars, etc.)
			sandboxCtx.Wrap(cmd)
		}
	}

	// Capture output
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

	// Register process with sandbox for monitoring
	var sandboxProc sandbox.Process
	if sandboxCtx != nil && cmd.Process != nil {
		sandboxProc, _ = sandboxCtx.Process(cmd.Process.Pid)
		if sandboxProc != nil && r.SandboxMonitor != nil {
			r.SandboxMonitor.Add(sandboxCtx, sandboxProc)
			defer r.SandboxMonitor.Remove(cmd.Process.Pid)
		}
	}

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
		result.Killed = true
		result.KillReason = "timeout"

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

// prepareWithLayers uses the layer system for fast environment setup.
func (r *ScriptRunner) prepareWithLayers(ctx context.Context, cfg RunConfig, manifest *Manifest) (*Environment, error) {
	// Convert manifest dependencies to layer packages
	var packages []layer.Package
	for _, dep := range manifest.Dependencies {
		packages = append(packages, layer.Package{
			Name:    dep.Name,
			Version: dep.Version,
		})
	}

	// Resolve the optimal stack
	stack, err := r.LayerResolver.ResolveFromPackages(packages)
	if err != nil {
		// Fall back to venv
		return r.EnvManager.Prepare(ctx, manifest)
	}

	// Build environment from stack
	env := &Environment{
		EnvVars: make(map[string]string),
		Cached:  true, // Layers are always "cached" (pre-built)
	}

	// Add PYTHONPATH for Python runtimes
	if len(stack.PythonPath) > 0 {
		env.EnvVars["PYTHONPATH"] = strings.Join(stack.PythonPath, ":")
	}

	// Set BinPath if available
	if stack.BinPath != "" {
		env.BinDir = stack.BinPath
	}

	return env, nil
}

// isPythonRuntime checks if the runtime is Python-based.
func isPythonRuntime(runtime string) bool {
	return runtime == "python" || runtime == "python3" || strings.HasPrefix(runtime, "python")
}

// isNodeRuntime checks if the runtime is Node.js-based.
func isNodeRuntime(runtime string) bool {
	return runtime == "node" || runtime == "nodejs" || runtime == "javascript" || runtime == "js"
}

// isShellRuntime checks if the runtime is a shell script.
func isShellRuntime(runtime string) bool {
	shells := map[string]bool{
		"bash": true, "sh": true, "zsh": true, "fish": true,
		"dash": true, "ksh": true, "csh": true, "tcsh": true,
	}
	return shells[runtime]
}

// prepareWithNodeLayers uses the layer system for Node.js environment setup.
func (r *ScriptRunner) prepareWithNodeLayers(ctx context.Context, cfg RunConfig, manifest *Manifest) (*Environment, error) {
	// Convert manifest dependencies to layer packages
	var packages []layer.Package
	for _, dep := range manifest.Dependencies {
		packages = append(packages, layer.Package{
			Name:    dep.Name,
			Version: dep.Version,
		})
	}

	// Resolve the optimal stack
	stack, err := r.NodeResolver.ResolveFromPackages(packages)
	if err != nil {
		// Fall back to npm
		return r.EnvManager.Prepare(ctx, manifest)
	}

	// Build environment from stack
	env := &Environment{
		EnvVars: make(map[string]string),
		Cached:  true,
	}

	// Add NODE_PATH for Node.js
	if len(stack.NodePath) > 0 {
		env.EnvVars["NODE_PATH"] = strings.Join(stack.NodePath, ":")
	}

	// Set BinPath if available
	if stack.BinPath != "" {
		env.BinDir = stack.BinPath
	}

	return env, nil
}

// hasResourceLimits checks if any resource limits are set.
func hasResourceLimits(r ResourceLimits) bool {
	return r.MemoryMB > 0 || r.CPUPercent > 0 || r.MaxProcesses > 0 || r.MaxOpenFiles > 0
}

// Close releases resources held by the script runner.
func (r *ScriptRunner) Close() error {
	if r.SandboxMonitor != nil {
		r.SandboxMonitor.Stop()
	}
	if r.Sandbox != nil {
		return r.Sandbox.Close()
	}
	return nil
}

// StartMonitor starts the background resource monitor.
// Call this if you want automatic enforcement of resource limits.
func (r *ScriptRunner) StartMonitor(ctx context.Context) {
	if r.SandboxMonitor != nil {
		go r.SandboxMonitor.Start(ctx)
	}
}

// buildCommand creates the exec.Cmd for a script.
func (r *ScriptRunner) buildCommand(ctx context.Context, cfg RunConfig, rt *Runtime, env *Environment) (*exec.Cmd, error) {
	// Determine interpreter
	interpreter := env.InterpreterPath
	if interpreter == "" {
		interpreter = rt.Command
	}

	// Build arguments from runtime template
	var args []string
	for _, arg := range rt.Args {
		if arg == "{script}" {
			args = append(args, cfg.Script)
		} else {
			args = append(args, arg)
		}
	}

	// Append user args
	args = append(args, cfg.Args...)

	cmd := exec.CommandContext(ctx, interpreter, args...)
	cmd.Dir = cfg.WorkDir

	// Build environment
	cmd.Env = os.Environ()

	// Add venv bin to PATH
	if env.BinDir != "" {
		for i, e := range cmd.Env {
			if strings.HasPrefix(e, "PATH=") {
				cmd.Env[i] = "PATH=" + env.BinDir + ":" + e[5:]
				break
			}
		}
	}

	// Add environment vars from runtime and env
	for k, v := range env.EnvVars {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// Add user environment variables (highest priority)
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	return cmd, nil
}

// QuickRun executes a script with default settings.
func (r *ScriptRunner) QuickRun(ctx context.Context, script string, args ...string) (*RunResult, error) {
	return r.Run(ctx, RunConfig{
		Script: script,
		Args:   args,
	})
}

// RunInline executes inline code.
func (r *ScriptRunner) RunInline(ctx context.Context, code, runtimeID string) (*RunResult, error) {
	rt, ok := r.Registry.Get(runtimeID)
	if !ok {
		return nil, fmt.Errorf("unknown runtime: %s", runtimeID)
	}

	// Determine extension
	ext := ".txt"
	if len(rt.Extensions) > 0 {
		ext = rt.Extensions[0]
	}

	// Create temp file
	tmpDir := os.TempDir()
	tmpFile, err := os.CreateTemp(tmpDir, "prisn-inline-*"+ext)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(code); err != nil {
		return nil, fmt.Errorf("failed to write code: %w", err)
	}
	tmpFile.Close()

	return r.Run(ctx, RunConfig{
		Script:    tmpFile.Name(),
		RuntimeID: runtimeID,
	})
}

// validateScriptPath validates that a script path is safe to execute.
func validateScriptPath(path string) error {
	if path == "" {
		return fmt.Errorf("script path is empty")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid script path: %w", err)
	}

	// Check for path traversal
	if strings.Contains(path, "..") {
		cleanPath := filepath.Clean(absPath)
		if cleanPath != absPath {
			return fmt.Errorf("path traversal detected")
		}
	}

	// Verify file exists
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("script not found: %s", path)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied: %s", path)
		}
		return fmt.Errorf("cannot access script: %w", err)
	}

	if info.IsDir() {
		return fmt.Errorf("script path is a directory: %s", path)
	}

	// Block dangerous paths
	dangerousPaths := []string{
		"/etc/passwd", "/etc/shadow", "/etc/hosts",
		"/proc/", "/sys/", "/dev/",
	}
	for _, dangerous := range dangerousPaths {
		if strings.HasPrefix(absPath, dangerous) {
			return fmt.Errorf("access denied: %s", path)
		}
	}

	return nil
}
