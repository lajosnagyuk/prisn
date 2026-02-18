// Package deps handles dependency detection and resolution.
//
// This is the most critical piece of prisn - without it, "just run my shit"
// fails the moment someone writes `import pandas`.
package deps

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Manifest represents detected dependencies for a script.
type Manifest struct {
	// Runtime is the detected runtime (python3.11, node20, etc.)
	Runtime string

	// Version is the specific version (3.11, 20, etc.)
	Version string

	// Dependencies are the required packages
	Dependencies []Dependency

	// Source is where dependencies were detected from
	Source string // "requirements.txt", "pyproject.toml", "imports", "package.json"

	// Hash is a cache key based on dependencies
	Hash string
}

// Dependency represents a single package requirement.
type Dependency struct {
	Name    string
	Version string // Can be empty, ">=1.0", "==1.0.0", etc.
}

// String returns the pip/npm format string.
func (d Dependency) String() string {
	if d.Version == "" {
		return d.Name
	}
	// If version starts with a comparison operator, use as-is
	if strings.HasPrefix(d.Version, ">=") ||
		strings.HasPrefix(d.Version, "<=") ||
		strings.HasPrefix(d.Version, "==") ||
		strings.HasPrefix(d.Version, "!=") ||
		strings.HasPrefix(d.Version, "~=") ||
		strings.HasPrefix(d.Version, "^") {
		return d.Name + d.Version
	}
	return d.Name + "==" + d.Version
}

// Detector finds dependencies for a script.
type Detector struct {
	// StdlibModules is a set of Python standard library modules to ignore
	StdlibModules map[string]bool
}

// NewDetector creates a dependency detector.
func NewDetector() *Detector {
	return &Detector{
		StdlibModules: pythonStdlib(),
	}
}

// projectMarkers are files that indicate a project root.
var projectMarkers = []string{
	"prisn.toml",        // Our config
	"requirements.txt",  // Python
	"pyproject.toml",    // Python (modern)
	"setup.py",          // Python (legacy)
	"package.json",      // Node.js
	".git",              // Git root
	"go.mod",            // Go
	"Cargo.toml",        // Rust
}

// findProjectRoot walks up from dir looking for project markers.
// Returns the original dir if no marker is found.
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
			// Reached root, return original dir
			return absDir
		}
		current = parent
	}
}

// Detect finds dependencies for a script file.
func (d *Detector) Detect(scriptPath string) (*Manifest, error) {
	ext := strings.ToLower(filepath.Ext(scriptPath))
	scriptDir := filepath.Dir(scriptPath)

	// Find project root (walks up looking for markers)
	dir := d.findProjectRoot(scriptDir)

	switch ext {
	case ".py":
		return d.detectPython(scriptPath, dir)
	case ".js", ".mjs":
		return d.detectNode(scriptPath, dir)
	case ".sh", ".bash":
		return d.detectBash(scriptPath)
	default:
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}
}

// detectPython handles Python dependency detection.
func (d *Detector) detectPython(scriptPath, dir string) (*Manifest, error) {
	manifest := &Manifest{
		Runtime: "python3",
		Version: "3.11", // Default, can be overridden
	}

	// Priority order for dependency sources:
	// 1. requirements.txt (most explicit)
	// 2. pyproject.toml (modern standard)
	// 3. Import scanning (fallback)

	// Check for requirements.txt
	reqPath := filepath.Join(dir, "requirements.txt")
	if deps, err := d.parseRequirementsTxt(reqPath); err == nil && len(deps) > 0 {
		manifest.Dependencies = deps
		manifest.Source = "requirements.txt"
		manifest.Hash = d.hashDeps(manifest.Runtime, deps)
		return manifest, nil
	}

	// Check for pyproject.toml
	pyprojectPath := filepath.Join(dir, "pyproject.toml")
	if deps, version, err := d.parsePyproject(pyprojectPath); err == nil && len(deps) > 0 {
		manifest.Dependencies = deps
		manifest.Source = "pyproject.toml"
		if version != "" {
			manifest.Version = version
		}
		manifest.Hash = d.hashDeps(manifest.Runtime, deps)
		return manifest, nil
	}

	// Fallback: scan imports
	deps, err := d.scanPythonImports(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("failed to scan imports: %w", err)
	}

	manifest.Dependencies = deps
	manifest.Source = "imports"
	manifest.Hash = d.hashDeps(manifest.Runtime, deps)
	return manifest, nil
}

