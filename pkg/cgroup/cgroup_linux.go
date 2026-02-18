//go:build linux

// Package cgroup provides cgroups v2 resource management for Linux.
//
// This implements THROTTLING instead of killing for resource limits.
// Heavy scripts can drive CPU to 100% for a while - they'll be throttled,
// not killed.
package cgroup

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	// cgroupRoot is the default cgroups v2 mount point
	cgroupRoot = "/sys/fs/cgroup"
	// prisnCgroupName is the parent cgroup for all prisn processes
	prisnCgroupName = "prisn.slice"
)

// Manager handles cgroup v2 operations.
type Manager struct {
	// root is the cgroups v2 root path
	root string
	// available indicates if cgroups v2 is available
	available bool
}

// NewManager creates a cgroup manager.
func NewManager() *Manager {
	m := &Manager{
		root: cgroupRoot,
	}
	m.available = m.checkAvailable()
	return m
}

// Available returns true if cgroups v2 is available.
func (m *Manager) Available() bool {
	return m.available
}

// checkAvailable verifies cgroups v2 is mounted and usable.
func (m *Manager) checkAvailable() bool {
	// Check for cgroups v2 unified hierarchy
	controllersPath := filepath.Join(m.root, "cgroup.controllers")
	data, err := os.ReadFile(controllersPath)
	if err != nil {
		return false
	}

	// Check we have the controllers we need
	controllers := string(data)
	return strings.Contains(controllers, "cpu") && strings.Contains(controllers, "memory")
}

// Limits specifies resource limits for a process.
type Limits struct {
	// CPUPercent is the CPU limit as a percentage (0 = unlimited)
	// 100 = 1 full core, 200 = 2 cores, etc.
	// Uses throttling, not killing - process will be slowed down, not terminated.
	CPUPercent int

	// MemoryBytes is the memory limit in bytes (0 = unlimited)
	// Uses memory.high for soft limit (triggers reclaim/throttle) and
	// memory.max as hard limit (OOM only if truly needed).
	MemoryBytes int64

	// MemorySoftPercent is what percentage of MemoryBytes triggers throttling (default: 90)
	// When memory.high is hit, the kernel throttles allocations and reclaims.
	// This is gentler than hitting memory.max which can cause OOM.
	MemorySoftPercent int

	// MaxProcesses limits the number of processes/threads (0 = unlimited)
	MaxProcesses int
}

// CgroupPath returns the path to a cgroup.
func (m *Manager) CgroupPath(name string) string {
	return filepath.Join(m.root, prisnCgroupName, name)
}

// Create creates a new cgroup for a process with the given limits.
// Returns the cgroup name to be cleaned up later.
func (m *Manager) Create(pid int, limits Limits) (string, error) {
	if !m.available {
		return "", fmt.Errorf("cgroups v2 not available")
	}

	// Ensure parent cgroup exists
	parentPath := filepath.Join(m.root, prisnCgroupName)
	if err := os.MkdirAll(parentPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create parent cgroup: %w", err)
	}

	// Enable controllers on parent
	if err := m.enableControllers(parentPath); err != nil {
		// Not fatal - controllers might already be enabled
	}

	// Create cgroup for this process
	cgroupName := fmt.Sprintf("prisn-%d", pid)
	cgroupPath := filepath.Join(parentPath, cgroupName)
	if err := os.MkdirAll(cgroupPath, 0755); err != nil {
		return "", fmt.Errorf("failed to create cgroup: %w", err)
	}

	// Apply limits
	if err := m.applyLimits(cgroupPath, limits); err != nil {
		os.RemoveAll(cgroupPath)
		return "", fmt.Errorf("failed to apply limits: %w", err)
	}

	// Add process to cgroup
	procsPath := filepath.Join(cgroupPath, "cgroup.procs")
	if err := os.WriteFile(procsPath, []byte(strconv.Itoa(pid)), 0644); err != nil {
		os.RemoveAll(cgroupPath)
		return "", fmt.Errorf("failed to add process to cgroup: %w", err)
	}

	return cgroupName, nil
}

// enableControllers enables CPU and memory controllers for a cgroup.
func (m *Manager) enableControllers(path string) error {
	subtreeControl := filepath.Join(path, "cgroup.subtree_control")
	return os.WriteFile(subtreeControl, []byte("+cpu +memory +pids"), 0644)
}

