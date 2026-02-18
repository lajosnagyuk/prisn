package layer

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Resolver analyzes project files and resolves to a Stack.
// This is the main interface for automatic layer resolution.
type Resolver struct {
	store *Store
}

// NewResolver creates a resolver backed by the given store.
func NewResolver(store *Store) *Resolver {
	return &Resolver{store: store}
}

// ResolveFromDir analyzes a directory and returns the optimal Stack.
// It looks for requirements.txt, pyproject.toml, or scans imports.
func (r *Resolver) ResolveFromDir(dir string) (*Stack, error) {
	packages, err := r.detectPackages(dir)
	if err != nil {
		return nil, err
	}

	if len(packages) == 0 {
		// No dependencies - use minimal base
		return r.store.Resolve(nil)
	}

	return r.store.Resolve(packages)
}

// ResolveFromPackages resolves from an explicit package list.
func (r *Resolver) ResolveFromPackages(packages []Package) (*Stack, error) {
	return r.store.Resolve(packages)
}

// detectPackages scans a directory for Python dependencies.
func (r *Resolver) detectPackages(dir string) ([]Package, error) {
	// Priority order:
	// 1. requirements.txt (most explicit)
	// 2. pyproject.toml (modern)
	// 3. setup.py (legacy)
	// 4. Import scanning (fallback)

	// Try requirements.txt
	reqFile := filepath.Join(dir, "requirements.txt")
	if packages, err := parseRequirementsTxt(reqFile); err == nil && len(packages) > 0 {
		return packages, nil
	}

	// Try pyproject.toml
	pyprojectFile := filepath.Join(dir, "pyproject.toml")
	if packages, err := parsePyprojectToml(pyprojectFile); err == nil && len(packages) > 0 {
		return packages, nil
	}

	// Try setup.py
	setupFile := filepath.Join(dir, "setup.py")
	if packages, err := parseSetupPy(setupFile); err == nil && len(packages) > 0 {
		return packages, nil
	}

	// Fall back to import scanning
	return scanImports(dir)
}

// parseRequirementsTxt parses a requirements.txt file.
func parseRequirementsTxt(path string) ([]Package, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var packages []Package
	scanner := bufio.NewScanner(file)

	// Pattern for package specs: name[extras]==version or just name
	specPattern := regexp.MustCompile(`^([a-zA-Z0-9_-]+)(?:\[.*\])?(?:([=<>!~]=?[^;#\s]+))?`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Skip -r, -e, and other pip options
		if strings.HasPrefix(line, "-") {
			continue
		}

		// Skip URLs and local paths
		if strings.Contains(line, "://") || strings.HasPrefix(line, ".") {
			continue
		}

		matches := specPattern.FindStringSubmatch(line)
		if len(matches) >= 2 {
			pkg := Package{Name: matches[1]}
			if len(matches) >= 3 && matches[2] != "" {
				// Extract version from specifier (e.g., ==1.0.0 -> 1.0.0)
				version := matches[2]
				if strings.HasPrefix(version, "==") {
					pkg.Version = strings.TrimPrefix(version, "==")
				}
				// For other specifiers (>=, <=, etc.), we don't pin
			}
			packages = append(packages, pkg)
		}
	}

	return packages, scanner.Err()
}

// parsePyprojectToml parses dependencies from pyproject.toml.
func parsePyprojectToml(path string) ([]Package, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	var packages []Package

	// Simple regex-based parsing for dependencies
	// Matches: dependencies = ["pkg1", "pkg2>=1.0"]
	depPattern := regexp.MustCompile(`dependencies\s*=\s*\[([\s\S]*?)\]`)
	matches := depPattern.FindStringSubmatch(content)
	if len(matches) < 2 {
		return nil, nil
	}

	// Parse individual dependencies
	pkgPattern := regexp.MustCompile(`"([a-zA-Z0-9_-]+)(?:\[.*\])?(?:[=<>!~][^"]*)?"|'([a-zA-Z0-9_-]+)(?:\[.*\])?(?:[=<>!~][^']*)?'`)
	pkgMatches := pkgPattern.FindAllStringSubmatch(matches[1], -1)

	for _, m := range pkgMatches {
		name := m[1]
		if name == "" {
			name = m[2]
		}
		if name != "" {
			packages = append(packages, Package{Name: name})
		}
	}

	return packages, nil
}

// parseSetupPy attempts to extract dependencies from setup.py.
func parseSetupPy(path string) ([]Package, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	content := string(data)
	var packages []Package

	// Look for install_requires = [...]
	pattern := regexp.MustCompile(`install_requires\s*=\s*\[([\s\S]*?)\]`)
	matches := pattern.FindStringSubmatch(content)
	if len(matches) < 2 {
		return nil, nil
	}

	// Parse package names from the list
	pkgPattern := regexp.MustCompile(`['"]([a-zA-Z0-9_-]+)(?:\[.*\])?`)
	pkgMatches := pkgPattern.FindAllStringSubmatch(matches[1], -1)

	for _, m := range pkgMatches {
		if len(m) >= 2 && m[1] != "" {
			packages = append(packages, Package{Name: m[1]})
		}
	}

	return packages, nil
}

