package runtime

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

// Registry manages available runtimes.
type Registry struct {
	mu       sync.RWMutex
	runtimes map[string]*Runtime
	byExt    map[string]*Runtime
	parsers  map[string]Parser
}

// NewRegistry creates a registry with built-in runtimes.
func NewRegistry() *Registry {
	r := &Registry{
		runtimes: make(map[string]*Runtime),
		byExt:    make(map[string]*Runtime),
		parsers:  make(map[string]Parser),
	}

	// Register built-in parsers
	r.RegisterParser(&RequirementsTxtParser{})
	r.RegisterParser(&PyprojectParser{})
	r.RegisterParser(&PackageJSONParser{})
	r.RegisterParser(&RegexParser{})

	// Register built-in runtimes
	for _, rt := range BuiltinRuntimes() {
		r.Register(rt)
	}

	return r
}

// Register adds a runtime to the registry.
func (r *Registry) Register(rt *Runtime) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if rt.ID == "" {
		return fmt.Errorf("runtime ID is required")
	}

	r.runtimes[rt.ID] = rt

	// Index by extension
	for _, ext := range rt.Extensions {
		ext = strings.ToLower(ext)
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		r.byExt[ext] = rt
	}

	return nil
}

// RegisterParser adds a dependency parser.
func (r *Registry) RegisterParser(p Parser) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.parsers[p.Name()] = p
}

// Get returns a runtime by ID.
func (r *Registry) Get(id string) (*Runtime, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.runtimes[id]
	return rt, ok
}

// GetByExtension returns a runtime for a file extension.
func (r *Registry) GetByExtension(ext string) (*Runtime, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ext = strings.ToLower(ext)
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	rt, ok := r.byExt[ext]
	return rt, ok
}

