//go:build unix && !linux

package executor

import (
	"os/exec"
	"runtime"
)

// newPlatformExecutor returns the best executor for the current platform.
func newPlatformExecutor() Executor {
	switch runtime.GOOS {
	case "freebsd":
		return &genericUnixExecutor{
			platform: "freebsd",
			caps:     detectFreeBSDCapabilities(),
		}
	case "darwin":
		return &genericUnixExecutor{
			platform: "darwin",
			caps:     detectDarwinCapabilities(),
		}
	default:
		return &genericUnixExecutor{
			platform: runtime.GOOS,
			caps: Capabilities{
				MaxIsolationLevel: IsolationBasic,
			},
		}
	}
}

// detectFreeBSDCapabilities probes for FreeBSD-specific features.
func detectFreeBSDCapabilities() Capabilities {
	caps := Capabilities{}

	// Check for jail support
	if _, err := exec.LookPath("jail"); err == nil {
		caps.SupportsJails = true
	}

	// Check for rctl support
	if _, err := exec.LookPath("rctl"); err == nil {
		caps.SupportsRctl = true
	}

	// Determine max isolation level
	if caps.SupportsJails && caps.SupportsRctl {
		caps.MaxIsolationLevel = IsolationParanoid
	} else if caps.SupportsJails {
		caps.MaxIsolationLevel = IsolationStrong
	} else {
		caps.MaxIsolationLevel = IsolationBasic
	}

	return caps
}

// detectDarwinCapabilities probes for macOS-specific features.
func detectDarwinCapabilities() Capabilities {
	caps := Capabilities{}

	// Check for sandbox-exec
	if _, err := exec.LookPath("sandbox-exec"); err == nil {
		caps.SupportsSandbox = true
		caps.MaxIsolationLevel = IsolationStrong
	} else {
		caps.MaxIsolationLevel = IsolationBasic
	}

	return caps
}
