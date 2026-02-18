package manifest

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Hints captures what we learned from analyzing the code.
// These are suggestions, not requirements.
type Hints struct {
	// Resource suggestions
	MemoryMB  int    `toml:"memory_mb" json:"memory_mb"`   // Suggested memory
	CPUCores  float64 `toml:"cpu_cores" json:"cpu_cores"`  // Suggested CPU

	// Architecture
	Arch      string `toml:"arch" json:"arch"`             // "any", "amd64", "arm64", "amd64-preferred"
	NeedsGPU  bool   `toml:"gpu" json:"gpu"`               // Needs GPU access

	// What we found
	HeavyLibs []string `toml:"heavy_libs" json:"heavy_libs"` // numpy, torch, etc.
	HasNative bool     `toml:"has_native" json:"has_native"` // C extensions detected

	// Confidence
	Confidence string `toml:"confidence" json:"confidence"` // "high", "medium", "low"
	Notes      []string `toml:"notes" json:"notes"`         // Human-readable notes
}

// Known heavy libraries and their resource implications
var heavyLibs = map[string]struct {
	memoryMB int
	needsGPU bool
	note     string
}{
	// ML/AI - the big ones
	"torch":        {4096, true, "PyTorch detected - needs GPU or lots of CPU"},
	"tensorflow":   {4096, true, "TensorFlow detected - needs GPU or lots of CPU"},
	"transformers": {8192, true, "Hugging Face detected - LLMs need serious memory"},
	"keras":        {2048, true, "Keras detected"},

	// Data science
	"numpy":        {512, false, "NumPy detected - numeric computing"},
	"pandas":       {1024, false, "Pandas detected - dataframes eat memory"},
	"scipy":        {512, false, "SciPy detected"},
	"sklearn":      {1024, false, "scikit-learn detected"},
	"polars":       {1024, false, "Polars detected - fast dataframes"},

	// Image/video
	"cv2":          {1024, false, "OpenCV detected - image processing"},
	"PIL":          {512, false, "Pillow detected - image handling"},
	"moviepy":      {2048, false, "MoviePy detected - video processing"},
	"ffmpeg":       {1024, false, "FFmpeg bindings detected"},

	// Web scraping
	"selenium":     {1024, false, "Selenium detected - browser automation"},
	"playwright":   {1024, false, "Playwright detected - browser automation"},
	"puppeteer":    {1024, false, "Puppeteer detected - browser automation"},

	// Databases (connection pools)
	"sqlalchemy":   {256, false, "SQLAlchemy detected"},
	"psycopg2":     {128, false, "PostgreSQL driver detected"},
	"pymongo":      {128, false, "MongoDB driver detected"},
}

// Libraries that suggest architecture-specific code
var nativeLibs = map[string]bool{
	"numpy": true, "scipy": true, "pandas": true,
	"torch": true, "tensorflow": true,
	"cv2": true, "PIL": true,
	"cryptography": true, "bcrypt": true,
	"lxml": true, "psycopg2": true,
	"grpcio": true, "protobuf": true,
}

