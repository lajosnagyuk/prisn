package layer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// NodeBaseDefinition defines a pre-built Node.js base environment.
type NodeBaseDefinition struct {
	Name        string
	Description string
	Packages    []Package
	Priority    int
}

// DefaultNodeBases are the pre-built Node.js bases we ship.
var DefaultNodeBases = []NodeBaseDefinition{
	{
		Name:        "node-minimal",
		Description: "Minimal Node.js with common utilities",
		Priority:    1,
		Packages: []Package{
			{Name: "dotenv"},
			{Name: "lodash"},
		},
	},
	{
		Name:        "node-web",
		Description: "Web development (Express, HTTP utilities)",
		Priority:    10,
		Packages: []Package{
			{Name: "express"},
			{Name: "cors"},
			{Name: "helmet"},
			{Name: "morgan"},
			{Name: "body-parser"},
			{Name: "cookie-parser"},
			{Name: "compression"},
			{Name: "dotenv"},
			{Name: "axios"},
			{Name: "node-fetch"},
		},
	},
	{
		Name:        "node-api",
		Description: "API development (Fastify, validation)",
		Priority:    15,
		Packages: []Package{
			{Name: "fastify"},
			{Name: "@fastify/cors"},
			{Name: "@fastify/helmet"},
			{Name: "zod"},
			{Name: "ajv"},
			{Name: "dotenv"},
			{Name: "axios"},
			{Name: "pino"},
		},
	},
	{
		Name:        "node-cli",
		Description: "CLI tool development",
		Priority:    8,
		Packages: []Package{
			{Name: "commander"},
			{Name: "yargs"},
			{Name: "chalk"},
			{Name: "ora"},
			{Name: "inquirer"},
			{Name: "boxen"},
			{Name: "dotenv"},
			{Name: "glob"},
			{Name: "fs-extra"},
		},
	},
	{
		Name:        "node-scraping",
		Description: "Web scraping and automation",
		Priority:    12,
		Packages: []Package{
			{Name: "puppeteer"},
			{Name: "cheerio"},
			{Name: "axios"},
			{Name: "node-fetch"},
			{Name: "dotenv"},
			{Name: "p-limit"},
			{Name: "p-queue"},
		},
	},
	{
		Name:        "node-database",
		Description: "Database access",
		Priority:    12,
		Packages: []Package{
			{Name: "pg"},
			{Name: "mysql2"},
			{Name: "mongodb"},
			{Name: "redis"},
			{Name: "ioredis"},
			{Name: "knex"},
			{Name: "dotenv"},
		},
	},
}

// NodeResolver resolves Node.js dependencies to layers.
type NodeResolver struct {
	store *Store
}

// NewNodeResolver creates a Node.js resolver.
func NewNodeResolver(store *Store) *NodeResolver {
	return &NodeResolver{store: store}
}

// ResolveFromDir analyzes a directory for Node.js dependencies.
func (r *NodeResolver) ResolveFromDir(dir string) (*Stack, error) {
	packages, err := r.detectPackages(dir)
	if err != nil {
		return nil, err
	}

	if len(packages) == 0 {
		return r.store.ResolveNode(nil)
	}

	return r.store.ResolveNode(packages)
}

// ResolveFromPackages resolves from an explicit package list.
func (r *NodeResolver) ResolveFromPackages(packages []Package) (*Stack, error) {
	return r.store.ResolveNode(packages)
}

// detectPackages scans a directory for Node.js dependencies.
func (r *NodeResolver) detectPackages(dir string) ([]Package, error) {
	// Priority order:
	// 1. package-lock.json (exact versions)
	// 2. package.json (declared deps)
	// 3. Import scanning (fallback)

	// Try package.json (most common)
	pkgFile := filepath.Join(dir, "package.json")
	if packages, err := parsePackageJSON(pkgFile); err == nil && len(packages) > 0 {
		return packages, nil
	}

	// Fall back to import scanning
	return scanNodeImports(dir)
}

// PackageJSON represents the structure of package.json
type PackageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// parsePackageJSON parses dependencies from package.json.
func parsePackageJSON(path string) ([]Package, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var pkg PackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, err
	}

	var packages []Package

	// Add regular dependencies
	for name, version := range pkg.Dependencies {
		// Clean version (remove ^, ~, etc. for exact matching)
		cleanVersion := strings.TrimLeft(version, "^~>=<")
		packages = append(packages, Package{
			Name:    name,
			Version: cleanVersion,
		})
	}

	// Optionally include dev dependencies for dev/test scenarios
	// For now, skip them - production only

	return packages, nil
}

