//go:build !windows

// Package supervisor manages long-running service processes.
//
// Design: Keep services alive. Restart on crash. Back off on repeated failures.
// Don't spam. Don't give up. Just keep it running.
package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/log"
	"github.com/lajosnagyuk/prisn/pkg/sandbox"
)

// Service represents a managed service process.
type Service struct {
	ID      string
	Name    string
	Command string
	Args    []string
	Dir     string
	Env     []string
	Port    int // For health checks

	// Behavior
	RestartPolicy   RestartPolicy
	MaxRestarts     int           // 0 = unlimited
	RestartDelay    time.Duration // Base delay between restarts
	MaxRestartDelay time.Duration // Cap for exponential backoff
	HealthCheck     *HealthCheck

	// Resource limits (optional)
	Resources *ResourceLimits

	// Runtime state
	mu               sync.Mutex
	cmd              *exec.Cmd
	pid              int
	state            ServiceState
	restarts         int
	lastStart        time.Time
	lastExit         time.Time
	lastExitCode     int
	consecutiveFails int
	stopCh           chan struct{}
	stopOnce         sync.Once // Ensures stopCh is only closed once
	sandboxCtx       sandbox.Context
	sandboxProc      sandbox.Process
}

// ResourceLimits specifies resource constraints for a service.
type ResourceLimits struct {
	MemoryMB     int // Maximum memory in megabytes (0 = unlimited)
	CPUPercent   int // CPU limit as percentage (100 = 1 core, 0 = unlimited)
	MaxProcesses int // Maximum number of child processes (0 = unlimited)
}

// requestStop safely signals the service to stop (prevents double-close panic).
func (svc *Service) requestStop() {
	svc.stopOnce.Do(func() {
		close(svc.stopCh)
	})
}

// RestartPolicy determines when to restart.
type RestartPolicy string

const (
	RestartAlways    RestartPolicy = "always"    // Always restart (default for services)
	RestartOnFailure RestartPolicy = "on-failure" // Only restart on non-zero exit
	RestartNever     RestartPolicy = "never"     // Never restart (for jobs)
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
	Type     string        // "tcp", "http", "exec"
	Target   string        // Address or command
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
	ExitCode  int  // Only valid for transitions to Stopped/Failed
	Restarts  int
	Timestamp time.Time
}

// StateChangeHandler is called when a service state changes.
type StateChangeHandler func(event StateChangeEvent)

// Supervisor manages multiple services.
type Supervisor struct {
	mu        sync.RWMutex
	services  map[string]*Service
	stopCh    chan struct{}
	stopOnce  sync.Once // Ensures stopCh is only closed once
	wg        sync.WaitGroup
	onChange  StateChangeHandler

	// Optional sandbox for resource protection
	sandbox sandbox.Sandbox
	monitor *sandbox.Monitor
}

// requestStop safely signals the supervisor to stop (prevents double-close panic).
func (s *Supervisor) requestStop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
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
	s := New()
	s.sandbox = sb
	s.monitor = sandbox.NewMonitor(sb, sandbox.DefaultEscalationPolicy())

	// Set up monitor callbacks
	s.monitor.OnWarn = func(name string, stats sandbox.ResourceStats) {
		log.Warn("%s: approaching resource limit (memory: %.1f%%, %s)",
			name, stats.MemoryUsage, formatBytes(stats.MemoryBytes))
	}
	s.monitor.OnPause = func(name string, stats sandbox.ResourceStats) {
		log.Warn("%s: paused due to resource pressure (memory: %.1f%%, %s)",
			name, stats.MemoryUsage, formatBytes(stats.MemoryBytes))
	}
	s.monitor.OnKill = func(name string, stats sandbox.ResourceStats) {
		log.Fail("%s: killed due to resource limit exceeded (memory: %.1f%%, %s)",
			name, stats.MemoryUsage, formatBytes(stats.MemoryBytes))
	}

	return s
}

// formatBytes formats bytes into human-readable string.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// SetSandbox sets the sandbox for resource protection.
func (s *Supervisor) SetSandbox(sb sandbox.Sandbox) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sandbox = sb
	if sb != nil {
		s.monitor = sandbox.NewMonitor(sb, sandbox.DefaultEscalationPolicy())
	} else {
		s.monitor = nil
	}
}

// StartMonitor starts the background resource monitor.
func (s *Supervisor) StartMonitor() {
	if s.monitor != nil {
		go s.monitor.Start(context.Background())
	}
}

// OnStateChange registers a handler for state change events.
// Only one handler can be registered; subsequent calls replace the previous handler.
func (s *Supervisor) OnStateChange(handler StateChangeHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = handler
}

