// Package venv manages virtual environments for script execution.
//
// Each unique set of dependencies gets its own cached venv.
// This is the key to making "just run my shit" actually work.
package venv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/deps"
	"github.com/lajosnagyuk/prisn/pkg/log"
)

// Manager handles virtual environment creation and caching.
type Manager struct {
	// CacheDir is where venvs are stored (default: ~/.prisn/envs)
	CacheDir string

	// MaxAge is how long to keep unused venvs (default: 7 days)
	MaxAge time.Duration

	// mu protects concurrent venv creation
	mu sync.Mutex

	// creating tracks venvs currently being created (for deduplication)
	creating map[string]chan struct{}
}

// NewManager creates a venv manager with default settings.
func NewManager() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot find home directory: %w", err)
	}

	cacheDir := filepath.Join(home, ".prisn", "envs")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("cannot create cache directory: %w", err)
	}

	return &Manager{
		CacheDir: cacheDir,
		MaxAge:   7 * 24 * time.Hour,
		creating: make(map[string]chan struct{}),
	}, nil
}

// EnvInfo contains information about a prepared environment.
type EnvInfo struct {
	// Path is the venv directory
	Path string

	// PythonPath is the path to the Python interpreter
	PythonPath string

	// NodePath is the path to the node executable (for Node.js)
	NodePath string

	// BinDir is the venv bin directory
	BinDir string

	// Hash is the dependency hash
	Hash string

	// Cached indicates if this was a cache hit
	Cached bool

	// CreatedAt is when the venv was created
	CreatedAt time.Time
}

// Prepare ensures a virtual environment exists for the given manifest.
// Returns the path to the prepared environment.
func (m *Manager) Prepare(ctx context.Context, manifest *deps.Manifest) (*EnvInfo, error) {
	if manifest == nil {
		return nil, fmt.Errorf("manifest is nil")
	}

	// No dependencies? No venv needed.
	if len(manifest.Dependencies) == 0 {
		return m.systemEnv(manifest.Runtime)
	}

	// Compute cache key
	hash := m.cacheKey(manifest)

	// Check cache
	envPath := filepath.Join(m.CacheDir, manifest.Runtime+"-"+hash)
	if info, err := m.checkCache(envPath, manifest.Runtime); err == nil {
		return info, nil
	}

	// Need to create - ensure only one goroutine creates at a time
	m.mu.Lock()
	if ch, ok := m.creating[hash]; ok {
		m.mu.Unlock()
		// Wait for other goroutine to finish
		select {
		case <-ch:
			return m.checkCache(envPath, manifest.Runtime)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// We're the creator
	ch := make(chan struct{})
	m.creating[hash] = ch
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.creating, hash)
		close(ch)
		m.mu.Unlock()
	}()

	// Create the environment
	switch manifest.Runtime {
	case "python3", "python":
		return m.createPythonEnv(ctx, envPath, manifest)
	case "node":
		return m.createNodeEnv(ctx, envPath, manifest)
	default:
		return nil, fmt.Errorf("unsupported runtime for venv: %s", manifest.Runtime)
	}
}

