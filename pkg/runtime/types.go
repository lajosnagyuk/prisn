// Package runtime provides a pluggable system for language runtimes.
//
// Any interpreter can be added by defining:
// - How to invoke it
// - How to detect/install it
// - How to find dependencies (optional)
// - How to install dependencies (optional)
//
// Example runtime definition (TOML):
//
//	[runtime.babashka]
//	name = "Babashka"
//	extensions = [".bb", ".clj"]
//	command = "bb"
//	args = ["{script}"]
//	env = { "BABASHKA_PRELOADS" = "(set! *warn-on-reflection* true)" }
//
//	[runtime.babashka.detect]
//	check = "bb --version"
//
//	[runtime.babashka.install]
//	macos = "brew install borkdude/brew/babashka"
//	linux_amd64 = "curl -sL https://raw.githubusercontent.com/babashka/babashka/master/install | bash"
package runtime

import (
	"context"
	"io"
)

// Runtime defines how to use a language interpreter.
type Runtime struct {
	// ID is the unique identifier (e.g., "python3", "babashka")
	ID string `toml:"id" json:"id"`

	// Name is the human-readable name (e.g., "Python 3", "Babashka")
	Name string `toml:"name" json:"name"`

	// Extensions are file extensions this runtime handles (e.g., [".py", ".pyw"])
	Extensions []string `toml:"extensions" json:"extensions"`

	// Command is the interpreter command (e.g., "python3", "bb", "/usr/local/bin/php")
	Command string `toml:"command" json:"command"`

	// Args are arguments to pass before the script. Use {script} as placeholder.
	// Example: ["-u", "{script}"] for Python unbuffered
	// Example: ["--strict", "{script}"] for PHP
	Args []string `toml:"args" json:"args"`

	// Env are environment variables to set when running
	Env map[string]string `toml:"env" json:"env"`

	// Detection configures how to detect if the runtime is installed
	Detect DetectConfig `toml:"detect" json:"detect"`

	// Install configures how to install the runtime
	Install InstallConfig `toml:"install" json:"install"`

	// Deps configures dependency detection and installation
	Deps DepsConfig `toml:"deps" json:"deps"`

	// Shebang patterns that indicate this runtime (e.g., "#!/usr/bin/env python")
	ShebangPatterns []string `toml:"shebang_patterns" json:"shebang_patterns"`

	// Builtin indicates this is a built-in runtime (not user-defined)
	Builtin bool `toml:"-" json:"-"`
}

// DetectConfig configures runtime detection.
type DetectConfig struct {
	// Check is a command to run to verify the runtime is installed
	// Example: "python3 --version"
	Check string `toml:"check" json:"check"`

	// VersionRegex extracts version from check output
	// Example: `Python (\d+\.\d+\.\d+)`
	VersionRegex string `toml:"version_regex" json:"version_regex"`

	// MinVersion is the minimum required version
	MinVersion string `toml:"min_version" json:"min_version"`
}

// InstallConfig configures runtime installation.
type InstallConfig struct {
	// Script is a URL to an install script
	Script string `toml:"script" json:"script"`

	// Commands are platform-specific install commands
	// Keys: "macos", "linux", "linux_amd64", "linux_arm64", "windows"
	Commands map[string]string `toml:"commands" json:"commands"`

	// DocURL is documentation for manual installation
	DocURL string `toml:"doc_url" json:"doc_url"`

	// Note is a human-readable installation note
	Note string `toml:"note" json:"note"`
}

// DepsConfig configures dependency detection and installation.
type DepsConfig struct {
	// Enabled indicates whether this runtime has dependency management
	Enabled bool `toml:"enabled" json:"enabled"`

	// ManifestFiles are files to check for dependencies
	// Example: ["requirements.txt", "pyproject.toml"] for Python
	// Example: ["bb.edn", "deps.edn"] for Clojure
	ManifestFiles []string `toml:"manifest_files" json:"manifest_files"`

	// Parser is the parser type for manifest files
	// Built-in: "requirements.txt", "pyproject.toml", "package.json", "edn", "json", "regex"
	// Or: "command" to run a custom command
	Parser string `toml:"parser" json:"parser"`

	// ParserCommand is used when Parser="command"
	// Should output JSON: {"deps": [{"name": "foo", "version": "1.0"}]}
	ParserCommand string `toml:"parser_command" json:"parser_command"`

	// InstallCommand is the command to install dependencies
	// Use {manifest} for manifest file, {deps} for space-separated dep list
	// Example: "pip install -r {manifest}"
	// Example: "npm install {deps}"
	InstallCommand string `toml:"install_command" json:"install_command"`

	// SelfManaged indicates the runtime handles its own deps (like Babashka)
	SelfManaged bool `toml:"self_managed" json:"self_managed"`

	// EnvType is the type of isolated environment to create
	// "venv" for Python, "node_modules" for Node, "none" for self-managed
	EnvType string `toml:"env_type" json:"env_type"`

	// StdlibModules are modules to ignore during import scanning
	StdlibModules []string `toml:"stdlib_modules" json:"stdlib_modules"`

	// ImportToPackage maps import names to package names
	// Example: {"cv2": "opencv-python", "PIL": "pillow"}
	ImportToPackage map[string]string `toml:"import_to_package" json:"import_to_package"`
}

// Dependency represents a single package requirement.
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// String returns the dependency in install format.
func (d Dependency) String() string {
	if d.Version == "" {
		return d.Name
	}
	// If version starts with operator, use as-is
	if len(d.Version) > 0 {
		switch d.Version[0] {
		case '>', '<', '=', '~', '^', '!':
			return d.Name + d.Version
		}
	}
	return d.Name + "==" + d.Version
}

// Manifest represents detected dependencies for a script.
type Manifest struct {
	// Runtime is the runtime ID
	Runtime string `json:"runtime"`

	// Version is the specific version (if detected)
	Version string `json:"version,omitempty"`

	// Dependencies are the required packages
	Dependencies []Dependency `json:"dependencies"`

	// Source is where dependencies were detected from
	Source string `json:"source"`

	// Hash is a cache key based on dependencies
	Hash string `json:"hash"`
}

// Environment represents a prepared execution environment.
type Environment struct {
	// Path is the environment directory (venv, node_modules parent, etc.)
	Path string

	// BinDir is the directory containing executables
	BinDir string

	// InterpreterPath is the full path to the interpreter
	InterpreterPath string

	// EnvVars are additional environment variables to set
	EnvVars map[string]string

	// Cached indicates this was a cache hit
	Cached bool
}

// ExecuteConfig specifies how to execute a script.
type ExecuteConfig struct {
	Script  string
	Args    []string
	Env     map[string]string
	WorkDir string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// Executor can execute scripts using a runtime.
type Executor interface {
	// Execute runs a script with the given configuration.
	Execute(ctx context.Context, rt *Runtime, env *Environment, cfg ExecuteConfig) error
}

// Parser can parse dependency manifests.
type Parser interface {
	// Parse reads a manifest file and returns dependencies.
	Parse(path string, rt *Runtime) ([]Dependency, error)

	// Name returns the parser name.
	Name() string
}