// scanNodeImports scans JavaScript files for require/import statements.
func scanNodeImports(dir string) ([]Package, error) {
	imports := make(map[string]bool)

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip node_modules and hidden directories
		if info.IsDir() {
			name := info.Name()
			if name == "node_modules" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		// Only process .js, .mjs, .cjs, .ts files
		ext := filepath.Ext(path)
		if ext != ".js" && ext != ".mjs" && ext != ".cjs" && ext != ".ts" {
			return nil
		}

		fileImports, _ := extractNodeImports(path)
		for _, imp := range fileImports {
			imports[imp] = true
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	var packages []Package
	for imp := range imports {
		if isExternalNodePackage(imp) {
			packages = append(packages, Package{Name: imp})
		}
	}

	return packages, nil
}

// extractNodeImports extracts package names from require/import statements.
func extractNodeImports(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var imports []string
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Match: require('package') or require("package")
		if strings.Contains(line, "require(") {
			if pkg := extractRequirePackage(line); pkg != "" {
				imports = append(imports, pkg)
			}
		}

		// Match: import ... from 'package' or import ... from "package"
		if strings.HasPrefix(line, "import ") {
			if pkg := extractImportPackage(line); pkg != "" {
				imports = append(imports, pkg)
			}
		}
	}

	return imports, scanner.Err()
}

// extractRequirePackage extracts package name from require() call.
func extractRequirePackage(line string) string {
	// Find require(' or require("
	for _, quote := range []string{"'", "\""} {
		start := strings.Index(line, "require("+quote)
		if start == -1 {
			continue
		}
		start += len("require(") + 1
		end := strings.Index(line[start:], quote)
		if end == -1 {
			continue
		}
		return extractPackageName(line[start : start+end])
	}
	return ""
}

// extractImportPackage extracts package name from import statement.
func extractImportPackage(line string) string {
	// Find from ' or from "
	for _, quote := range []string{"'", "\""} {
		marker := "from " + quote
		start := strings.Index(line, marker)
		if start == -1 {
			continue
		}
		start += len(marker)
		end := strings.Index(line[start:], quote)
		if end == -1 {
			continue
		}
		return extractPackageName(line[start : start+end])
	}
	return ""
}

// extractPackageName extracts the npm package name from an import path.
// Handles scoped packages (@org/package) and subpath imports (package/subpath).
func extractPackageName(importPath string) string {
	// Skip relative imports
	if strings.HasPrefix(importPath, ".") || strings.HasPrefix(importPath, "/") {
		return ""
	}

	// Scoped package: @org/package or @org/package/subpath
	if strings.HasPrefix(importPath, "@") {
		parts := strings.SplitN(importPath, "/", 3)
		if len(parts) >= 2 {
			return parts[0] + "/" + parts[1]
		}
		return ""
	}

	// Regular package: package or package/subpath
	parts := strings.SplitN(importPath, "/", 2)
	return parts[0]
}

// isExternalNodePackage checks if a package name is external (not builtin).
func isExternalNodePackage(name string) bool {
	// Node.js builtin modules
	builtins := map[string]bool{
		"assert": true, "async_hooks": true, "buffer": true, "child_process": true,
		"cluster": true, "console": true, "constants": true, "crypto": true,
		"dgram": true, "dns": true, "domain": true, "events": true,
		"fs": true, "http": true, "http2": true, "https": true,
		"inspector": true, "module": true, "net": true, "os": true,
		"path": true, "perf_hooks": true, "process": true, "punycode": true,
		"querystring": true, "readline": true, "repl": true, "stream": true,
		"string_decoder": true, "sys": true, "timers": true, "tls": true,
		"trace_events": true, "tty": true, "url": true, "util": true,
		"v8": true, "vm": true, "wasi": true, "worker_threads": true,
		"zlib": true,
		// Node.js prefixed builtins
		"node:assert": true, "node:buffer": true, "node:child_process": true,
		"node:crypto": true, "node:events": true, "node:fs": true,
		"node:http": true, "node:https": true, "node:net": true,
		"node:os": true, "node:path": true, "node:process": true,
		"node:stream": true, "node:url": true, "node:util": true,
		"node:zlib": true,
	}

	return !builtins[name]
}

// ResolveNode resolves Node.js packages to a stack.
func (s *Store) ResolveNode(packages []Package) (*Stack, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	packages = normalizePackages(packages)

	// Find the best matching Node base
	base, covered := s.findBestNodeBase(packages)

	// Compute delta
	delta := computeDelta(packages, covered)

	var layerIDs []string
	if base != nil {
		layerIDs = append(layerIDs, base.LayerID)
	}

	if len(delta) > 0 {
		deltaLayer, err := s.getOrCreateNodeLayer(delta)
		if err != nil {
			return nil, fmt.Errorf("create node delta layer: %w", err)
		}
		layerIDs = append(layerIDs, deltaLayer.ID)
	}

	stack := s.composeNodeStack(layerIDs)
	return stack, nil
}

// findBestNodeBase finds the Node base that covers the most packages.
func (s *Store) findBestNodeBase(packages []Package) (*Base, []Package) {
	packageSet := make(map[string]bool)
	for _, p := range packages {
		packageSet[strings.ToLower(p.Name)] = true
	}

	var bestBase *Base
	var bestCovered []Package
	bestScore := 0

	// Check node bases (stored with "node-" prefix)
	for name, base := range s.bases {
		if !strings.HasPrefix(name, "node-") {
			continue
		}

		var covered []Package
		for _, bp := range base.Packages {
			if packageSet[strings.ToLower(bp.Name)] {
				covered = append(covered, bp)
			}
		}

		score := len(covered) * (base.Priority + 1)
		if score > bestScore {
			bestScore = score
			bestBase = base
			bestCovered = covered
		}
	}

	if bestBase != nil && (len(bestCovered) >= len(packages)/2 || len(bestCovered) >= 3) {
		return bestBase, bestCovered
	}

	return nil, nil
}

// getOrCreateNodeLayer gets or creates a Node.js layer.
func (s *Store) getOrCreateNodeLayer(packages []Package) (*Layer, error) {
	id := "node-" + computeLayerID(packages)

	if layer, ok := s.layers[id]; ok {
		layer.LastUsed = s.layers[id].LastUsed
		return layer, nil
	}

	layer, err := s.createNodeLayer(id, packages)
	if err != nil {
		return nil, err
	}

	s.layers[id] = layer
	s.saveIndex()

	return layer, nil
}

// createNodeLayer creates a Node.js layer by installing packages.
func (s *Store) createNodeLayer(id string, packages []Package) (*Layer, error) {
	layerPath := filepath.Join(s.root, "layers", id)

	// Create the layer directory
	if err := os.MkdirAll(layerPath, 0755); err != nil {
		return nil, err
	}

	// Install packages using npm
	if err := s.installNodePackages(layerPath, packages); err != nil {
		os.RemoveAll(layerPath)
		return nil, fmt.Errorf("install node packages: %w", err)
	}

	size, _ := dirSize(layerPath)

	layer := &Layer{
		ID:       id,
		Packages: packages,
		Size:     size,
		Path:     layerPath,
	}

	return layer, nil
}

// installNodePackages runs npm install for the given packages.
func (s *Store) installNodePackages(targetDir string, packages []Package) error {
	if len(packages) == 0 {
		return nil
	}

	// Build package specs
	var specs []string
	for _, p := range packages {
		if p.Version != "" {
			specs = append(specs, fmt.Sprintf("%s@%s", p.Name, p.Version))
		} else {
			specs = append(specs, p.Name)
		}
	}

	// Use npm install with --prefix to install to target directory
	args := []string{
		"install",
		"--prefix", targetDir,
		"--no-save",
		"--no-audit",
		"--no-fund",
		"--loglevel", "error",
	}
	args = append(args, specs...)

	cmd := exec.Command("npm", args...)
	cmd.Env = append(os.Environ(), "npm_config_loglevel=error")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("npm install failed: %s\n%s", err, output)
	}

	return nil
}

