package runtime

import (
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
)

// EnvManager handles environment creation and caching for all runtimes.
type EnvManager struct {
	CacheDir string
	MaxAge   time.Duration
	Registry *Registry

	mu       sync.Mutex
	creating map[string]chan struct{}
}

// NewEnvManager creates an environment manager.
func NewEnvManager(registry *Registry) (*EnvManager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot find home directory: %w", err)
	}

	cacheDir := filepath.Join(home, ".prisn", "envs")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("cannot create cache directory: %w", err)
	}

	return &EnvManager{
		CacheDir: cacheDir,
		MaxAge:   7 * 24 * time.Hour,
		Registry: registry,
		creating: make(map[string]chan struct{}),
	}, nil
}

// Prepare ensures an environment exists for the given manifest.
func (m *EnvManager) Prepare(ctx context.Context, manifest *Manifest) (*Environment, error) {
	rt, ok := m.Registry.Get(manifest.Runtime)
	if !ok {
		return nil, fmt.Errorf("unknown runtime: %s", manifest.Runtime)
	}

	// No dependencies or self-managed? Use system interpreter
	if len(manifest.Dependencies) == 0 || rt.Deps.SelfManaged {
		return m.systemEnv(rt)
	}

	// Compute cache key
	hash := m.cacheKey(manifest)
	envPath := filepath.Join(m.CacheDir, manifest.Runtime+"-"+hash)

	// Check cache
	if env, err := m.checkCache(envPath, rt); err == nil {
		return env, nil
	}

	// Need to create - ensure only one goroutine creates at a time
	m.mu.Lock()
	if ch, ok := m.creating[hash]; ok {
		m.mu.Unlock()
		select {
		case <-ch:
			return m.checkCache(envPath, rt)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	ch := make(chan struct{})
	m.creating[hash] = ch
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		delete(m.creating, hash)
		close(ch)
		m.mu.Unlock()
	}()

	// Create the environment based on runtime type
	return m.createEnv(ctx, envPath, manifest, rt)
}

// systemEnv returns an environment pointing to the system interpreter.
func (m *EnvManager) systemEnv(rt *Runtime) (*Environment, error) {
	path, err := m.Registry.Check(rt)
	if err != nil {
		return nil, err
	}

	return &Environment{
		InterpreterPath: path,
		BinDir:          filepath.Dir(path),
		Cached:          true,
		EnvVars:         rt.Env,
	}, nil
}

// checkCache checks if a cached environment exists and is valid.
func (m *EnvManager) checkCache(envPath string, rt *Runtime) (*Environment, error) {
	info, err := os.Stat(envPath)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory")
	}

	// Update access time for LRU cleanup
	now := time.Now()
	os.Chtimes(envPath, now, now)

	env := &Environment{
		Path:    envPath,
		Cached:  true,
		EnvVars: make(map[string]string),
	}

	// Copy runtime env vars
	for k, v := range rt.Env {
		env.EnvVars[k] = v
	}

	// Set up paths based on env type
	switch rt.Deps.EnvType {
	case "venv":
		pythonPath := filepath.Join(envPath, "bin", "python")
		if _, err := os.Stat(pythonPath); err != nil {
			return nil, fmt.Errorf("python not found in venv")
		}
		env.InterpreterPath = pythonPath
		env.BinDir = filepath.Join(envPath, "bin")
		env.EnvVars["VIRTUAL_ENV"] = envPath

	case "node_modules":
		nodePath, _ := exec.LookPath("node")
		env.InterpreterPath = nodePath
		env.BinDir = filepath.Join(envPath, "node_modules", ".bin")
		env.EnvVars["NODE_PATH"] = filepath.Join(envPath, "node_modules")

	case "vendor":
		phpPath, _ := exec.LookPath("php")
		env.InterpreterPath = phpPath
		env.BinDir = filepath.Join(envPath, "vendor", "bin")

	case "bundle":
		rubyPath, _ := exec.LookPath("ruby")
		env.InterpreterPath = rubyPath
		env.BinDir = filepath.Join(envPath, "bin")
		env.EnvVars["BUNDLE_PATH"] = envPath

	default:
		// Find interpreter in system PATH
		interpreterPath, _ := exec.LookPath(rt.Command)
		env.InterpreterPath = interpreterPath
		env.BinDir = filepath.Dir(interpreterPath)
	}

	return env, nil
}