// systemEnv returns an EnvInfo pointing to the system interpreter.
func (m *Manager) systemEnv(runtime string) (*EnvInfo, error) {
	var path string
	var err error

	switch runtime {
	case "python3", "python":
		path, err = exec.LookPath("python3")
		if err != nil {
			path, err = exec.LookPath("python")
		}
		if err != nil {
			return nil, fmt.Errorf("python not found: %w", err)
		}
		return &EnvInfo{
			PythonPath: path,
			BinDir:     filepath.Dir(path),
			Cached:     true,
		}, nil

	case "node":
		path, err = exec.LookPath("node")
		if err != nil {
			return nil, fmt.Errorf("node not found: %w", err)
		}
		return &EnvInfo{
			NodePath: path,
			BinDir:   filepath.Dir(path),
			Cached:   true,
		}, nil

	case "bash":
		path, err = exec.LookPath("bash")
		if err != nil {
			return nil, fmt.Errorf("bash not found: %w", err)
		}
		return &EnvInfo{
			BinDir: filepath.Dir(path),
			Cached: true,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported runtime: %s", runtime)
	}
}

// checkCache checks if a cached venv exists and is valid.
func (m *Manager) checkCache(envPath, runtime string) (*EnvInfo, error) {
	info, err := os.Stat(envPath)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory")
	}

	// Update access time (for LRU cleanup)
	now := time.Now()
	os.Chtimes(envPath, now, now)

	switch runtime {
	case "python3", "python":
		pythonPath := filepath.Join(envPath, "bin", "python")
		if _, err := os.Stat(pythonPath); err != nil {
			return nil, fmt.Errorf("python not found in venv")
		}
		return &EnvInfo{
			Path:       envPath,
			PythonPath: pythonPath,
			BinDir:     filepath.Join(envPath, "bin"),
			Hash:       filepath.Base(envPath),
			Cached:     true,
			CreatedAt:  info.ModTime(),
		}, nil

	case "node":
		// Node doesn't copy the binary, use system node with node_modules
		systemNode, _ := exec.LookPath("node")
		return &EnvInfo{
			Path:     envPath,
			NodePath: systemNode,
			BinDir:   filepath.Join(envPath, "node_modules", ".bin"),
			Hash:     filepath.Base(envPath),
			Cached:   true,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported runtime")
	}
}

// createPythonEnv creates a new Python virtual environment.
func (m *Manager) createPythonEnv(ctx context.Context, envPath string, manifest *deps.Manifest) (*EnvInfo, error) {
	// Find Python
	pythonCmd := "python3"
	if manifest.Version != "" {
		// Try version-specific first
		specific := fmt.Sprintf("python%s", manifest.Version)
		if _, err := exec.LookPath(specific); err == nil {
			pythonCmd = specific
		}
	}

	log.Start("Creating Python environment (%d dependencies)", len(manifest.Dependencies))

	// Create venv
	cmd := exec.CommandContext(ctx, pythonCmd, "-m", "venv", envPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to create venv: %w", err)
	}

	// Install dependencies
	if len(manifest.Dependencies) > 0 {
		log.Info("Installing dependencies...")
		pipPath := filepath.Join(envPath, "bin", "pip")
		args := []string{"install", "--quiet"}
		for _, dep := range manifest.Dependencies {
			args = append(args, dep.String())
		}

		cmd = exec.CommandContext(ctx, pipPath, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			// Clean up failed venv
			os.RemoveAll(envPath)
			return nil, interpretPipError(stderr.String(), manifest.Dependencies)
		}
	}

	log.Done("Environment ready")

	return &EnvInfo{
		Path:       envPath,
		PythonPath: filepath.Join(envPath, "bin", "python"),
		BinDir:     filepath.Join(envPath, "bin"),
		Hash:       filepath.Base(envPath),
		Cached:     false,
		CreatedAt:  time.Now(),
	}, nil
}

// interpretPipError converts raw pip errors into actionable messages.
func interpretPipError(stderr string, deps []deps.Dependency) error {
	stderrLower := strings.ToLower(stderr)

	// Check for common error patterns
	switch {
	case strings.Contains(stderrLower, "could not find a version that satisfies"):
		// Extract the package name if possible
		for _, d := range deps {
			if strings.Contains(stderrLower, strings.ToLower(d.Name)) {
				return fmt.Errorf("package %q not found or version constraint cannot be satisfied\n\n  Check:\n  - Package name spelling in requirements.txt\n  - Version constraint (e.g., %s)\n  - PyPI availability: pip search %s", d.Name, d.String(), d.Name)
			}
		}
		return fmt.Errorf("dependency version conflict\n\n  %s\n\n  Try: pip install <package> to see detailed error", strings.TrimSpace(stderr))

	case strings.Contains(stderrLower, "no matching distribution"):
		return fmt.Errorf("package not found on PyPI\n\n  %s\n\n  Check:\n  - Package name spelling\n  - Python version compatibility\n  - Platform compatibility (some packages are OS-specific)", strings.TrimSpace(stderr))

	case strings.Contains(stderrLower, "connection") || strings.Contains(stderrLower, "network") || strings.Contains(stderrLower, "timeout"):
		return fmt.Errorf("network error during pip install\n\n  Check your internet connection and try again.\n  If behind a proxy, set HTTP_PROXY and HTTPS_PROXY environment variables.")

	case strings.Contains(stderrLower, "permission denied"):
		return fmt.Errorf("permission denied during install\n\n  This shouldn't happen with prisn's venv isolation.\n  Check disk space and directory permissions for ~/.prisn/envs/")

	case strings.Contains(stderrLower, "gcc") || strings.Contains(stderrLower, "compilation") || strings.Contains(stderrLower, "error: command") || strings.Contains(stderrLower, "failed building wheel"):
		return fmt.Errorf("compilation failed - missing build tools\n\n  Some packages require C/C++ compilation.\n\n  Install build tools:\n  - macOS:  xcode-select --install\n  - Ubuntu: sudo apt install build-essential python3-dev\n  - Fedora: sudo dnf install gcc python3-devel\n\n  Original error:\n  %s", truncateError(stderr, 500))

	case strings.Contains(stderrLower, "disk") || strings.Contains(stderrLower, "space") || strings.Contains(stderrLower, "no space left"):
		return fmt.Errorf("disk full - cannot install dependencies\n\n  Free up disk space and try again.\n  You can also clean old environments: rm -rf ~/.prisn/envs/*")

	default:
		// Generic error with the original output
		return fmt.Errorf("failed to install dependencies\n\n  %s\n\n  Try running manually: pip install -r requirements.txt", truncateError(stderr, 800))
	}
}

// truncateError truncates error messages to a reasonable length.
func truncateError(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n  ... (truncated)"
}

// createNodeEnv creates a new Node.js environment (node_modules).
func (m *Manager) createNodeEnv(ctx context.Context, envPath string, manifest *deps.Manifest) (*EnvInfo, error) {
	log.Start("Creating Node.js environment (%d dependencies)", len(manifest.Dependencies))

	// Create directory
	if err := os.MkdirAll(envPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create env directory: %w", err)
	}

	// Create minimal package.json
	pkgJSON := `{"name": "prisn-env", "private": true}`
	if err := os.WriteFile(filepath.Join(envPath, "package.json"), []byte(pkgJSON), 0644); err != nil {
		return nil, fmt.Errorf("failed to create package.json: %w", err)
	}

	// Install dependencies
	if len(manifest.Dependencies) > 0 {
		log.Info("Installing dependencies...")
		args := []string{"install", "--silent"}
		for _, dep := range manifest.Dependencies {
			args = append(args, dep.String())
		}

		cmd := exec.CommandContext(ctx, "npm", args...)
		cmd.Dir = envPath
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			os.RemoveAll(envPath)
			return nil, interpretNpmError(stderr.String())
		}
	}

	log.Done("Environment ready")

	nodePath, _ := exec.LookPath("node")
	return &EnvInfo{
		Path:     envPath,
		NodePath: nodePath,
		BinDir:   filepath.Join(envPath, "node_modules", ".bin"),
		Hash:     filepath.Base(envPath),
		Cached:   false,
	}, nil
}