// GetByFile returns the runtime for a file based on extension or shebang.
func (r *Registry) GetByFile(path string) (*Runtime, error) {
	ext := strings.ToLower(filepath.Ext(path))

	// Try extension first
	if rt, ok := r.GetByExtension(ext); ok {
		return rt, nil
	}

	// Try shebang
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}

	// Check first line for shebang
	firstLine := strings.Split(string(content), "\n")[0]
	if strings.HasPrefix(firstLine, "#!") {
		r.mu.RLock()
		defer r.mu.RUnlock()
		for _, rt := range r.runtimes {
			for _, pattern := range rt.ShebangPatterns {
				if matched, _ := regexp.MatchString(pattern, firstLine); matched {
					return rt, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("unsupported file type: %s", ext)
}

// GetParser returns a parser by name.
func (r *Registry) GetParser(name string) (Parser, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.parsers[name]
	return p, ok
}

// List returns all registered runtimes.
func (r *Registry) List() []*Runtime {
	r.mu.RLock()
	defer r.mu.RUnlock()

	list := make([]*Runtime, 0, len(r.runtimes))
	for _, rt := range r.runtimes {
		list = append(list, rt)
	}
	return list
}

// Check verifies a runtime is installed and returns its path.
func (r *Registry) Check(rt *Runtime) (string, error) {
	// Try to find the command
	path, err := exec.LookPath(rt.Command)
	if err != nil {
		// Try common locations
		commonPaths := []string{
			"/usr/local/bin/" + rt.Command,
			"/usr/bin/" + rt.Command,
			"/opt/homebrew/bin/" + rt.Command,
			filepath.Join(os.Getenv("HOME"), ".local/bin", rt.Command),
		}
		for _, p := range commonPaths {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
		if path == "" {
			return "", fmt.Errorf("%s not found in PATH", rt.Command)
		}
	}

	// Run check command if specified
	if rt.Detect.Check != "" {
		parts := strings.Fields(rt.Detect.Check)
		cmd := exec.Command(parts[0], parts[1:]...)
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("%s check failed: %w", rt.Name, err)
		}
	}

	return path, nil
}

// InstallHint returns installation instructions for the current platform.
func (r *Registry) InstallHint(rt *Runtime) string {
	if rt.Install.Note != "" {
		return rt.Install.Note
	}

	// Determine platform key
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	keys := []string{
		goos + "_" + goarch,
		goos,
	}

	for _, key := range keys {
		if cmd, ok := rt.Install.Commands[key]; ok {
			return fmt.Sprintf("Install %s:\n  %s", rt.Name, cmd)
		}
	}

	if rt.Install.Script != "" {
		return fmt.Sprintf("Install %s:\n  curl -fsSL %s | sh", rt.Name, rt.Install.Script)
	}

	if rt.Install.DocURL != "" {
		return fmt.Sprintf("Install %s: see %s", rt.Name, rt.Install.DocURL)
	}

	return fmt.Sprintf("%s is required but not installed", rt.Name)
}

// GetInstallHints returns multiple installation hints for different platforms.
func (r *Registry) GetInstallHints(rt *Runtime) []string {
	var hints []string

	// Add platform-specific commands
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Check platform-specific first
	keys := []string{goos + "_" + goarch, goos}
	for _, key := range keys {
		if cmd, ok := rt.Install.Commands[key]; ok {
			hints = append(hints, cmd)
			break
		}
	}

	// Add script if available
	if rt.Install.Script != "" {
		hints = append(hints, fmt.Sprintf("curl -fsSL %s | sh", rt.Install.Script))
	}

	// Add doc URL if available
	if rt.Install.DocURL != "" {
		hints = append(hints, fmt.Sprintf("See: %s", rt.Install.DocURL))
	}

	// Add note if available
	if rt.Install.Note != "" {
		hints = append(hints, rt.Install.Note)
	}

	return hints
}

// LoadFromFile loads runtime definitions from a TOML file.
func (r *Registry) LoadFromFile(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return r.LoadFromTOML(string(content))
}

// LoadFromTOML parses runtime definitions from TOML content.
func (r *Registry) LoadFromTOML(content string) error {
	// Simple TOML parsing for runtime definitions
	// Format:
	// [runtime.NAME]
	// ...

	var currentRT *Runtime
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// New runtime section
		if strings.HasPrefix(line, "[runtime.") && strings.HasSuffix(line, "]") {
			if currentRT != nil {
				r.Register(currentRT)
			}
			id := strings.TrimSuffix(strings.TrimPrefix(line, "[runtime."), "]")
			// Handle subsections like [runtime.python.detect]
			if strings.Contains(id, ".") {
				continue // Skip subsections, handle inline
			}
			currentRT = &Runtime{ID: id}
			continue
		}

		if currentRT == nil {
			continue
		}

		// Parse key = value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, `"'`)

		switch key {
		case "name":
			currentRT.Name = value
		case "command":
			currentRT.Command = value
		case "extensions":
			currentRT.Extensions = parseStringArray(value)
		case "args":
			currentRT.Args = parseStringArray(value)
		case "shebang_patterns":
			currentRT.ShebangPatterns = parseStringArray(value)
		}
	}

	if currentRT != nil {
		r.Register(currentRT)
	}

	return nil
}

// parseStringArray parses a TOML array like ["a", "b"]
func parseStringArray(s string) []string {
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil
	}

	var result []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, `"'`)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// DefaultRegistry is the global runtime registry.
var DefaultRegistry = NewRegistry()

// Register adds a runtime to the default registry.
func Register(rt *Runtime) error {
	return DefaultRegistry.Register(rt)
}

// Get returns a runtime from the default registry.
func Get(id string) (*Runtime, bool) {
	return DefaultRegistry.Get(id)
}

// GetByExtension returns a runtime from the default registry.
func GetByExtension(ext string) (*Runtime, bool) {
	return DefaultRegistry.GetByExtension(ext)
}

// GetByFile returns a runtime from the default registry.
func GetByFile(path string) (*Runtime, error) {
	return DefaultRegistry.GetByFile(path)
}