// applyLimits sets resource limits on a cgroup.
func (m *Manager) applyLimits(cgroupPath string, limits Limits) error {
	// CPU limit using cpu.max
	// Format: "$MAX $PERIOD" where MAX is the quota and PERIOD is typically 100000 (100ms)
	// This implements THROTTLING - process is slowed down, not killed
	if limits.CPUPercent > 0 {
		period := 100000 // 100ms period
		quota := (limits.CPUPercent * period) / 100
		cpuMax := fmt.Sprintf("%d %d", quota, period)
		if err := os.WriteFile(filepath.Join(cgroupPath, "cpu.max"), []byte(cpuMax), 0644); err != nil {
			return fmt.Errorf("failed to set cpu.max: %w", err)
		}
	}

	// Memory limits using memory.high (soft) and memory.max (hard)
	// memory.high triggers reclaim and throttling - this is the gentle limit
	// memory.max is the hard limit - only triggers OOM if truly necessary
	if limits.MemoryBytes > 0 {
		// Set soft limit (memory.high) - triggers throttling/reclaim
		softPercent := limits.MemorySoftPercent
		if softPercent <= 0 {
			softPercent = 90 // Default: start throttling at 90% of limit
		}
		softLimit := (limits.MemoryBytes * int64(softPercent)) / 100
		if err := os.WriteFile(filepath.Join(cgroupPath, "memory.high"), []byte(strconv.FormatInt(softLimit, 10)), 0644); err != nil {
			return fmt.Errorf("failed to set memory.high: %w", err)
		}

		// Set hard limit (memory.max) - OOM if exceeded
		if err := os.WriteFile(filepath.Join(cgroupPath, "memory.max"), []byte(strconv.FormatInt(limits.MemoryBytes, 10)), 0644); err != nil {
			return fmt.Errorf("failed to set memory.max: %w", err)
		}

		// Disable swap for predictable behavior
		if err := os.WriteFile(filepath.Join(cgroupPath, "memory.swap.max"), []byte("0"), 0644); err != nil {
			// Not fatal - swap control might not be available
		}
	}

	// Process/thread limit using pids.max
	if limits.MaxProcesses > 0 {
		if err := os.WriteFile(filepath.Join(cgroupPath, "pids.max"), []byte(strconv.Itoa(limits.MaxProcesses)), 0644); err != nil {
			return fmt.Errorf("failed to set pids.max: %w", err)
		}
	}

	return nil
}

// Remove removes a cgroup. Should be called after the process exits.
func (m *Manager) Remove(cgroupName string) error {
	if cgroupName == "" {
		return nil
	}

	cgroupPath := filepath.Join(m.root, prisnCgroupName, cgroupName)

	// Wait for cgroup to be empty (all processes exited)
	// The kernel won't let us remove a cgroup with processes in it
	procsPath := filepath.Join(cgroupPath, "cgroup.procs")
	for i := 0; i < 10; i++ {
		data, err := os.ReadFile(procsPath)
		if err != nil || len(strings.TrimSpace(string(data))) == 0 {
			break
		}
		// Still has processes, wait a bit
		syscall.Nanosleep(&syscall.Timespec{Nsec: 100_000_000}, nil) // 100ms
	}

	return os.Remove(cgroupPath)
}

// GetStats returns current resource usage for a cgroup.
func (m *Manager) GetStats(cgroupName string) (*Stats, error) {
	cgroupPath := filepath.Join(m.root, prisnCgroupName, cgroupName)

	stats := &Stats{}

	// CPU usage (microseconds)
	cpuStatPath := filepath.Join(cgroupPath, "cpu.stat")
	if data, err := os.ReadFile(cpuStatPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.Fields(line)
			if len(parts) == 2 && parts[0] == "usage_usec" {
				stats.CPUUsageUsec, _ = strconv.ParseInt(parts[1], 10, 64)
			}
			if len(parts) == 2 && parts[0] == "nr_throttled" {
				stats.CPUThrottled, _ = strconv.ParseInt(parts[1], 10, 64)
			}
		}
	}

	// Memory usage
	memCurrentPath := filepath.Join(cgroupPath, "memory.current")
	if data, err := os.ReadFile(memCurrentPath); err == nil {
		stats.MemoryBytes, _ = strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	}

	// Memory events (OOM, throttle events)
	memEventsPath := filepath.Join(cgroupPath, "memory.events")
	if data, err := os.ReadFile(memEventsPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			parts := strings.Fields(line)
			if len(parts) == 2 {
				switch parts[0] {
				case "high":
					stats.MemoryThrottled, _ = strconv.ParseInt(parts[1], 10, 64)
				case "oom_kill":
					stats.OOMKills, _ = strconv.ParseInt(parts[1], 10, 64)
				}
			}
		}
	}

	return stats, nil
}

// Stats contains resource usage statistics.
type Stats struct {
	CPUUsageUsec    int64 // CPU time used in microseconds
	CPUThrottled    int64 // Number of times CPU was throttled
	MemoryBytes     int64 // Current memory usage
	MemoryThrottled int64 // Number of times memory was throttled (hit high watermark)
	OOMKills        int64 // Number of OOM kills
}