// emitStateChange sends a state change event to the registered handler.
// This should be called after the state has been changed (not while holding svc.mu).
func (s *Supervisor) emitStateChange(svc *Service, oldState, newState ServiceState, exitCode int) {
	if oldState == newState {
		return
	}

	s.mu.RLock()
	handler := s.onChange
	s.mu.RUnlock()

	if handler != nil {
		svc.mu.Lock()
		restarts := svc.restarts
		svc.mu.Unlock()

		handler(StateChangeEvent{
			ServiceID: svc.ID,
			Name:      svc.Name,
			OldState:  oldState,
			NewState:  newState,
			ExitCode:  exitCode,
			Restarts:  restarts,
			Timestamp: time.Now(),
		})
	}
}

// Add registers a service with the supervisor.
func (s *Supervisor) Add(svc *Service) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Set defaults
	if svc.RestartPolicy == "" {
		svc.RestartPolicy = RestartAlways
	}
	if svc.RestartDelay == 0 {
		svc.RestartDelay = time.Second
	}
	if svc.MaxRestartDelay == 0 {
		svc.MaxRestartDelay = 30 * time.Second
	}

	svc.state = StateStopped
	svc.stopCh = make(chan struct{})
	s.services[svc.ID] = svc
}

// Start starts a service.
func (s *Supervisor) Start(id string) error {
	s.mu.RLock()
	svc, ok := s.services[id]
	s.mu.RUnlock()

	if !ok {
		return nil
	}

	s.wg.Add(1)
	go s.supervise(svc)
	return nil
}

// StartAll starts all registered services.
func (s *Supervisor) StartAll() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, svc := range s.services {
		s.wg.Add(1)
		go s.supervise(svc)
	}
}

// Stop stops a service gracefully.
func (s *Supervisor) Stop(id string) error {
	s.mu.RLock()
	svc, ok := s.services[id]
	s.mu.RUnlock()

	if !ok {
		return nil
	}

	svc.requestStop()
	return s.terminateProcess(svc)
}

// StopAll stops all services.
func (s *Supervisor) StopAll() {
	s.StopAllWithTimeout(30 * time.Second)
}

// StopAllWithTimeout stops all services with a configurable timeout.
func (s *Supervisor) StopAllWithTimeout(timeout time.Duration) {
	s.requestStop()

	s.mu.RLock()
	services := make([]*Service, 0, len(s.services))
	for _, svc := range s.services {
		services = append(services, svc)
	}
	s.mu.RUnlock()

	if len(services) == 0 {
		return
	}

	log.Start("stopping %d services (timeout: %v)", len(services), timeout)

	// Stop all services in parallel
	var wg sync.WaitGroup
	for _, svc := range services {
		wg.Add(1)
		go func(svc *Service) {
			defer wg.Done()
			svc.requestStop()
			s.terminateProcessWithTimeout(svc, timeout)
		}(svc)
	}

	// Wait for all services to stop
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Done("all services stopped")
	case <-time.After(timeout + 5*time.Second):
		log.Warn("some services did not stop in time")
	}

	s.wg.Wait()
}

// DrainAndStop gracefully drains connections then stops services.
// This gives services time to finish in-flight requests.
func (s *Supervisor) DrainAndStop(drainTimeout, stopTimeout time.Duration) {
	s.mu.RLock()
	count := len(s.services)
	s.mu.RUnlock()

	if count == 0 {
		return
	}

	log.Start("draining %d services (drain: %v, stop: %v)", count, drainTimeout, stopTimeout)

	// Phase 1: Mark as draining (stop accepting new connections)
	// In a real implementation, this would signal to a load balancer
	// For now, we just wait to let in-flight requests complete
	log.Info("waiting %v for in-flight requests to complete", drainTimeout)
	time.Sleep(drainTimeout)

	// Phase 2: Stop services
	s.StopAllWithTimeout(stopTimeout)
}

// Shutdown stops all services and releases all resources.
func (s *Supervisor) Shutdown() {
	s.StopAll()

	// Stop the resource monitor
	if s.monitor != nil {
		s.monitor.Stop()
	}

	// Close the sandbox
	if s.sandbox != nil {
		s.sandbox.Close()
	}
}

