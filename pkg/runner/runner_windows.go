//go:build windows

// Package runner executes scripts in isolated environments.
// Windows stub returns errors for all operations.
package runner

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/cgroup"
	"github.com/lajosnagyuk/prisn/pkg/deps"
	prisnruntime "github.com/lajosnagyuk/prisn/pkg/runtime"
	"github.com/lajosnagyuk/prisn/pkg/venv"
)

var errWindowsUnsupported = errors.New("script execution is not supported on Windows; use WSL2 or a Linux VM")

// Runner executes scripts with proper isolation.
// Windows stub that returns errors.
type Runner struct {
	VenvManager         *venv.Manager
	DepDetector         *deps.Detector
	RuntimeRunner       *prisnruntime.ScriptRunner
	WorkDir             string
	DefaultTimeout      time.Duration
	UsePluggableRuntime bool
	cgroupManager       *cgroup.Manager
}

// New creates a new runner.
func New() (*Runner, error) {
	return nil, errWindowsUnsupported
}

// RunConfig specifies how to run a script.
type RunConfig struct {
	Script    string
	Args      []string
	Env       map[string]string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	Timeout   time.Duration
	WorkDir   string
	Resources ResourceConfig
}

// ResourceConfig specifies resource limits.
type ResourceConfig struct {
	MaxMemoryMB   int
	MaxCPUPercent int
	MaxProcesses  int
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
}

// Run executes a script with full dependency handling.
func (r *Runner) Run(ctx context.Context, cfg RunConfig) (*RunResult, error) {
	return nil, errWindowsUnsupported
}

// QuickRun executes a script with default settings.
func (r *Runner) QuickRun(ctx context.Context, script string, args ...string) (*RunResult, error) {
	return nil, errWindowsUnsupported
}

// RunInline executes inline code.
func (r *Runner) RunInline(ctx context.Context, code, runtimeID string) (*RunResult, error) {
	return nil, errWindowsUnsupported
}
