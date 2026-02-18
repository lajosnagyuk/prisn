//go:build darwin

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

// darwinSandbox implements Sandbox using macOS primitives.
// macOS doesn't have cgroups, so we use:
// - setrlimit for memory/file limits
// - nice/setpriority for CPU priority
// - launchd for process management (future)
type darwinSandbox struct {
	dataDir  string
	contexts map[string]*darwinContext
	mu       sync.Mutex
}

// newPlatformSandbox creates a macOS sandbox.
func newPlatformSandbox(dataDir string) (Sandbox, error) {
	return &darwinSandbox{
		dataDir:  dataDir,
		contexts: make(map[string]*darwinContext),
	}, nil
}

func (s *darwinSandbox) ReserveSupervisor(cfg SupervisorConfig) error {
	// Set ourselves to higher priority
	// Priority range is -20 (highest) to 20 (lowest), 0 is default
	return syscall.Setpriority(syscall.PRIO_PROCESS, 0, -10)
}

func (s *darwinSandbox) Create(name string, limits Limits) (Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := &darwinContext{
		name:      name,
		limits:    limits,
		processes: make(map[int]*darwinProcess),
	}
	s.contexts[name] = ctx
	return ctx, nil
}

func (s *darwinSandbox) Stats(ctx Context) (ResourceStats, error) {
	dCtx, ok := ctx.(*darwinContext)
	if !ok {
		return ResourceStats{}, fmt.Errorf("invalid context type")
	}
	return dCtx.aggregateStats()
}

func (s *darwinSandbox) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, ctx := range s.contexts {
		ctx.Cleanup()
	}
	return nil
}

// darwinContext represents a sandboxed execution context on macOS.
type darwinContext struct {
	name      string
	limits    Limits
	processes map[int]*darwinProcess
	mu        sync.Mutex
}

func (c *darwinContext) Name() string {
	return c.name
}

func (c *darwinContext) Limits() Limits {
	return c.limits
}

func (c *darwinContext) Wrap(cmd *exec.Cmd) error {
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

func (c *darwinContext) Process(pid int) (Process, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	proc := &darwinProcess{
		pid:     pid,
		context: c,
		started: time.Now(),
	}

	// Apply rlimits to the process
	// Note: On macOS, we can't set rlimits for another process directly
	// The child process needs to set them itself using the PRISN_RLIMIT_* env vars

	// Set lower priority for workload processes
	if err := syscall.Setpriority(syscall.PRIO_PROCESS, pid, 10); err != nil {
		// Not fatal, just means we don't get priority control
	}

	c.processes[pid] = proc
	return proc, nil
}

func (c *darwinContext) Cleanup() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Kill all processes in this context
	for pid, proc := range c.processes {
		// Send SIGTERM first
		proc.Signal(syscall.SIGTERM)
		// Give it a moment
		time.Sleep(100 * time.Millisecond)
		// Then SIGKILL if still running
		if isProcessRunning(pid) {
			proc.Signal(syscall.SIGKILL)
		}
	}

	c.processes = make(map[int]*darwinProcess)
	return nil
}

func (c *darwinContext) aggregateStats() (ResourceStats, error) {
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

// darwinProcess represents a process on macOS.
type darwinProcess struct {
	pid     int
	context *darwinContext
	started time.Time
}

func (p *darwinProcess) Pid() int {
	return p.pid
}

func (p *darwinProcess) Stats() (ResourceStats, error) {
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

func (p *darwinProcess) getProcessStats() (ResourceStats, error) {
	stats := ResourceStats{
		State: StateRunning,
	}

	// Get resource usage via getrusage
	var rusage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_CHILDREN, &rusage); err == nil {
		// rusage.Maxrss is in bytes on macOS (unlike Linux where it's KB)
		stats.MemoryBytes = rusage.Maxrss
	}

	// Check if process is still running
	if !isProcessRunning(p.pid) {
		stats.State = StateStopped
	}

	return stats, nil
}

func (p *darwinProcess) Pause() error {
	return syscall.Kill(p.pid, syscall.SIGSTOP)
}

func (p *darwinProcess) Resume() error {
	return syscall.Kill(p.pid, syscall.SIGCONT)
}

func (p *darwinProcess) Signal(sig os.Signal) error {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return fmt.Errorf("unsupported signal type")
	}
	return syscall.Kill(p.pid, s)
}

func (p *darwinProcess) Wait() error {
	var status syscall.WaitStatus
	_, err := syscall.Wait4(p.pid, &status, 0, nil)
	return err
}

// isProcessRunning checks if a process is still running.
func isProcessRunning(pid int) bool {
	// Signal 0 doesn't send a signal but checks if process exists
	err := syscall.Kill(pid, 0)
	return err == nil
}

// ApplyRlimits applies resource limits stored in environment variables.
// This should be called by child processes early in their execution.
func ApplyRlimits() error {
	// Memory limit (virtual address space)
	if val := os.Getenv("PRISN_RLIMIT_AS"); val != "" {
		var limit uint64
		if _, err := fmt.Sscanf(val, "%d", &limit); err == nil {
			var rlim unix.Rlimit
			rlim.Cur = limit
			rlim.Max = limit
			unix.Setrlimit(unix.RLIMIT_AS, &rlim)
		}
	}

	// Data segment limit
	if val := os.Getenv("PRISN_RLIMIT_DATA"); val != "" {
		var limit uint64
		if _, err := fmt.Sscanf(val, "%d", &limit); err == nil {
			var rlim unix.Rlimit
			rlim.Cur = limit
			rlim.Max = limit
			unix.Setrlimit(unix.RLIMIT_DATA, &rlim)
		}
	}

	// Open files limit
	if val := os.Getenv("PRISN_RLIMIT_NOFILE"); val != "" {
		var limit uint64
		if _, err := fmt.Sscanf(val, "%d", &limit); err == nil {
			var rlim unix.Rlimit
			rlim.Cur = limit
			rlim.Max = limit
			unix.Setrlimit(unix.RLIMIT_NOFILE, &rlim)
		}
	}

	// Process limit
	if val := os.Getenv("PRISN_RLIMIT_NPROC"); val != "" {
		var limit uint64
		if _, err := fmt.Sscanf(val, "%d", &limit); err == nil {
			var rlim unix.Rlimit
			rlim.Cur = limit
			rlim.Max = limit
			unix.Setrlimit(unix.RLIMIT_NPROC, &rlim)
		}
	}

	return nil
}
