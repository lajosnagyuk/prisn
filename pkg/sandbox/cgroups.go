//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// cgroupSandbox implements Sandbox using Linux cgroups v2.
type cgroupSandbox struct {
	root     string // cgroup hierarchy root (usually /sys/fs/cgroup)
	dataDir  string // prisn data directory
	basePath string // our cgroup path: /sys/fs/cgroup/prisn
	mu       sync.Mutex
	contexts map[string]*cgroupContext
}

// newPlatformSandbox creates a Linux cgroups-based sandbox.
func newPlatformSandbox(dataDir string) (Sandbox, error) {
	// Check if cgroups v2 is available
	if !isCgroupsV2() {
		return nil, fmt.Errorf("cgroups v2 not available (unified hierarchy required)")
	}

	root := "/sys/fs/cgroup"
	basePath := filepath.Join(root, "prisn")

	// Create our cgroup hierarchy
	if err := os.MkdirAll(basePath, 0755); err != nil {
		// If we can't create cgroups, fall back to rlimit-only mode
		return newRlimitSandbox(dataDir)
	}

	// Enable controllers in our cgroup
	if err := enableControllers(basePath); err != nil {
		// Controller setup failed, fall back
		return newRlimitSandbox(dataDir)
	}

	return &cgroupSandbox{
		root:     root,
		dataDir:  dataDir,
		basePath: basePath,
		contexts: make(map[string]*cgroupContext),
	}, nil
}

// isCgroupsV2 checks if cgroups v2 (unified hierarchy) is mounted.
func isCgroupsV2() bool {
	data, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "cgroup2")
}

// enableControllers enables CPU, memory, and PID controllers.
func enableControllers(path string) error {
	// Read available controllers from parent
	parentPath := filepath.Dir(path)
	data, err := os.ReadFile(filepath.Join(parentPath, "cgroup.controllers"))
	if err != nil {
		return err
	}

	controllers := strings.Fields(string(data))
	var enable []string
	for _, c := range controllers {
		switch c {
		case "cpu", "memory", "pids", "io":
			enable = append(enable, "+"+c)
		}
	}

	if len(enable) == 0 {
		return nil // No controllers to enable
	}

	// Enable controllers for subtree
	subtreeControl := filepath.Join(parentPath, "cgroup.subtree_control")
	return os.WriteFile(subtreeControl, []byte(strings.Join(enable, " ")), 0644)
}

func (s *cgroupSandbox) ReserveSupervisor(cfg SupervisorConfig) error {
	// Create a supervisor cgroup with guaranteed resources
	supervisorPath := filepath.Join(s.basePath, "supervisor")
	if err := os.MkdirAll(supervisorPath, 0755); err != nil {
		return fmt.Errorf("create supervisor cgroup: %w", err)
	}

	// Move ourselves into the supervisor cgroup
	if err := os.WriteFile(
		filepath.Join(supervisorPath, "cgroup.procs"),
		[]byte(strconv.Itoa(os.Getpid())),
		0644,
	); err != nil {
		return fmt.Errorf("move to supervisor cgroup: %w", err)
	}

	// Set CPU weight (higher = more priority)
	// Default weight is 100, we set supervisor to 1000
	if err := writeIfExists(filepath.Join(supervisorPath, "cpu.weight"), "1000"); err != nil {
		// Not fatal, just means we don't get priority
	}

	// Reserve memory for supervisor (soft limit)
	if cfg.MinMemory > 0 {
		// memory.low is a soft guarantee
		if err := writeIfExists(
			filepath.Join(supervisorPath, "memory.low"),
			strconv.FormatInt(cfg.MinMemory, 10),
		); err != nil {
			// Not fatal
		}
	}

	return nil
}

func (s *cgroupSandbox) Create(name string, limits Limits) (Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create workload cgroup
	workloadPath := filepath.Join(s.basePath, "workloads")
	if err := os.MkdirAll(workloadPath, 0755); err != nil {
		return nil, err
	}

	// Enable controllers for workloads subtree
	enableControllers(workloadPath)

	contextPath := filepath.Join(workloadPath, name)
	if err := os.MkdirAll(contextPath, 0755); err != nil {
		return nil, err
	}

	ctx := &cgroupContext{
		name:   name,
		path:   contextPath,
		limits: limits,
	}

	// Apply limits
	if err := ctx.applyLimits(); err != nil {
		os.RemoveAll(contextPath)
		return nil, err
	}

	s.contexts[name] = ctx
	return ctx, nil
}

func (s *cgroupSandbox) Stats(ctx Context) (ResourceStats, error) {
	cgCtx, ok := ctx.(*cgroupContext)
	if !ok {
		return ResourceStats{}, fmt.Errorf("invalid context type")
	}
	return cgCtx.readStats()
}

func (s *cgroupSandbox) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clean up all contexts
	for _, ctx := range s.contexts {
		ctx.Cleanup()
	}

	// Remove our cgroup hierarchy
	// This will fail if processes are still running, which is fine
	os.RemoveAll(filepath.Join(s.basePath, "workloads"))

	return nil
}

