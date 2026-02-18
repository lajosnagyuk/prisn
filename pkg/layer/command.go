package layer

import (
	"os/exec"
	"runtime"
)

// newCommand creates an exec.Cmd with platform-appropriate settings.
func newCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)

	// Platform-specific adjustments
	switch runtime.GOOS {
	case "windows":
		// Windows uses python instead of python3
		if name == "python3" {
			cmd = exec.Command("python", args...)
		}
	}

	return cmd
}

// pythonExecutable returns the Python executable name for this platform.
func pythonExecutable() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

// pipExecutable returns the pip executable name for this platform.
func pipExecutable() string {
	if runtime.GOOS == "windows" {
		return "pip"
	}
	return "pip3"
}