// createEnv creates a new environment for the manifest.
func (m *EnvManager) createEnv(ctx context.Context, envPath string, manifest *Manifest, rt *Runtime) (*Environment, error) {
	switch rt.Deps.EnvType {
	case "venv":
		return m.createPythonEnv(ctx, envPath, manifest, rt)
	case "node_modules":
		return m.createNodeEnv(ctx, envPath, manifest, rt)
	case "vendor":
		return m.createPHPEnv(ctx, envPath, manifest, rt)
	case "bundle":
		return m.createRubyEnv(ctx, envPath, manifest, rt)
	default:
		return m.createGenericEnv(ctx, envPath, manifest, rt)
	}
}

func (m *EnvManager) createPythonEnv(ctx context.Context, envPath string, manifest *Manifest, rt *Runtime) (*Environment, error) {
	// Find Python
	pythonCmd := rt.Command
	if manifest.Version != "" {
		specific := fmt.Sprintf("python%s", manifest.Version)
		if _, err := exec.LookPath(specific); err == nil {
			pythonCmd = specific
		}
	}

	// Create venv
	cmd := exec.CommandContext(ctx, pythonCmd, "-m", "venv", envPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to create venv: %w", err)
	}

	// Install dependencies
	if len(manifest.Dependencies) > 0 {
		pipPath := filepath.Join(envPath, "bin", "pip")
		args := []string{"install", "--quiet"}
		for _, dep := range manifest.Dependencies {
			args = append(args, dep.String())
		}

		cmd = exec.CommandContext(ctx, pipPath, args...)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			os.RemoveAll(envPath)
			return nil, fmt.Errorf("failed to install dependencies: %w", err)
		}
	}

	env := &Environment{
		Path:            envPath,
		InterpreterPath: filepath.Join(envPath, "bin", "python"),
		BinDir:          filepath.Join(envPath, "bin"),
		Cached:          false,
		EnvVars: map[string]string{
			"VIRTUAL_ENV": envPath,
		},
	}
	for k, v := range rt.Env {
		env.EnvVars[k] = v
	}

	return env, nil
}

func (m *EnvManager) createNodeEnv(ctx context.Context, envPath string, manifest *Manifest, rt *Runtime) (*Environment, error) {
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
		args := []string{"install", "--silent"}
		for _, dep := range manifest.Dependencies {
			// npm uses @ as separator: package@version
			if dep.Version != "" {
				args = append(args, dep.Name+"@"+dep.Version)
			} else {
				args = append(args, dep.Name)
			}
		}

		cmd := exec.CommandContext(ctx, "npm", args...)
		cmd.Dir = envPath
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			os.RemoveAll(envPath)
			return nil, fmt.Errorf("failed to install dependencies: %w", err)
		}
	}

	nodePath, _ := exec.LookPath("node")
	env := &Environment{
		Path:            envPath,
		InterpreterPath: nodePath,
		BinDir:          filepath.Join(envPath, "node_modules", ".bin"),
		Cached:          false,
		EnvVars: map[string]string{
			"NODE_PATH": filepath.Join(envPath, "node_modules"),
		},
	}
	for k, v := range rt.Env {
		env.EnvVars[k] = v
	}

	return env, nil
}

func (m *EnvManager) createPHPEnv(ctx context.Context, envPath string, manifest *Manifest, rt *Runtime) (*Environment, error) {
	if err := os.MkdirAll(envPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create env directory: %w", err)
	}

	// Create composer.json with dependencies
	composerJSON := `{"require": {}}`
	if err := os.WriteFile(filepath.Join(envPath, "composer.json"), []byte(composerJSON), 0644); err != nil {
		return nil, fmt.Errorf("failed to create composer.json: %w", err)
	}

	// Install dependencies
	if len(manifest.Dependencies) > 0 {
		args := []string{"require", "--quiet"}
		for _, dep := range manifest.Dependencies {
			args = append(args, dep.String())
		}

		cmd := exec.CommandContext(ctx, "composer", args...)
		cmd.Dir = envPath
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			os.RemoveAll(envPath)
			return nil, fmt.Errorf("failed to install dependencies: %w", err)
		}
	}

	phpPath, _ := exec.LookPath("php")
	return &Environment{
		Path:            envPath,
		InterpreterPath: phpPath,
		BinDir:          filepath.Join(envPath, "vendor", "bin"),
		Cached:          false,
		EnvVars:         rt.Env,
	}, nil
}

