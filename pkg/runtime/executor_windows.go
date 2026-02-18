//go:build windows

package runtime

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/layer"
	"github.com/lajosnagyuk/prisn/pkg/sandbox"
)

var errWindowsUnsupported = errors.New("script execution is not supported on Windows; use WSL2 or a Linux VM")

// ScriptRunner executes scripts using the runtime registry.
// Windows stub returns errors for all operations.
type ScriptRunner struct {
	Registry   *Registry
	Detector   *Detector
	EnvManager *EnvManager

	LayerStore       *layer.Store
	LayerResolver    *layer.Resolver
	NodeResolver     *layer.NodeResolver
	ShellValidator   *layer.ShellValidator

	Sandbox        sandbox.Sandbox
	SandboxMonitor *sandbox.Monitor

	DefaultTimeout time.Duration

	UseLayerSystem    bool
	ValidateShellDeps bool
}

// NewScriptRunner creates a script runner with the default registry.
func NewScriptRunner() (*ScriptRunner, error) {
	return nil, errWindowsUnsupported
}

// ScriptRunnerOptions configures the script runner.
type ScriptRunnerOptions struct {
	DataDir           string
	EnableLayers      bool
	EnableSandbox     bool
	ValidateShellDeps bool
	SupervisorConfig  *sandbox.SupervisorConfig
	DefaultTimeout    time.Duration
}

// NewScriptRunnerWithOptions creates a script runner with custom options.
func NewScriptRunnerWithOptions(opts ScriptRunnerOptions) (*ScriptRunner, error) {
	return nil, errWindowsUnsupported
}

// RunConfig specifies how to run a script.
type RunConfig struct {
	Script    string
	Args      []string
	Env       map[string]string
	WorkDir   string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	Timeout   time.Duration
	RuntimeID string
	Resources ResourceLimits
}

// ResourceLimits specifies resource constraints for execution.
type ResourceLimits struct {
	MemoryMB     int
	CPUPercent   int
	MaxProcesses int
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
	return nil, errWindowsUnsupported
}

// Close releases resources held by the script runner.
func (r *ScriptRunner) Close() error {
	return nil
}

// StartMonitor starts the background resource monitor.
func (r *ScriptRunner) StartMonitor(ctx context.Context) {}

// QuickRun executes a script with default settings.
func (r *ScriptRunner) QuickRun(ctx context.Context, script string, args ...string) (*RunResult, error) {
	return nil, errWindowsUnsupported
}

// RunInline executes inline code.
func (r *ScriptRunner) RunInline(ctx context.Context, code, runtimeID string) (*RunResult, error) {
	return nil, errWindowsUnsupported
}