// composeNodeStack creates a stack for Node.js layers.
func (s *Store) composeNodeStack(layerIDs []string) *Stack {
	stackID := "node-" + computeStackID(layerIDs)

	var nodePath []string
	for _, id := range layerIDs {
		if layer, ok := s.layers[id]; ok {
			// Node modules are in <layer>/node_modules
			nodeModules := filepath.Join(layer.Path, "node_modules")
			if _, err := os.Stat(nodeModules); err == nil {
				nodePath = append(nodePath, nodeModules)
			}
		}
	}

	return &Stack{
		ID:       stackID,
		LayerIDs: layerIDs,
		NodePath: nodePath,
	}
}

// BuildNodeBase creates a Node.js base layer from a definition.
func (s *Store) BuildNodeBase(def NodeBaseDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if already exists
	if existing, ok := s.bases[def.Name]; ok {
		if _, layerOk := s.layers[existing.LayerID]; layerOk {
			return nil
		}
	}

	fmt.Printf("Building node base: %s (%s)\n", def.Name, def.Description)

	// Create the layer
	layerID := "node-" + computeLayerID(def.Packages)
	layer, err := s.createNodeLayer(layerID, def.Packages)
	if err != nil {
		return fmt.Errorf("build node base %s: %w", def.Name, err)
	}

	s.layers[layerID] = layer

	base := &Base{
		Name:        def.Name,
		Description: def.Description,
		LayerID:     layerID,
		Packages:    def.Packages,
		Priority:    def.Priority,
	}
	s.bases[def.Name] = base

	return s.saveIndex()
}

// BuildAllNodeBases builds all default Node.js bases.
func (s *Store) BuildAllNodeBases() error {
	for _, def := range DefaultNodeBases {
		if err := s.BuildNodeBase(def); err != nil {
			return err
		}
	}
	return nil
}