func (m *EnvManager) createRubyEnv(ctx context.Context, envPath string, manifest *Manifest, rt *Runtime) (*Environment, error) {
	if err := os.MkdirAll(envPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create env directory: %w", err)
	}

	// Create Gemfile
	var gemfileContent strings.Builder
	gemfileContent.WriteString("source 'https://rubygems.org'\n")
	for _, dep := range manifest.Dependencies {
		if dep.Version != "" {
			gemfileContent.WriteString(fmt.Sprintf("gem '%s', '%s'\n", dep.Name, dep.Version))
		} else {
			gemfileContent.WriteString(fmt.Sprintf("gem '%s'\n", dep.Name))
		}
	}

	if err := os.WriteFile(filepath.Join(envPath, "Gemfile"), []byte(gemfileContent.String()), 0644); err != nil {
		return nil, fmt.Errorf("failed to create Gemfile: %w", err)
	}

	// Install dependencies
	cmd := exec.CommandContext(ctx, "bundle", "install", "--path", envPath)
	cmd.Dir = envPath
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.RemoveAll(envPath)
		return nil, fmt.Errorf("failed to install dependencies: %w", err)
	}

	rubyPath, _ := exec.LookPath("ruby")
	return &Environment{
		Path:            envPath,
		InterpreterPath: rubyPath,
		BinDir:          filepath.Join(envPath, "bin"),
		Cached:          false,
		EnvVars: map[string]string{
			"BUNDLE_PATH": envPath,
		},
	}, nil
}

func (m *EnvManager) createGenericEnv(ctx context.Context, envPath string, manifest *Manifest, rt *Runtime) (*Environment, error) {
	// For runtimes without specific env handling, just use system interpreter
	// and run the install command if specified

	if err := os.MkdirAll(envPath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create env directory: %w", err)
	}

	if rt.Deps.InstallCommand != "" && len(manifest.Dependencies) > 0 {
		// Build dep list
		var depList []string
		for _, dep := range manifest.Dependencies {
			depList = append(depList, dep.String())
		}

		// Replace placeholders
		installCmd := rt.Deps.InstallCommand
		installCmd = strings.ReplaceAll(installCmd, "{deps}", strings.Join(depList, " "))

		// Run install command
		cmd := exec.CommandContext(ctx, "sh", "-c", installCmd)
		cmd.Dir = envPath
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			os.RemoveAll(envPath)
			return nil, fmt.Errorf("failed to install dependencies: %w", err)
		}
	}

	interpreterPath, _ := exec.LookPath(rt.Command)
	return &Environment{
		Path:            envPath,
		InterpreterPath: interpreterPath,
		BinDir:          filepath.Dir(interpreterPath),
		Cached:          false,
		EnvVars:         rt.Env,
	}, nil
}

func (m *EnvManager) cacheKey(manifest *Manifest) string {
	h := sha256.New()
	h.Write([]byte(manifest.Runtime))
	h.Write([]byte(manifest.Version))

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

// Cleanup removes old, unused environments.
func (m *EnvManager) Cleanup() error {
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

		if info.ModTime().Before(cutoff) {
			path := filepath.Join(m.CacheDir, entry.Name())
			os.RemoveAll(path)
		}
	}

	return nil
}

// List returns all cached environments.
func (m *EnvManager) List() ([]Environment, error) {
	entries, err := os.ReadDir(m.CacheDir)
	if err != nil {
		return nil, err
	}

	var envs []Environment
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		envPath := filepath.Join(m.CacheDir, entry.Name())
		envs = append(envs, Environment{
			Path:   envPath,
			Cached: true,
		})
	}

	return envs, nil
}
