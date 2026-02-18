//go:build freebsd

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// freebsdSandbox implements Sandbox using FreeBSD primitives.
// FreeBSD supports jail(2) for isolation and rctl(8) for resource limits,
// but this implementation uses portable POSIX primitives:
// - setrlimit for memory/file limits
// - setpriority for CPU priority
// - standard signals for process control
type freebsdSandbox struct {
	dataDir  string
	contexts map[string]*freebsdContext
	mu       sync.Mutex
}

// newPlatformSandbox creates a FreeBSD sandbox.
func newPlatformSandbox(dataDir string) (Sandbox, error) {
	return &freebsdSandbox{
		dataDir:  dataDir,
		contexts: make(map[string]*freebsdContext),
	}, nil
}

func (s *freebsdSandbox) ReserveSupervisor(cfg SupervisorConfig) error {
	// Set ourselves to higher priority
	// Priority range is -20 (highest) to 20 (lowest), 0 is default
	return syscall.Setpriority(syscall.PRIO_PROCESS, 0, -10)
}

func (s *freebsdSandbox) Create(name string, limits Limits) (Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := &freebsdContext{
		name:      name,
		limits:    limits,
		processes: make(map[int]*freebsdProcess),
	}
	s.contexts[name] = ctx
	return ctx, nil
}

func (s *freebsdSandbox) Stats(ctx Context) (ResourceStats, error) {
	fCtx, ok := ctx.(*freebsdContext)
	if !ok {
		return ResourceStats{}, fmt.Errorf("invalid context type")
	}
	return fCtx.aggregateStats()
}

func (s *freebsdSandbox) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, ctx := range s.contexts {
		ctx.Cleanup()
	}
	return nil
}

// freebsdContext represents a sandboxed execution context on FreeBSD.
type freebsdContext struct {
	name      string
	limits    Limits
	processes map[int]*freebsdProcess
	mu        sync.Mutex
}

func (c *freebsdContext) Name() string {
	return c.name
}

func (c *freebsdContext) Limits() Limits {
	return c.limits
}

func (c *freebsdContext) Wrap(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	// Set process group for signal handling
	cmd.SysProcAttr.Setpgid = true

	// Store limits in environment for the child to apply
	if c.limits.MemoryHard > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PRISN_RLIMIT_AS=%d", c.limits.MemoryHard))
		cmd.Env = append(cmd.Env, fmt.Sprintf("PRISN_RLIMIT_DATA=%d", c.limits.MemoryHard))
	}
	if c.limits.MaxOpenFiles > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PRISN_RLIMIT_NOFILE=%d", c.limits.MaxOpenFiles))
	}
	if c.limits.MaxPIDs > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PRISN_RLIMIT_NPROC=%d", c.limits.MaxPIDs))
	}

	return nil
}

func (c *freebsdContext) Process(pid int) (Process, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	proc := &freebsdProcess{
		pid:     pid,
		context: c,
		started: time.Now(),
	}

	// Apply rlimits to the process
	// Note: On FreeBSD, we can't set rlimits for another process directly
	// The child process needs to set them itself using the PRISN_RLIMIT_* env vars

	// Set lower priority for workload processes
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, pid, 10); err != nil {
		// Not fatal, just means we don't get priority control
	}

	c.processes[pid] = proc
	return proc, nil
}

func (c *freebsdContext) Cleanup() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Kill all processes in this context
	for pid, proc := range c.processes {
		// Send SIGTERM first
		proc.Signal(syscall.SIGTERM)
		// Give it a moment
		time.Sleep(100 * time.Millisecond)
		// Then SIGKILL if still running
		if isFreeBSDProcessRunning(pid) {
			proc.Signal(syscall.SIGKILL)
		}
	}

	c.processes = make(map[int]*freebsdProcess)
	return nil
}

func (c *freebsdContext) aggregateStats() (ResourceStats, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	stats := ResourceStats{
		PIDs:        len(c.processes),
		MemoryLimit: c.limits.MemoryHard,
		State:       StateRunning,
	}

	// Aggregate stats from all processes
	for _, proc := range c.processes {
		if procStats, err := proc.getProcessStats(); err == nil {
			stats.MemoryBytes += procStats.MemoryBytes
			stats.CPUPercent += procStats.CPUPercent
		}
	}

	if stats.MemoryLimit > 0 {
		stats.MemoryUsage = float64(stats.MemoryBytes) / float64(stats.MemoryLimit) * 100
	}

	return stats, nil
}

// freebsdProcess represents a process on FreeBSD.
type freebsdProcess struct {
	pid     int
	context *freebsdContext
	started time.Time
}

func (p *freebsdProcess) Pid() int {
	return p.pid
}

func (p *freebsdProcess) Stats() (ResourceStats, error) {
	stats, err := p.getProcessStats()
	if err != nil {
		return stats, err
	}
	stats.Uptime = time.Since(p.started)
	stats.MemoryLimit = p.context.limits.MemoryHard
	if stats.MemoryLimit > 0 {
		stats.MemoryUsage = float64(stats.MemoryBytes) / float64(stats.MemoryLimit) * 100
	}
	return stats, nil
}

func (p *freebsdProcess) getProcessStats() (ResourceStats, error) {
	stats := ResourceStats{
		State: StateRunning,
	}

	// Get resource usage via getrusage
	var rusage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_CHILDREN, &rusage); err == nil {
		// rusage.Maxrss is in kilobytes on FreeBSD
		stats.MemoryBytes = int64(rusage.Maxrss) * 1024
	}

	// Check if process is still running
	if !isFreeBSDProcessRunning(p.pid) {
		stats.State = StateStopped
	}

	return stats, nil
}

func (p *freebsdProcess) Pause() error {
	return syscall.Kill(p.pid, syscall.SIGSTOP)
}

func (p *freebsdProcess) Resume() error {
	return syscall.Kill(p.pid, syscall.SIGCONT)
}

func (p *freebsdProcess) Signal(sig os.Signal) error {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return fmt.Errorf("unsupported signal type")
	}
	return syscall.Kill(p.pid, s)
}

func (p *freebsdProcess) Wait() error {
	var status syscall.WaitStatus
	_, err := syscall.Wait4(p.pid, &status, 0, nil)
	return err
}

// isFreeBSDProcessRunning checks if a process is still running.
func isFreeBSDProcessRunning(pid int) bool {
	// Signal 0 doesn't send a signal but checks if process exists
	err := syscall.Kill(pid, 0)
	return err == nil
}
