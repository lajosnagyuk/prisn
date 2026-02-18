//go:build windows

// Package supervisor manages long-running service processes.
// Windows stub returns errors for all operations.
package supervisor

import (
	"errors"
	"sync"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/sandbox"
)

var errWindowsUnsupported = errors.New("supervisor is not supported on Windows; use WSL2 or a Linux VM")

// Service represents a managed service process.
type Service struct {
	ID      string
	Name    string
	Command string
	Args    []string
	Dir     string
	Env     []string
	Port    int

	RestartPolicy   RestartPolicy
	MaxRestarts     int
	RestartDelay    time.Duration
	MaxRestartDelay time.Duration
	HealthCheck     *HealthCheck

	Resources *ResourceLimits

	mu               sync.Mutex
	pid              int
	state            ServiceState
	restarts         int
	lastStart        time.Time
	lastExit         time.Time
	lastExitCode     int
	consecutiveFails int
	stopCh           chan struct{}
	stopOnce         sync.Once
	sandboxCtx       sandbox.Context
	sandboxProc      sandbox.Process
}

// ResourceLimits specifies resource constraints for a service.
type ResourceLimits struct {
	MemoryMB     int
	CPUPercent   int
	MaxProcesses int
	MaxOpenFiles int
}

// RestartPolicy determines when to restart.
type RestartPolicy string

const (
	RestartAlways    RestartPolicy = "always"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartNever     RestartPolicy = "never"
)

// ServiceState represents the current state.
type ServiceState string

const (
	StateStopped  ServiceState = "stopped"
	StateStarting ServiceState = "starting"
	StateRunning  ServiceState = "running"
	StateFailed   ServiceState = "failed"
	StateBackoff  ServiceState = "backoff"
)

// HealthCheck configuration.
type HealthCheck struct {
	Type     string
	Target   string
	Interval time.Duration
	Timeout  time.Duration
	Retries  int
}

// StateChangeEvent is emitted when a service's state changes.
type StateChangeEvent struct {
	ServiceID string
	Name      string
	OldState  ServiceState
	NewState  ServiceState
	ExitCode  int
	Restarts  int
	Timestamp time.Time
}

// StateChangeHandler is called when a service state changes.
type StateChangeHandler func(event StateChangeEvent)

// Supervisor manages multiple services.
type Supervisor struct {
	mu       sync.RWMutex
	services map[string]*Service
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	onChange StateChangeHandler

	sandbox sandbox.Sandbox
	monitor *sandbox.Monitor
}

// ServiceStatus is the public status view.
type ServiceStatus struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	State        ServiceState `json:"state"`
	PID          int          `json:"pid,omitempty"`
	Restarts     int          `json:"restarts"`
	LastStart    time.Time    `json:"last_start,omitempty"`
	LastExit     time.Time    `json:"last_exit,omitempty"`
	LastExitCode int          `json:"last_exit_code,omitempty"`
}

// New creates a new supervisor.
func New() *Supervisor {
	return &Supervisor{
		services: make(map[string]*Service),
		stopCh:   make(chan struct{}),
	}
}

// NewWithSandbox creates a supervisor with resource protection.
func NewWithSandbox(sb sandbox.Sandbox) *Supervisor {
	return New()
}

// SetSandbox sets the sandbox for resource protection.
func (s *Supervisor) SetSandbox(sb sandbox.Sandbox) {}

// StartMonitor starts the background resource monitor.
func (s *Supervisor) StartMonitor() {}

// OnStateChange registers a handler for state changes.
func (s *Supervisor) OnStateChange(handler StateChangeHandler) {
	s.onChange = handler
}

// Add adds a service to the supervisor.
func (s *Supervisor) Add(svc *Service) {}

// Start starts a specific service.
func (s *Supervisor) Start(id string) error {
	return errWindowsUnsupported
}

// StartAll starts all services.
func (s *Supervisor) StartAll() {}

// Stop stops a specific service.
func (s *Supervisor) Stop(id string) error {
	return errWindowsUnsupported
}

// StopAll stops all services.
func (s *Supervisor) StopAll() {}

// StopAllWithTimeout stops all services with a timeout.
func (s *Supervisor) StopAllWithTimeout(timeout time.Duration) {}

// DrainAndStop drains then stops all services.
func (s *Supervisor) DrainAndStop(drainTimeout, stopTimeout time.Duration) {}

// Shutdown stops the supervisor and all services.
func (s *Supervisor) Shutdown() {}

// Status returns the status of a specific service.
func (s *Supervisor) Status(id string) *ServiceStatus {
	return nil
}

// ListStatus returns status of all services.
func (s *Supervisor) ListStatus() []*ServiceStatus {
	return nil
}