// Analyze scans a project and returns resource hints.
func Analyze(dir string) (*Hints, error) {
	hints := &Hints{
		MemoryMB:   256, // baseline
		CPUCores:   0.5, // baseline
		Arch:       "any",
		Confidence: "low",
	}

	// Find Python files
	pyFiles, err := filepath.Glob(filepath.Join(dir, "*.py"))
	if err != nil {
		return hints, nil
	}

	// Also check subdirectories (one level)
	subdirs, _ := filepath.Glob(filepath.Join(dir, "*", "*.py"))
	pyFiles = append(pyFiles, subdirs...)

	if len(pyFiles) == 0 {
		hints.Notes = append(hints.Notes, "no Python files found, using defaults")
		return hints, nil
	}

	// Scan for imports
	imports := scanPythonImports(pyFiles)

	// Check requirements.txt too
	reqPath := filepath.Join(dir, "requirements.txt")
	if reqs, err := parseRequirements(reqPath); err == nil {
		for _, req := range reqs {
			imports[req] = true
		}
	}

	// Analyze imports
	var totalMemory int
	var foundHeavy []string
	hasNative := false
	needsGPU := false

	for imp := range imports {
		// Check against known heavy libs
		if info, ok := heavyLibs[imp]; ok {
			foundHeavy = append(foundHeavy, imp)
			if info.memoryMB > totalMemory {
				totalMemory = info.memoryMB
			}
			if info.needsGPU {
				needsGPU = true
			}
			hints.Notes = append(hints.Notes, info.note)
		}

		// Check for native extensions
		if nativeLibs[imp] {
			hasNative = true
		}
	}

	// Set resource suggestions
	if totalMemory > 0 {
		hints.MemoryMB = totalMemory
		hints.Confidence = "medium"
	}

	if len(foundHeavy) >= 3 {
		hints.Confidence = "high"
	}

	hints.HeavyLibs = foundHeavy
	hints.HasNative = hasNative
	hints.NeedsGPU = needsGPU

	// Architecture decision
	if hasNative {
		hints.Arch = "amd64-preferred"
		hints.Notes = append(hints.Notes, "native extensions detected - amd64 preferred but arm64 usually works")
	}

	if needsGPU {
		hints.Arch = "amd64" // GPU support is better on amd64
		hints.Notes = append(hints.Notes, "GPU libraries detected - amd64 recommended")
	}

	// CPU scaling based on workload
	if needsGPU || totalMemory >= 2048 {
		hints.CPUCores = 2.0
	} else if totalMemory >= 512 {
		hints.CPUCores = 1.0
	}

	return hints, nil
}

// scanPythonImports extracts import statements from Python files.
func scanPythonImports(files []string) map[string]bool {
	imports := make(map[string]bool)

	// Match: import foo, from foo import bar
	importRe := regexp.MustCompile(`^(?:import|from)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)

	for _, file := range files {
		f, err := os.Open(file)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if matches := importRe.FindStringSubmatch(line); matches != nil {
				imports[matches[1]] = true
			}
		}
		f.Close()
	}

	return imports
}

// parseRequirements extracts package names from requirements.txt.
func parseRequirements(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var packages []string
	// Match: package, package==1.0, package>=1.0, package[extra]
	pkgRe := regexp.MustCompile(`^([a-zA-Z0-9_-]+)`)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		if matches := pkgRe.FindStringSubmatch(line); matches != nil {
			// Normalize: sklearn -> scikit-learn uses "sklearn" as import
			pkg := strings.ToLower(matches[1])
			pkg = strings.ReplaceAll(pkg, "-", "_")
			packages = append(packages, pkg)
		}
	}

	return packages, nil
}

// SuggestResources returns a human-readable summary of suggestions.
func (h *Hints) Summary() string {
	var sb strings.Builder

	sb.WriteString("Resource analysis:\n")

	if len(h.HeavyLibs) > 0 {
		sb.WriteString("  detected:  " + strings.Join(h.HeavyLibs, ", ") + "\n")
	}

	sb.WriteString("  memory:    " + formatMemory(h.MemoryMB) + "\n")
	sb.WriteString("  cpu:       " + formatCPU(h.CPUCores) + "\n")
	sb.WriteString("  arch:      " + h.Arch + "\n")

	if h.NeedsGPU {
		sb.WriteString("  gpu:       recommended\n")
	}

	sb.WriteString("  confidence: " + h.Confidence + "\n")

	return sb.String()
}

func formatMemory(mb int) string {
	if mb >= 1024 {
		gb := float64(mb) / 1024
		if gb == float64(int(gb)) {
			return fmt.Sprintf("%dGB", int(gb))
		}
		return fmt.Sprintf("%.1fGB", gb)
	}
	return fmt.Sprintf("%dMB", mb)
}

func formatCPU(cores float64) string {
	if cores == float64(int(cores)) {
		if cores == 1 {
			return "1 core"
		}
		return fmt.Sprintf("%d cores", int(cores))
	}
	return fmt.Sprintf("%.1f cores", cores)
}
