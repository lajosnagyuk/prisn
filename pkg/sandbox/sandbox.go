// Package sandbox provides cross-platform resource protection for workloads.
// This is NOT isolation - processes can see each other. This is protection:
// - Prevent runaway processes from killing prisn
// - Soft limits with warnings before action
// - Reserve resources for the supervisor
package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Sandbox manages resource protection for workloads.
type Sandbox interface {
	// ReserveSupervisor reserves resources for prisn itself.
	// These resources are protected from workloads.
	ReserveSupervisor(cfg SupervisorConfig) error

	// Create creates a sandboxed execution context.
	Create(name string, limits Limits) (Context, error)

	// Stats returns current resource usage for a context.
	Stats(ctx Context) (ResourceStats, error)

	// Close cleans up sandbox resources.
	Close() error
}

// SupervisorConfig defines resources reserved for prisn.
type SupervisorConfig struct {
	MinCPU    float64 // Minimum CPU cores guaranteed (e.g., 0.5)
	MinMemory int64   // Minimum memory in bytes guaranteed (e.g., 256MB)
}

// Limits defines resource limits for a workload.
type Limits struct {
	// Memory limits
	MemorySoft int64 // Soft limit - warn at this point
	MemoryHard int64 // Hard limit - kill/pause at this point

	// CPU limits
	CPUCores float64 // Max CPU cores (e.g., 2.0)
	CPUQuota int     // CPU quota as percentage (100 = 1 core)

	// Process limits
	MaxPIDs int // Maximum number of processes (fork bomb protection)

	// File limits
	MaxOpenFiles int // Maximum open file descriptors

	// Network policy
	Network NetworkPolicy

	// Timeout for jobs (0 = no timeout)
	Timeout time.Duration
}

// NetworkPolicy defines network access restrictions.
type NetworkPolicy int

const (
	NetworkAllowAll   NetworkPolicy = iota // Full network access
	NetworkLocalOnly                       // Only localhost
	NetworkDisabled                        // No network
)

// DefaultLimits returns sensible defaults for most workloads.
func DefaultLimits() Limits {
	return Limits{
		MemorySoft:   512 * 1024 * 1024,  // 512MB soft
		MemoryHard:   1024 * 1024 * 1024, // 1GB hard
		CPUCores:     2.0,
		MaxPIDs:      256,
		MaxOpenFiles: 1024,
		Network:      NetworkAllowAll,
		Timeout:      0, // No timeout for services
	}
}

// UnlimitedLimits returns limits that effectively don't limit.
// Use with --unlimited flag.
func UnlimitedLimits() Limits {
	return Limits{
		MemorySoft:   0, // 0 = unlimited
		MemoryHard:   0,
		CPUCores:     0,
		MaxPIDs:      0,
		MaxOpenFiles: 0,
		Network:      NetworkAllowAll,
		Timeout:      0,
	}
}

// Context represents a sandboxed execution environment.
type Context interface {
	// Name returns the context name.
	Name() string

	// Limits returns the configured limits.
	Limits() Limits

	// Wrap wraps an exec.Cmd to run within this context.
	Wrap(cmd *exec.Cmd) error

	// Process returns a handle to monitor/control a running process.
	Process(pid int) (Process, error)

	// Cleanup releases resources associated with this context.
	Cleanup() error
}

// Process provides control over a sandboxed process.
type Process interface {
	// Pid returns the process ID.
	Pid() int

	// Stats returns current resource usage.
	Stats() (ResourceStats, error)

	// Pause suspends the process (SIGSTOP on Unix).
	Pause() error

	// Resume continues a paused process (SIGCONT on Unix).
	Resume() error

	// Signal sends a signal to the process.
	Signal(sig os.Signal) error

	// Wait waits for the process to exit.
	Wait() error
}

// ResourceStats contains current resource usage.
type ResourceStats struct {
	CPUPercent   float64       // CPU usage as percentage
	MemoryBytes  int64         // Current memory usage
	MemoryLimit  int64         // Configured limit
	MemoryUsage  float64       // Memory usage as percentage of limit
	PIDs         int           // Number of processes
	OpenFiles    int           // Number of open files
	Uptime       time.Duration // How long the process has been running
	State        ProcessState  // Current state
}

// ProcessState represents the current state of a process.
type ProcessState int

const (
	StateRunning ProcessState = iota
	StatePaused
	StateStopped
	StateUnknown
)