// scanImports scans Python files for import statements.
func scanImports(dir string) ([]Package, error) {
	imports := make(map[string]bool)

	// Walk Python files
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Skip hidden directories and common non-source dirs
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "__pycache__" ||
				name == "venv" || name == ".venv" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only process .py files
		if !strings.HasSuffix(path, ".py") {
			return nil
		}

		fileImports, _ := extractImports(path)
		for _, imp := range fileImports {
			imports[imp] = true
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// Filter to only external packages (not stdlib, not local)
	var packages []Package
	for imp := range imports {
		if isExternalPackage(imp) {
			packages = append(packages, Package{Name: imp})
		}
	}

	return packages, nil
}

// extractImports extracts import names from a Python file.
func extractImports(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var imports []string
	scanner := bufio.NewScanner(file)

	importPattern := regexp.MustCompile(`^(?:import|from)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		matches := importPattern.FindStringSubmatch(line)
		if len(matches) >= 2 {
			imports = append(imports, matches[1])
		}
	}

	return imports, scanner.Err()
}

// isExternalPackage checks if a package name is likely external (not stdlib).
func isExternalPackage(name string) bool {
	// Common stdlib modules to exclude
	stdlib := map[string]bool{
		"abc": true, "argparse": true, "array": true, "ast": true,
		"asyncio": true, "base64": true, "binascii": true, "builtins": true,
		"calendar": true, "cgi": true, "cgitb": true, "chunk": true,
		"cmath": true, "cmd": true, "code": true, "codecs": true,
		"collections": true, "colorsys": true, "compileall": true,
		"concurrent": true, "configparser": true, "contextlib": true,
		"contextvars": true, "copy": true, "copyreg": true, "cProfile": true,
		"csv": true, "ctypes": true, "curses": true, "dataclasses": true,
		"datetime": true, "dbm": true, "decimal": true, "difflib": true,
		"dis": true, "distutils": true, "doctest": true, "email": true,
		"encodings": true, "enum": true, "errno": true, "faulthandler": true,
		"fcntl": true, "filecmp": true, "fileinput": true, "fnmatch": true,
		"fractions": true, "ftplib": true, "functools": true, "gc": true,
		"getopt": true, "getpass": true, "gettext": true, "glob": true,
		"graphlib": true, "grp": true, "gzip": true, "hashlib": true,
		"heapq": true, "hmac": true, "html": true, "http": true,
		"imaplib": true, "imghdr": true, "imp": true, "importlib": true,
		"inspect": true, "io": true, "ipaddress": true, "itertools": true,
		"json": true, "keyword": true, "lib2to3": true, "linecache": true,
		"locale": true, "logging": true, "lzma": true, "mailbox": true,
		"mailcap": true, "marshal": true, "math": true, "mimetypes": true,
		"mmap": true, "modulefinder": true, "multiprocessing": true,
		"netrc": true, "nis": true, "nntplib": true, "numbers": true,
		"operator": true, "optparse": true, "os": true, "pathlib": true,
		"pdb": true, "pickle": true, "pickletools": true, "pipes": true,
		"pkgutil": true, "platform": true, "plistlib": true, "poplib": true,
		"posix": true, "posixpath": true, "pprint": true, "profile": true,
		"pstats": true, "pty": true, "pwd": true, "py_compile": true,
		"pyclbr": true, "pydoc": true, "queue": true, "quopri": true,
		"random": true, "re": true, "readline": true, "reprlib": true,
		"resource": true, "rlcompleter": true, "runpy": true, "sched": true,
		"secrets": true, "select": true, "selectors": true, "shelve": true,
		"shlex": true, "shutil": true, "signal": true, "site": true,
		"smtpd": true, "smtplib": true, "sndhdr": true, "socket": true,
		"socketserver": true, "spwd": true, "sqlite3": true, "ssl": true,
		"stat": true, "statistics": true, "string": true, "stringprep": true,
		"struct": true, "subprocess": true, "sunau": true, "symtable": true,
		"sys": true, "sysconfig": true, "syslog": true, "tabnanny": true,
		"tarfile": true, "telnetlib": true, "tempfile": true, "termios": true,
		"test": true, "textwrap": true, "threading": true, "time": true,
		"timeit": true, "tkinter": true, "token": true, "tokenize": true,
		"tomllib": true, "trace": true, "traceback": true, "tracemalloc": true,
		"tty": true, "turtle": true, "turtledemo": true, "types": true,
		"typing": true, "unicodedata": true, "unittest": true, "urllib": true,
		"uu": true, "uuid": true, "venv": true, "warnings": true,
		"wave": true, "weakref": true, "webbrowser": true, "winreg": true,
		"winsound": true, "wsgiref": true, "xdrlib": true, "xml": true,
		"xmlrpc": true, "zipapp": true, "zipfile": true, "zipimport": true,
		"zlib": true, "zoneinfo": true,
		// Also skip private/local modules
		"_": true, "__": true,
	}

	return !stdlib[name] && !strings.HasPrefix(name, "_")
}

// mapImportToPackage maps import names to pip package names.
// Some packages have different import vs pip names.
var importToPackageMap = map[string]string{
	"cv2":            "opencv-python",
	"PIL":            "Pillow",
	"sklearn":        "scikit-learn",
	"yaml":           "pyyaml",
	"bs4":            "beautifulsoup4",
	"dateutil":       "python-dateutil",
	"dotenv":         "python-dotenv",
	"jwt":            "pyjwt",
	"magic":          "python-magic",
	"serial":         "pyserial",
	"usb":            "pyusb",
	"Crypto":         "pycryptodome",
	"OpenSSL":        "pyopenssl",
	"googleapiclient": "google-api-python-client",
}

// NormalizeImport converts an import name to a pip package name.
func NormalizeImport(importName string) string {
	if pkg, ok := importToPackageMap[importName]; ok {
		return pkg
	}
	return importName
}
