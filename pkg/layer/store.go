// Package layer provides container-lite layered environment management.
// Users never interact with layers directly - they just run scripts and
// we figure out the optimal layer composition automatically.
package layer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store manages the layer cache and stack composition.
type Store struct {
	root string // ~/.prisn/layers
	mu   sync.RWMutex

	// In-memory cache of layer metadata
	layers map[string]*Layer
	bases  map[string]*Base
}

// Layer is a content-addressable set of installed packages.
type Layer struct {
	ID        string    `json:"id"`         // SHA256 of sorted package list
	Packages  []Package `json:"packages"`   // What's in this layer
	Size      int64     `json:"size"`       // Bytes on disk
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used"`
	Path      string    `json:"path"`       // Absolute path to layer
}

// Package represents an installed package with version.
type Package struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Base is a pre-built layer optimized for common use cases.
type Base struct {
	Name        string    `json:"name"`        // e.g., "python-web"
	Description string    `json:"description"`
	LayerID     string    `json:"layer_id"`    // Points to actual layer
	Packages    []Package `json:"packages"`    // What's included
	Priority    int       `json:"priority"`    // Higher = prefer this base
}

// Stack is a composed environment from multiple layers.
// This is an internal concept - users never see this.
type Stack struct {
	ID         string   `json:"id"`
	BaseID     string   `json:"base_id,omitempty"`
	LayerIDs   []string `json:"layer_ids"`
	PythonPath []string `json:"python_path"` // Computed PYTHONPATH
	NodePath   []string `json:"node_path"`   // Computed NODE_PATH
	BinPath    string   `json:"bin_path"`    // Path to executables
}

// NewStore creates a new layer store at the given root.
func NewStore(root string) (*Store, error) {
	s := &Store{
		root:   root,
		layers: make(map[string]*Layer),
		bases:  make(map[string]*Base),
	}

	// Create directory structure
	dirs := []string{
		filepath.Join(root, "layers"),
		filepath.Join(root, "bases"),
		filepath.Join(root, "stacks"),
		filepath.Join(root, "tmp"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}

	// Load existing layers and bases
	if err := s.loadIndex(); err != nil {
		// Not fatal - we can rebuild
		s.layers = make(map[string]*Layer)
		s.bases = make(map[string]*Base)
	}

	return s, nil
}

// Resolve takes a list of required packages and returns the optimal Stack.
// This is the main entry point - completely automatic.
func (s *Store) Resolve(packages []Package) (*Stack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Normalize and sort packages
	packages = normalizePackages(packages)

	// Find the best matching base
	base, covered := s.findBestBase(packages)

	// Compute delta (what's not in the base)
	delta := computeDelta(packages, covered)

	// Get or create the delta layer
	var layerIDs []string
	if base != nil {
		layerIDs = append(layerIDs, base.LayerID)
	}

	if len(delta) > 0 {
		// Need a delta layer
		deltaLayer, err := s.getOrCreateLayer(delta)
		if err != nil {
			return nil, fmt.Errorf("create delta layer: %w", err)
		}
		layerIDs = append(layerIDs, deltaLayer.ID)
	}

	// Compose the stack
	stack := s.composeStack(layerIDs)

	return stack, nil
}

// findBestBase finds the base that covers the most packages.
func (s *Store) findBestBase(packages []Package) (*Base, []Package) {
	if len(s.bases) == 0 {
		return nil, nil
	}

	packageSet := make(map[string]bool)
	for _, p := range packages {
		packageSet[strings.ToLower(p.Name)] = true
	}

	var bestBase *Base
	var bestCovered []Package
	bestScore := 0

	for _, base := range s.bases {
		var covered []Package
		for _, bp := range base.Packages {
			if packageSet[strings.ToLower(bp.Name)] {
				covered = append(covered, bp)
			}
		}

		// Score = covered packages * priority (prefer more specific bases)
		score := len(covered) * (base.Priority + 1)
		if score > bestScore {
			bestScore = score
			bestBase = base
			bestCovered = covered
		}
	}

	// Only use base if it covers at least 50% of packages or 3+ packages
	if bestBase != nil && (len(bestCovered) >= len(packages)/2 || len(bestCovered) >= 3) {
		return bestBase, bestCovered
	}

	return nil, nil
}

// computeDelta returns packages not covered by the base.
func computeDelta(required, covered []Package) []Package {
	coveredSet := make(map[string]bool)
	for _, p := range covered {
		coveredSet[strings.ToLower(p.Name)] = true
	}

	var delta []Package
	for _, p := range required {
		if !coveredSet[strings.ToLower(p.Name)] {
			delta = append(delta, p)
		}
	}
	return delta
}

// getOrCreateLayer gets an existing layer or creates a new one.
func (s *Store) getOrCreateLayer(packages []Package) (*Layer, error) {
	id := computeLayerID(packages)

	// Check if it exists
	if layer, ok := s.layers[id]; ok {
		layer.LastUsed = time.Now()
		return layer, nil
	}

	// Need to create it
	layer, err := s.createLayer(id, packages)
	if err != nil {
		return nil, err
	}

	s.layers[id] = layer
	s.saveIndex()

	return layer, nil
}

// createLayer creates a new layer by installing packages.
func (s *Store) createLayer(id string, packages []Package) (*Layer, error) {
	layerPath := filepath.Join(s.root, "layers", id)

	// Create temp directory for installation
	tmpDir := filepath.Join(s.root, "tmp", id)
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	// Install packages to temp
	if err := s.installPackages(tmpDir, packages); err != nil {
		return nil, fmt.Errorf("install packages: %w", err)
	}

	// Move to final location (atomic on same filesystem)
	if err := os.Rename(tmpDir, layerPath); err != nil {
		// Cross-filesystem, need to copy
		if err := copyDir(tmpDir, layerPath); err != nil {
			return nil, err
		}
	}

	// Calculate size
	size, _ := dirSize(layerPath)

	layer := &Layer{
		ID:        id,
		Packages:  packages,
		Size:      size,
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
		Path:      layerPath,
	}

	return layer, nil
}

// installPackages runs pip install into the target directory.
func (s *Store) installPackages(targetDir string, packages []Package) error {
	if len(packages) == 0 {
		return nil
	}

	// Build package specs
	var specs []string
	for _, p := range packages {
		if p.Version != "" {
			specs = append(specs, fmt.Sprintf("%s==%s", p.Name, p.Version))
		} else {
			specs = append(specs, p.Name)
		}
	}

	// Use pip install --target
	args := []string{
		"-m", "pip", "install",
		"--target", targetDir,
		"--no-deps", // We handle deps ourselves via layers
		"--disable-pip-version-check",
		"--quiet",
	}
	args = append(args, specs...)

	cmd := newCommand("python3", args...)
	cmd.Env = append(os.Environ(), "PIP_DISABLE_PIP_VERSION_CHECK=1")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pip install failed: %s\n%s", err, output)
	}

	return nil
}