// Status returns the status of a service.
func (s *Supervisor) Status(id string) *ServiceStatus {
	s.mu.RLock()
	svc, ok := s.services[id]
	s.mu.RUnlock()

	if !ok {
		return nil
	}

	svc.mu.Lock()
	defer svc.mu.Unlock()

	return &ServiceStatus{
		ID:           svc.ID,
		Name:         svc.Name,
		State:        svc.state,
		PID:          svc.pid,
		Restarts:     svc.restarts,
		LastStart:    svc.lastStart,
		LastExit:     svc.lastExit,
		LastExitCode: svc.lastExitCode,
	}
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

// ListStatus returns status of all services.
func (s *Supervisor) ListStatus() []*ServiceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*ServiceStatus, 0, len(s.services))
	for id := range s.services {
		if status := s.Status(id); status != nil {
			result = append(result, status)
		}
	}
	return result
}

// supervise is the main supervision loop for a service.
func (s *Supervisor) supervise(svc *Service) {
	defer s.wg.Done()

	for {
		select {
		case <-s.stopCh:
			return
		case <-svc.stopCh:
			return
		default:
		}

		// Start the process
		if err := s.startProcess(svc); err != nil {
			log.Fail("%s: couldn't start: %v", svc.Name, err)
			svc.mu.Lock()
			oldState := svc.state
			svc.state = StateFailed
			svc.consecutiveFails++
			svc.mu.Unlock()
			s.emitStateChange(svc, oldState, StateFailed, -1)
		} else {
			// Wait for it to exit
			s.waitForExit(svc)
		}

		// Check restart policy
		svc.mu.Lock()
		shouldRestart := s.shouldRestart(svc)
		delay := s.calculateBackoff(svc)
		svc.mu.Unlock()

		if !shouldRestart {
			log.Info("%s: not restarting (policy: %s)", svc.Name, svc.RestartPolicy)
			return
		}

		// Backoff before restart
		svc.mu.Lock()
		oldState := svc.state
		svc.state = StateBackoff
		svc.mu.Unlock()
		s.emitStateChange(svc, oldState, StateBackoff, 0)

		log.Wait("%s: restarting in %v", svc.Name, delay)

		select {
		case <-time.After(delay):
		case <-s.stopCh:
			return
		case <-svc.stopCh:
			return
		}
	}
}

// startProcess starts the service process.
func (s *Supervisor) startProcess(svc *Service) error {
	svc.mu.Lock()
	oldState := svc.state
	svc.state = StateStarting
	svc.lastStart = time.Now()
	svc.mu.Unlock()
	s.emitStateChange(svc, oldState, StateStarting, 0)

	cmd := exec.Command(svc.Command, svc.Args...)
	cmd.Dir = svc.Dir
	cmd.Env = append(os.Environ(), svc.Env...)

	// Set process group for clean termination
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Set up sandbox context if available and resource limits are specified
	var sandboxCtx sandbox.Context
	if s.sandbox != nil && svc.Resources != nil && hasResourceLimits(svc.Resources) {
		limits := sandbox.Limits{
			MemorySoft:   int64(svc.Resources.MemoryMB) * 1024 * 1024 * 80 / 100, // 80% of hard
			MemoryHard:   int64(svc.Resources.MemoryMB) * 1024 * 1024,
			CPUQuota:     svc.Resources.CPUPercent,
			MaxPIDs:      svc.Resources.MaxProcesses,
			MaxOpenFiles: 1024, // Default
		}
		if limits.MaxPIDs == 0 {
			limits.MaxPIDs = 256 // Default fork bomb protection
		}

		ctx, err := s.sandbox.Create(fmt.Sprintf("svc-%s-%d", svc.ID, time.Now().UnixNano()), limits)
		if err != nil {
			log.Warn("%s: failed to create sandbox context: %v", svc.Name, err)
		} else {
			sandboxCtx = ctx
			// Let sandbox wrap the command
			sandboxCtx.Wrap(cmd)
		}
	}

	if err := cmd.Start(); err != nil {
		if sandboxCtx != nil {
			sandboxCtx.Cleanup()
		}
		return err
	}

	// Register process with sandbox
	var sandboxProc sandbox.Process
	if sandboxCtx != nil {
		proc, err := sandboxCtx.Process(cmd.Process.Pid)
		if err != nil {
			log.Warn("%s: failed to register process with sandbox: %v", svc.Name, err)
		} else {
			sandboxProc = proc
			// Add to monitor
			if s.monitor != nil {
				s.monitor.Add(sandboxCtx, sandboxProc)
			}
		}
	}

	svc.mu.Lock()
	svc.cmd = cmd
	svc.pid = cmd.Process.Pid
	svc.state = StateRunning
	svc.restarts++
	restarts := svc.restarts
	svc.sandboxCtx = sandboxCtx
	svc.sandboxProc = sandboxProc
	svc.mu.Unlock()
	s.emitStateChange(svc, StateStarting, StateRunning, 0)

	log.OK("%s: started (pid %d, restart #%d)", svc.Name, svc.pid, restarts)
	return nil
}