// parseRequirementsTxt parses a requirements.txt file.
func (d *Detector) parseRequirementsTxt(path string) ([]Dependency, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var deps []Dependency
	scanner := bufio.NewScanner(f)

	// Regex for requirements.txt lines:
	// package>=1.0.0
	// package==1.0.0
	// package
	// -r other.txt (include, skip for now)
	// # comment
	reqRe := regexp.MustCompile(`^([a-zA-Z0-9_-]+)\s*(.*?)$`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Skip include directives for now
		if strings.HasPrefix(line, "-r") || strings.HasPrefix(line, "-e") {
			continue
		}

		matches := reqRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		deps = append(deps, Dependency{
			Name:    strings.ToLower(matches[1]),
			Version: strings.TrimSpace(matches[2]),
		})
	}

	return deps, scanner.Err()
}

// parsePyproject parses a pyproject.toml file.
func (d *Detector) parsePyproject(path string) ([]Dependency, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}

	var deps []Dependency
	var pythonVersion string

	// Simple TOML parsing for common patterns
	// Full TOML parsing would use a library, but we want minimal deps
	lines := strings.Split(string(content), "\n")
	inDependencies := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Look for [project.dependencies] or [tool.poetry.dependencies]
		if strings.Contains(line, "dependencies") && strings.HasPrefix(line, "[") {
			inDependencies = true
			continue
		}

		// New section starts
		if strings.HasPrefix(line, "[") {
			inDependencies = false
			continue
		}

		// Parse python version requirement
		if strings.HasPrefix(line, "requires-python") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				pythonVersion = extractPythonVersion(strings.Trim(parts[1], `"' `))
			}
		}

		// Parse dependencies
		if inDependencies && strings.Contains(line, "=") {
			// Handle: numpy = ">=1.24"
			// Handle: numpy = {version = ">=1.24"}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				name := strings.Trim(parts[0], `"' `)
				version := extractVersion(parts[1])
				if name != "" && name != "python" {
					deps = append(deps, Dependency{
						Name:    strings.ToLower(name),
						Version: version,
					})
				}
			}
		}
	}

	return deps, pythonVersion, nil
}

// scanPythonImports scans a Python file for import statements.
func (d *Detector) scanPythonImports(path string) ([]Dependency, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Regex for import statements:
	// import foo
	// import foo.bar
	// from foo import bar
	// from foo.bar import baz
	importRe := regexp.MustCompile(`(?m)^(?:from\s+(\w+)|import\s+(\w+))`)

	matches := importRe.FindAllStringSubmatch(string(content), -1)
	seen := make(map[string]bool)
	var deps []Dependency

	for _, match := range matches {
		var module string
		if match[1] != "" {
			module = match[1]
		} else {
			module = match[2]
		}

		// Skip stdlib modules
		if d.StdlibModules[module] {
			continue
		}

		// Skip already seen
		if seen[module] {
			continue
		}
		seen[module] = true

		// Map common import names to package names
		pkgName := mapImportToPackage(module)
		deps = append(deps, Dependency{Name: pkgName})
	}

	return deps, nil
}