// cgroupContext represents a sandboxed execution context using cgroups.
type cgroupContext struct {
	name      string
	path      string
	limits    Limits
	processes map[int]*cgroupProcess
	mu        sync.Mutex
}

func (c *cgroupContext) Name() string {
	return c.name
}

func (c *cgroupContext) Limits() Limits {
	return c.limits
}

func (c *cgroupContext) Wrap(cmd *exec.Cmd) error {
	// Add process to cgroup after it starts
	// We do this via SysProcAttr to ensure it's in the cgroup from start
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	// Set process group for signal handling
	cmd.SysProcAttr.Setpgid = true

	// We'll move the process to cgroup after Start()
	// Store the cgroup path in env so we can use it
	cmd.Env = append(cmd.Env, "PRISN_CGROUP_PATH="+c.path)

	return nil
}

// AddProcess moves a running process into this cgroup.
func (c *cgroupContext) AddProcess(pid int) error {
	procsFile := filepath.Join(c.path, "cgroup.procs")
	return os.WriteFile(procsFile, []byte(strconv.Itoa(pid)), 0644)
}

func (c *cgroupContext) Process(pid int) (Process, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.processes == nil {
		c.processes = make(map[int]*cgroupProcess)
	}

	proc := &cgroupProcess{
		pid:     pid,
		context: c,
		started: time.Now(),
	}
	c.processes[pid] = proc

	// Move process to our cgroup
	if err := c.AddProcess(pid); err != nil {
		return nil, fmt.Errorf("add process to cgroup: %w", err)
	}

	return proc, nil
}

func (c *cgroupContext) Cleanup() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Kill all processes in the cgroup
	killFile := filepath.Join(c.path, "cgroup.kill")
	if _, err := os.Stat(killFile); err == nil {
		os.WriteFile(killFile, []byte("1"), 0644)
	}

	// Remove the cgroup (might fail if processes still exiting)
	return os.RemoveAll(c.path)
}

func (c *cgroupContext) applyLimits() error {
	// Memory limits
	if c.limits.MemoryHard > 0 {
		if err := writeIfExists(
			filepath.Join(c.path, "memory.max"),
			strconv.FormatInt(c.limits.MemoryHard, 10),
		); err != nil {
			return fmt.Errorf("set memory.max: %w", err)
		}
	}

	if c.limits.MemorySoft > 0 {
		if err := writeIfExists(
			filepath.Join(c.path, "memory.high"),
			strconv.FormatInt(c.limits.MemorySoft, 10),
		); err != nil {
			// Not fatal, high is optional
		}
	}

	// CPU limits
	if c.limits.CPUQuota > 0 {
		// cpu.max format: "$MAX $PERIOD" (in microseconds)
		// 100 = 1 core, 200 = 2 cores, etc.
		period := 100000 // 100ms period
		max := (c.limits.CPUQuota * period) / 100
		if err := writeIfExists(
			filepath.Join(c.path, "cpu.max"),
			fmt.Sprintf("%d %d", max, period),
		); err != nil {
			return fmt.Errorf("set cpu.max: %w", err)
		}
	} else if c.limits.CPUCores > 0 {
		period := 100000
		max := int(c.limits.CPUCores * float64(period))
		if err := writeIfExists(
			filepath.Join(c.path, "cpu.max"),
			fmt.Sprintf("%d %d", max, period),
		); err != nil {
			return fmt.Errorf("set cpu.max: %w", err)
		}
	}

	// PID limit (fork bomb protection)
	if c.limits.MaxPIDs > 0 {
		if err := writeIfExists(
			filepath.Join(c.path, "pids.max"),
			strconv.Itoa(c.limits.MaxPIDs),
		); err != nil {
			return fmt.Errorf("set pids.max: %w", err)
		}
	}

	return nil
}

func (c *cgroupContext) readStats() (ResourceStats, error) {
	stats := ResourceStats{}

	// Read memory stats
	memCurrent := filepath.Join(c.path, "memory.current")
	if data, err := os.ReadFile(memCurrent); err == nil {
		if val, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil {
			stats.MemoryBytes = val
		}
	}

	// Read memory limit
	memMax := filepath.Join(c.path, "memory.max")
	if data, err := os.ReadFile(memMax); err == nil {
		str := strings.TrimSpace(string(data))
		if str != "max" {
			if val, err := strconv.ParseInt(str, 10, 64); err == nil {
				stats.MemoryLimit = val
				if stats.MemoryLimit > 0 {
					stats.MemoryUsage = float64(stats.MemoryBytes) / float64(stats.MemoryLimit) * 100
				}
			}
		}
	}

	// Read CPU stats
	cpuStat := filepath.Join(c.path, "cpu.stat")
	if data, err := os.ReadFile(cpuStat); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "usage_usec ") {
				// Could calculate CPU percentage from this over time
			}
		}
	}

	// Count PIDs
	procs := filepath.Join(c.path, "cgroup.procs")
	if data, err := os.ReadFile(procs); err == nil {
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if lines[0] != "" {
			stats.PIDs = len(lines)
		}
	}

	stats.State = StateRunning

	return stats, nil
}