func (s ProcessState) String() string {
	switch s {
	case StateRunning:
		return "running"
	case StatePaused:
		return "paused"
	case StateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// EscalationPolicy defines how to handle resource pressure.
type EscalationPolicy struct {
	WarnAt    float64       // Percentage of limit to warn (e.g., 0.8)
	PauseAt   float64       // Percentage of limit to pause (e.g., 0.95)
	KillAt    float64       // Percentage of limit to kill (e.g., 1.0)
	GracePeriod time.Duration // Time to wait before killing after pause
}

// DefaultEscalationPolicy returns the default escalation policy.
func DefaultEscalationPolicy() EscalationPolicy {
	return EscalationPolicy{
		WarnAt:    0.80,
		PauseAt:   0.95,
		KillAt:    1.0,
		GracePeriod: 30 * time.Second,
	}
}

// Monitor watches processes and enforces escalation policies.
type Monitor struct {
	sandbox   Sandbox
	policy    EscalationPolicy
	processes map[int]*monitoredProcess
	mu        sync.Mutex
	done      chan struct{}

	// Callbacks
	OnWarn  func(name string, stats ResourceStats)
	OnPause func(name string, stats ResourceStats)
	OnKill  func(name string, stats ResourceStats)
}

type monitoredProcess struct {
	ctx        Context
	process    Process
	warnedAt   time.Time
	pausedAt   time.Time
}

// NewMonitor creates a new resource monitor.
func NewMonitor(sandbox Sandbox, policy EscalationPolicy) *Monitor {
	return &Monitor{
		sandbox:   sandbox,
		policy:    policy,
		processes: make(map[int]*monitoredProcess),
		done:      make(chan struct{}),
	}
}

// Add starts monitoring a process.
func (m *Monitor) Add(ctx Context, proc Process) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.processes[proc.Pid()] = &monitoredProcess{
		ctx:     ctx,
		process: proc,
	}
}

// Remove stops monitoring a process.
func (m *Monitor) Remove(pid int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.processes, pid)
}

// Start begins the monitoring loop.
func (m *Monitor) Start(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.done:
			return
		case <-ticker.C:
			m.check()
		}
	}
}

// Stop stops the monitoring loop.
func (m *Monitor) Stop() {
	close(m.done)
}

func (m *Monitor) check() {
	m.mu.Lock()
	processes := make([]*monitoredProcess, 0, len(m.processes))
	for _, p := range m.processes {
		processes = append(processes, p)
	}
	m.mu.Unlock()

	for _, mp := range processes {
		stats, err := mp.process.Stats()
		if err != nil {
			continue
		}

		limits := mp.ctx.Limits()
		if limits.MemoryHard == 0 {
			continue // Unlimited
		}

		usage := float64(stats.MemoryBytes) / float64(limits.MemoryHard)

		switch {
		case usage >= m.policy.KillAt:
			// Kill
			if m.OnKill != nil {
				m.OnKill(mp.ctx.Name(), stats)
			}
			mp.process.Signal(os.Kill)

		case usage >= m.policy.PauseAt:
			// Pause if not already paused
			if mp.pausedAt.IsZero() {
				if m.OnPause != nil {
					m.OnPause(mp.ctx.Name(), stats)
				}
				mp.process.Pause()
				mp.pausedAt = time.Now()
			} else if time.Since(mp.pausedAt) > m.policy.GracePeriod {
				// Grace period expired, kill
				if m.OnKill != nil {
					m.OnKill(mp.ctx.Name(), stats)
				}
				mp.process.Signal(os.Kill)
			}

		case usage >= m.policy.WarnAt:
			// Warn if not warned recently
			if time.Since(mp.warnedAt) > 30*time.Second {
				if m.OnWarn != nil {
					m.OnWarn(mp.ctx.Name(), stats)
				}
				mp.warnedAt = time.Now()
			}

		default:
			// Below warning threshold - resume if paused
			if !mp.pausedAt.IsZero() {
				mp.process.Resume()
				mp.pausedAt = time.Time{}
			}
		}
	}
}

// New creates a new Sandbox appropriate for the current platform.
func New(dataDir string) (Sandbox, error) {
	return newPlatformSandbox(dataDir)
}

// MustNew creates a new Sandbox, panicking on error.
func MustNew(dataDir string) Sandbox {
	s, err := New(dataDir)
	if err != nil {
		panic(fmt.Sprintf("failed to create sandbox: %v", err))
	}
	return s
}
