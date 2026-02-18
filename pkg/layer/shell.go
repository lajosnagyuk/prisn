package layer

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// ShellDependency represents a tool required by a shell script.
type ShellDependency struct {
	Name        string   // Tool name (e.g., "jq")
	Required    bool     // Whether the script will fail without it
	Lines       []int    // Line numbers where it's used
	InstallHint string   // How to install it
}

// ShellValidation contains the results of shell script analysis.
type ShellValidation struct {
	Script       string
	Shell        string             // bash, zsh, fish, sh
	Dependencies []ShellDependency
	Missing      []ShellDependency  // Dependencies not found on system
	Warnings     []string
}

// ShellValidator analyzes shell scripts for dependencies.
type ShellValidator struct{}

// NewShellValidator creates a shell script validator.
func NewShellValidator() *ShellValidator {
	return &ShellValidator{}
}

// Validate analyzes a shell script and checks for missing dependencies.
func (v *ShellValidator) Validate(scriptPath string) (*ShellValidation, error) {
	file, err := os.Open(scriptPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	result := &ShellValidation{
		Script: scriptPath,
	}

	// Detect shell from shebang
	scanner := bufio.NewScanner(file)
	lineNum := 0
	toolUsage := make(map[string][]int)

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// First line: detect shell
		if lineNum == 1 {
			result.Shell = detectShell(line)
			continue
		}

		// Skip comments and empty lines
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Extract tool usage from this line
		tools := extractToolsFromLine(trimmed)
		for _, tool := range tools {
			toolUsage[tool] = append(toolUsage[tool], lineNum)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Build dependency list
	for tool, lines := range toolUsage {
		dep := ShellDependency{
			Name:        tool,
			Required:    true,
			Lines:       lines,
			InstallHint: getInstallHint(tool),
		}
		result.Dependencies = append(result.Dependencies, dep)

		// Check if tool exists
		if !toolExists(tool) {
			result.Missing = append(result.Missing, dep)
		}
	}

	// Sort for consistent output
	sort.Slice(result.Dependencies, func(i, j int) bool {
		return result.Dependencies[i].Name < result.Dependencies[j].Name
	})
	sort.Slice(result.Missing, func(i, j int) bool {
		return result.Missing[i].Name < result.Missing[j].Name
	})

	return result, nil
}

// detectShell determines the shell from the shebang line.
func detectShell(line string) string {
	if !strings.HasPrefix(line, "#!") {
		return "sh" // Default
	}

	shebang := strings.TrimPrefix(line, "#!")
	shebang = strings.TrimSpace(shebang)

	// Handle /usr/bin/env bash style
	if strings.Contains(shebang, "env ") {
		parts := strings.Fields(shebang)
		if len(parts) >= 2 {
			return filepath.Base(parts[len(parts)-1])
		}
	}

	// Direct path like /bin/bash
	return filepath.Base(shebang)
}

// extractToolsFromLine extracts external tool names from a shell line.
func extractToolsFromLine(line string) []string {
	var tools []string
	seen := make(map[string]bool)

	// Remove string literals to avoid false positives
	line = removeStringLiterals(line)

	// Split by common shell operators
	parts := splitShellLine(line)

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Get the command (first word)
		words := strings.Fields(part)
		if len(words) == 0 {
			continue
		}

		cmd := words[0]

		// Skip shell builtins and syntax
		if isShellBuiltin(cmd) || isShellSyntax(cmd) {
			continue
		}

		// Skip variable assignments
		if strings.Contains(cmd, "=") {
			continue
		}

		// Skip if starts with $ (variable)
		if strings.HasPrefix(cmd, "$") {
			continue
		}

		// Skip paths that look like scripts being sourced
		if strings.HasPrefix(cmd, "./") || strings.HasPrefix(cmd, "/") {
			continue
		}

		// Check if it's a known external tool
		if isKnownTool(cmd) && !seen[cmd] {
			tools = append(tools, cmd)
			seen[cmd] = true
		}
	}

	return tools
}

// removeStringLiterals removes quoted strings from a line.
func removeStringLiterals(line string) string {
	// Simple removal of double and single quoted strings
	result := regexp.MustCompile(`"[^"]*"`).ReplaceAllString(line, "")
	result = regexp.MustCompile(`'[^']*'`).ReplaceAllString(result, "")
	return result
}

// splitShellLine splits a line by shell operators.
func splitShellLine(line string) []string {
	// Split by pipes, semicolons, &&, ||, $()
	re := regexp.MustCompile(`[|;&]|\$\(|\)|\|\||&&`)
	return re.Split(line, -1)
}

// isShellBuiltin checks if a command is a shell builtin.
func isShellBuiltin(cmd string) bool {
	builtins := map[string]bool{
		// POSIX builtins
		"break": true, "colon": true, "continue": true, "dot": true,
		"eval": true, "exec": true, "exit": true, "export": true,
		"readonly": true, "return": true, "set": true, "shift": true,
		"times": true, "trap": true, "unset": true,
		// Bash builtins
		"alias": true, "bg": true, "bind": true, "builtin": true,
		"caller": true, "cd": true, "command": true, "compgen": true,
		"complete": true, "compopt": true, "declare": true, "dirs": true,
		"disown": true, "echo": true, "enable": true, "fc": true,
		"fg": true, "getopts": true, "hash": true, "help": true,
		"history": true, "jobs": true, "kill": true, "let": true,
		"local": true, "logout": true, "mapfile": true, "popd": true,
		"printf": true, "pushd": true, "pwd": true, "read": true,
		"readarray": true, "source": true, "suspend": true, "test": true,
		"type": true, "typeset": true, "ulimit": true, "umask": true,
		"unalias": true, "wait": true,
		// Zsh additions
		"autoload": true, "bindkey": true, "chdir": true, "compctl": true,
		"emulate": true, "functions": true, "getln": true, "limit": true,
		"print": true, "sched": true, "setopt": true, "stat": true,
		"unhash": true, "unlimit": true, "unsetopt": true, "vared": true,
		"whence": true, "where": true, "which": true, "zcompile": true,
		"zle": true, "zmodload": true, "zparseopts": true, "zstyle": true,
		// Fish builtins
		"abbr": true, "and": true, "argparse": true, "begin": true,
		"block": true, "breakpoint": true, "case": true, "commandline": true,
		"contains": true, "count": true, "else": true, "emit": true,
		"end": true, "false": true, "for": true, "function": true,
		"if": true, "math": true, "not": true, "or": true,
		"random": true, "realpath": true, "set_color": true, "status": true,
		"string": true, "switch": true, "true": true, "while": true,
	}
	return builtins[cmd]
}

// isShellSyntax checks if a string is shell syntax rather than a command.
func isShellSyntax(s string) bool {
	syntax := map[string]bool{
		"then": true, "fi": true, "else": true, "elif": true,
		"do": true, "done": true, "case": true, "esac": true,
		"in": true, "for": true, "while": true, "until": true,
		"if": true, "[[": true, "]]": true, "{": true, "}": true,
		"!": true,
	}
	return syntax[s]
}

// isKnownTool checks if a command is a known external tool.
func isKnownTool(cmd string) bool {
	// Common external tools that scripts depend on
	tools := map[string]bool{
		// Data processing
		"jq": true, "yq": true, "xq": true, "gron": true,
		"awk": true, "sed": true, "grep": true, "egrep": true, "fgrep": true,
		"cut": true, "sort": true, "uniq": true, "wc": true,
		"head": true, "tail": true, "tr": true, "column": true,
		"paste": true, "join": true, "comm": true, "diff": true,
		"patch": true,
		// File operations
		"find": true, "xargs": true, "locate": true, "updatedb": true,
		"ls": true, "cp": true, "mv": true, "rm": true, "mkdir": true,
		"rmdir": true, "touch": true, "cat": true, "less": true,
		"more": true, "file": true, "stat": true, "ln": true,
		"chmod": true, "chown": true, "chgrp": true,
		// Compression
		"tar": true, "gzip": true, "gunzip": true, "bzip2": true,
		"bunzip2": true, "xz": true, "unxz": true, "zip": true,
		"unzip": true, "zcat": true, "zless": true,
		// Network
		"curl": true, "wget": true, "httpie": true, "http": true,
		"ssh": true, "scp": true, "rsync": true, "sftp": true,
		"nc": true, "netcat": true, "ncat": true, "socat": true,
		"ping": true, "traceroute": true, "dig": true, "nslookup": true,
		"host": true, "whois": true, "ip": true, "ifconfig": true,
		"netstat": true, "ss": true, "lsof": true,
		// Cloud CLIs
		"aws": true, "gcloud": true, "az": true, "doctl": true,
		"kubectl": true, "helm": true, "terraform": true, "pulumi": true,
		"docker": true, "podman": true, "docker-compose": true,
		// Development
		"git": true, "gh": true, "hub": true, "make": true,
		"cmake": true, "ninja": true, "gcc": true, "clang": true,
		"go": true, "python": true, "python3": true, "pip": true,
		"node": true, "npm": true, "yarn": true, "pnpm": true,
		"ruby": true, "gem": true, "bundler": true,
		"java": true, "javac": true, "mvn": true, "gradle": true,
		"rustc": true, "cargo": true,
		// System
		"sudo": true, "su": true, "doas": true,
		"systemctl": true, "service": true, "journalctl": true,
		"ps": true, "top": true, "htop": true, "kill": true, "pkill": true,
		"killall": true, "pgrep": true, "nohup": true, "timeout": true,
		"watch": true, "time": true, "date": true, "cal": true,
		"env": true, "printenv": true, "id": true, "whoami": true,
		"hostname": true, "uname": true, "uptime": true, "free": true,
		"df": true, "du": true, "mount": true, "umount": true,
		// Text editors (when used in scripts, usually for here-docs)
		"tee": true, "xclip": true, "pbcopy": true, "pbpaste": true,
		// Misc utilities
		"base64": true, "md5sum": true, "sha256sum": true, "shasum": true,
		"openssl": true, "gpg": true, "age": true, "sops": true,
		"envsubst": true, "gettext": true, "iconv": true,
		"realpath": true, "dirname": true, "basename": true,
		"mktemp": true, "seq": true, "shuf": true, "yes": true,
		"parallel": true,
		// macOS specific
		"brew": true, "open": true, "say": true, "osascript": true,
		"defaults": true, "plutil": true, "xcrun": true, "xcode-select": true,
		// Linux specific
		"apt": true, "apt-get": true, "dpkg": true,
		"yum": true, "dnf": true, "rpm": true,
		"pacman": true, "apk": true, "zypper": true,
		"snap": true, "flatpak": true,
	}
	return tools[cmd]
}

// toolExists checks if a tool is available on the system.
func toolExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// getInstallHint returns installation instructions for a tool.
func getInstallHint(tool string) string {
	// Platform-specific hints
	hints := map[string]map[string]string{
		"jq": {
			"darwin": "brew install jq",
			"linux":  "apt install jq  # or: yum install jq",
			"common": "https://stedolan.github.io/jq/download/",
		},
		"yq": {
			"darwin": "brew install yq",
			"linux":  "snap install yq  # or: pip install yq",
			"common": "https://github.com/mikefarah/yq",
		},
		"curl": {
			"darwin": "brew install curl  # usually pre-installed",
			"linux":  "apt install curl",
			"common": "https://curl.se/download.html",
		},
		"wget": {
			"darwin": "brew install wget",
			"linux":  "apt install wget",
			"common": "https://www.gnu.org/software/wget/",
		},
		"aws": {
			"common": "pip install awscli  # or: brew install awscli",
		},
		"kubectl": {
			"darwin": "brew install kubectl",
			"linux":  "snap install kubectl --classic",
			"common": "https://kubernetes.io/docs/tasks/tools/",
		},
		"docker": {
			"darwin": "brew install --cask docker",
			"linux":  "apt install docker.io  # or: https://docs.docker.com/engine/install/",
			"common": "https://docs.docker.com/get-docker/",
		},
		"git": {
			"darwin": "xcode-select --install  # or: brew install git",
			"linux":  "apt install git",
			"common": "https://git-scm.com/downloads",
		},
		"gh": {
			"darwin": "brew install gh",
			"linux":  "apt install gh  # or: https://cli.github.com/",
			"common": "https://cli.github.com/",
		},
		"terraform": {
			"darwin": "brew install terraform",
			"linux":  "apt install terraform  # or: tfenv",
			"common": "https://www.terraform.io/downloads",
		},
		"helm": {
			"darwin": "brew install helm",
			"linux":  "snap install helm --classic",
			"common": "https://helm.sh/docs/intro/install/",
		},
		"parallel": {
			"darwin": "brew install parallel",
			"linux":  "apt install parallel",
			"common": "https://www.gnu.org/software/parallel/",
		},
		"ripgrep": {
			"darwin": "brew install ripgrep",
			"linux":  "apt install ripgrep",
			"common": "https://github.com/BurntSushi/ripgrep",
		},
		"fd": {
			"darwin": "brew install fd",
			"linux":  "apt install fd-find",
			"common": "https://github.com/sharkdp/fd",
		},
		"fzf": {
			"darwin": "brew install fzf",
			"linux":  "apt install fzf",
			"common": "https://github.com/junegunn/fzf",
		},
		"bat": {
			"darwin": "brew install bat",
			"linux":  "apt install bat",
			"common": "https://github.com/sharkdp/bat",
		},
	}

	if toolHints, ok := hints[tool]; ok {
		os := runtime.GOOS
		if hint, ok := toolHints[os]; ok {
			return hint
		}
		if hint, ok := toolHints["common"]; ok {
			return hint
		}
	}

	// Generic hint based on platform
	switch runtime.GOOS {
	case "darwin":
		return fmt.Sprintf("brew install %s", tool)
	case "linux":
		return fmt.Sprintf("apt install %s  # or check your package manager", tool)
	default:
		return fmt.Sprintf("install %s using your system package manager", tool)
	}
}

// FormatValidation returns a human-readable validation report.
func (v *ShellValidation) FormatValidation() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Script: %s\n", v.Script))
	sb.WriteString(fmt.Sprintf("Shell: %s\n", v.Shell))
	sb.WriteString(fmt.Sprintf("Dependencies: %d tools detected\n", len(v.Dependencies)))

	if len(v.Missing) == 0 {
		sb.WriteString("\nAll dependencies satisfied.\n")
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("\nMissing %d dependencies:\n", len(v.Missing)))
	for _, dep := range v.Missing {
		sb.WriteString(fmt.Sprintf("\n  %s\n", dep.Name))
		sb.WriteString(fmt.Sprintf("    Used on lines: %v\n", dep.Lines))
		sb.WriteString(fmt.Sprintf("    Install: %s\n", dep.InstallHint))
	}

	return sb.String()
}

// HasMissing returns true if there are missing dependencies.
func (v *ShellValidation) HasMissing() bool {
	return len(v.Missing) > 0
}

// MissingNames returns the names of missing tools.
func (v *ShellValidation) MissingNames() []string {
	var names []string
	for _, dep := range v.Missing {
		names = append(names, dep.Name)
	}
	return names
}
