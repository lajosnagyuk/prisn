//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// windowsSandbox implements Sandbox using Windows Job Objects.
// This is a stub implementation that provides basic process management.
// Full Windows Job Object resource limiting is not yet implemented.
type windowsSandbox struct {
	dataDir  string
	contexts map[string]*windowsContext
	mu       sync.Mutex
}

// newPlatformSandbox creates a Windows sandbox.
func newPlatformSandbox(dataDir string) (Sandbox, error) {
	return &windowsSandbox{
		dataDir:  dataDir,
		contexts: make(map[string]*windowsContext),
	}, nil
}

func (s *windowsSandbox) ReserveSupervisor(cfg SupervisorConfig) error {
	// Priority boosting via SetPriorityClass not yet implemented
	return nil
}

func (s *windowsSandbox) Create(name string, limits Limits) (Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := &windowsContext{
		name:      name,
		limits:    limits,
		processes: make(map[int]*windowsProcess),
	}
	s.contexts[name] = ctx
	return ctx, nil
}

func (s *windowsSandbox) Stats(ctx Context) (ResourceStats, error) {
	wCtx, ok := ctx.(*windowsContext)
	if !ok {
		return ResourceStats{}, fmt.Errorf("invalid context type")
	}
	return wCtx.aggregateStats()
}

func (s *windowsSandbox) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, ctx := range s.contexts {
		ctx.Cleanup()
	}
	return nil
}

// windowsContext represents a sandboxed execution context on Windows.
// Note: Windows Job Object handle integration not yet implemented.
type windowsContext struct {
	name      string
	limits    Limits
	processes map[int]*windowsProcess
	mu        sync.Mutex
}

func (c *windowsContext) Name() string {
	return c.name
}

func (c *windowsContext) Limits() Limits {
	return c.limits
}

func (c *windowsContext) Wrap(cmd *exec.Cmd) error {
	// Job Object association not yet implemented; limits passed via environment
	if c.limits.MemoryHard > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PRISN_MEMORY_LIMIT=%d", c.limits.MemoryHard))
	}
	if c.limits.MaxPIDs > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PRISN_PROCESS_LIMIT=%d", c.limits.MaxPIDs))
	}
	return nil
}

func (c *windowsContext) Process(pid int) (Process, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	proc := &windowsProcess{
		pid:     pid,
		context: c,
		started: time.Now(),
	}

	c.processes[pid] = proc
	return proc, nil
}

func (c *windowsContext) Cleanup() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, proc := range c.processes {
		proc.Signal(os.Kill)
	}

	c.processes = make(map[int]*windowsProcess)
	return nil
}

func (c *windowsContext) aggregateStats() (ResourceStats, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	stats := ResourceStats{
		PIDs:        len(c.processes),
		MemoryLimit: c.limits.MemoryHard,
		State:       StateRunning,
	}

	return stats, nil
}

// windowsProcess represents a process on Windows.
type windowsProcess struct {
	pid     int
	context *windowsContext
	started time.Time
}

func (p *windowsProcess) Pid() int {
	return p.pid
}

func (p *windowsProcess) Stats() (ResourceStats, error) {
	stats := ResourceStats{
		Uptime:      time.Since(p.started),
		MemoryLimit: p.context.limits.MemoryHard,
		State:       StateRunning,
	}
	return stats, nil
}

func (p *windowsProcess) Pause() error {
	// Windows process suspension requires NtSuspendProcess
	return fmt.Errorf("pause not implemented on Windows")
}

func (p *windowsProcess) Resume() error {
	// Windows process resumption requires NtResumeProcess
	return fmt.Errorf("resume not implemented on Windows")
}

func (p *windowsProcess) Signal(sig os.Signal) error {
	// Windows doesn't have Unix signals
	if sig == os.Kill {
		// TerminateProcess integration not yet implemented
		return nil
	}
	return fmt.Errorf("signal %v not supported on Windows", sig)
}

func (p *windowsProcess) Wait() error {
	// WaitForSingleObject integration not yet implemented
	return nil
}
