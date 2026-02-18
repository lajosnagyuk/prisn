// Package cli implements the prisn command-line interface.
package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/client"
	prisnctx "github.com/lajosnagyuk/prisn/pkg/context"
	"github.com/lajosnagyuk/prisn/pkg/store"
	"github.com/spf13/cobra"
)

// Check represents a single diagnostic check.
type Check struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok", "warn", "fail"
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// DoctorReport represents the full doctor output.
type DoctorReport struct {
	Checks  []Check `json:"checks"`
	Summary struct {
		OK      int `json:"ok"`
		Warn    int `json:"warn"`
		Fail    int `json:"fail"`
		Total   int `json:"total"`
	} `json:"summary"`
}

func newDoctorCmd() *cobra.Command {
	var fix bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check system health and diagnose issues",
		Long: `Run diagnostic checks on your prisn installation.

Checks include:
- Required runtimes (Python, Node.js, Bash)
- Server connectivity
- Database health
- Configuration validity
- Environment cache

Examples:
  prisn doctor          # Run all checks
  prisn doctor --fix    # Attempt to fix issues
  prisn doctor -o json  # JSON output for CI`,

		RunE: func(cmd *cobra.Command, args []string) error {
			printer := NewPrinter(cmd)
			report := runDoctorChecks(cmd, fix)

			if printer.IsStructured() {
				return printer.Print(report)
			}

			// Text output
			fmt.Println("PRISN DOCTOR")
			fmt.Println("============")
			fmt.Println()

			for _, check := range report.Checks {
				symbol := "+"
				color := "\033[32m" // green
				switch check.Status {
				case "warn":
					symbol = "~"
					color = "\033[33m" // yellow
				case "fail":
					symbol = "!"
					color = "\033[31m" // red
				}
				reset := "\033[0m"

				fmt.Printf("%s%s%s %s: %s\n", color, symbol, reset, check.Name, check.Message)
				if check.Fix != "" && check.Status != "ok" {
					fmt.Printf("    Fix: %s\n", check.Fix)
				}
			}

			fmt.Println()
			fmt.Printf("Summary: %d passed, %d warnings, %d failed\n",
				report.Summary.OK, report.Summary.Warn, report.Summary.Fail)

			if report.Summary.Fail > 0 {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&fix, "fix", false, "Attempt to fix issues automatically")

	return cmd
}

func runDoctorChecks(cmd *cobra.Command, fix bool) DoctorReport {
	var report DoctorReport

	// Check Python
	report.Checks = append(report.Checks, checkPython())

	// Check Node.js
	report.Checks = append(report.Checks, checkNode())

	// Check Bash
	report.Checks = append(report.Checks, checkBash())

	// Check pip
	report.Checks = append(report.Checks, checkPip())

	// Check server connectivity
	report.Checks = append(report.Checks, checkServer(cmd))

	// Check database
	report.Checks = append(report.Checks, checkDatabase())

	// Check config directory
	report.Checks = append(report.Checks, checkConfigDir(fix))

	// Check environment cache
	report.Checks = append(report.Checks, checkEnvCache())

	// Calculate summary
	for _, check := range report.Checks {
		report.Summary.Total++
		switch check.Status {
		case "ok":
			report.Summary.OK++
		case "warn":
			report.Summary.Warn++
		case "fail":
			report.Summary.Fail++
		}
	}

	return report
}

func checkPython() Check {
	check := Check{Name: "Python"}

	// Try python3 first, then python
	for _, cmd := range []string{"python3", "python"} {
		out, err := exec.Command(cmd, "--version").Output()
		if err == nil {
			version := strings.TrimSpace(string(out))
			check.Status = "ok"
			check.Message = version
			return check
		}
	}

	check.Status = "fail"
	check.Message = "not found"
	check.Fix = "Install Python 3: brew install python3 (macOS) or apt install python3 (Linux)"
	return check
}

func checkNode() Check {
	check := Check{Name: "Node.js"}

	out, err := exec.Command("node", "--version").Output()
	if err != nil {
		check.Status = "warn"
		check.Message = "not found (optional, only needed for JavaScript runtimes)"
		check.Fix = "Install Node.js: brew install node (macOS) or apt install nodejs (Linux)"
		return check
	}

	version := strings.TrimSpace(string(out))
	check.Status = "ok"
	check.Message = version
	return check
}

func checkBash() Check {
	check := Check{Name: "Bash"}

	out, err := exec.Command("bash", "--version").Output()
	if err != nil {
		check.Status = "fail"
		check.Message = "not found"
		return check
	}

	// Extract first line of version
	version := strings.Split(string(out), "\n")[0]
	check.Status = "ok"
	check.Message = version
	return check
}

func checkPip() Check {
	check := Check{Name: "pip"}

	// Try pip3 first, then pip
	for _, cmd := range []string{"pip3", "pip"} {
		out, err := exec.Command(cmd, "--version").Output()
		if err == nil {
			version := strings.TrimSpace(string(out))
			// Truncate long version strings
			if len(version) > 50 {
				version = version[:50] + "..."
			}
			check.Status = "ok"
			check.Message = version
			return check
		}
	}

	check.Status = "warn"
	check.Message = "not found (needed for Python dependencies)"
	check.Fix = "Install pip: python3 -m ensurepip --upgrade"
	return check
}

func checkServer(cmd *cobra.Command) Check {
	check := Check{Name: "Server"}

	flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
	flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
	resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
	if err != nil {
		check.Status = "fail"
		check.Message = fmt.Sprintf("failed to resolve context: %v", err)
		return check
	}

	c, err := client.New(resolved)
	if err != nil {
		check.Status = "fail"
		check.Message = fmt.Sprintf("failed to create client: %v", err)
		return check
	}

	start := time.Now()
	var health struct {
		Status string `json:"status"`
		NodeID string `json:"node_id"`
		Uptime string `json:"uptime"`
	}

	if err := c.GetJSON("/health", &health); err != nil {
		if _, ok := err.(*client.ConnectError); ok {
			check.Status = "warn"
			check.Message = fmt.Sprintf("not running at %s", resolved.Server)
			check.Fix = "Start server: prisn server"
			return check
		}
		check.Status = "fail"
		check.Message = err.Error()
		return check
	}

	latency := time.Since(start).Round(time.Millisecond)
	check.Status = "ok"
	check.Message = fmt.Sprintf("%s (%s, latency: %v)", resolved.Server, health.Status, latency)
	return check
}

func checkDatabase() Check {
	check := Check{Name: "Database"}

	home, err := os.UserHomeDir()
	if err != nil {
		check.Status = "fail"
		check.Message = "cannot determine home directory"
		return check
	}

	dbPath := filepath.Join(home, ".prisn", "state.db")
	info, err := os.Stat(dbPath)
	if err != nil {
		if os.IsNotExist(err) {
			check.Status = "ok"
			check.Message = "not created yet (will be created on first use)"
			return check
		}
		check.Status = "fail"
		check.Message = err.Error()
		return check
	}

	// Try to open the database
	st, err := store.Open(dbPath)
	if err != nil {
		check.Status = "fail"
		check.Message = fmt.Sprintf("cannot open: %v", err)
		check.Fix = "Database may be corrupted. Backup and remove: mv ~/.prisn/state.db ~/.prisn/state.db.bak"
		return check
	}
	defer st.Close()

	// Get stats
	stats, err := st.GetStats()
	if err != nil {
		check.Status = "warn"
		check.Message = fmt.Sprintf("exists but cannot read stats: %v", err)
		return check
	}

	size := formatBytes(info.Size())
	check.Status = "ok"
	check.Message = fmt.Sprintf("%s (%d deployments, %d executions)", size, stats.Deployments, stats.Executions)
	return check
}

func checkConfigDir(fix bool) Check {
	check := Check{Name: "Config directory"}

	home, err := os.UserHomeDir()
	if err != nil {
		check.Status = "fail"
		check.Message = "cannot determine home directory"
		return check
	}

	configDir := filepath.Join(home, ".prisn")
	info, err := os.Stat(configDir)
	if err != nil {
		if os.IsNotExist(err) {
			if fix {
				if err := os.MkdirAll(configDir, 0700); err != nil {
					check.Status = "fail"
					check.Message = fmt.Sprintf("cannot create: %v", err)
					return check
				}
				check.Status = "ok"
				check.Message = fmt.Sprintf("created %s", configDir)
				return check
			}
			check.Status = "warn"
			check.Message = fmt.Sprintf("%s does not exist", configDir)
			check.Fix = fmt.Sprintf("Create it: mkdir -p %s", configDir)
			return check
		}
		check.Status = "fail"
		check.Message = err.Error()
		return check
	}

	if !info.IsDir() {
		check.Status = "fail"
		check.Message = fmt.Sprintf("%s exists but is not a directory", configDir)
		return check
	}

	// Check permissions
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0700 {
		if fix {
			if err := os.Chmod(configDir, 0700); err != nil {
				check.Status = "warn"
				check.Message = fmt.Sprintf("%s has insecure permissions", configDir)
				check.Fix = fmt.Sprintf("Fix permissions: chmod 700 %s", configDir)
				return check
			}
		} else {
			check.Status = "warn"
			check.Message = fmt.Sprintf("%s has insecure permissions (%o)", configDir, info.Mode().Perm())
			check.Fix = fmt.Sprintf("Fix permissions: chmod 700 %s", configDir)
			return check
		}
	}

	check.Status = "ok"
	check.Message = configDir
	return check
}

func checkEnvCache() Check {
	check := Check{Name: "Environment cache"}

	home, err := os.UserHomeDir()
	if err != nil {
		check.Status = "fail"
		check.Message = "cannot determine home directory"
		return check
	}

	envsDir := filepath.Join(home, ".prisn", "envs")
	entries, err := os.ReadDir(envsDir)
	if err != nil {
		if os.IsNotExist(err) {
			check.Status = "ok"
			check.Message = "empty (no cached environments)"
			return check
		}
		check.Status = "fail"
		check.Message = err.Error()
		return check
	}

	// Calculate total size
	var totalSize int64
	for _, entry := range entries {
		if entry.IsDir() {
			filepath.Walk(filepath.Join(envsDir, entry.Name()), func(_ string, info os.FileInfo, _ error) error {
				if info != nil && !info.IsDir() {
					totalSize += info.Size()
				}
				return nil
			})
		}
	}

	check.Status = "ok"
	check.Message = fmt.Sprintf("%d environments (%s)", len(entries), formatBytes(totalSize))
	return check
}