// cgroupProcess represents a process in a cgroup context.
type cgroupProcess struct {
	pid     int
	context *cgroupContext
	started time.Time
}

func (p *cgroupProcess) Pid() int {
	return p.pid
}

func (p *cgroupProcess) Stats() (ResourceStats, error) {
	// For individual process stats, we'd need to read /proc/[pid]/stat
	// For now, return cgroup-level stats
	stats, err := p.context.readStats()
	if err != nil {
		return stats, err
	}
	stats.Uptime = time.Since(p.started)
	return stats, nil
}

func (p *cgroupProcess) Pause() error {
	// Use cgroup freeze
	freezeFile := filepath.Join(p.context.path, "cgroup.freeze")
	if _, err := os.Stat(freezeFile); err == nil {
		return os.WriteFile(freezeFile, []byte("1"), 0644)
	}
	// Fall back to SIGSTOP
	return syscall.Kill(p.pid, syscall.SIGSTOP)
}

func (p *cgroupProcess) Resume() error {
	// Use cgroup freeze
	freezeFile := filepath.Join(p.context.path, "cgroup.freeze")
	if _, err := os.Stat(freezeFile); err == nil {
		return os.WriteFile(freezeFile, []byte("0"), 0644)
	}
	// Fall back to SIGCONT
	return syscall.Kill(p.pid, syscall.SIGCONT)
}

func (p *cgroupProcess) Signal(sig os.Signal) error {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return fmt.Errorf("unsupported signal type")
	}
	return syscall.Kill(p.pid, s)
}

func (p *cgroupProcess) Wait() error {
	var status syscall.WaitStatus
	_, err := syscall.Wait4(p.pid, &status, 0, nil)
	return err
}

// writeIfExists writes to a file only if it exists.
func writeIfExists(path, content string) error {
	if _, err := os.Stat(path); err != nil {
		return nil // File doesn't exist, skip
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// rlimitSandbox is a fallback when cgroups aren't available.
// Uses setrlimit for basic resource limits.
type rlimitSandbox struct {
	dataDir  string
	contexts map[string]*rlimitContext
	mu       sync.Mutex
}

func newRlimitSandbox(dataDir string) (Sandbox, error) {
	return &rlimitSandbox{
		dataDir:  dataDir,
		contexts: make(map[string]*rlimitContext),
	}, nil
}

func (s *rlimitSandbox) ReserveSupervisor(cfg SupervisorConfig) error {
	// Can't really reserve resources with rlimit alone
	// Just set ourselves to higher priority
	return syscall.Setpriority(syscall.PRIO_PROCESS, 0, -5)
}

func (s *rlimitSandbox) Create(name string, limits Limits) (Context, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := &rlimitContext{
		name:   name,
		limits: limits,
	}
	s.contexts[name] = ctx
	return ctx, nil
}

func (s *rlimitSandbox) Stats(ctx Context) (ResourceStats, error) {
	return ResourceStats{}, nil
}

func (s *rlimitSandbox) Close() error {
	return nil
}

type rlimitContext struct {
	name      string
	limits    Limits
	processes map[int]*rlimitProcess
	mu        sync.Mutex
}

func (c *rlimitContext) Name() string {
	return c.name
}

func (c *rlimitContext) Limits() Limits {
	return c.limits
}

func (c *rlimitContext) Wrap(cmd *exec.Cmd) error {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true

	// Set rlimits via environment (child will need to apply them)
	if c.limits.MemoryHard > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PRISN_RLIMIT_AS=%d", c.limits.MemoryHard))
	}
	if c.limits.MaxOpenFiles > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PRISN_RLIMIT_NOFILE=%d", c.limits.MaxOpenFiles))
	}
	if c.limits.MaxPIDs > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PRISN_RLIMIT_NPROC=%d", c.limits.MaxPIDs))
	}

	return nil
}

func (c *rlimitContext) Process(pid int) (Process, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.processes == nil {
		c.processes = make(map[int]*rlimitProcess)
	}

	proc := &rlimitProcess{
		pid:     pid,
		started: time.Now(),
	}
	c.processes[pid] = proc
	return proc, nil
}

func (c *rlimitContext) Cleanup() error {
	return nil
}

type rlimitProcess struct {
	pid     int
	started time.Time
}

func (p *rlimitProcess) Pid() int {
	return p.pid
}

func (p *rlimitProcess) Stats() (ResourceStats, error) {
	return ResourceStats{
		Uptime: time.Since(p.started),
		State:  StateRunning,
	}, nil
}

func (p *rlimitProcess) Pause() error {
	return syscall.Kill(p.pid, syscall.SIGSTOP)
}

func (p *rlimitProcess) Resume() error {
	return syscall.Kill(p.pid, syscall.SIGCONT)
}

func (p *rlimitProcess) Signal(sig os.Signal) error {
	s, ok := sig.(syscall.Signal)
	if !ok {
		return fmt.Errorf("unsupported signal type")
	}
	return syscall.Kill(p.pid, s)
}

func (p *rlimitProcess) Wait() error {
	var status syscall.WaitStatus
	_, err := syscall.Wait4(p.pid, &status, 0, nil)
	return err
}
