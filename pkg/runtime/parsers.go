package runtime

import (
	"bufio"
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

// RequirementsTxtParser parses Python requirements.txt files.
type RequirementsTxtParser struct{}

func (p *RequirementsTxtParser) Name() string { return "requirements.txt" }

func (p *RequirementsTxtParser) Parse(path string, rt *Runtime) ([]Dependency, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var deps []Dependency
	scanner := bufio.NewScanner(f)

	// Regex for requirements.txt lines
	reqRe := regexp.MustCompile(`^([a-zA-Z0-9_-]+)\s*(.*?)$`)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Skip include/editable directives
		if strings.HasPrefix(line, "-r") || strings.HasPrefix(line, "-e") ||
			strings.HasPrefix(line, "-c") || strings.HasPrefix(line, "--") {
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

// PyprojectParser parses Python pyproject.toml files.
type PyprojectParser struct{}

func (p *PyprojectParser) Name() string { return "pyproject.toml" }

func (p *PyprojectParser) Parse(path string, rt *Runtime) ([]Dependency, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var deps []Dependency
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

		// Parse dependencies
		if inDependencies && strings.Contains(line, "=") {
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

	return deps, nil
}

// PackageJSONParser parses Node.js package.json files.
type PackageJSONParser struct{}

func (p *PackageJSONParser) Name() string { return "package.json" }

func (p *PackageJSONParser) Parse(path string, rt *Runtime) ([]Dependency, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}

	if err := json.Unmarshal(content, &pkg); err != nil {
		// Fall back to regex parsing
		return p.parseWithRegex(string(content))
	}

	var deps []Dependency
	for name, version := range pkg.Dependencies {
		deps = append(deps, Dependency{Name: name, Version: version})
	}

	return deps, nil
}

func (p *PackageJSONParser) parseWithRegex(content string) ([]Dependency, error) {
	var deps []Dependency

	// Find dependencies block
	depRe := regexp.MustCompile(`"dependencies"\s*:\s*\{([^}]+)\}`)
	matches := depRe.FindStringSubmatch(content)
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

// RegexParser uses a configurable regex to parse dependencies.
type RegexParser struct {
	Pattern string
}

func (p *RegexParser) Name() string { return "regex" }

func (p *RegexParser) Parse(path string, rt *Runtime) ([]Dependency, error) {
	if p.Pattern == "" {
		// Default pattern: name version (one per line)
		p.Pattern = `^(\S+)\s+(\S+)?`
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	re := regexp.MustCompile(p.Pattern)
	var deps []Dependency

	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		matches := re.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		dep := Dependency{Name: matches[1]}
		if len(matches) > 2 {
			dep.Version = matches[2]
		}
		deps = append(deps, dep)
	}

	return deps, nil
}

// ImportScanner scans source files for import statements.
type ImportScanner struct {
	runtime *Runtime
}

// NewImportScanner creates an import scanner for a runtime.
func NewImportScanner(rt *Runtime) *ImportScanner {
	return &ImportScanner{runtime: rt}
}

// Scan scans a source file for imports and returns dependencies.
func (s *ImportScanner) Scan(path string) ([]Dependency, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	switch s.runtime.ID {
	case "python3", "python":
		return s.scanPython(string(content))
	case "node":
		return s.scanNode(string(content))
	default:
		// No import scanning for this runtime
		return nil, nil
	}
}

func (s *ImportScanner) scanPython(content string) ([]Dependency, error) {
	// Regex for import statements
	importRe := regexp.MustCompile(`(?m)^(?:from\s+(\w+)|import\s+(\w+))`)

	matches := importRe.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var deps []Dependency

	// Build stdlib set
	stdlib := make(map[string]bool)
	for _, m := range s.runtime.Deps.StdlibModules {
		stdlib[m] = true
	}

	for _, match := range matches {
		var module string
		if match[1] != "" {
			module = match[1]
		} else {
			module = match[2]
		}

		// Skip stdlib
		if stdlib[module] {
			continue
		}

		// Skip already seen
		if seen[module] {
			continue
		}
		seen[module] = true

		// Map import name to package name
		pkgName := module
		if mapped, ok := s.runtime.Deps.ImportToPackage[module]; ok {
			pkgName = mapped
		}
		deps = append(deps, Dependency{Name: strings.ToLower(pkgName)})
	}

	return deps, nil
}

func (s *ImportScanner) scanNode(content string) ([]Dependency, error) {
	requireRe := regexp.MustCompile(`require\s*\(\s*['"]([^'"./][^'"]+)['"]\s*\)`)
	importRe := regexp.MustCompile(`import\s+(?:[\w\s{},*]+\s+from\s+)?['"]([^'"./][^'"]+)['"]`)

	seen := make(map[string]bool)
	var deps []Dependency

	// Build stdlib set
	stdlib := make(map[string]bool)
	for _, m := range s.runtime.Deps.StdlibModules {
		stdlib[m] = true
	}

	for _, re := range []*regexp.Regexp{requireRe, importRe} {
		for _, match := range re.FindAllStringSubmatch(content, -1) {
			pkg := match[1]

			// Get root package name
			if strings.HasPrefix(pkg, "@") {
				parts := strings.SplitN(pkg, "/", 3)
				if len(parts) >= 2 {
					pkg = parts[0] + "/" + parts[1]
				}
			} else {
				pkg = strings.Split(pkg, "/")[0]
			}

			if stdlib[pkg] || seen[pkg] {
				continue
			}
			seen[pkg] = true
			deps = append(deps, Dependency{Name: pkg})
		}
	}

	return deps, nil
}

// extractVersion extracts version from various formats.
func extractVersion(s string) string {
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
	return re.FindString(s)
}
