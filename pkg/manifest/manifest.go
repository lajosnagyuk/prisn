// Package manifest handles Prisnfile parsing and runtime packaging.
//
// A Prisnfile is a simple, human-readable manifest for deployments.
// TOML-based with sensible defaults - most fields are optional.
//
// Example Prisnfile:
//
//	name = "api"
//	entrypoint = "main.py"
//
//	[env]
//	DEBUG = "true"
//
// That's it. Everything else is inferred.
package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Prisnfile is the manifest format for prisn deployments.
type Prisnfile struct {
	// Identity
	Name      string `toml:"name"`      // Deployment name (default: directory name)
	Namespace string `toml:"namespace"` // Namespace (default: "default")

	// What to run
	Entrypoint string   `toml:"entrypoint"` // Main script (auto-detected if omitted)
	Args       []string `toml:"args"`       // Arguments to pass
	Runtime    string   `toml:"runtime"`    // python3, node, bash (auto-detected)

	// Type (inferred from other fields if not set)
	Type string `toml:"type"` // service, job, cronjob

	// Service config (makes it a service)
	Port        int    `toml:"port"`         // Port to expose
	HealthCheck string `toml:"health_check"` // Health check path (default: /health)

	// Job config
	Schedule string `toml:"schedule"` // Cron expression (makes it a cronjob)
	Timeout  string `toml:"timeout"`  // Max runtime (default: 1h for jobs)

	// Scaling
	Replicas int `toml:"replicas"` // Number of instances (default: 1)

	// Environment
	Env     map[string]string `toml:"env"`      // Inline env vars
	EnvFile string            `toml:"env_file"` // Path to .env file

	// Resources
	Resources ResourceSpec `toml:"resources"`

	// Dependencies (usually auto-detected)
	Dependencies DependencySpec `toml:"dependencies"`

	// Advanced
	WorkDir    string            `toml:"work_dir"`    // Working directory
	User       string            `toml:"user"`        // Run as user
	Labels     map[string]string `toml:"labels"`      // Custom labels
	RestartOn  string            `toml:"restart_on"`  // always, failure, never
	MaxRetries int               `toml:"max_retries"` // Retry count on failure

	// Internal (set by loader)
	baseDir string
}

// ResourceSpec defines resource limits and preferences.
type ResourceSpec struct {
	Memory string `toml:"memory"` // e.g., "512MB", "2GB"
	CPU    string `toml:"cpu"`    // e.g., "0.5", "2"

	// Architecture preferences
	// "any"             - no preference, works everywhere (default)
	// "amd64"           - requires x86_64
	// "arm64"           - requires ARM
	// "amd64-preferred" - prefers amd64 but arm64 works
	Arch string `toml:"arch"`

	// GPU access needed
	GPU bool `toml:"gpu"`
}

// DependencySpec defines how dependencies are handled.
type DependencySpec struct {
	// Python
	RequirementsFile string `toml:"requirements"` // Path to requirements.txt
	PythonVersion    string `toml:"python"`       // e.g., "3.11"

	// Node
	PackageFile string `toml:"package"` // Path to package.json
	NodeVersion string `toml:"node"`    // e.g., "18"

	// System
	AptPackages []string `toml:"apt"` // System packages to install
}

// Load reads a Prisnfile from the given path.
func Load(path string) (*Prisnfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read Prisnfile: %w", err)
	}

	var pf Prisnfile
	if err := toml.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("invalid Prisnfile syntax: %w", err)
	}

	pf.baseDir = filepath.Dir(path)

	// Infer type if not set
	pf.inferType()

	return &pf, nil
}

// LoadOrInfer loads a Prisnfile if it exists, otherwise infers from directory.
func LoadOrInfer(dir string) (*Prisnfile, error) {
	// Try to find Prisnfile
	candidates := []string{
		filepath.Join(dir, "Prisnfile"),
		filepath.Join(dir, "prisnfile"),
		filepath.Join(dir, "Prisnfile.toml"),
		filepath.Join(dir, "prisnfile.toml"),
		filepath.Join(dir, ".prisn"),
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return Load(path)
		}
	}

	// No Prisnfile found, infer from directory
	return Infer(dir)
}

// Infer creates a Prisnfile by analyzing a directory.
func Infer(dir string) (*Prisnfile, error) {
	pf := &Prisnfile{
		baseDir:   dir,
		Name:      filepath.Base(dir),
		Namespace: "default",
		Replicas:  1,
	}

	// Detect entrypoint
	entrypoint, runtime := detectEntrypoint(dir)
	if entrypoint != "" {
		pf.Entrypoint = entrypoint
		pf.Runtime = runtime
	}

	// Detect dependencies
	pf.detectDependencies()

	// Load .env if present
	pf.loadEnvFile()

	// Infer type
	pf.inferType()

	return pf, nil
}

