// Package validate provides input validation for prisn.
//
// Design: Fail fast with helpful errors. No surprises later.
package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Path validates a file path is safe.
func Path(path string) error {
	if path == "" {
		return fmt.Errorf("path is empty")
	}

	// Check for path traversal attempts in original path
	// This catches both literal ".." and disguised attempts
	if strings.Contains(path, "..") {
		return fmt.Errorf("path traversal not allowed: %s", path)
	}

	return nil
}

// PathExists validates path exists and is accessible.
func PathExists(path string) error {
	if err := Path(path); err != nil {
		return err
	}

	// Use Lstat to detect symlinks (Stat follows them)
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("does not exist: %s", path)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied: %s", path)
		}
		return fmt.Errorf("cannot access: %s (%v)", path, err)
	}

	// Check if it's a symlink and validate the target
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("cannot resolve symlink: %s", path)
		}
		// Make sure resolved target doesn't escape expected area
		absPath, err := filepath.Abs(path)
		if err == nil {
			dir := filepath.Dir(absPath)
			if err := PathWithinBase(target, dir); err != nil {
				return fmt.Errorf("symlink target escapes directory: %s -> %s", path, target)
			}
		}
	}

	return nil
}

// PathWithinBase ensures path is within a base directory.
func PathWithinBase(path, base string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve path: %s", path)
	}

	absBase, err := filepath.Abs(base)
	if err != nil {
		return fmt.Errorf("cannot resolve base: %s", base)
	}

	// Resolve any symlinks
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		// Path might not exist yet, use cleaned abs path
		realPath = absPath
	}

	realBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		realBase = absBase
	}

	// Check if path is within base
	rel, err := filepath.Rel(realBase, realPath)
	if err != nil {
		return fmt.Errorf("path outside allowed area: %s", path)
	}

	if strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path escapes base directory: %s", path)
	}

	return nil
}

// Port validates a port number.
func Port(port int) error {
	if port < 0 {
		return fmt.Errorf("port cannot be negative: %d", port)
	}
	if port > 65535 {
		return fmt.Errorf("port must be <= 65535: %d", port)
	}
	if port > 0 && port < 1024 {
		return fmt.Errorf("port %d requires root (use >= 1024)", port)
	}
	return nil
}

// Replicas validates replica count.
func Replicas(n int) error {
	if n < 0 {
		return fmt.Errorf("replicas cannot be negative: %d", n)
	}
	if n > 1000 {
		return fmt.Errorf("replicas seems too high: %d (max 1000)", n)
	}
	return nil
}

// Memory validates memory limit in MB.
func Memory(mb int) error {
	if mb < 0 {
		return fmt.Errorf("memory cannot be negative: %d", mb)
	}
	if mb > 0 && mb < 16 {
		return fmt.Errorf("memory too low: %dMB (min 16MB)", mb)
	}
	if mb > 65536 {
		return fmt.Errorf("memory seems too high: %dMB (max 64GB)", mb)
	}
	return nil
}

// Name validates a deployment/resource name.
func Name(name string) error {
	if name == "" {
		return fmt.Errorf("name is empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("name cannot be '.' or '..'")
	}
	if len(name) > 63 {
		return fmt.Errorf("name too long: %d chars (max 63)", len(name))
	}

	// Must start with letter or number
	if !unicode.IsLetter(rune(name[0])) && !unicode.IsDigit(rune(name[0])) {
		return fmt.Errorf("name must start with letter or number: %s", name)
	}

	// Only allow alphanumeric, dash, underscore
	valid := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
	if !valid.MatchString(name) {
		return fmt.Errorf("name contains invalid characters (use a-z, 0-9, -, _): %s", name)
	}

	return nil
}

// EnvVar validates environment variable format.
func EnvVar(s string) (string, string, error) {
	if s == "" {
		return "", "", fmt.Errorf("empty environment variable")
	}

	parts := strings.SplitN(s, "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("format must be KEY=value: %s", s)
	}

	key, value := parts[0], parts[1]

	// Validate key
	if key == "" {
		return "", "", fmt.Errorf("empty key in: %s", s)
	}

	keyValid := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	if !keyValid.MatchString(key) {
		return "", "", fmt.Errorf("invalid key %q (use A-Z, 0-9, _)", key)
	}

	return key, value, nil
}

// Namespace validates a namespace name.
func Namespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("namespace is empty")
	}
	return Name(ns) // Same rules as names
}

// CronSchedule validates a cron expression (basic check).
func CronSchedule(expr string) error {
	if expr == "" {
		return fmt.Errorf("schedule is empty")
	}

	// Handle shortcuts
	shortcuts := map[string]bool{
		"@hourly":   true,
		"@daily":    true,
		"@weekly":   true,
		"@monthly":  true,
		"@yearly":   true,
		"@annually": true,
	}
	if shortcuts[expr] {
		return nil
	}

	// Must have 5 fields
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return fmt.Errorf("cron must have 5 fields (min hour dom mon dow): got %d", len(fields))
	}

	return nil
}

// Timeout validates a duration string.
func Timeout(s string) error {
	if s == "" {
		return nil // Empty is OK, will use default
	}

	// Quick sanity check for common mistakes
	if s == "0" {
		return fmt.Errorf("timeout cannot be zero (use '0s' for no timeout)")
	}

	// Check for missing unit - pure numeric string
	isNumeric := true
	for _, r := range s {
		if !unicode.IsDigit(r) {
			isNumeric = false
			break
		}
	}
	if isNumeric {
		return fmt.Errorf("timeout needs unit (e.g., '30s', '5m', '1h'): %s", s)
	}

	return nil
}