// hasResourceLimits checks if any resource limits are set.
func hasResourceLimits(r *ResourceLimits) bool {
	return r.MemoryMB > 0 || r.CPUPercent > 0 || r.MaxProcesses > 0
}

// waitForExit waits for the process to exit and records the result.
func (s *Supervisor) waitForExit(svc *Service) {
	svc.mu.Lock()
	cmd := svc.cmd
	sandboxCtx := svc.sandboxCtx
	sandboxProc := svc.sandboxProc
	svc.mu.Unlock()

	if cmd == nil {
		return
	}

	err := cmd.Wait()

	// Clean up sandbox resources
	if sandboxProc != nil && s.monitor != nil {
		s.monitor.Remove(sandboxProc.Pid())
	}
	if sandboxCtx != nil {
		sandboxCtx.Cleanup()
	}

	svc.mu.Lock()
	svc.lastExit = time.Now()
	svc.pid = 0
	svc.cmd = nil
	svc.sandboxCtx = nil
	svc.sandboxProc = nil

	var newState ServiceState
	var exitCode int

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			svc.lastExitCode = exitCode
			svc.consecutiveFails++
			svc.state = StateFailed
			newState = StateFailed
			duration := svc.lastExit.Sub(svc.lastStart)
			svc.mu.Unlock()
			s.emitStateChange(svc, StateRunning, StateFailed, exitCode)
			log.Fail("%s: exited %d after %v", svc.Name, exitCode, duration)
		} else {
			exitCode = -1
			svc.lastExitCode = exitCode
			svc.consecutiveFails++
			svc.state = StateFailed
			newState = StateFailed
			svc.mu.Unlock()
			s.emitStateChange(svc, StateRunning, StateFailed, exitCode)
			log.Fail("%s: crashed: %v", svc.Name, err)
		}
	} else {
		exitCode = 0
		svc.lastExitCode = 0
		svc.consecutiveFails = 0 // Reset on clean exit
		svc.state = StateStopped
		newState = StateStopped
		duration := svc.lastExit.Sub(svc.lastStart)
		svc.mu.Unlock()
		s.emitStateChange(svc, StateRunning, StateStopped, exitCode)
		log.Done("%s: exited cleanly after %v", svc.Name, duration)
	}
	_ = newState // Suppress unused variable warning
}

// shouldRestart determines if the service should be restarted.
func (s *Supervisor) shouldRestart(svc *Service) bool {
	switch svc.RestartPolicy {
	case RestartAlways:
		return true
	case RestartOnFailure:
		return svc.lastExitCode != 0
	case RestartNever:
		return false
	default:
		return false
	}
}

// calculateBackoff calculates the restart delay with exponential backoff.
func (s *Supervisor) calculateBackoff(svc *Service) time.Duration {
	// Exponential backoff: delay * 2^(consecutive_fails - 1)
	delay := svc.RestartDelay
	for i := 1; i < svc.consecutiveFails && delay < svc.MaxRestartDelay; i++ {
		delay *= 2
	}
	if delay > svc.MaxRestartDelay {
		delay = svc.MaxRestartDelay
	}

	// Reset backoff if it's been a while since last failure
	if time.Since(svc.lastExit) > svc.MaxRestartDelay*2 {
		svc.consecutiveFails = 0
		delay = svc.RestartDelay
	}

	return delay
}

// terminateProcess gracefully terminates a process with default timeout.
func (s *Supervisor) terminateProcess(svc *Service) error {
	return s.terminateProcessWithTimeout(svc, 10*time.Second)
}

// terminateProcessWithTimeout gracefully terminates a process with configurable timeout.
func (s *Supervisor) terminateProcessWithTimeout(svc *Service, timeout time.Duration) error {
	svc.mu.Lock()
	cmd := svc.cmd
	pid := svc.pid
	name := svc.Name
	svc.mu.Unlock()

	if cmd == nil || pid == 0 {
		return nil
	}

	log.Info("%s: stopping (pid %d)", name, pid)

	// Try SIGTERM first
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		// Process might already be gone
		log.Done("stopped service %s", name)
		return nil
	}

	// Wait for graceful shutdown
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Done("stopped service %s", name)
		return nil
	case <-time.After(timeout):
		// Force kill
		log.Warn("%s: force killing (pid %d) after %v", name, pid, timeout)
		syscall.Kill(-pid, syscall.SIGKILL)
		<-done // Wait for process to actually exit
		return nil
	}
}