// composeStack creates a Stack from layer IDs.
func (s *Store) composeStack(layerIDs []string) *Stack {
	stackID := computeStackID(layerIDs)

	var pythonPath []string
	for _, id := range layerIDs {
		if layer, ok := s.layers[id]; ok {
			// Add site-packages path
			sitePackages := filepath.Join(layer.Path, "lib", "python3.*", "site-packages")
			matches, _ := filepath.Glob(sitePackages)
			if len(matches) > 0 {
				pythonPath = append(pythonPath, matches[0])
			} else {
				// Flat layout (--target installs here)
				pythonPath = append(pythonPath, layer.Path)
			}
		}
	}

	return &Stack{
		ID:         stackID,
		LayerIDs:   layerIDs,
		PythonPath: pythonPath,
	}
}

// GetLayer retrieves a layer by ID.
func (s *Store) GetLayer(id string) (*Layer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	layer, ok := s.layers[id]
	return layer, ok
}

// RegisterBase adds a base environment.
func (s *Store) RegisterBase(base *Base) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.bases[base.Name] = base
	return s.saveIndex()
}

// GarbageCollect removes unused layers.
func (s *Store) GarbageCollect(maxAge time.Duration) (int, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	var removed int
	var freedBytes int64

	// Find layers to remove (not used recently, not a base)
	baseLayerIDs := make(map[string]bool)
	for _, base := range s.bases {
		baseLayerIDs[base.LayerID] = true
	}

	for id, layer := range s.layers {
		if baseLayerIDs[id] {
			continue // Don't GC base layers
		}
		if layer.LastUsed.Before(cutoff) {
			if err := os.RemoveAll(layer.Path); err == nil {
				freedBytes += layer.Size
				removed++
				delete(s.layers, id)
			}
		}
	}

	if removed > 0 {
		s.saveIndex()
	}

	return removed, freedBytes, nil
}

// Stats returns store statistics.
func (s *Store) Stats() (layers int, bases int, totalSize int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, layer := range s.layers {
		totalSize += layer.Size
	}
	return len(s.layers), len(s.bases), totalSize
}

// --- Index persistence ---

type storeIndex struct {
	Layers map[string]*Layer `json:"layers"`
	Bases  map[string]*Base  `json:"bases"`
}

func (s *Store) loadIndex() error {
	indexPath := filepath.Join(s.root, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return err
	}

	var idx storeIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return err
	}

	s.layers = idx.Layers
	s.bases = idx.Bases

	if s.layers == nil {
		s.layers = make(map[string]*Layer)
	}
	if s.bases == nil {
		s.bases = make(map[string]*Base)
	}

	return nil
}

func (s *Store) saveIndex() error {
	idx := storeIndex{
		Layers: s.layers,
		Bases:  s.bases,
	}

	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}

	indexPath := filepath.Join(s.root, "index.json")
	return os.WriteFile(indexPath, data, 0644)
}

// --- Helpers ---

func normalizePackages(packages []Package) []Package {
	// Deduplicate and sort
	seen := make(map[string]Package)
	for _, p := range packages {
		key := strings.ToLower(p.Name)
		if existing, ok := seen[key]; ok {
			// Keep version if we have one
			if p.Version != "" && existing.Version == "" {
				seen[key] = p
			}
		} else {
			seen[key] = p
		}
	}

	result := make([]Package, 0, len(seen))
	for _, p := range seen {
		result = append(result, p)
	}

	sort.Slice(result, func(i, j int) bool {
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})

	return result
}

func computeLayerID(packages []Package) string {
	packages = normalizePackages(packages)

	var parts []string
	for _, p := range packages {
		if p.Version != "" {
			parts = append(parts, fmt.Sprintf("%s==%s", p.Name, p.Version))
		} else {
			parts = append(parts, p.Name)
		}
	}

	h := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(h[:])[:16] // Short hash is fine
}

func computeStackID(layerIDs []string) string {
	h := sha256.Sum256([]byte(strings.Join(layerIDs, ":")))
	return hex.EncodeToString(h[:])[:16]
}

func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(src, path)
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		// Try hardlink first (much faster)
		if err := os.Link(path, dstPath); err == nil {
			return nil
		}

		// Fall back to copy
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, info.Mode())
	})
}
