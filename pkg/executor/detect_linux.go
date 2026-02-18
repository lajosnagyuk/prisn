//go:build linux

package executor

import (
	"os"
	"os/exec"
)

// newPlatformExecutor returns the best executor for Linux.
func newPlatformExecutor() Executor {
	caps := detectLinuxCapabilities()
	// Use the Linux-specific executor with namespace support
	if caps.SupportsNamespaces {
		return newLinuxExecutor(caps)
	}
	return &genericUnixExecutor{
		platform: "linux",
		caps:     caps,
	}
}

// detectLinuxCapabilities probes for Linux-specific features.
func detectLinuxCapabilities() Capabilities {
	caps := Capabilities{}

	// Check for cgroups v2
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		caps.SupportsCgroups = true
	}

	// Check for namespace support
	if _, err := os.Stat("/proc/self/ns/mnt"); err == nil {
		caps.SupportsNamespaces = true
	}

	// Check for overlayfs
	if _, err := exec.LookPath("mount"); err == nil {
		caps.SupportsOverlayFS = true
	}

	// seccomp is always available on modern Linux
	caps.SupportsSeccomp = true

	// Determine max isolation level
	if caps.SupportsNamespaces && caps.SupportsCgroups {
		caps.MaxIsolationLevel = IsolationParanoid
	} else if caps.SupportsCgroups {
		caps.MaxIsolationLevel = IsolationStrong
	} else {
		caps.MaxIsolationLevel = IsolationBasic
	}

	return caps
}