// interpretNpmError converts raw npm errors into actionable messages.
func interpretNpmError(stderr string) error {
	stderrLower := strings.ToLower(stderr)

	switch {
	case strings.Contains(stderrLower, "404") || strings.Contains(stderrLower, "not found"):
		return fmt.Errorf("package not found on npm\n\n  %s\n\n  Check package name spelling in package.json", truncateError(stderr, 500))

	case strings.Contains(stderrLower, "network") || strings.Contains(stderrLower, "enotfound") || strings.Contains(stderrLower, "etimedout"):
		return fmt.Errorf("network error during npm install\n\n  Check your internet connection and try again.")

	case strings.Contains(stderrLower, "eacces") || strings.Contains(stderrLower, "permission"):
		return fmt.Errorf("permission denied during install\n\n  Check directory permissions for ~/.prisn/envs/")

	default:
		return fmt.Errorf("failed to install dependencies\n\n  %s", truncateError(stderr, 800))
	}
}

// cacheKey computes a deterministic cache key for a manifest.
func (m *Manager) cacheKey(manifest *deps.Manifest) string {
	h := sha256.New()

	// Include runtime and version
	h.Write([]byte(manifest.Runtime))
	h.Write([]byte(manifest.Version))

	// Sort deps for determinism
	depStrings := make([]string, len(manifest.Dependencies))
	for i, d := range manifest.Dependencies {
		depStrings[i] = d.String()
	}
	sort.Strings(depStrings)

	for _, s := range depStrings {
		h.Write([]byte(s))
	}

	return hex.EncodeToString(h.Sum(nil))[:12]
}

// Cleanup removes old, unused virtual environments.
func (m *Manager) Cleanup() error {
	entries, err := os.ReadDir(m.CacheDir)
	if err != nil {
		return err
	}

	cutoff := time.Now().Add(-m.MaxAge)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Check last access time (we update on cache hit)
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(m.CacheDir, entry.Name())
			os.RemoveAll(path)
		}
	}

	return nil
}

// List returns all cached environments.
func (m *Manager) List() ([]EnvInfo, error) {
	entries, err := os.ReadDir(m.CacheDir)
	if err != nil {
		return nil, err
	}

	var envs []EnvInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		envPath := filepath.Join(m.CacheDir, entry.Name())
		runtime := "unknown"
		parts := strings.SplitN(entry.Name(), "-", 2)
		if len(parts) > 0 {
			runtime = parts[0]
		}

		envs = append(envs, EnvInfo{
			Path:      envPath,
			Hash:      entry.Name(),
			CreatedAt: info.ModTime(),
			Cached:    true,
		})

		// Try to detect more info
		if _, err := os.Stat(filepath.Join(envPath, "bin", "python")); err == nil {
			envs[len(envs)-1].PythonPath = filepath.Join(envPath, "bin", "python")
			envs[len(envs)-1].BinDir = filepath.Join(envPath, "bin")
		}
		_ = runtime // Avoid unused variable warning
	}

	return envs, nil
}