// detectEntrypoint finds the main script in a directory.
func detectEntrypoint(dir string) (string, string) {
	// Priority order for entrypoints
	candidates := []struct {
		name    string
		runtime string
	}{
		{"main.py", "python3"},
		{"app.py", "python3"},
		{"server.py", "python3"},
		{"run.py", "python3"},
		{"__main__.py", "python3"},
		{"index.js", "node"},
		{"main.js", "node"},
		{"app.js", "node"},
		{"server.js", "node"},
		{"main.sh", "bash"},
		{"run.sh", "bash"},
		{"start.sh", "bash"},
	}

	for _, c := range candidates {
		path := filepath.Join(dir, c.name)
		if _, err := os.Stat(path); err == nil {
			return c.name, c.runtime
		}
	}

	// Fallback: find first script
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		switch ext {
		case ".py":
			return e.Name(), "python3"
		case ".js":
			return e.Name(), "node"
		case ".sh":
			return e.Name(), "bash"
		}
	}

	return "", ""
}

// detectDependencies looks for dependency files.
func (pf *Prisnfile) detectDependencies() {
	// Python
	reqPath := filepath.Join(pf.baseDir, "requirements.txt")
	if _, err := os.Stat(reqPath); err == nil {
		pf.Dependencies.RequirementsFile = "requirements.txt"
	}

	// Check pyproject.toml (parsing not yet implemented - uses requirements.txt fallback)
	pyprojectPath := filepath.Join(pf.baseDir, "pyproject.toml")
	if _, err := os.Stat(pyprojectPath); err == nil {
		// pyproject.toml detected but not parsed; use requirements.txt for dependencies
	}

	// Node
	pkgPath := filepath.Join(pf.baseDir, "package.json")
	if _, err := os.Stat(pkgPath); err == nil {
		pf.Dependencies.PackageFile = "package.json"
	}
}

// loadEnvFile loads .env file if present.
func (pf *Prisnfile) loadEnvFile() {
	envPath := filepath.Join(pf.baseDir, ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		return // No .env file, that's fine
	}

	if pf.Env == nil {
		pf.Env = make(map[string]string)
	}

	// Parse .env file
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// Remove quotes if present
			value = strings.Trim(value, `"'`)
			pf.Env[key] = value
		}
	}
}

// inferType determines the deployment type from config.
func (pf *Prisnfile) inferType() {
	if pf.Type != "" {
		return // Already set
	}

	if pf.Schedule != "" {
		pf.Type = "cronjob"
	} else if pf.Port > 0 {
		pf.Type = "service"
	} else {
		pf.Type = "job"
	}
}

// Validate checks the Prisnfile for errors.
func (pf *Prisnfile) Validate() error {
	if pf.Name == "" {
		return fmt.Errorf("name is required")
	}

	if pf.Entrypoint == "" {
		return fmt.Errorf("no entrypoint found (create main.py, app.py, or set entrypoint in Prisnfile)")
	}

	if pf.Port < 0 || pf.Port > 65535 {
		return fmt.Errorf("port must be 0-65535: %d", pf.Port)
	}

	if pf.Replicas < 0 {
		return fmt.Errorf("replicas cannot be negative: %d", pf.Replicas)
	}

	return nil
}

// EntrypointPath returns the full path to the entrypoint.
func (pf *Prisnfile) EntrypointPath() string {
	if filepath.IsAbs(pf.Entrypoint) {
		return pf.Entrypoint
	}
	return filepath.Join(pf.baseDir, pf.Entrypoint)
}

// String returns a human-readable representation.
func (pf *Prisnfile) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "name = %q\n", pf.Name)
	if pf.Namespace != "default" {
		fmt.Fprintf(&b, "namespace = %q\n", pf.Namespace)
	}
	fmt.Fprintf(&b, "entrypoint = %q\n", pf.Entrypoint)
	fmt.Fprintf(&b, "runtime = %q\n", pf.Runtime)
	fmt.Fprintf(&b, "type = %q\n", pf.Type)

	if pf.Port > 0 {
		fmt.Fprintf(&b, "port = %d\n", pf.Port)
	}
	if pf.Schedule != "" {
		fmt.Fprintf(&b, "schedule = %q\n", pf.Schedule)
	}
	if pf.Replicas > 1 {
		fmt.Fprintf(&b, "replicas = %d\n", pf.Replicas)
	}

	if len(pf.Env) > 0 {
		b.WriteString("\n[env]\n")
		for k, v := range pf.Env {
			fmt.Fprintf(&b, "%s = %q\n", k, v)
		}
	}

	return b.String()
}
