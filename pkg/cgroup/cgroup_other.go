//go:build !linux

// Package cgroup provides resource management stubs for non-Linux platforms.
package cgroup

import "fmt"

// Manager handles cgroup operations (stub for non-Linux).
type Manager struct{}

// NewManager creates a cgroup manager.
func NewManager() *Manager {
	return &Manager{}
}

// Available returns true if cgroups are available.
func (m *Manager) Available() bool {
	return false
}

// Limits specifies resource limits for a process.
type Limits struct {
	CPUPercent        int
	MemoryBytes       int64
	MemorySoftPercent int
	MaxProcesses      int
}

// Create creates a new cgroup (stub - returns error on non-Linux).
func (m *Manager) Create(pid int, limits Limits) (string, error) {
	return "", fmt.Errorf("cgroups not available on this platform")
}

// Remove removes a cgroup.
func (m *Manager) Remove(cgroupName string) error {
	return nil
}

// CgroupPath returns the path to a cgroup.
func (m *Manager) CgroupPath(name string) string {
	return ""
}

// GetStats returns resource usage (stub).
func (m *Manager) GetStats(cgroupName string) (*Stats, error) {
	return &Stats{}, nil
}

// Stats contains resource usage statistics.
type Stats struct {
	CPUUsageUsec    int64
	CPUThrottled    int64
	MemoryBytes     int64
	MemoryThrottled int64
	OOMKills        int64
}