// hashDeps creates a cache key from dependencies.
func (d *Detector) hashDeps(runtime string, deps []Dependency) string {
	// Sort for consistent hashing
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

// detectNode handles Node.js dependency detection.
func (d *Detector) detectNode(scriptPath, dir string) (*Manifest, error) {
	manifest := &Manifest{
		Runtime: "node",
		Version: "20", // LTS
	}

	// Check for package.json
	pkgPath := filepath.Join(dir, "package.json")
	if deps, err := d.parsePackageJSON(pkgPath); err == nil && len(deps) > 0 {
		manifest.Dependencies = deps
		manifest.Source = "package.json"
		manifest.Hash = d.hashDeps(manifest.Runtime, deps)
		return manifest, nil
	}

	// Scan for require/import statements
	deps, err := d.scanNodeImports(scriptPath)
	if err != nil {
		return nil, err
	}

	manifest.Dependencies = deps
	manifest.Source = "imports"
	manifest.Hash = d.hashDeps(manifest.Runtime, deps)
	return manifest, nil
}

// parsePackageJSON parses a package.json file.
func (d *Detector) parsePackageJSON(path string) ([]Dependency, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Simple JSON parsing without external dependency
	// Look for "dependencies": { "pkg": "version" }
	var deps []Dependency

	// Find dependencies block
	depRe := regexp.MustCompile(`"dependencies"\s*:\s*\{([^}]+)\}`)
	matches := depRe.FindStringSubmatch(string(content))
	if matches == nil {
		return deps, nil
	}

	// Parse individual dependencies
	pkgRe := regexp.MustCompile(`"([^"]+)"\s*:\s*"([^"]+)"`)
	for _, match := range pkgRe.FindAllStringSubmatch(matches[1], -1) {
		deps = append(deps, Dependency{
			Name:    match[1],
			Version: match[2],
		})
	}

	return deps, nil
}

// scanNodeImports scans a JavaScript file for require/import statements.
func (d *Detector) scanNodeImports(path string) ([]Dependency, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Patterns:
	// require('package')
	// require("package")
	// import x from 'package'
	// import { x } from "package"
	requireRe := regexp.MustCompile(`require\s*\(\s*['"]([^'"./][^'"]+)['"]\s*\)`)
	importRe := regexp.MustCompile(`import\s+(?:[\w\s{},*]+\s+from\s+)?['"]([^'"./][^'"]+)['"]`)

	seen := make(map[string]bool)
	var deps []Dependency

	// Node.js built-in modules to skip
	builtins := map[string]bool{
		"fs": true, "path": true, "http": true, "https": true, "url": true,
		"util": true, "os": true, "crypto": true, "stream": true, "events": true,
		"buffer": true, "child_process": true, "cluster": true, "dgram": true,
		"dns": true, "net": true, "readline": true, "repl": true, "tls": true,
		"tty": true, "v8": true, "vm": true, "zlib": true, "assert": true,
		"async_hooks": true, "console": true, "constants": true, "domain": true,
		"inspector": true, "module": true, "perf_hooks": true, "process": true,
		"punycode": true, "querystring": true, "string_decoder": true,
		"sys": true, "timers": true, "trace_events": true, "worker_threads": true,
	}

	for _, re := range []*regexp.Regexp{requireRe, importRe} {
		for _, match := range re.FindAllStringSubmatch(string(content), -1) {
			pkg := match[1]
			// Get root package name (e.g., @org/pkg -> @org/pkg, lodash/fp -> lodash)
			if strings.HasPrefix(pkg, "@") {
				parts := strings.SplitN(pkg, "/", 3)
				if len(parts) >= 2 {
					pkg = parts[0] + "/" + parts[1]
				}
			} else {
				pkg = strings.Split(pkg, "/")[0]
			}

			if builtins[pkg] || seen[pkg] {
				continue
			}
			seen[pkg] = true
			deps = append(deps, Dependency{Name: pkg})
		}
	}

	return deps, nil
}

// detectBash handles shell script detection (no deps to detect).
func (d *Detector) detectBash(_ string) (*Manifest, error) {
	return &Manifest{
		Runtime: "bash",
		Source:  "extension",
		Hash:    "bash-no-deps",
	}, nil
}

// Helper functions

func extractPythonVersion(s string) string {
	// Extract version from ">=3.9" or "~=3.11"
	re := regexp.MustCompile(`(\d+\.\d+)`)
	match := re.FindString(s)
	if match != "" {
		return match
	}
	return ""
}

func extractVersion(s string) string {
	// Extract version from various formats
	s = strings.Trim(s, `"' {}`)

	// Handle {version = ">=1.0"}
	if strings.Contains(s, "version") {
		re := regexp.MustCompile(`version\s*=\s*["']([^"']+)["']`)
		match := re.FindStringSubmatch(s)
		if match != nil {
			return match[1]
		}
	}

	// Direct version string
	re := regexp.MustCompile(`^[>=<~^!]*[\d.]+`)
	match := re.FindString(s)
	return match
}

// mapImportToPackage maps Python import names to PyPI package names.
func mapImportToPackage(importName string) string {
	// Common mappings where import name != package name
	mappings := map[string]string{
		"cv2":        "opencv-python",
		"PIL":        "pillow",
		"sklearn":    "scikit-learn",
		"skimage":    "scikit-image",
		"yaml":       "pyyaml",
		"bs4":        "beautifulsoup4",
		"dotenv":     "python-dotenv",
		"dateutil":   "python-dateutil",
		"jwt":        "pyjwt",
		"magic":      "python-magic",
		"serial":     "pyserial",
		"usb":        "pyusb",
		"wx":         "wxpython",
		"git":        "gitpython",
		"lxml":       "lxml",
		"psycopg2":   "psycopg2-binary",
		"googleapi":  "google-api-python-client",
		"google":     "google-cloud-core",
		"tensorflow": "tensorflow",
		"tf":         "tensorflow",
		"torch":      "torch",
		"np":         "numpy",
		"pd":         "pandas",
		"plt":        "matplotlib",
		"sns":        "seaborn",
	}

	if pkg, ok := mappings[importName]; ok {
		return pkg
	}
	return strings.ToLower(importName)
}

// pythonStdlib returns a set of Python standard library module names.
func pythonStdlib() map[string]bool {
	// Python 3.11 stdlib (top-level modules only)
	modules := []string{
		"abc", "aifc", "argparse", "array", "ast", "asynchat", "asyncio",
		"asyncore", "atexit", "audioop", "base64", "bdb", "binascii",
		"binhex", "bisect", "builtins", "bz2", "calendar", "cgi", "cgitb",
		"chunk", "cmath", "cmd", "code", "codecs", "codeop", "collections",
		"colorsys", "compileall", "concurrent", "configparser", "contextlib",
		"contextvars", "copy", "copyreg", "cProfile", "crypt", "csv",
		"ctypes", "curses", "dataclasses", "datetime", "dbm", "decimal",
		"difflib", "dis", "distutils", "doctest", "email", "encodings",
		"enum", "errno", "faulthandler", "fcntl", "filecmp", "fileinput",
		"fnmatch", "fractions", "ftplib", "functools", "gc", "getopt",
		"getpass", "gettext", "glob", "graphlib", "grp", "gzip", "hashlib",
		"heapq", "hmac", "html", "http", "idlelib", "imaplib", "imghdr",
		"imp", "importlib", "inspect", "io", "ipaddress", "itertools",
		"json", "keyword", "lib2to3", "linecache", "locale", "logging",
		"lzma", "mailbox", "mailcap", "marshal", "math", "mimetypes",
		"mmap", "modulefinder", "multiprocessing", "netrc", "nis",
		"nntplib", "numbers", "operator", "optparse", "os", "ossaudiodev",
		"pathlib", "pdb", "pickle", "pickletools", "pipes", "pkgutil",
		"platform", "plistlib", "poplib", "posix", "posixpath", "pprint",
		"profile", "pstats", "pty", "pwd", "py_compile", "pyclbr",
		"pydoc", "queue", "quopri", "random", "re", "readline", "reprlib",
		"resource", "rlcompleter", "runpy", "sched", "secrets", "select",
		"selectors", "shelve", "shlex", "shutil", "signal", "site",
		"smtpd", "smtplib", "sndhdr", "socket", "socketserver", "spwd",
		"sqlite3", "ssl", "stat", "statistics", "string", "stringprep",
		"struct", "subprocess", "sunau", "symtable", "sys", "sysconfig",
		"syslog", "tabnanny", "tarfile", "telnetlib", "tempfile", "termios",
		"test", "textwrap", "threading", "time", "timeit", "tkinter",
		"token", "tokenize", "tomllib", "trace", "traceback", "tracemalloc",
		"tty", "turtle", "turtledemo", "types", "typing", "unicodedata",
		"unittest", "urllib", "uu", "uuid", "venv", "warnings", "wave",
		"weakref", "webbrowser", "winreg", "winsound", "wsgiref", "xdrlib",
		"xml", "xmlrpc", "zipapp", "zipfile", "zipimport", "zlib",
		"__future__", "_thread",
	}

	stdlib := make(map[string]bool)
	for _, m := range modules {
		stdlib[m] = true
	}
	return stdlib
}
