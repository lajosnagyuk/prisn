package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Detector finds dependencies for scripts using the runtime registry.
type Detector struct {
	Registry *Registry
}

// NewDetector creates a detector with the default registry.
func NewDetector() *Detector {
	return &Detector{Registry: DefaultRegistry}
}

// projectMarkers are files that indicate a project root.
var projectMarkers = []string{
	"prisn.toml",
	"requirements.txt",
	"pyproject.toml",
	"setup.py",
	"package.json",
	".git",
	"go.mod",
	"Cargo.toml",
	"bb.edn",
	"deps.edn",
	"cpanfile",
	"composer.json",
	"Gemfile",
	"Project.toml",
}

// Detect finds dependencies for a script file.
func (d *Detector) Detect(scriptPath string) (*Manifest, error) {
	// Find the runtime for this file
	rt, err := d.Registry.GetByFile(scriptPath)
	if err != nil {
		return nil, err
	}

	// Find project root
	scriptDir := filepath.Dir(scriptPath)
	projectRoot := d.findProjectRoot(scriptDir)

	return d.detect(scriptPath, projectRoot, rt)
}

// DetectWithRuntime detects dependencies for a specific runtime.
func (d *Detector) DetectWithRuntime(scriptPath string, runtimeID string) (*Manifest, error) {
	rt, ok := d.Registry.Get(runtimeID)
	if !ok {
		return nil, fmt.Errorf("unknown runtime: %s", runtimeID)
	}

	scriptDir := filepath.Dir(scriptPath)
	projectRoot := d.findProjectRoot(scriptDir)

	return d.detect(scriptPath, projectRoot, rt)
}

func (d *Detector) detect(scriptPath, projectRoot string, rt *Runtime) (*Manifest, error) {
	manifest := &Manifest{
		Runtime: rt.ID,
	}

	// If runtime doesn't have dependency management, we're done
	if !rt.Deps.Enabled {
		manifest.Source = "none"
		manifest.Hash = hashString(rt.ID + "-no-deps")
		return manifest, nil
	}

	// If runtime manages its own deps (like Babashka, Deno), we're done
	if rt.Deps.SelfManaged {
		manifest.Source = "self-managed"
		manifest.Hash = hashString(rt.ID + "-self-managed")
		return manifest, nil
	}

	// Try manifest files in order
	for _, manifestFile := range rt.Deps.ManifestFiles {
		manifestPath := filepath.Join(projectRoot, manifestFile)

		// Handle glob patterns
		if strings.Contains(manifestFile, "*") {
			matches, err := filepath.Glob(manifestPath)
			if err == nil && len(matches) > 0 {
				manifestPath = matches[0]
			} else {
				continue
			}
		}

		if _, err := os.Stat(manifestPath); err != nil {
			continue
		}

		// Get parser for this runtime
		parser, ok := d.Registry.GetParser(rt.Deps.Parser)
		if !ok {
			// Try to use the manifest file name as parser
			parser, ok = d.Registry.GetParser(filepath.Base(manifestFile))
		}
		if !ok {
			continue
		}

		deps, err := parser.Parse(manifestPath, rt)
		if err != nil {
			continue
		}

		if len(deps) > 0 {
			manifest.Dependencies = deps
			manifest.Source = manifestFile
			manifest.Hash = d.hashDeps(rt.ID, deps)
			return manifest, nil
		}
	}

	// Fall back to import scanning
	scanner := NewImportScanner(rt)
	deps, err := scanner.Scan(scriptPath)
	if err == nil && len(deps) > 0 {
		manifest.Dependencies = deps
		manifest.Source = "imports"
		manifest.Hash = d.hashDeps(rt.ID, deps)
		return manifest, nil
	}

	// No dependencies found
	manifest.Source = "none"
	manifest.Hash = hashString(rt.ID + "-no-deps")
	return manifest, nil
}

// findProjectRoot walks up from dir looking for project markers.
func (d *Detector) findProjectRoot(dir string) string {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}

	current := absDir
	for {
		// Check for any marker
		for _, marker := range projectMarkers {
			markerPath := filepath.Join(current, marker)
			if _, err := os.Stat(markerPath); err == nil {
				return current
			}
		}

		// Move up one directory
		parent := filepath.Dir(current)
		if parent == current {
			return absDir
		}
		current = parent
	}
}

// hashDeps creates a cache key from dependencies.
func (d *Detector) hashDeps(runtime string, deps []Dependency) string {
	sorted := make([]string, len(deps))
	for i, dep := range deps {
		sorted[i] = dep.String()
	}
	sort.Strings(sorted)

	h := sha256.New()
	h.Write([]byte(runtime))
	for _, s := range sorted {
		h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])[:16]
}
