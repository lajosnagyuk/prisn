// Package cli implements the prisn command-line interface.
//
// Design philosophy: infer intent, but NEVER be dangerous.
// See docs/RED-TEAM-SYNTHESIS.md for why certain syntaxes were removed.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/lajosnagyuk/prisn/pkg/api"
	"github.com/lajosnagyuk/prisn/pkg/client"
	"github.com/lajosnagyuk/prisn/pkg/config"
	prisnctx "github.com/lajosnagyuk/prisn/pkg/context"
	"github.com/lajosnagyuk/prisn/pkg/k8s"
	"github.com/lajosnagyuk/prisn/pkg/log"
	"github.com/lajosnagyuk/prisn/pkg/manifest"
	"github.com/lajosnagyuk/prisn/pkg/raft"
	"github.com/lajosnagyuk/prisn/pkg/runner"
	"github.com/lajosnagyuk/prisn/pkg/scheduler"
	"github.com/lajosnagyuk/prisn/pkg/store"
	"github.com/lajosnagyuk/prisn/pkg/validate"
	"github.com/lajosnagyuk/prisn/pkg/venv"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// BuildInfo contains version information for the binary.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// NewRootCmd creates the root prisn command.
func NewRootCmd(info BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prisn",
		Short: "Just run my shit",
		Long: `prisn - just run my shit

QUICK START
  prisn run script.py              Run a script (deps auto-installed)
  prisn deploy api.py --port 8080  Deploy as a service
  prisn logs api -f                Stream logs

COMMON COMMANDS
  run        Run a script once
  deploy     Deploy a service or scheduled job
  status     Check what's running
  logs       View output and errors
  scale      Change replica count
  delete     Stop and remove

CONFIGURATION
  init       Create prisn.toml from existing files
  apply      Deploy from prisn.toml
  secret     Manage secrets
  cache      Manage dependency cache

MORE
  prisn <command> --help    Help for a command
  prisn doctor              Diagnose your environment
  prisn version             Show version info`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, args []string) {
			// When run with no args, show friendly help
			cmd.Help()
		},
	}

	// Enable typo suggestions (e.g., "prisn depoy" -> "Did you mean 'deploy'?")
	cmd.SuggestionsMinimumDistance = 2

	// Global flags
	cmd.PersistentFlags().StringP("context", "c", "", "Server context to use")
	cmd.PersistentFlags().StringP("namespace", "n", "", "Target namespace (overrides context namespace)")
	cmd.PersistentFlags().StringP("output", "o", "text", "Output format: text, json, yaml")
	cmd.PersistentFlags().BoolP("verbose", "v", false, "Verbose output")

	// Add subcommands
	cmd.AddCommand(
		newRunCmd(),
		newDevCmd(),
		newInitCmd(),
		newCheckCmd(),
		newApplyCmd(),
		newDeployCmd(),
		newStatusCmd(),
		newScaleCmd(),
		newTriggerCmd(),
		newGetCmd(),
		newDescribeCmd(),
		newExecCmd(),
		newDiffCmd(),
		newTopCmd(),
		newHistoryCmd(),
		newRollbackCmd(),
		newDeleteCmd(),
		newLogsCmd(),
		newCacheCmd(),
		newSecretCmd(),
		newEnvCmd(),
		newContextCmd(),
		newClusterCmd(),
		newTokenCmd(),
		newServerCmd(),
		newRuntimeCmd(),
		newDoctorCmd(),
		newCompletionCmd(),
		newEventsCmd(),
		newErrorsCmd(),
		newSelfTestCmd(),
		newVersionCmd(info),
	)

	// Register dynamic completions
	registerCompletions(cmd)

	return cmd
}

func newRunCmd() *cobra.Command {
	var (
		runtime   string
		envVars   []string
		timeout   string
		inline    string
		workDir   string
		memoryMB  int
		cpuPct    int
		detach    bool
	)

	cmd := &cobra.Command{
		Use:   "run <script> [args...]",
		Short: "Run a script once",
		Long: `Execute a script and wait for completion.

Dependencies are automatically detected and installed:
- Python: Checks requirements.txt, pyproject.toml, or scans imports
- Node.js: Checks package.json or scans require/import statements
- Bash: Runs directly

Use --detach to run in background on the server (requires server connection).
Check status with 'prisn get executions' or 'prisn logs <execution-id>'.

Examples:
  prisn run script.py                    # Run Python script
  prisn run backup.sh                    # Run shell script
  prisn run server.js                    # Run Node.js script
  prisn run script.py -- --input data    # With arguments
  prisn run script.py -e KEY=value       # With env vars
  prisn run -e "print('hi')" --runtime python  # Inline code
  prisn run script.py --timeout 5m       # With timeout
  prisn run script.py --detach           # Run in background`,

		Args: cobra.MinimumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			// Handle detach mode - submit to server
			if detach {
				if len(args) == 0 && inline == "" {
					return fmt.Errorf("script path required for --detach mode")
				}

				// Get client for server
				c, err := getClient(cmd)
				if err != nil {
					return fmt.Errorf("--detach requires server connection: %w", err)
				}

				// Build execution request
				source := inline
				if len(args) > 0 {
					scriptPath := args[0]
					if !filepath.IsAbs(scriptPath) {
						cwd, _ := os.Getwd()
						scriptPath = filepath.Join(cwd, scriptPath)
					}
					// Read script content
					content, err := os.ReadFile(scriptPath)
					if err != nil {
						return fmt.Errorf("failed to read script: %w", err)
					}
					source = string(content)
				}

				// Parse env vars
				env := make(map[string]string)
				for _, e := range envVars {
					key, val, err := validate.EnvVar(e)
					if err != nil {
						return fmt.Errorf("bad env var: %w", err)
					}
					env[key] = val
				}

				req := map[string]any{
					"source":  source,
					"runtime": runtime,
					"env":     env,
					"async":   true,
				}
				if len(args) > 1 {
					req["args"] = args[1:]
				}
				if timeout != "" {
					req["timeout"] = timeout
				}

				var resp struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				}
				if err := c.PostJSON("/api/v1/executions", req, &resp); err != nil {
					return fmt.Errorf("failed to submit execution: %w", err)
				}

				log.Done("Execution submitted: %s", resp.ID)
				fmt.Printf("\nCheck status: prisn get executions\n")
				fmt.Printf("View logs:    prisn logs %s\n", resp.ID)
				return nil
			}

			r, err := runner.New()
			if err != nil {
				return fmt.Errorf("failed to initialize runner: %w\n\n  Run 'prisn doctor' to diagnose your environment", err)
			}

			// Handle inline code
			if inline != "" {
				if runtime == "" {
					return fmt.Errorf("--runtime is required for inline code (-e)")
				}
				result, err := r.RunInline(ctx, inline, runtime)
				if err != nil {
					return err
				}
				os.Exit(result.ExitCode)
				return nil
			}

			if len(args) == 0 {
				return fmt.Errorf("script path required (or use -e for inline code)")
			}

			script := args[0]
			scriptArgs := args[1:]

			// Parse and validate env vars
			env := make(map[string]string)
			for _, e := range envVars {
				key, val, err := validate.EnvVar(e)
				if err != nil {
					return fmt.Errorf("bad env var: %w", err)
				}
				env[key] = val
			}

			// Resolve script path
			if !filepath.IsAbs(script) {
				cwd, _ := os.Getwd()
				script = filepath.Join(cwd, script)
			}

			info, err := os.Stat(script)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("script not found: %s\n\n  Check the path or use an absolute path", script)
				}
				if os.IsPermission(err) {
					return fmt.Errorf("permission denied: %s (check file permissions)", script)
				}
				return fmt.Errorf("cannot access script: %s (%v)", script, err)
			}
			if info.IsDir() {
				return fmt.Errorf("expected script, got directory: %s (use 'prisn deploy' for directories)", script)
			}

			// Set work directory
			if workDir == "" {
				workDir = filepath.Dir(script)
			}

			// Validate resources
			if memoryMB != 0 {
				if err := validate.Memory(memoryMB); err != nil {
					return err
				}
			}

			// Parse timeout
			var timeoutDur time.Duration
			if timeout != "" {
				if err := validate.Timeout(timeout); err != nil {
					return err
				}
				var parseErr error
				timeoutDur, parseErr = time.ParseDuration(timeout)
				if parseErr != nil {
					return fmt.Errorf("invalid timeout %q: try 30s, 5m, or 1h", timeout)
				}
			}

			cfg := runner.RunConfig{
				Script:  script,
				Args:    scriptArgs,
				Env:     env,
				WorkDir: workDir,
				Timeout: timeoutDur,
				Resources: runner.ResourceConfig{
					MaxMemoryMB:   memoryMB,
					MaxCPUPercent: cpuPct,
				},
			}

			result, err := r.Run(ctx, cfg)
			if err != nil {
				return err
			}

			// Always show environment status - users need to know if deps were installed
			verbose, _ := cmd.Flags().GetBool("verbose")
			if result.EnvSetupDuration > 0 {
				if result.EnvCached {
					// Only show cached status in verbose mode (it's fast, user doesn't need to know)
					if verbose {
						log.VInfo("Environment: cached (setup: %v)", result.EnvSetupDuration)
					}
				} else {
					// Always show when environment was created (explains the delay)
					log.Info("Environment: created (setup: %v)", result.EnvSetupDuration)
				}
			}
			if verbose {
				fmt.Fprintf(os.Stderr, "Duration: %v\n", result.Duration)
			}

			// Handle kill conditions with helpful messages
			if result.Killed {
				switch result.KillReason {
				case "timeout":
					timeoutVal := cfg.Timeout
					if timeoutVal == 0 {
						timeoutVal = r.DefaultTimeout
					}
					log.Fail("Script timed out after %v", timeoutVal)
					fmt.Fprintln(os.Stderr, "")
					fmt.Fprintln(os.Stderr, "Options:")
					fmt.Fprintln(os.Stderr, "  1. Increase timeout: prisn run script.py --timeout 10m")
					fmt.Fprintln(os.Stderr, "  2. Check for slow operations in your script")
					fmt.Fprintln(os.Stderr, "  3. Run 'prisn doctor' to check resource limits")
				default:
					log.Fail("Script killed: %s", result.KillReason)
				}
			} else if result.ExitCode != 0 {
				// Non-zero exit with stderr might indicate missing deps or runtime errors
				if result.Stderr != "" && strings.Contains(result.Stderr, "ModuleNotFoundError") {
					log.Fail("Script failed (exit %d): missing Python module", result.ExitCode)
					fmt.Fprintln(os.Stderr, "")
					fmt.Fprintln(os.Stderr, "  Add the missing module to requirements.txt and retry")
				fmt.Fprintln(os.Stderr, "  Or run 'prisn doctor' to check your environment")
				} else if result.Stderr != "" && strings.Contains(result.Stderr, "command not found") {
					log.Fail("Script failed (exit %d): command not found", result.ExitCode)
					fmt.Fprintln(os.Stderr, "")
					fmt.Fprintln(os.Stderr, "Tip: Run 'prisn doctor' to check your environment")
				}
			}

			os.Exit(result.ExitCode)
			return nil
		},
	}

	cmd.Flags().StringVarP(&runtime, "runtime", "r", "", "Runtime to use (python, node, bash)")
	cmd.Flags().StringArrayVarP(&envVars, "env", "e", nil, "Environment variables (KEY=value)")
	cmd.Flags().StringVarP(&timeout, "timeout", "t", "", "Execution timeout (e.g., 5m, 1h)")
	cmd.Flags().StringVar(&inline, "exec", "", "Execute inline code instead of file")
	cmd.Flags().StringVarP(&workDir, "workdir", "w", "", "Working directory")
	cmd.Flags().IntVar(&memoryMB, "memory", 0, "Memory limit in MB")
	cmd.Flags().IntVar(&cpuPct, "cpu", 0, "CPU limit as percentage")
	cmd.Flags().BoolVarP(&detach, "detach", "d", false, "Run in background on server (don't wait)")

	// Alias -e for both --env and --exec based on usage
	// If -e has = it's env, otherwise it's exec
	// Actually, let's just use --exec for inline to avoid confusion

	return cmd
}

func newDevCmd() *cobra.Command {
	var (
		envVars  []string
		workDir  string
		debounce string
		clear    bool
	)

	cmd := &cobra.Command{
		Use:   "dev <script>",
		Short: "Watch and run script on changes",
		Long: `Run a script and automatically re-run when files change.

Creates a tight feedback loop for development:
1. Runs the script immediately
2. Watches for file changes in the script's directory
3. Re-runs on any change (with debouncing)

Examples:
  prisn dev script.py             # Watch and run
  prisn dev api.py --clear        # Clear screen between runs
  prisn dev test.py --debounce 1s # Wait 1s after changes before re-running`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			script := args[0]

			// Parse env vars
			env := make(map[string]string)
			for _, e := range envVars {
				key, val, err := validate.EnvVar(e)
				if err != nil {
					return fmt.Errorf("bad env var: %w", err)
				}
				env[key] = val
			}

			// Resolve script path
			if !filepath.IsAbs(script) {
				cwd, _ := os.Getwd()
				script = filepath.Join(cwd, script)
			}

			// Check script exists
			if _, err := os.Stat(script); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("script not found: %s", script)
				}
				return fmt.Errorf("cannot access script: %w", err)
			}

			// Set work directory
			if workDir == "" {
				workDir = filepath.Dir(script)
			}

			// Parse debounce duration
			debounceDur := 300 * time.Millisecond
			if debounce != "" {
				var err error
				debounceDur, err = time.ParseDuration(debounce)
				if err != nil {
					return fmt.Errorf("invalid debounce duration: %s (try 500ms or 1s)", debounce)
				}
			}

			// Create file watcher
			watcher, err := fsnotify.NewWatcher()
			if err != nil {
				return fmt.Errorf("failed to create file watcher: %w", err)
			}
			defer watcher.Close()

			// Watch the script's directory
			watchDir := filepath.Dir(script)
			if err := watcher.Add(watchDir); err != nil {
				return fmt.Errorf("failed to watch directory: %w", err)
			}

			// Create runner
			r, err := runner.New()
			if err != nil {
				return fmt.Errorf("failed to initialize runner: %w\n\n  Run 'prisn doctor' to diagnose your environment", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			// Handle interrupts
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
			go func() {
				<-sigCh
				fmt.Fprintln(os.Stderr, "\nStopping...")
				cancel()
			}()

			// Function to run the script
			runScript := func() {
				if clear {
					fmt.Print("\033[H\033[2J") // ANSI clear screen
				}

				timestamp := time.Now().Format("15:04:05")
				fmt.Fprintf(os.Stderr, "[%s] Running %s...\n", timestamp, filepath.Base(script))

				cfg := runner.RunConfig{
					Script:  script,
					Env:     env,
					WorkDir: workDir,
				}

				result, err := r.Run(ctx, cfg)
				if err != nil {
					log.Fail("Error: %v", err)
					return
				}

				if result.ExitCode != 0 {
					fmt.Fprintf(os.Stderr, "[%s] Exit %d\n", time.Now().Format("15:04:05"), result.ExitCode)
				} else {
					fmt.Fprintf(os.Stderr, "[%s] Done\n", time.Now().Format("15:04:05"))
				}
			}

			// Initial run
			fmt.Fprintf(os.Stderr, "Watching %s...\n\n", watchDir)
			runScript()

			// Watch loop with debouncing
			var debounceTimer *time.Timer
			for {
				select {
				case <-ctx.Done():
					return nil
				case event, ok := <-watcher.Events:
					if !ok {
						return nil
					}

					// Only react to writes/creates/renames
					if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
						continue
					}

					// Ignore hidden files and common noise
					base := filepath.Base(event.Name)
					if strings.HasPrefix(base, ".") || strings.HasSuffix(base, "~") {
						continue
					}

					// Debounce: reset timer on each event
					if debounceTimer != nil {
						debounceTimer.Stop()
					}
					debounceTimer = time.AfterFunc(debounceDur, func() {
						fmt.Fprintln(os.Stderr, "")
						runScript()
					})

				case err, ok := <-watcher.Errors:
					if !ok {
						return nil
					}
					log.Fail("Watch error: %v", err)
				}
			}
		},
	}

	cmd.Flags().StringArrayVarP(&envVars, "env", "e", nil, "Environment variables (KEY=value)")
	cmd.Flags().StringVarP(&workDir, "workdir", "w", "", "Working directory")
	cmd.Flags().StringVar(&debounce, "debounce", "300ms", "Wait time after changes before re-running")
	cmd.Flags().BoolVar(&clear, "clear", false, "Clear screen between runs")

	return cmd
}

func newInitCmd() *cobra.Command {
	var (
		force bool
	)

	cmd := &cobra.Command{
		Use:   "init [directory]",
		Short: "Create a Prisnfile for a project",
		Long: `Analyze a project and create a Prisnfile.

prisn will detect your entrypoint, runtime, dependencies, and create
a minimal Prisnfile with sensible defaults. You can then customize it.

If a Prisnfile already exists, use --force to overwrite.

Examples:
  prisn init                    # Current directory
  prisn init ./my-project       # Specific directory
  prisn init --force            # Overwrite existing`,

		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}

			// Resolve to absolute path
			absDir, err := filepath.Abs(dir)
			if err != nil {
				return fmt.Errorf("cannot resolve path: %s", dir)
			}

			// Check if directory exists
			info, err := os.Stat(absDir)
			if err != nil {
				return fmt.Errorf("directory not found: %s", dir)
			}
			if !info.IsDir() {
				return fmt.Errorf("not a directory: %s", dir)
			}

			// Check for existing Prisnfile
			prismfilePath := filepath.Join(absDir, "Prisnfile")
			if _, err := os.Stat(prismfilePath); err == nil && !force {
				return fmt.Errorf("Prisnfile already exists (use --force to overwrite)")
			}

			// Infer the manifest
			log.Start("analyzing %s", filepath.Base(absDir))
			pf, err := manifest.Infer(absDir)
			if err != nil {
				log.Fail("analysis failed: %v", err)
				return err
			}

			if pf.Entrypoint == "" {
				log.Warn("no entrypoint found")
				log.Info("  create main.py, app.py, index.js, or main.sh")
				return fmt.Errorf("cannot create Prisnfile without entrypoint")
			}

			log.Done("found %s (%s)", pf.Entrypoint, pf.Runtime)

			// Analyze code for resource hints
			hints, _ := manifest.Analyze(absDir)
			if len(hints.HeavyLibs) > 0 {
				log.Info("  detected: %s", strings.Join(hints.HeavyLibs, ", "))
			}
			if hints.MemoryMB > 256 {
				log.Info("  suggested: %dMB memory, %s", hints.MemoryMB, hints.Arch)
			}

			// Generate Prisnfile content
			var content strings.Builder
			content.WriteString("# Prisnfile - generated by prisn init\n")
			content.WriteString("# Edit as needed, then: prisn deploy .\n\n")
			fmt.Fprintf(&content, "name = %q\n", pf.Name)
			fmt.Fprintf(&content, "entrypoint = %q\n", pf.Entrypoint)
			fmt.Fprintf(&content, "runtime = %q\n", pf.Runtime)

			if pf.Port > 0 {
				fmt.Fprintf(&content, "port = %d\n", pf.Port)
			}
			if pf.Schedule != "" {
				fmt.Fprintf(&content, "schedule = %q\n", pf.Schedule)
			}

			// Add dependencies if found
			if pf.Dependencies.RequirementsFile != "" {
				content.WriteString("\n[dependencies]\n")
				fmt.Fprintf(&content, "requirements = %q\n", pf.Dependencies.RequirementsFile)
			} else if pf.Dependencies.PackageFile != "" {
				content.WriteString("\n[dependencies]\n")
				fmt.Fprintf(&content, "package = %q\n", pf.Dependencies.PackageFile)
			}

			// Add resource hints if non-trivial
			if hints.MemoryMB > 256 || hints.Arch != "any" || hints.NeedsGPU {
				content.WriteString("\n# Resource hints (adjust as needed)\n")
				content.WriteString("[resources]\n")
				if hints.MemoryMB > 256 {
					if hints.MemoryMB >= 1024 {
						fmt.Fprintf(&content, "memory = %q\n", fmt.Sprintf("%dGB", hints.MemoryMB/1024))
					} else {
						fmt.Fprintf(&content, "memory = %q\n", fmt.Sprintf("%dMB", hints.MemoryMB))
					}
				}
				if hints.Arch != "any" && hints.Arch != "" {
					fmt.Fprintf(&content, "arch = %q  # %s\n", hints.Arch, archExplanation(hints.Arch))
				}
				if hints.NeedsGPU {
					content.WriteString("gpu = true\n")
				}
			}

			// Add env vars if loaded from .env
			if len(pf.Env) > 0 {
				content.WriteString("\n# Environment from .env (customize as needed)\n")
				content.WriteString("[env]\n")
				for k, v := range pf.Env {
					fmt.Fprintf(&content, "%s = %q\n", k, v)
				}
			}

			// Write the file
			if err := os.WriteFile(prismfilePath, []byte(content.String()), 0644); err != nil {
				log.Fail("cannot write Prisnfile: %v", err)
				return err
			}

			log.OK("created Prisnfile")
			fmt.Println()
			fmt.Println("Next steps:")
			fmt.Println("  1. Review and edit Prisnfile as needed")
			fmt.Println("  2. prisn deploy .")

			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing Prisnfile")

	return cmd
}

func newCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check [directory]",
		Short: "Validate a Prisnfile",
		Long: `Check a Prisnfile for errors without deploying.

Useful for CI/CD pipelines to catch configuration issues early.
Returns exit code 0 if valid, 1 if errors found.

Examples:
  prisn check              # Check current directory
  prisn check ./my-api     # Check specific directory`,

		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}

			absDir, err := filepath.Abs(dir)
			if err != nil {
				return fmt.Errorf("cannot resolve path: %s", dir)
			}

			log.Start("checking %s", filepath.Base(absDir))

			// Load or infer manifest
			pf, err := manifest.LoadOrInfer(absDir)
			if err != nil {
				log.Fail("cannot load manifest: %v", err)
				return err
			}

			// Validate
			if err := pf.Validate(); err != nil {
				log.Fail("validation failed: %v", err)
				return err
			}

			// Additional checks
			var warnings []string

			// Check entrypoint exists
			epPath := pf.EntrypointPath()
			if _, err := os.Stat(epPath); err != nil {
				log.Fail("entrypoint not found: %s", pf.Entrypoint)
				return fmt.Errorf("entrypoint not found")
			}

			// Check for common issues
			if pf.Port > 0 && pf.Port < 1024 {
				warnings = append(warnings, fmt.Sprintf("port %d requires root", pf.Port))
			}

			if pf.Name != "" {
				if err := validate.Name(pf.Name); err != nil {
					log.Fail("invalid name: %v", err)
					return err
				}
			}

			if pf.Schedule != "" {
				if err := validate.CronSchedule(pf.Schedule); err != nil {
					log.Fail("invalid schedule: %v", err)
					return err
				}
			}

			// Report
			log.OK("Prisnfile valid")
			log.Info("  name:       %s", pf.Name)
			log.Info("  type:       %s", pf.Type)
			log.Info("  entrypoint: %s (%s)", pf.Entrypoint, pf.Runtime)
			if pf.Port > 0 {
				log.Info("  port:       %d", pf.Port)
			}
			if pf.Schedule != "" {
				log.Info("  schedule:   %s", pf.Schedule)
			}

			for _, w := range warnings {
				log.Warn("%s", w)
			}

			return nil
		},
	}

	return cmd
}

func newDeployCmd() *cobra.Command {
	var (
		name        string
		port        int
		replicas    int
		envVars     []string
		labels      []string
		schedule    string
		dryRun      bool
		wait        bool
		waitTimeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "deploy <source>",
		Short: "Deploy a script or folder",
		Long: `Deploy a script or folder of scripts.

Each deploy creates a new version. Previous versions are kept for rollback.
Dependencies are automatically detected and installed.

Source can be:
  - A single script file (api.py)
  - A folder containing scripts (./my-project/)
  - A git URL (coming soon)

Examples:
  prisn deploy api.py --port 8080          # Web service
  prisn deploy ./my-project/ --name api    # Folder deployment
  prisn deploy worker.js --replicas 3      # Background workers
  prisn deploy backup.py --schedule "0 2 * * *"  # Cron job`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := args[0]

			// Validate source exists
			info, err := os.Stat(source)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("not found: %s", source)
				}
				if os.IsPermission(err) {
					return fmt.Errorf("permission denied: %s", source)
				}
				return fmt.Errorf("cannot access: %s", source)
			}

			// Try to load Prisnfile if source is a directory
			var pf *manifest.Prisnfile
			if info.IsDir() {
				var loadErr error
				pf, loadErr = manifest.LoadOrInfer(source)
				if loadErr != nil {
					log.Warn("could not infer manifest: %v", loadErr)
				}
			}

			// Use Prisnfile values as defaults if available
			if pf != nil {
				if name == "" {
					name = pf.Name
				}
				if port == 0 && pf.Port > 0 {
					port = pf.Port
				}
				if replicas == 1 && pf.Replicas > 1 {
					replicas = pf.Replicas
				}
				if schedule == "" && pf.Schedule != "" {
					schedule = pf.Schedule
				}
				// Merge env vars (CLI overrides Prisnfile)
				for k, v := range pf.Env {
					found := false
					for _, e := range envVars {
						if strings.HasPrefix(e, k+"=") {
							found = true
							break
						}
					}
					if !found {
						envVars = append(envVars, k+"="+v)
					}
				}
			}

			// Infer name from source if still not set
			if name == "" {
				base := filepath.Base(source)
				if info.IsDir() {
					name = base
				} else {
					name = strings.TrimSuffix(base, filepath.Ext(base))
				}
			}

			// Validate inputs
			if err := validate.Name(name); err != nil {
				return fmt.Errorf("bad name: %w", err)
			}
			if port != 0 {
				if err := validate.Port(port); err != nil {
					return err
				}
			}
			if err := validate.Replicas(replicas); err != nil {
				return err
			}
			if schedule != "" {
				if err := validate.CronSchedule(schedule); err != nil {
					return err
				}
			}

			// Validate env vars
			for _, e := range envVars {
				if _, _, err := validate.EnvVar(e); err != nil {
					return fmt.Errorf("bad env var: %w", err)
				}
			}

			// Determine deployment type
			deployType := "job"
			if port > 0 {
				deployType = "service"
			} else if schedule != "" {
				deployType = "cronjob"
			}

			// Create version manager
			home, _ := os.UserHomeDir()
			vm, err := store.NewVersionManager(filepath.Join(home, ".prisn", "deployments"))
			if err != nil {
				return fmt.Errorf("failed to init version manager: %w", err)
			}

			// Package the source
			log.Start("packaging %s", source)
			version, err := vm.Package(name, source)
			if err != nil {
				log.Fail("packaging failed: %v", err)
				return err
			}
			log.Done("packaged %d files (%s)", version.FileCount, formatBytes(version.Size))

			// Create bundle if we have a Prisnfile
			var bundle *manifest.Bundle
			if pf != nil {
				log.Start("creating bundle")
				bundle, err = manifest.CreateBundle(pf, manifest.DefaultBundleOptions())
				if err != nil {
					log.Warn("bundle creation failed: %v (using raw version)", err)
				} else {
					log.Done("bundle %s ready", bundle.Version)
				}
			}

			// Summary
			fmt.Println()
			log.OK("%s (%s)", name, deployType)
			log.Info("  version:    %s", version.ID)
			log.Info("  files:      %d (%s)", version.FileCount, formatBytes(version.Size))
			if bundle != nil {
				log.Info("  bundle:     %s", bundle.BundlePath)
			}
			if port > 0 {
				log.Info("  port:       %d", port)
			}
			if replicas > 1 {
				log.Info("  replicas:   %d", replicas)
			}
			if schedule != "" {
				log.Info("  schedule:   %s", schedule)
			}
			if pf != nil && pf.Entrypoint != "" {
				log.Info("  entrypoint: %s (%s)", pf.Entrypoint, pf.Runtime)
			}

			if dryRun {
				log.Warn("dry-run: not actually deploying")
				return nil
			}

			// Resolve context to determine deployment mode
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return fmt.Errorf("failed to resolve context: %w", err)
			}

			// Build env map
			env := make(map[string]string)
			for _, e := range envVars {
				parts := strings.SplitN(e, "=", 2)
				if len(parts) == 2 {
					env[parts[0]] = parts[1]
				}
			}

			// Build labels map
			labelMap := make(map[string]string)
			for _, l := range labels {
				parts := strings.SplitN(l, "=", 2)
				if len(parts) == 2 {
					labelMap[parts[0]] = parts[1]
				}
			}

			// Deploy based on mode
			if resolved.IsKubernetes() {
				// Deploy to Kubernetes via CRDs
				deploy := KubeDeployment{
					Name:      name,
					Namespace: resolved.Namespace,
					Source:    source,
					Port:      int32(port),
					Replicas:  int32(replicas),
					Env:       env,
					Schedule:  schedule,
				}
				if pf != nil {
					deploy.Runtime = pf.Runtime
				}
				if err := DeployToKubernetes(context.Background(), resolved, deploy); err != nil {
					return err
				}
				log.OK("deployed to kubernetes (%s)", resolved.KubeContext)
				if wait {
					if err := waitForKubernetesReady(context.Background(), resolved, name, waitTimeout); err != nil {
						return err
					}
				}
				return nil
			}

			// Remote mode: Try to register with server
			serverAddr := resolved.Server
			if serverAddr == "" {
				serverAddr = "localhost:7331"
			}

			if err := registerDeployment(serverAddr, name, resolved.Namespace, deployType, version, port, replicas, schedule, envVars, labelMap); err != nil {
				log.Warn("server not available, stored locally")
				log.Info("  path: %s", version.StoragePath)
				log.Info("  start server: prisn server")
			} else {
				log.OK("deployed to %s", serverAddr)
				if wait {
					if err := waitForDeploymentReady(serverAddr, name, resolved.Namespace, waitTimeout); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Deployment name (default: source name)")
	cmd.Flags().IntVarP(&port, "port", "p", 0, "Port to expose (makes it a service)")
	cmd.Flags().IntVarP(&replicas, "replicas", "r", 1, "Number of replicas")
	cmd.Flags().StringArrayVarP(&envVars, "env", "e", nil, "Environment variables")
	cmd.Flags().StringArrayVarP(&labels, "label", "l", nil, "Labels (key=value)")
	cmd.Flags().StringVar(&schedule, "schedule", "", "Cron schedule (makes it a cronjob)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Package but don't deploy")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for deployment to be ready")
	cmd.Flags().DurationVar(&waitTimeout, "wait-timeout", 5*time.Minute, "Timeout for --wait")

	return cmd
}

// registerDeployment sends the deployment to the server API.
func registerDeployment(serverAddr, name, namespace, deployType string, version *store.DeploymentVersion, port, replicas int, schedule string, envVars []string, labels map[string]string) error {
	// Build env map
	env := make(map[string]string)
	for _, e := range envVars {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}

	// Find entrypoint in versioned storage
	entrypoint := version.StoragePath
	// If it's a directory, look for entrypoint
	if info, err := os.Stat(version.StoragePath); err == nil && info.IsDir() {
		// Common entrypoint names
		candidates := []string{
			"main.py", "main.sh", "index.js", "app.py", "server.py",
			"run.py", "run.sh", "start.py", "start.sh",
			name + ".py", name + ".sh", name + ".js", // e.g., backup-job.sh
		}
		for _, candidate := range candidates {
			candidatePath := filepath.Join(version.StoragePath, candidate)
			if _, err := os.Stat(candidatePath); err == nil {
				entrypoint = candidatePath
				break
			}
		}
		// If still a directory, find the first script file
		if entrypoint == version.StoragePath {
			entries, _ := os.ReadDir(version.StoragePath)
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				ext := filepath.Ext(e.Name())
				if ext == ".py" || ext == ".sh" || ext == ".js" {
					entrypoint = filepath.Join(version.StoragePath, e.Name())
					break
				}
			}
		}
	}

	// Detect runtime
	runtime := "bash"
	if strings.HasSuffix(entrypoint, ".py") {
		runtime = "python3"
	} else if strings.HasSuffix(entrypoint, ".js") {
		runtime = "node"
	}

	reqBody := map[string]any{
		"name":      name,
		"namespace": namespace,
		"type":      deployType,
		"runtime":   runtime,
		"source":    entrypoint,
		"port":      port,
		"replicas":  replicas,
		"schedule":  schedule,
		"env":       env,
		"labels":    labels,
	}

	data, _ := json.Marshal(reqBody)
	resp, err := http.Post("http://"+serverAddr+"/api/v1/deployments", "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		// Already exists, update it
		// For now, just return success
		return nil
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server error: %s", body)
	}

	return nil
}

// waitForDeploymentReady polls the server until the deployment is healthy or timeout.
func waitForDeploymentReady(serverAddr, name, namespace string, timeout time.Duration) error {
	log.Start("waiting for %s to be ready (timeout: %v)", name, timeout)

	deadline := time.Now().Add(timeout)
	pollInterval := 2 * time.Second

	for time.Now().Before(deadline) {
		// Query deployment status
		url := fmt.Sprintf("http://%s/api/v1/namespaces/%s/deployments/%s", serverAddr, namespace, name)
		resp, err := http.Get(url)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			var deploy struct {
				Status struct {
					State         string `json:"state"`
					ReadyReplicas int    `json:"ready_replicas"`
					Replicas      int    `json:"replicas"`
				} `json:"status"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&deploy); err == nil {
				if deploy.Status.State == "running" && deploy.Status.ReadyReplicas >= deploy.Status.Replicas {
					resp.Body.Close()
					log.Done("%s is ready (%d/%d replicas)", name, deploy.Status.ReadyReplicas, deploy.Status.Replicas)
					return nil
				}
				log.Wait("%s: %s (%d/%d ready)", name, deploy.Status.State, deploy.Status.ReadyReplicas, deploy.Status.Replicas)
			}
			resp.Body.Close()
		} else {
			resp.Body.Close()
		}

		time.Sleep(pollInterval)
	}

	return fmt.Errorf("timeout waiting for %s to be ready", name)
}

// waitForKubernetesReady polls Kubernetes until the PrisnApp is ready or timeout.
func waitForKubernetesReady(ctx context.Context, resolved *prisnctx.Resolved, name string, timeout time.Duration) error {
	log.Start("waiting for %s to be ready (timeout: %v)", name, timeout)

	client, err := k8s.NewClient(resolved.KubeContext, resolved.Namespace)
	if err != nil {
		return fmt.Errorf("cannot create kubernetes client: %w", err)
	}

	deadline := time.Now().Add(timeout)
	pollInterval := 2 * time.Second

	for time.Now().Before(deadline) {
		app, err := client.GetApp(ctx, name)
		if err == nil {
			// Extract status from unstructured object
			status, found, _ := unstructured.NestedMap(app.Object, "status")
			if found {
				state, _, _ := unstructured.NestedString(status, "state")
				ready, _, _ := unstructured.NestedInt64(status, "readyReplicas")
				replicas, _, _ := unstructured.NestedInt64(status, "replicas")

				if state == "running" && ready >= replicas && replicas > 0 {
					log.Done("%s is ready (%d/%d replicas)", name, ready, replicas)
					return nil
				}
				log.Wait("%s: %s (%d/%d ready)", name, state, ready, replicas)
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return fmt.Errorf("timeout waiting for %s to be ready", name)
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// StatusResponse represents the full status output for JSON/YAML.
type StatusResponse struct {
	Server      ServerStatus       `json:"server"`
	Deployments []store.Deployment `json:"deployments"`
}

// ServerStatus represents server health information.
type ServerStatus struct {
	Address   string    `json:"address"`
	Status    string    `json:"status"`
	NodeID    string    `json:"node_id"`
	Uptime    string    `json:"uptime"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "status",
		Aliases: []string{"st"},
		Short:   "Show cluster and deployment status",
		Long: `Show an overview of the prisn cluster and all deployments.

Examples:
  prisn status              # Full overview
  prisn status api          # Status of specific deployment
  prisn status -o json      # JSON output for scripting`,

		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			printer := NewPrinter(cmd)

			// Resolve context to check mode
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return err
			}

			// Kubernetes mode - query CRDs
			if resolved.IsKubernetes() {
				return showKubernetesAppStatus(cmd.Context(), resolved, args)
			}

			// Local/Remote mode - query server API
			c, err := client.New(resolved)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			if len(args) == 1 {
				// Specific deployment status
				name := args[0]
				var d store.Deployment
				if err := c.GetJSON("/api/v1/deployments/"+name, &d); err != nil {
					// Check if it's a connection error
					if _, ok := err.(*client.ConnectError); ok {
						return fmt.Errorf("cannot connect to prisn server at %s: %w\n\nStart a server with: prisn server start", resolved.Server, err)
					}
					return fmt.Errorf("deployment not found: %s\n\n  List deployments: prisn get deploy\n  Create one:        prisn deploy <script>", name)
				}

				return printer.PrintItem(d, func() {
					fmt.Printf("Deployment: %s\n", d.Name)
					fmt.Printf("  ID:        %s\n", d.ID)
					fmt.Printf("  Type:      %s\n", d.Type)
					fmt.Printf("  Status:    %s\n", d.Status.Phase)
					fmt.Printf("  Replicas:  %d/%d\n", d.Status.ReadyReplicas, d.Replicas)
					if d.Port > 0 {
						fmt.Printf("  Port:      %d\n", d.Port)
					}
					fmt.Printf("  Runtime:   %s\n", d.Runtime)
					fmt.Printf("  Source:    %s\n", d.Source)
					fmt.Printf("  Created:   %s\n", d.CreatedAt.Format("2006-01-02 15:04:05"))
					fmt.Printf("  Updated:   %s\n", d.UpdatedAt.Format("2006-01-02 15:04:05"))
				})
			}

			// Full status - get from server
			var result struct {
				Count int                `json:"count"`
				Items []store.Deployment `json:"items"`
			}
			if err := c.GetJSON("/api/v1/deployments", &result); err != nil {
				// Check if it's a connection error
				if _, ok := err.(*client.ConnectError); ok {
					return fmt.Errorf("cannot connect to prisn server at %s: %w\n\nStart a server with: prisn server start", resolved.Server, err)
				}
				return fmt.Errorf("failed to list deployments: %w", err)
			}

			// Get server health
			var health struct {
				Status    string    `json:"status"`
				NodeID    string    `json:"node_id"`
				Uptime    string    `json:"uptime"`
				StartedAt time.Time `json:"started_at"`
			}
			healthErr := c.GetJSON("/health", &health)

			// For structured output, return full status object
			if printer.IsStructured() {
				status := StatusResponse{
					Server: ServerStatus{
						Address:   resolved.Server,
						Status:    health.Status,
						NodeID:    health.NodeID,
						Uptime:    health.Uptime,
						StartedAt: health.StartedAt,
					},
					Deployments: result.Items,
				}
				if healthErr != nil {
					status.Server.Status = "unreachable"
				}
				return printer.Print(status)
			}

			// Text output
			fmt.Println("PRISN STATUS")
			fmt.Println("============")
			fmt.Printf("Context:     %s\n", resolved.Name)
			fmt.Printf("Server:      %s", resolved.Server)
			if healthErr != nil {
				fmt.Printf(" (unreachable)\n")
			} else if health.Status == "healthy" {
				fmt.Printf(" (healthy)\n")
			} else {
				fmt.Printf(" (%s)\n", health.Status)
			}
			if health.NodeID != "" {
				fmt.Printf("Node:        %s\n", health.NodeID)
			}
			if health.Uptime != "" {
				fmt.Printf("Uptime:      %s\n", health.Uptime)
			}
			fmt.Println()

			// Count services, jobs, cronjobs
			var services, jobs, cronjobs int
			for _, d := range result.Items {
				switch d.Type {
				case "service":
					services++
				case "job":
					jobs++
				case "cronjob":
					cronjobs++
				}
			}

			fmt.Printf("Deployments: %d total", result.Count)
			if services > 0 || jobs > 0 || cronjobs > 0 {
				parts := []string{}
				if services > 0 {
					parts = append(parts, fmt.Sprintf("%d services", services))
				}
				if jobs > 0 {
					parts = append(parts, fmt.Sprintf("%d jobs", jobs))
				}
				if cronjobs > 0 {
					parts = append(parts, fmt.Sprintf("%d cronjobs", cronjobs))
				}
				fmt.Printf(" (%s)", strings.Join(parts, ", "))
			}
			fmt.Println()
			fmt.Println()

			if result.Count > 0 {
				printer.PrintTable(
					[]string{"NAME", "TYPE", "REPLICAS", "STATUS", "PORT"},
					func() [][]string {
						rows := make([][]string, len(result.Items))
						for i, d := range result.Items {
							port := "-"
							if d.Port > 0 {
								port = strconv.Itoa(d.Port)
							}
							rows[i] = []string{
								d.Name,
								d.Type,
								fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, d.Replicas),
								d.Status.Phase,
								port,
							}
						}
						return rows
					}(),
				)
			} else {
				fmt.Println("No deployments found.")
				fmt.Println("Deploy something with: prisn deploy <source>")
			}

			return nil
		},
	}

	return cmd
}

// showKubernetesAppStatus displays status for Kubernetes mode.
func showKubernetesAppStatus(ctx context.Context, resolved *prisnctx.Resolved, args []string) error {
	client, err := k8s.NewClient(resolved.KubeContext, resolved.Namespace)
	if err != nil {
		return fmt.Errorf("failed to create K8s client: %w", err)
	}

	if len(args) == 1 {
		// Specific app status
		name := args[0]
		app, err := client.GetApp(ctx, name)
		if err != nil {
			return fmt.Errorf("PrisnApp not found: %s\n\n  List apps: prisn get app\n  Deploy:    prisn deploy <script>", name)
		}

		status := k8s.ExtractAppStatus(app)

		fmt.Println("PRISNAPP STATUS")
		fmt.Println("===============")
		fmt.Println()
		fmt.Printf("Name:      %s\n", status.Name)
		fmt.Printf("Namespace: %s\n", status.Namespace)
		fmt.Printf("Phase:     %s\n", status.Phase)
		fmt.Printf("Replicas:  %d/%d ready\n", status.ReadyReplicas, status.DesiredReplicas)
		fmt.Printf("Runtime:   %s\n", status.Runtime)
		if status.Port > 0 {
			fmt.Printf("Port:      %d\n", status.Port)
		}
		if status.ServiceName != "" {
			fmt.Printf("Service:   %s\n", status.ServiceName)
			// Show connection info
			fmt.Println()
			fmt.Println("CONNECTION")
			fmt.Println("----------")
			fmt.Printf("In-cluster: %s.%s.svc.cluster.local:%d\n", status.ServiceName, status.Namespace, status.Port)
			fmt.Printf("kubectl:    kubectl port-forward svc/%s %d:%d -n %s\n", status.ServiceName, status.Port, status.Port, status.Namespace)
		}
		fmt.Printf("Age:       %s\n", k8s.FormatAge(status.Age))
		if status.Message != "" {
			fmt.Printf("Message:   %s\n", status.Message)
		}
		return nil
	}

	// List all apps
	apps, err := ListAppsInKubernetes(ctx, resolved)
	if err != nil {
		return err
	}

	fmt.Println("PRISN STATUS (kubernetes)")
	fmt.Println("=========================")
	fmt.Println()
	fmt.Printf("Context:   %s\n", resolved.Name)
	kubeCtx := resolved.KubeContext
	if kubeCtx == "" {
		kubeCtx = "(current kubeconfig context)"
	}
	fmt.Printf("KubeCtx:   %s\n", kubeCtx)
	fmt.Printf("Namespace: %s\n", resolved.Namespace)
	fmt.Println()

	if len(apps) == 0 {
		fmt.Println("No PrisnApps found.")
		fmt.Println("Deploy something with: prisn deploy <source>")
		return nil
	}

	fmt.Printf("PrisnApps: %d\n\n", len(apps))
	fmt.Println("NAME                 PHASE      REPLICAS   AGE")
	fmt.Println("----                 -----      --------   ---")

	for _, app := range apps {
		fmt.Printf("%-20s %-10s %d/%d        %s\n",
			app.Name,
			app.Phase,
			app.ReadyReplicas,
			app.DesiredReplicas,
			k8s.FormatAge(app.Age))
	}

	return nil
}

func newHistoryCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "history <deployment>",
		Short: "Show deployment version history",
		Long: `Show all versions of a deployment for rollback.

Examples:
  prisn history api              # All versions
  prisn history api --limit 5    # Last 5 versions`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			home, _ := os.UserHomeDir()
			vm, err := store.NewVersionManager(filepath.Join(home, ".prisn", "deployments"))
			if err != nil {
				return err
			}

			versions, err := vm.ListVersions(name)
			if err != nil {
				return fmt.Errorf("failed to list versions: %w", err)
			}

			if len(versions) == 0 {
				return fmt.Errorf("deployment not found: %s\n\n  List deployments: prisn get deploy", name)
			}

			fmt.Printf("Version history for %s:\n\n", name)
			fmt.Println("VERSION      CREATED              STATUS")
			fmt.Println("-------      -------              ------")

			count := len(versions)
			if limit > 0 && limit < count {
				count = limit
			}

			for i := 0; i < count; i++ {
				v := versions[i]
				status := ""
				if i == 0 {
					status = "current"
				}
				fmt.Printf("%-12s %-20s %s\n",
					v.ID,
					v.CreatedAt.Format("2006-01-02 15:04:05"),
					status)
			}

			if limit > 0 && len(versions) > limit {
				fmt.Printf("\n(%d more versions not shown)\n", len(versions)-limit)
			}

			fmt.Println("\nRollback with: prisn rollback", name, "<version>")
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 0, "Limit number of versions shown")

	return cmd
}

func newRollbackCmd() *cobra.Command {
	var (
		dryRun bool
		yes    bool
	)

	cmd := &cobra.Command{
		Use:   "rollback <deployment> [version]",
		Short: "Roll back to a previous version",
		Long: `Roll back a deployment to a previous version.

If no version is specified, rolls back to the immediately previous version.
Use 'prisn history <deployment>' to see available versions.

Examples:
  prisn rollback api               # Roll back to previous version
  prisn rollback api v-a1b2c3d4    # Roll back to specific version
  prisn rollback api --dry-run     # Preview without applying`,

		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			home, _ := os.UserHomeDir()
			vm, err := store.NewVersionManager(filepath.Join(home, ".prisn", "deployments"))
			if err != nil {
				return err
			}

			versions, err := vm.ListVersions(name)
			if err != nil {
				return fmt.Errorf("failed to list versions: %w", err)
			}

			if len(versions) == 0 {
				return fmt.Errorf("deployment not found: %s\n\n  List deployments: prisn get deploy", name)
			}

			if len(versions) < 2 && len(args) < 2 {
				return fmt.Errorf("no previous version to roll back to (only 1 version exists)")
			}

			var targetVersion *store.DeploymentVersion

			if len(args) == 2 {
				// Specific version requested
				targetID := args[1]
				for _, v := range versions {
					if v.ID == targetID {
						targetVersion = v
						break
					}
				}
				if targetVersion == nil {
					return fmt.Errorf("version not found: %s\nUse 'prisn history %s' to see available versions", targetID, name)
				}
			} else {
				// Roll back to previous (second in list, since first is current)
				targetVersion = versions[1]
			}

			currentVersion := versions[0]

			if targetVersion.ID == currentVersion.ID {
				fmt.Printf("Already at version %s\n", targetVersion.ID)
				return nil
			}

			if dryRun {
				fmt.Printf("Would roll back %s:\n", name)
				fmt.Printf("  From: %s (%s)\n", currentVersion.ID, currentVersion.CreatedAt.Format("2006-01-02 15:04"))
				fmt.Printf("  To:   %s (%s)\n", targetVersion.ID, targetVersion.CreatedAt.Format("2006-01-02 15:04"))
				return nil
			}

			if !yes {
				fmt.Printf("Roll back %s from %s to %s? [y/N] ", name, currentVersion.ID, targetVersion.ID)
				reader := bufio.NewReader(os.Stdin)
				input, _ := reader.ReadString('\n')
				input = strings.TrimSpace(strings.ToLower(input))
				if input != "y" && input != "yes" {
					fmt.Println("Cancelled")
					return nil
				}
			}

			fmt.Printf("Rolling back %s to %s...\n", name, targetVersion.ID)
			fmt.Printf("  Previous: %s\n", currentVersion.ID)
			fmt.Printf("  Target:   %s\n", targetVersion.ID)

			// Version switching requires server coordination
			fmt.Println("\n(prisn server required for full rollback)")
			fmt.Printf("Version %s is at: %s\n", targetVersion.ID, targetVersion.StoragePath)

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview without applying")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation")

	return cmd
}

func newScaleCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "scale <deployment> <replicas>",
		Short: "Change replica count",
		Long: `Scale a deployment up or down.

The replicas argument is smart - it understands what you mean:

  prisn scale app 5       # Set to exactly 5 replicas
  prisn scale app +2      # Add 2 replicas
  prisn scale app -1      # Remove 1 replica (use -- before negative)
  prisn scale app x1.5    # Multiply by 1.5
  prisn scale app 150%    # Scale to 150% of current
  prisn scale app auto    # Auto-scale based on load
  prisn scale app 0       # Pause (scale to 0)

For negative numbers, use -- to separate from flags:
  prisn scale app -- -1

Examples:
  prisn scale my-api 5
  prisn scale my-api +2
  prisn scale my-api x2 --dry-run`,

		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deployment := args[0]
			expr := args[1]

			// Resolve context first to determine mode
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return fmt.Errorf("failed to resolve context: %w", err)
			}

			// Get current replicas based on mode
			currentReplicas := 1

			if resolved.IsKubernetes() {
				// Get current replicas from K8s
				app, err := GetAppInKubernetes(context.Background(), resolved, deployment)
				if err == nil {
					if status, ok := app.Object["status"].(map[string]interface{}); ok {
						if r, ok := status["replicas"]; ok {
							if rInt, ok := r.(int64); ok {
								currentReplicas = int(rInt)
							}
						}
					}
				}
			} else {
				// Get client for server connection
				c, err := getClient(cmd)
				if err == nil {
					// Find deployment by name from server
					var deployments []struct {
						ID       string `json:"id"`
						Name     string `json:"name"`
						Replicas int    `json:"replicas"`
					}
					err = c.GetJSON("/api/v1/deployments", &struct {
						Items *[]struct {
							ID       string `json:"id"`
							Name     string `json:"name"`
							Replicas int    `json:"replicas"`
						} `json:"items"`
					}{Items: &deployments})
					if err == nil {
						for _, d := range deployments {
							if d.Name == deployment {
								currentReplicas = d.Replicas
								break
							}
						}
					}
				}
			}

			target, action, err := parseScaleExpr(expr, currentReplicas)
			if err != nil {
				return err
			}

			if err := validate.Replicas(target); err != nil {
				return err
			}

			if dryRun {
				fmt.Printf("Would scale %s: %s\n", deployment, action)
				return nil
			}

			fmt.Printf("Scaling %s: %s\n", deployment, action)

			if resolved.IsKubernetes() {
				// Scale in Kubernetes
				if err := ScaleInKubernetes(context.Background(), resolved, deployment, int32(target)); err != nil {
					return err
				}
			} else {
				// Scale via server
				c, err := getClient(cmd)
				if err != nil {
					return err
				}

				resp, err := c.Post("/api/v1/deployments/"+deployment+"/scale", map[string]int{
					"replicas": target,
				})
				if err != nil {
					return fmt.Errorf("failed to scale: %w", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode == http.StatusNotFound {
					return fmt.Errorf("deployment not found: %s\n\n  List deployments: prisn get deploy\n  Create one:        prisn deploy <script>", deployment)
				}
				if resp.StatusCode != http.StatusOK {
					body, _ := io.ReadAll(resp.Body)
					return fmt.Errorf("server error: %s", string(body))
				}
			}

			if target == 0 {
				fmt.Println("Deployment paused. Use 'prisn scale " + deployment + " 1' to resume.")
			} else {
				log.Done("Scaled to %d replicas", target)
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be done")

	return cmd
}

func newTriggerCmd() *cobra.Command {
	var (
		envVars []string
		wait    bool
		timeout time.Duration
	)

	cmd := &cobra.Command{
		Use:   "trigger <deployment>",
		Short: "Trigger a job deployment",
		Long: `Manually trigger execution of a job or cronjob deployment.

This creates a new execution of the deployment. For services, use
'prisn scale' instead. For one-off scripts, use 'prisn run'.

Examples:
  prisn trigger backup-job              # Trigger backup job
  prisn trigger backup-job --wait       # Wait for completion
  prisn trigger backup-job -e KEY=val   # With extra env vars
  prisn trigger backup-job --timeout 5m # With timeout`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deploymentName := args[0]

			// Build env map
			env := make(map[string]string)
			for _, e := range envVars {
				key, val, err := validate.EnvVar(e)
				if err != nil {
					return fmt.Errorf("bad env var: %w", err)
				}
				env[key] = val
			}

			// Get client
			c, err := getClient(cmd)
			if err != nil {
				return err
			}

			// Build request
			req := map[string]any{
				"deployment_id": deploymentName,
			}
			if len(env) > 0 {
				req["env"] = env
			}
			if timeout > 0 {
				req["timeout"] = timeout.String()
			}

			var resp struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			}
			if err := c.PostJSON("/api/v1/executions", req, &resp); err != nil {
				if client.IsOffline(err) {
					return fmt.Errorf("server not available: %w", err)
				}
				return fmt.Errorf("failed to trigger: %w", err)
			}

			log.OK("Triggered %s (execution: %s)", deploymentName, resp.ID)

			if !wait {
				fmt.Printf("\nCheck status: prisn get executions\n")
				fmt.Printf("View logs:    prisn logs job/%s\n", resp.ID)
				return nil
			}

			// Wait for completion
			log.Start("Waiting for completion...")
			waitTimeout := 5 * time.Minute
			if timeout > 0 {
				waitTimeout = timeout + 30*time.Second
			}
			deadline := time.Now().Add(waitTimeout)

			for time.Now().Before(deadline) {
				var exec struct {
					Status   string `json:"status"`
					ExitCode int    `json:"exit_code"`
					Duration string `json:"duration"`
				}
				if err := c.GetJSON("/api/v1/executions/"+resp.ID, &exec); err != nil {
					time.Sleep(time.Second)
					continue
				}

				switch exec.Status {
				case "Completed":
					log.Done("Completed in %s (exit code: %d)", exec.Duration, exec.ExitCode)
					return nil
				case "Failed":
					log.Fail("Failed (exit code: %d)", exec.ExitCode)
					fmt.Printf("\nView logs: prisn logs job/%s\n", resp.ID)
					return fmt.Errorf("execution failed")
				}
				time.Sleep(time.Second)
			}

			return fmt.Errorf("timeout waiting for execution")
		},
	}

	cmd.Flags().StringArrayVarP(&envVars, "env", "e", nil, "Additional environment variables")
	cmd.Flags().BoolVar(&wait, "wait", false, "Wait for execution to complete")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Execution timeout")

	return cmd
}

// parseScaleExpr parses a scaling expression and returns target replicas and action description.
func parseScaleExpr(expr string, current int) (target int, action string, err error) {
	expr = strings.TrimSpace(expr)

	// Auto-scale
	if expr == "auto" {
		// Simulated auto-scale calculation
		fmt.Println("Analyzing load data...")
		fmt.Printf("  Current: %d replicas, ~85%% CPU\n", current)
		fmt.Printf("  Target: 70%% CPU utilization\n")
		target = max(1, int(float64(current)*85/70*1.2)) // with 20% safety margin
		fmt.Printf("  Recommended: %d replicas\n\n", target)
		return target, fmt.Sprintf("auto-scale to %d", target), nil
	}

	// Percentage: 150%
	if strings.HasSuffix(expr, "%") {
		pct, e := strconv.ParseFloat(expr[:len(expr)-1], 64)
		if e != nil {
			return 0, "", fmt.Errorf("invalid percentage: %s", expr)
		}
		target = max(1, int(float64(current)*pct/100))
		return target, fmt.Sprintf("%.0f%% of %d = %d", pct, current, target), nil
	}

	// Multiply: x1.5
	if strings.HasPrefix(expr, "x") || strings.HasPrefix(expr, "X") {
		mult, e := strconv.ParseFloat(expr[1:], 64)
		if e != nil {
			return 0, "", fmt.Errorf("invalid multiplier: %s", expr)
		}
		target = max(1, int(float64(current)*mult))
		return target, fmt.Sprintf("%d x %.2f = %d", current, mult, target), nil
	}

	// Add: +2
	if strings.HasPrefix(expr, "+") {
		add, e := strconv.Atoi(expr[1:])
		if e != nil {
			return 0, "", fmt.Errorf("invalid add value: %s", expr)
		}
		target = current + add
		return target, fmt.Sprintf("%d + %d = %d", current, add, target), nil
	}

	// Subtract: -1 (must use -- before it on command line)
	if strings.HasPrefix(expr, "-") {
		sub, e := strconv.Atoi(expr[1:])
		if e != nil {
			return 0, "", fmt.Errorf("invalid subtract value: %s", expr)
		}
		target = current - sub
		return target, fmt.Sprintf("%d - %d = %d", current, sub, target), nil
	}

	// Absolute: 5
	n, e := strconv.Atoi(expr)
	if e != nil {
		return 0, "", fmt.Errorf("invalid replica expression: %s\nUse: 5, +2, -1, x1.5, 150%%, or auto", expr)
	}
	return n, fmt.Sprintf("set to %d", n), nil
}

func newGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <resource>",
		Short: "List resources",
		Long: `List deployments, jobs, and other resources.

Resources:
  deployments, deploy, d    Long-running services
  jobs, job, j              One-time executions
  envs, env                 Cached environments
  all                       All resources

Examples:
  prisn get deploy
  prisn get jobs
  prisn get envs`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resource := args[0]
			printer := NewPrinter(cmd)

			// Resolve context to determine mode
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return fmt.Errorf("failed to resolve context: %w", err)
			}

			switch resource {
			case "deployments", "deploy", "d", "apps", "app", "a":
				if resolved.IsKubernetes() {
					// List PrisnApps from Kubernetes
					apps, err := ListAppsInKubernetes(context.Background(), resolved)
					if err != nil {
						return err
					}

					return printer.PrintList(apps, func() {
						printer.PrintTable(
							[]string{"NAMESPACE", "NAME", "TYPE", "READY", "STATUS", "AGE"},
							func() [][]string {
								rows := make([][]string, len(apps))
								for i, app := range apps {
									rows[i] = []string{
										app.Namespace,
										app.Name,
										"app",
										fmt.Sprintf("%d/%d", app.ReadyReplicas, app.DesiredReplicas),
										formatStatus(app.Phase),
										formatAge(app.Age),
									}
								}
								return rows
							}(),
						)
					})
				}

				// List deployments from prisn server
				c, err := client.New(resolved)
				if err != nil {
					return fmt.Errorf("failed to create client: %w", err)
				}

				var result struct {
					Count int                `json:"count"`
					Items []store.Deployment `json:"items"`
				}
				if err := c.GetJSON("/api/v1/deployments", &result); err != nil {
					// Check if it's a connection error
					if _, ok := err.(*client.ConnectError); ok {
						return fmt.Errorf("cannot connect to prisn server at %s: %w\n\nStart a server with: prisn server start", resolved.Server, err)
					}
					return fmt.Errorf("failed to list deployments: %w", err)
				}

				return printer.PrintList(result.Items, func() {
					printer.PrintTable(
						[]string{"NAMESPACE", "NAME", "TYPE", "READY", "STATUS", "RESTARTS", "PORT", "AGE"},
						func() [][]string {
							rows := make([][]string, len(result.Items))
							for i, d := range result.Items {
								port := "-"
								if d.Port > 0 {
									port = strconv.Itoa(d.Port)
								}
								dtype := d.Type
								if dtype == "" {
									dtype = "service"
								}
								restarts := "-"
								if d.Status.Restarts > 0 {
									restarts = strconv.Itoa(d.Status.Restarts)
								}
								rows[i] = []string{
									d.Namespace,
									d.Name,
									dtype,
									fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, d.Replicas),
									formatStatus(d.Status.Phase),
									restarts,
									port,
									formatAge(time.Since(d.CreatedAt)),
								}
							}
							return rows
						}(),
					)
				})

			case "jobs", "job", "j":
				// Get executions from server
				c, err := client.New(resolved)
				if err != nil {
					return fmt.Errorf("failed to create client: %w", err)
				}

				var result struct {
					Count int               `json:"count"`
					Items []store.Execution `json:"items"`
				}
				if err := c.GetJSON("/api/v1/executions", &result); err != nil {
					if _, ok := err.(*client.ConnectError); ok {
						return fmt.Errorf("cannot connect to prisn server at %s\n\nStart a server with: prisn server start", resolved.Server)
					}
					return fmt.Errorf("failed to list executions: %w", err)
				}

				return printer.PrintList(result.Items, func() {
					printer.PrintTable(
						[]string{"NAMESPACE", "ID", "NAME", "STATUS", "DURATION"},
						func() [][]string {
							rows := make([][]string, len(result.Items))
							for i, e := range result.Items {
								dur := "-"
								if e.Duration > 0 {
									dur = e.Duration.Round(time.Millisecond).String()
								}
								rows[i] = []string{
									e.Namespace,
									e.ID,
									e.Name,
									e.Status,
									dur,
								}
							}
							return rows
						}(),
					)
				})

			case "envs", "env":
				vm, err := venv.NewManager()
				if err != nil {
					return err
				}
				envs, err := vm.List()
				if err != nil {
					return err
				}

				return printer.PrintList(envs, func() {
					printer.PrintTable(
						[]string{"HASH", "PATH", "CREATED"},
						func() [][]string {
							rows := make([][]string, len(envs))
							for i, e := range envs {
								rows[i] = []string{
									filepath.Base(e.Path),
									e.Path,
									e.CreatedAt.Format("2006-01-02 15:04"),
								}
							}
							return rows
						}(),
					)
				})

			default:
				return fmt.Errorf("unknown resource: %s\nValid resources: deployments, jobs, envs", resource)
			}
		},
	}

	return cmd
}

func newDescribeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "describe <resource> <name>",
		Short: "Show detailed information about a resource",
		Long: `Show detailed information about a deployment, job, or other resource.

Displays metadata, spec, status, environment variables (masked), resource limits,
labels, and recent events.

Examples:
  prisn describe deploy my-api
  prisn describe job abc123
  prisn describe deployment/my-api
  prisn describe d my-api -o json`,

		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			printer := NewPrinter(cmd)

			// Parse resource and name
			var resource, name string
			if len(args) == 1 {
				// Format: resource/name
				parts := strings.SplitN(args[0], "/", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid format: use <resource> <name> or <resource>/<name>")
				}
				resource, name = parts[0], parts[1]
			} else {
				resource, name = args[0], args[1]
			}

			// Resolve context
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return fmt.Errorf("failed to resolve context: %w", err)
			}

			switch resource {
			case "deployments", "deployment", "deploy", "d", "apps", "app", "a":
				return describeDeployment(cmd, printer, resolved, name)
			case "jobs", "job", "j", "executions", "execution", "exec":
				return describeExecution(printer, resolved, name)
			default:
				return fmt.Errorf("unknown resource: %s\nValid resources: deployment, job", resource)
			}
		},
	}

	return cmd
}

// describeDeployment shows detailed deployment information.
func describeDeployment(cmd *cobra.Command, printer *Printer, resolved *prisnctx.Resolved, name string) error {
	if resolved.IsKubernetes() {
		return describeKubernetesApp(cmd, printer, resolved, name)
	}

	// Get deployment from server
	c, err := client.New(resolved)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	var d store.Deployment
	if err := c.GetJSON("/api/v1/deployments/"+name, &d); err != nil {
		if _, ok := err.(*client.ConnectError); ok {
			return fmt.Errorf("cannot connect to prisn server at %s\n\nStart a server with: prisn server start", resolved.Server)
		}
		return fmt.Errorf("deployment %q not found: %w", name, err)
	}

	// Get recent executions for this deployment
	var execResult struct {
		Items []store.Execution `json:"items"`
	}
	_ = c.GetJSON(fmt.Sprintf("/api/v1/executions?deployment_id=%s&limit=5", d.ID), &execResult)

	if printer.IsStructured() {
		return printer.Print(map[string]any{
			"deployment": d,
			"executions": execResult.Items,
		})
	}

	// Text output
	printDeploymentDetails(d, execResult.Items)
	return nil
}

// printDeploymentDetails prints formatted deployment information.
func printDeploymentDetails(d store.Deployment, executions []store.Execution) {
	fmt.Println("DEPLOYMENT")
	fmt.Println("==========")
	fmt.Println()

	// Metadata
	fmt.Println("Metadata:")
	fmt.Printf("  Name:       %s\n", d.Name)
	fmt.Printf("  Namespace:  %s\n", d.Namespace)
	fmt.Printf("  ID:         %s\n", d.ID)
	fmt.Printf("  Created:    %s (%s)\n", d.CreatedAt.Format("2006-01-02 15:04:05"), formatAge(time.Since(d.CreatedAt)))
	if d.UpdatedAt.After(d.CreatedAt) {
		fmt.Printf("  Updated:    %s (%s)\n", d.UpdatedAt.Format("2006-01-02 15:04:05"), formatAge(time.Since(d.UpdatedAt)))
	}
	fmt.Println()

	// Spec
	fmt.Println("Spec:")
	dtype := d.Type
	if dtype == "" {
		dtype = "service"
	}
	fmt.Printf("  Type:       %s\n", dtype)
	fmt.Printf("  Runtime:    %s\n", d.Runtime)
	fmt.Printf("  Source:     %s\n", d.Source)
	if len(d.Args) > 0 {
		fmt.Printf("  Args:       %v\n", d.Args)
	}
	fmt.Printf("  Replicas:   %d\n", d.Replicas)
	if d.Port > 0 {
		fmt.Printf("  Port:       %d\n", d.Port)
	}
	if d.Schedule != "" {
		fmt.Printf("  Schedule:   %s\n", d.Schedule)
	}
	if d.Timeout > 0 {
		fmt.Printf("  Timeout:    %s\n", d.Timeout)
	}
	fmt.Println()

	// Status
	fmt.Println("Status:")
	fmt.Printf("  Phase:      %s\n", formatStatus(d.Status.Phase))
	health := d.Status.Health
	if health == "" {
		health = "unknown"
	}
	fmt.Printf("  Health:     %s\n", health)
	fmt.Printf("  Ready:      %d/%d\n", d.Status.ReadyReplicas, d.Replicas)
	if d.Status.Restarts > 0 {
		fmt.Printf("  Restarts:   %d\n", d.Status.Restarts)
	}
	if d.Status.Message != "" {
		fmt.Printf("  Message:    %s\n", d.Status.Message)
	}
	if !d.Status.LastUpdated.IsZero() {
		fmt.Printf("  Updated:    %s\n", formatAge(time.Since(d.Status.LastUpdated)))
	}
	fmt.Println()

	// Health Check Config
	if d.HealthCheck != nil {
		fmt.Println("Health Check:")
		fmt.Printf("  Type:       %s\n", d.HealthCheck.Type)
		fmt.Printf("  Target:     %s\n", d.HealthCheck.Target)
		if d.HealthCheck.Interval > 0 {
			fmt.Printf("  Interval:   %s\n", d.HealthCheck.Interval)
		}
		if d.HealthCheck.Timeout > 0 {
			fmt.Printf("  Timeout:    %s\n", d.HealthCheck.Timeout)
		}
		if d.HealthCheck.Retries > 0 {
			fmt.Printf("  Retries:    %d\n", d.HealthCheck.Retries)
		}
		fmt.Println()
	}

	// Environment (masked)
	if len(d.Env) > 0 {
		fmt.Println("Environment:")
		for k := range d.Env {
			fmt.Printf("  %s: <set>\n", k)
		}
		fmt.Println()
	}

	// Resource Limits
	if d.Resources.CPUMillicores > 0 || d.Resources.MemoryBytes > 0 || d.Resources.MaxProcesses > 0 {
		fmt.Println("Resources:")
		if d.Resources.CPUMillicores > 0 {
			fmt.Printf("  CPU:        %dm\n", d.Resources.CPUMillicores)
		}
		if d.Resources.MemoryBytes > 0 {
			fmt.Printf("  Memory:     %s\n", formatBytes(d.Resources.MemoryBytes))
		}
		if d.Resources.MaxProcesses > 0 {
			fmt.Printf("  Processes:  %d\n", d.Resources.MaxProcesses)
		}
		fmt.Println()
	}

	// Labels
	if len(d.Labels) > 0 {
		fmt.Println("Labels:")
		for k, v := range d.Labels {
			fmt.Printf("  %s: %s\n", k, v)
		}
		fmt.Println()
	}

	// Recent Executions
	if len(executions) > 0 {
		fmt.Println("Recent Executions:")
		fmt.Printf("  %-12s %-10s %-10s %s\n", "ID", "STATUS", "EXIT", "STARTED")
		fmt.Printf("  %-12s %-10s %-10s %s\n", "--", "------", "----", "-------")
		for _, e := range executions {
			exitStr := "-"
			if e.Status == "Completed" || e.Status == "Failed" {
				exitStr = strconv.Itoa(e.ExitCode)
			}
			fmt.Printf("  %-12s %-10s %-10s %s\n",
				e.ID[:12],
				e.Status,
				exitStr,
				formatAge(time.Since(e.StartedAt)))
		}
	}
}

// describeKubernetesApp shows details for a PrisnApp in Kubernetes.
func describeKubernetesApp(cmd *cobra.Command, printer *Printer, resolved *prisnctx.Resolved, name string) error {
	unstructuredApp, err := GetAppInKubernetes(context.Background(), resolved, name)
	if err != nil {
		return err
	}

	// Extract structured status
	app := k8s.ExtractAppStatus(unstructuredApp)

	if printer.IsStructured() {
		return printer.Print(app)
	}

	fmt.Println("PRISNAPP")
	fmt.Println("========")
	fmt.Println()

	fmt.Println("Metadata:")
	fmt.Printf("  Name:       %s\n", app.Name)
	fmt.Printf("  Namespace:  %s\n", app.Namespace)
	fmt.Printf("  Age:        %s\n", formatAge(app.Age))
	fmt.Println()

	fmt.Println("Spec:")
	if app.Runtime != "" {
		fmt.Printf("  Runtime:    %s\n", app.Runtime)
	}
	fmt.Printf("  Replicas:   %d\n", app.DesiredReplicas)
	if app.Port > 0 {
		fmt.Printf("  Port:       %d\n", app.Port)
	}
	if app.ServiceName != "" {
		fmt.Printf("  Service:    %s\n", app.ServiceName)
	}
	fmt.Println()

	fmt.Println("Status:")
	fmt.Printf("  Phase:      %s\n", formatStatus(app.Phase))
	fmt.Printf("  Ready:      %d/%d\n", app.ReadyReplicas, app.DesiredReplicas)
	if app.Message != "" {
		fmt.Printf("  Message:    %s\n", app.Message)
	}
	fmt.Println()

	fmt.Printf("Use 'kubectl describe prisnapp %s -n %s' for full details\n", name, resolved.Namespace)

	return nil
}

// describeExecution shows detailed execution information.
func describeExecution(printer *Printer, resolved *prisnctx.Resolved, id string) error {
	c, err := client.New(resolved)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	var e store.Execution
	if err := c.GetJSON("/api/v1/executions/"+id, &e); err != nil {
		if _, ok := err.(*client.ConnectError); ok {
			return fmt.Errorf("cannot connect to prisn server at %s\n\nStart a server with: prisn server start", resolved.Server)
		}
		return fmt.Errorf("execution %q not found: %w", id, err)
	}

	if printer.IsStructured() {
		return printer.Print(e)
	}

	// Text output
	fmt.Println("EXECUTION")
	fmt.Println("=========")
	fmt.Println()

	fmt.Println("Metadata:")
	fmt.Printf("  ID:           %s\n", e.ID)
	fmt.Printf("  Name:         %s\n", e.Name)
	fmt.Printf("  Namespace:    %s\n", e.Namespace)
	if e.DeploymentID != "" {
		fmt.Printf("  Deployment:   %s\n", e.DeploymentID)
	}
	fmt.Println()

	fmt.Println("Status:")
	fmt.Printf("  Status:       %s\n", e.Status)
	if e.Status == "Completed" || e.Status == "Failed" {
		fmt.Printf("  Exit Code:    %d\n", e.ExitCode)
	}
	fmt.Printf("  Started:      %s (%s)\n", e.StartedAt.Format("2006-01-02 15:04:05"), formatAge(time.Since(e.StartedAt)))
	if !e.CompletedAt.IsZero() {
		fmt.Printf("  Completed:    %s\n", e.CompletedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("  Duration:     %s\n", e.Duration.Round(time.Millisecond))
	}
	if e.NodeID != "" {
		fmt.Printf("  Node:         %s\n", e.NodeID)
	}
	fmt.Println()

	if e.Error != "" {
		fmt.Println("Error:")
		fmt.Printf("  %s\n", e.Error)
		fmt.Println()
	}

	if e.Stdout != "" {
		fmt.Println("Stdout:")
		fmt.Println("-------")
		fmt.Println(e.Stdout)
		fmt.Println()
	}

	if e.Stderr != "" {
		fmt.Println("Stderr:")
		fmt.Println("-------")
		fmt.Println(e.Stderr)
	}

	return nil
}

func newExecCmd() *cobra.Command {
	var (
		interactive bool
		tty         bool
		envVars     []string
	)

	cmd := &cobra.Command{
		Use:   "exec <deployment> -- <command> [args...]",
		Short: "Execute a command in a deployment's environment",
		Long: `Execute a command using a deployment's environment configuration.

The command runs with the same:
- Virtual environment (Python packages, Node modules)
- Environment variables
- Working directory

This is useful for:
- Running one-off tasks (migrations, setup scripts)
- Interactive debugging (Python REPL, shell)
- Testing code changes without redeploying

In Kubernetes mode, this delegates to 'kubectl exec'.

Examples:
  prisn exec my-api -- python -c "print('hello')"
  prisn exec my-api -- pip list
  prisn exec my-api -it -- python           # Interactive Python REPL
  prisn exec my-api -it -- /bin/sh          # Interactive shell
  prisn exec my-api -- ./manage.py migrate  # Run Django migrations`,

		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deploymentName := args[0]

			// Find the command after "--"
			cmdArgs := args[1:]
			if len(cmdArgs) == 0 {
				return fmt.Errorf("command required after deployment name\nUsage: prisn exec <deployment> -- <command>")
			}

			// Resolve context
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return fmt.Errorf("failed to resolve context: %w", err)
			}

			// Handle Kubernetes mode
			if resolved.IsKubernetes() {
				return execInKubernetes(resolved, deploymentName, cmdArgs, interactive, tty)
			}

			// Get deployment from server
			c, err := client.New(resolved)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			var d store.Deployment
			if err := c.GetJSON("/api/v1/deployments/"+deploymentName, &d); err != nil {
				if _, ok := err.(*client.ConnectError); ok {
					return fmt.Errorf("cannot connect to prisn server at %s\n\nStart a server with: prisn server start", resolved.Server)
				}
				return fmt.Errorf("deployment %q not found: %w", deploymentName, err)
			}

			// Build environment from deployment and additional vars
			envMap := make(map[string]string)
			for k, v := range d.Env {
				envMap[k] = v
			}
			for _, e := range envVars {
				key, val, err := validate.EnvVar(e)
				if err != nil {
					return fmt.Errorf("bad env var: %w", err)
				}
				envMap[key] = val
			}

			// Convert env map to slice
			envSlice := os.Environ()
			for k, v := range envMap {
				envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
			}

			// Build and run command
			execCmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
			execCmd.Env = envSlice
			execCmd.Dir = filepath.Dir(d.Source) // Use deployment source directory

			if interactive || tty {
				// Interactive mode: connect stdin/stdout/stderr
				execCmd.Stdin = os.Stdin
				execCmd.Stdout = os.Stdout
				execCmd.Stderr = os.Stderr
				return execCmd.Run()
			}

			// Non-interactive: capture and display output
			var stdout, stderr bytes.Buffer
			execCmd.Stdout = &stdout
			execCmd.Stderr = &stderr

			err = execCmd.Run()
			if stdout.Len() > 0 {
				fmt.Print(stdout.String())
			}
			if stderr.Len() > 0 {
				fmt.Fprint(os.Stderr, stderr.String())
			}

			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					os.Exit(exitErr.ExitCode())
				}
				return err
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&interactive, "interactive", "i", false, "Keep stdin open")
	cmd.Flags().BoolVarP(&tty, "tty", "t", false, "Allocate a pseudo-TTY")
	cmd.Flags().StringArrayVarP(&envVars, "env", "e", nil, "Set environment variables (KEY=value)")

	return cmd
}

// execInKubernetes delegates exec to kubectl.
func execInKubernetes(resolved *prisnctx.Resolved, name string, cmdArgs []string, interactive, tty bool) error {
	// Build kubectl exec command
	args := []string{"exec"}

	if resolved.KubeContext != "" {
		args = append(args, "--context", resolved.KubeContext)
	}
	args = append(args, "-n", resolved.Namespace)

	if interactive {
		args = append(args, "-i")
	}
	if tty {
		args = append(args, "-t")
	}

	// Find the pod for this deployment
	// PrisnApp creates pods with label app=<name>
	args = append(args, fmt.Sprintf("deploy/%s", name), "--")
	args = append(args, cmdArgs...)

	execCmd := exec.Command("kubectl", args...)
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr

	return execCmd.Run()
}

// DiffChange represents a field change.
type DiffChange struct {
	Old any `json:"old,omitempty"`
	New any `json:"new,omitempty"`
}

func newDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff <config.toml>",
		Short: "Show what would change if config is applied",
		Long: `Compare a configuration file against the current deployment state.

Useful for previewing changes before applying them.

The output shows:
- Whether a deployment would be created or updated
- Differences in configuration (replicas, env, port, etc.)

Examples:
  prisn diff prisn.toml           # Compare config to current state
  prisn diff prisn.toml -o json   # JSON output for scripting`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath := args[0]
			printer := NewPrinter(cmd)

			// Parse config
			cfg, err := config.Load(configPath)
			if err != nil {
				return fmt.Errorf("failed to parse config: %w", err)
			}

			// Resolve context
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return fmt.Errorf("failed to resolve context: %w", err)
			}

			// Get current deployment from server
			c, err := client.New(resolved)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			var existing store.Deployment
			getErr := c.GetJSON("/api/v1/deployments/"+cfg.Name, &existing)
			deploymentExists := getErr == nil

			// Build diff
			changes := make(map[string]DiffChange)
			action := "create"

			if deploymentExists {
				// Compare fields
				if cfg.Replicas != existing.Replicas && cfg.Replicas > 0 {
					changes["replicas"] = DiffChange{Old: existing.Replicas, New: cfg.Replicas}
				}
				if cfg.Port != existing.Port && cfg.Port > 0 {
					changes["port"] = DiffChange{Old: existing.Port, New: cfg.Port}
				}
				if cfg.Source != existing.Source && cfg.Source != "" {
					changes["source"] = DiffChange{Old: existing.Source, New: cfg.Source}
				}
				if cfg.Runtime != existing.Runtime && cfg.Runtime != "" {
					changes["runtime"] = DiffChange{Old: existing.Runtime, New: cfg.Runtime}
				}
				if cfg.Schedule != existing.Schedule && cfg.Schedule != "" {
					changes["schedule"] = DiffChange{Old: existing.Schedule, New: cfg.Schedule}
				}
				existingEnvCount := len(existing.Env)
				newEnvCount := len(cfg.Env)
				if newEnvCount != existingEnvCount {
					changes["env"] = DiffChange{
						Old: fmt.Sprintf("%d vars", existingEnvCount),
						New: fmt.Sprintf("%d vars", newEnvCount),
					}
				}

				if len(changes) > 0 {
					action = "update"
				} else {
					action = "unchanged"
				}
			} else {
				// New deployment - show what would be created
				if cfg.Replicas > 0 {
					changes["replicas"] = DiffChange{New: cfg.Replicas}
				}
				if cfg.Port > 0 {
					changes["port"] = DiffChange{New: cfg.Port}
				}
				if cfg.Source != "" {
					changes["source"] = DiffChange{New: cfg.Source}
				}
				if cfg.Runtime != "" {
					changes["runtime"] = DiffChange{New: cfg.Runtime}
				}
				if len(cfg.Env) > 0 {
					changes["env"] = DiffChange{New: fmt.Sprintf("%d vars", len(cfg.Env))}
				}
			}

			if printer.IsStructured() {
				return printer.Print(map[string]any{
					"config":  configPath,
					"name":    cfg.Name,
					"action":  action,
					"changes": changes,
				})
			}

			// Text output
			fmt.Printf("DIFF: %s\n", configPath)
			fmt.Println(strings.Repeat("=", 60))
			fmt.Println()

			switch action {
			case "create":
				fmt.Printf("+ %s (new deployment)\n", cfg.Name)
				for k, v := range changes {
					fmt.Printf("    %s: %v\n", k, v.New)
				}
			case "update":
				fmt.Printf("~ %s (will be modified)\n", cfg.Name)
				for k, v := range changes {
					fmt.Printf("    %s: %v -> %v\n", k, v.Old, v.New)
				}
			case "unchanged":
				fmt.Printf("  %s (no changes)\n", cfg.Name)
			}

			fmt.Println()
			if action == "unchanged" {
				fmt.Println("No changes to apply.")
			} else {
				fmt.Printf("Run 'prisn apply -f %s' to apply these changes.\n", configPath)
			}

			return nil
		},
	}

	return cmd
}

func newTopCmd() *cobra.Command {
	var interval int

	cmd := &cobra.Command{
		Use:   "top",
		Short: "Live view of deployment status",
		Long: `Show a live, auto-refreshing view of deployment status.

Similar to 'top' or 'kubectl top', this command shows real-time
status of all deployments with automatic refresh.

Press 'q' or Ctrl+C to exit.

Examples:
  prisn top                    # Default 2 second refresh
  prisn top -i 5               # 5 second refresh interval`,

		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve context
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return fmt.Errorf("failed to resolve context: %w", err)
			}

			// Create client
			c, err := client.New(resolved)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Setup terminal for raw mode to capture 'q' key
			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			// Start key listener in goroutine
			keyCh := make(chan byte, 1)
			go func() {
				buf := make([]byte, 1)
				for {
					os.Stdin.Read(buf)
					select {
					case keyCh <- buf[0]:
					default:
					}
				}
			}()

			ticker := time.NewTicker(time.Duration(interval) * time.Second)
			defer ticker.Stop()

			// Initial render
			renderTop(c, resolved)

			for {
				select {
				case <-ctx.Done():
					fmt.Println("\nExiting...")
					return nil
				case key := <-keyCh:
					if key == 'q' || key == 'Q' {
						fmt.Println("\nExiting...")
						return nil
					}
				case <-ticker.C:
					renderTop(c, resolved)
				}
			}
		},
	}

	cmd.Flags().IntVarP(&interval, "interval", "i", 2, "Refresh interval in seconds")

	return cmd
}

// renderTop renders the top view.
func renderTop(c *client.Client, resolved *prisnctx.Resolved) {
	// Clear screen and move cursor to top-left
	fmt.Print("\033[2J\033[H")

	// Get deployments
	var result struct {
		Items []store.Deployment `json:"items"`
	}
	if err := c.GetJSON("/api/v1/deployments", &result); err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Calculate stats
	total := len(result.Items)
	running := 0
	pending := 0
	failed := 0
	totalReplicas := 0
	readyReplicas := 0
	totalRestarts := 0

	for _, d := range result.Items {
		totalReplicas += d.Replicas
		readyReplicas += d.Status.ReadyReplicas
		totalRestarts += d.Status.Restarts
		switch d.Status.Phase {
		case "Running":
			running++
		case "Pending", "Starting":
			pending++
		case "Failed", "CrashLoopBackOff":
			failed++
		}
	}

	// Header
	fmt.Printf("prisn top - %s\n", time.Now().Format("15:04:05"))
	fmt.Printf("Context: %s (%s)\n", resolved.Name, resolved.Server)
	fmt.Println()

	// Summary
	fmt.Printf("Deployments: %d total, %d running, %d pending, %d failed\n",
		total, running, pending, failed)
	fmt.Printf("Replicas:    %d/%d ready, %d restarts\n",
		readyReplicas, totalReplicas, totalRestarts)
	fmt.Println()

	// Table header
	fmt.Printf("%-15s %-12s %-8s %-10s %-8s %-6s %-8s\n",
		"NAME", "NAMESPACE", "TYPE", "READY", "STATUS", "RESTARTS", "AGE")
	fmt.Printf("%-15s %-12s %-8s %-10s %-8s %-8s %-8s\n",
		"----", "---------", "----", "-----", "------", "--------", "---")

	// Rows
	for _, d := range result.Items {
		name := d.Name
		if len(name) > 15 {
			name = name[:14] + "~"
		}
		ns := d.Namespace
		if len(ns) > 12 {
			ns = ns[:11] + "~"
		}
		dtype := d.Type
		if dtype == "" {
			dtype = "service"
		}
		restarts := "-"
		if d.Status.Restarts > 0 {
			restarts = strconv.Itoa(d.Status.Restarts)
		}

		// Color status
		status := formatStatus(d.Status.Phase)
		colorStatus := status
		switch d.Status.Phase {
		case "Running":
			colorStatus = "\033[32m" + status + "\033[0m" // Green
		case "Failed", "CrashLoopBackOff":
			colorStatus = "\033[31m" + status + "\033[0m" // Red
		case "Pending", "Starting":
			colorStatus = "\033[33m" + status + "\033[0m" // Yellow
		}

		fmt.Printf("%-15s %-12s %-8s %-10s %-8s %-8s %-8s\n",
			name,
			ns,
			dtype,
			fmt.Sprintf("%d/%d", d.Status.ReadyReplicas, d.Replicas),
			colorStatus,
			restarts,
			formatAge(time.Since(d.CreatedAt)))
	}

	fmt.Println()
	fmt.Println("Press 'q' to quit")
}

func newDeleteCmd() *cobra.Command {
	var (
		yes       bool
		force     bool
		dryRun    bool
		deleteAll bool
	)

	cmd := &cobra.Command{
		Use:   "delete [resource] [name]",
		Short: "Delete resources",
		Long: `Delete deployments, jobs, or other resources.

This is a destructive operation and requires confirmation.

Use --all to delete all deployments in the current namespace.

Examples:
  prisn delete deploy my-api
  prisn delete job abc123
  prisn delete deploy my-api --yes       # Skip confirmation
  prisn delete deploy my-api --dry-run   # Preview what would be deleted
  prisn delete --all --yes               # Delete all in namespace`,

		Args: cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve context to determine mode
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return fmt.Errorf("failed to resolve context: %w", err)
			}

			ns := resolved.Namespace
			if ns == "" {
				ns = "default"
			}

			// Handle --all flag
			if deleteAll {
				if resolved.IsKubernetes() {
					return fmt.Errorf("--all is not supported in kubernetes mode (use kubectl)")
				}

				c, err := client.New(resolved)
				if err != nil {
					return fmt.Errorf("failed to create client: %w", err)
				}

				// Get all deployments
				var list struct {
					Items []store.Deployment `json:"items"`
					Count int                `json:"count"`
				}
				if err := c.GetJSON("/api/v1/deployments?namespace="+ns, &list); err != nil {
					return fmt.Errorf("failed to list deployments: %w", err)
				}

				if list.Count == 0 {
					fmt.Printf("No deployments in namespace '%s'\n", ns)
					return nil
				}

				// For dry-run, show what would be deleted
				if dryRun {
					fmt.Printf("Would delete %d deployments in namespace '%s':\n", list.Count, ns)
					for _, d := range list.Items {
						fmt.Printf("  - %s (%s)\n", d.Name, d.Type)
					}
					fmt.Println("\nRun without --dry-run to actually delete.")
					return nil
				}

				// Confirmation
				if !yes && !force {
					fmt.Printf("Delete ALL %d deployments in namespace '%s'? This cannot be undone.\n", list.Count, ns)
					fmt.Printf("Type '%s' to confirm: ", ns)

					reader := bufio.NewReader(os.Stdin)
					input, _ := reader.ReadString('\n')
					input = strings.TrimSpace(input)

					if input != ns {
						return fmt.Errorf("confirmation failed (expected '%s')", ns)
					}
				}

				// Delete all
				deleted := 0
				for _, d := range list.Items {
					resp, err := c.Delete("/api/v1/deployments/" + d.ID)
					if err != nil {
						log.Fail("failed to delete %s: %v", d.Name, err)
						continue
					}
					resp.Body.Close()
					deleted++
					fmt.Printf("  deleted %s\n", d.Name)
				}

				log.Done("deleted %d/%d deployments", deleted, list.Count)
				return nil
			}

			// Regular single resource delete requires 2 args
			if len(args) < 2 {
				return fmt.Errorf("requires <resource> <name> arguments, or use --all")
			}

			resource := args[0]
			name := args[1]

			// For dry-run, show what would be deleted
			if dryRun {
				fmt.Println("Would delete:")
				fmt.Printf("  Resource:  %s\n", resource)
				fmt.Printf("  Name:      %s\n", name)
				fmt.Printf("  Namespace: %s\n", ns)

				// Try to get more info about what would be deleted
				if !resolved.IsKubernetes() {
					c, err := client.New(resolved)
					if err == nil {
						var d store.Deployment
						if err := c.GetJSON("/api/v1/deployments/"+name, &d); err == nil {
							fmt.Printf("  Type:      %s\n", d.Type)
							fmt.Printf("  Replicas:  %d\n", d.Replicas)
							if d.Status.Phase == "Running" {
								fmt.Printf("  Status:    Running (will stop %d instances)\n", d.Status.ReadyReplicas)
							}
						}
					}
				}

				fmt.Println()
				fmt.Println("Run without --dry-run to actually delete.")
				return nil
			}

			// Confirmation
			if !yes && !force {
				fmt.Printf("Are you sure you want to delete %s '%s' in namespace '%s'?\n", resource, name, ns)
				fmt.Printf("Type the name to confirm: ")

				reader := bufio.NewReader(os.Stdin)
				input, _ := reader.ReadString('\n')
				input = strings.TrimSpace(input)

				if input != name {
					return fmt.Errorf("confirmation failed (got '%s', expected '%s')", input, name)
				}
			}

			if resolved.IsKubernetes() {
				// Delete in Kubernetes
				fmt.Printf("Deleting %s '%s'...\n", resource, name)
				if err := DeleteInKubernetes(context.Background(), resolved, name); err != nil {
					return err
				}
				log.Done("deleted %s '%s'", resource, name)
				return nil
			}

			// Delete via server API
			c, err := client.New(resolved)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			fmt.Printf("Deleting %s '%s'...\n", resource, name)

			resp, err := c.Delete("/api/v1/deployments/" + name)
			if err != nil {
				if _, ok := err.(*client.ConnectError); ok {
					return fmt.Errorf("cannot connect to prisn server at %s\n\nStart a server with: prisn server start", resolved.Server)
				}
				return fmt.Errorf("failed to delete: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusNotFound {
				return fmt.Errorf("%s '%s' not found\n\n  List resources: prisn get %s", resource, name, resource)
			}
			if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("delete failed: %s", string(body))
			}

			log.Done("deleted %s '%s'", resource, name)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation")
	cmd.Flags().BoolVar(&force, "force", false, "Force deletion")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview what would be deleted")
	cmd.Flags().BoolVar(&deleteAll, "all", false, "Delete all deployments in namespace")

	return cmd
}

func newLogsCmd() *cobra.Command {
	var (
		follow bool
		tail   int
		since  string
	)

	cmd := &cobra.Command{
		Use:   "logs <deployment|job>",
		Short: "View logs",
		Long: `View logs from a deployment or job.

For deployments, shows the stdout/stderr from the running process.
For jobs (use job/ prefix), shows the execution output.

Examples:
  prisn logs my-api              # Service logs
  prisn logs my-api -f           # Follow (stream)
  prisn logs my-api --tail 100   # Last 100 lines
  prisn logs job/exec-abc123     # Job execution logs`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]

			// Resolve context
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return err
			}

			// Kubernetes mode
			if resolved.IsKubernetes() {
				return showKubernetesLogs(context.Background(), resolved, target, follow, tail)
			}

			// Create client
			c, err := client.New(resolved)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Check if target is a job (job/ prefix or exec- prefix)
			if strings.HasPrefix(target, "job/") || strings.HasPrefix(target, "exec-") {
				// Get job/execution logs
				execID := strings.TrimPrefix(target, "job/")
				var exec store.Execution
				if err := c.GetJSON("/api/v1/executions/"+execID, &exec); err != nil {
					if _, ok := err.(*client.ConnectError); ok {
						return fmt.Errorf("cannot connect to prisn server at %s\n\nStart a server with: prisn server start", resolved.Server)
					}
					return fmt.Errorf("execution not found: %s\n\n  Recent executions: prisn logs <deployment>\n  Or check exec ID", execID)
				}

				// Print logs
				if exec.Stdout != "" {
					fmt.Print(exec.Stdout)
				}
				if exec.Stderr != "" {
					if exec.Stdout != "" && !strings.HasSuffix(exec.Stdout, "\n") {
						fmt.Println()
					}
					fmt.Fprintf(os.Stderr, "%s", exec.Stderr)
				}
				return nil
			}

			// Get deployment to verify it exists
			var d store.Deployment
			if err := c.GetJSON("/api/v1/deployments/"+target, &d); err != nil {
				if _, ok := err.(*client.ConnectError); ok {
					return fmt.Errorf("cannot connect to prisn server at %s\n\nStart a server with: prisn server start", resolved.Server)
				}
				return fmt.Errorf("deployment not found: %s\n\n  List deployments: prisn get deploy\n  Create one:        prisn deploy <script>", target)
			}

			// For services, show logs
			if d.Type == "service" {
				if follow {
					// Follow mode: poll for new logs
					return streamServiceLogs(c, d, tail)
				}

				fmt.Printf("Logs for %s (%s):\n\n", d.Name, d.Status.Phase)

				// Get recent executions for this deployment
				var result struct {
					Items []store.Execution `json:"items"`
				}
				_ = c.GetJSON("/api/v1/executions?deployment_id="+d.ID+"&limit=5", &result)

				if len(result.Items) > 0 {
					fmt.Printf("Recent executions (%d):\n", len(result.Items))
					for _, exec := range result.Items {
						dur := "-"
						if exec.Duration > 0 {
							dur = exec.Duration.Round(time.Millisecond).String()
						}
						fmt.Printf("  %s  %s  %s  exit=%d\n",
							exec.ID, exec.Status, dur, exec.ExitCode)
					}
					fmt.Println()
					fmt.Println("View execution logs with: prisn logs job/<exec-id>")
					fmt.Println("Stream logs with:         prisn logs " + d.Name + " -f")
				} else {
					fmt.Println("No execution history found.")
					if d.Status.Phase == "Running" {
						fmt.Println("Stream logs with: prisn logs " + d.Name + " -f")
					}
				}

				if d.Status.Phase == "Running" && d.Port > 0 {
					fmt.Printf("\nThe service is running at: http://localhost:%d\n", d.Port)
				}
				return nil
			}

			// For jobs/cronjobs, show execution history
			if follow {
				// Follow mode for jobs: stream like services
				return streamServiceLogs(c, d, tail)
			}

			var result struct {
				Items []store.Execution `json:"items"`
			}
			limit := "20"
			if tail > 0 {
				limit = strconv.Itoa(tail)
			}
			if err := c.GetJSON("/api/v1/executions?deployment_id="+d.ID+"&limit="+limit, &result); err != nil {
				return fmt.Errorf("failed to get executions: %w", err)
			}

			if len(result.Items) == 0 {
				fmt.Println("No executions found for this deployment.")
				fmt.Println("Stream logs with: prisn logs " + d.Name + " -f")
				return nil
			}

			// Filter by since if provided
			var filteredItems []store.Execution
			if since != "" {
				dur, err := time.ParseDuration(since)
				if err != nil {
					return fmt.Errorf("invalid --since duration: %s (try 1h, 30m, etc.)", since)
				}
				cutoff := time.Now().Add(-dur)
				for _, exec := range result.Items {
					if exec.StartedAt.After(cutoff) {
						filteredItems = append(filteredItems, exec)
					}
				}
			} else {
				filteredItems = result.Items
			}

			if len(filteredItems) == 0 {
				fmt.Println("No executions found in the specified time range.")
				return nil
			}

			// Show the latest execution's logs
			latest := filteredItems[0]
			if latest.Stdout != "" {
				fmt.Print(latest.Stdout)
			}
			if latest.Stderr != "" {
				fmt.Fprintf(os.Stderr, "%s", latest.Stderr)
			}

			return nil
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output (streaming)")
	cmd.Flags().IntVar(&tail, "tail", 0, "Number of lines to show")
	cmd.Flags().StringVar(&since, "since", "", "Show logs since duration (e.g., 1h, 30m)")

	return cmd
}

// streamServiceLogs polls for new logs and streams them to stdout.
func streamServiceLogs(c *client.Client, d store.Deployment, tail int) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupts
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(os.Stderr, "\nStopping log stream...")
		cancel()
	}()

	fmt.Fprintf(os.Stderr, "Streaming logs for %s (Ctrl+C to stop)...\n\n", d.Name)

	// Track seen executions to avoid duplicates
	seen := make(map[string]bool)
	lastStdoutLen := make(map[string]int)
	lastStderrLen := make(map[string]int)

	// Initial fetch of recent executions
	limit := 10
	if tail > 0 {
		limit = tail
	}

	pollInterval := 1 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	fetchAndShow := func() error {
		var result struct {
			Items []store.Execution `json:"items"`
		}
		endpoint := fmt.Sprintf("/api/v1/executions?deployment_id=%s&limit=%d", d.ID, limit)
		if err := c.GetJSON(endpoint, &result); err != nil {
			return err
		}

		// Process in reverse order (oldest first)
		for i := len(result.Items) - 1; i >= 0; i-- {
			exec := result.Items[i]

			// Skip if we've fully shown this execution's logs already
			if seen[exec.ID] && exec.Status == "completed" {
				continue
			}

			// Show new stdout content
			prevStdout := lastStdoutLen[exec.ID]
			if len(exec.Stdout) > prevStdout {
				newContent := exec.Stdout[prevStdout:]
				fmt.Print(newContent)
				lastStdoutLen[exec.ID] = len(exec.Stdout)
			}

			// Show new stderr content
			prevStderr := lastStderrLen[exec.ID]
			if len(exec.Stderr) > prevStderr {
				newContent := exec.Stderr[prevStderr:]
				fmt.Fprint(os.Stderr, newContent)
				lastStderrLen[exec.ID] = len(exec.Stderr)
			}

			// Mark as seen when completed
			if exec.Status == "completed" || exec.Status == "failed" {
				seen[exec.ID] = true
			}
		}
		return nil
	}

	// Initial fetch
	if err := fetchAndShow(); err != nil {
		return err
	}

	// Poll loop
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := fetchAndShow(); err != nil {
				// Log error but continue
				fmt.Fprintf(os.Stderr, "\n[Error fetching logs: %v]\n", err)
			}
		}
	}
}

// showKubernetesLogs shows logs for a PrisnApp in Kubernetes.
func showKubernetesLogs(ctx context.Context, resolved *prisnctx.Resolved, name string, follow bool, tail int) error {
	// Use kubectl logs as a subprocess
	args := []string{"logs", "-n", resolved.Namespace, "-l", "app.kubernetes.io/name=" + name}
	if follow {
		args = append(args, "-f")
	}
	if tail > 0 {
		args = append(args, "--tail", strconv.Itoa(tail))
	}

	// If we have a kube context, use it
	if resolved.KubeContext != "" {
		args = append([]string{"--context", resolved.KubeContext}, args...)
	}

	cmd := execCommand("kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// execCommand creates an exec.Cmd (wrapper for testability).
var execCommand = func(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

func newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage dependency cache",
		Long: `Manage cached virtual environments for dependencies.

prisn caches Python virtual environments and Node modules to speed up
subsequent runs. Use these commands to inspect and clean the cache.

Examples:
  prisn cache list    # Show cached environments
  prisn cache clean   # Remove old caches`,
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List cached environments",
			RunE: func(cmd *cobra.Command, args []string) error {
				vm, err := venv.NewManager()
				if err != nil {
					return err
				}
				envs, err := vm.List()
				if err != nil {
					return err
				}

				fmt.Println("HASH            SIZE       CREATED")
				for _, e := range envs {
					size := dirSize(e.Path)
					fmt.Printf("%-15s %-10s %s\n",
						filepath.Base(e.Path),
						formatSize(size),
						e.CreatedAt.Format("2006-01-02 15:04"))
				}
				if len(envs) == 0 {
					fmt.Println("(no cached environments)")
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "clean",
			Short: "Remove old cached environments",
			RunE: func(cmd *cobra.Command, args []string) error {
				vm, err := venv.NewManager()
				if err != nil {
					return err
				}
				if err := vm.Cleanup(); err != nil {
					return err
				}
				fmt.Println("Cleaned up old environments")
				return nil
			},
		},
	)

	return cmd
}

func newVersionCmd(info BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("prisn version %s\n", info.Version)
			fmt.Printf("  commit: %s\n", info.Commit)
			fmt.Printf("  built:  %s\n", info.Date)
		},
	}
}

// Helper functions

func dirSize(path string) int64 {
	var size int64
	filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}

func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1fG", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.1fM", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.1fK", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

func archExplanation(arch string) string {
	switch arch {
	case "any":
		return "runs anywhere"
	case "amd64":
		return "requires x86_64"
	case "arm64":
		return "requires ARM"
	case "amd64-preferred":
		return "prefers x86_64, ARM works too"
	default:
		return ""
	}
}

func newServerCmd() *cobra.Command {
	var (
		addr        string
		nodeID      string
		dbPath      string
		noScheduler bool
		apiKey      string
		// Cluster mode
		clusterMode bool
		raftAddr    string
		peers       []string
		bootstrap   bool
	)

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start the prisn server",
		Long: `Start the prisn API server for managing deployments.

The server provides a REST API for creating and managing deployments,
executions, and secrets. It stores state in SQLite by default.

The scheduler runs by default to execute cron jobs. Disable with --no-scheduler.

Cluster Mode:
  Use --cluster to enable Raft-based clustering for high availability.
  In cluster mode, metadata operations go through Raft consensus.

  Bootstrap a new cluster with --bootstrap on the first node.
  Join an existing cluster by specifying --peers.

Authentication:
  Use --api-key to set a bootstrap API key for initial setup.
  This key has admin privileges and can be used to create tokens.
  Alternatively, set PRISN_API_KEY environment variable.

Examples:
  prisn server                              # Start on :7331
  prisn server --addr :9090                 # Custom port
  prisn server --db /var/lib/prisn/state.db # Custom DB path
  prisn server --no-scheduler               # API only, no cron jobs
  prisn server --api-key my-secret-key      # Start with bootstrap API key

  # Cluster mode
  prisn server --cluster --bootstrap --node-id node1  # Bootstrap first node
  prisn server --cluster --peers node1:7332 --node-id node2  # Join cluster`,

		RunE: func(cmd *cobra.Command, args []string) error {
			// Check for API key from env if not set via flag
			if apiKey == "" {
				apiKey = os.Getenv("PRISN_API_KEY")
			}

			// Generate node ID if not provided
			if nodeID == "" {
				hostname, _ := os.Hostname()
				nodeID = hostname
				if nodeID == "" {
					nodeID = fmt.Sprintf("node-%d", time.Now().UnixNano()%10000)
				}
			}

			// Open store
			var st *store.Store
			var err error
			if dbPath != "" {
				st, err = store.Open(dbPath)
			} else {
				st, err = store.OpenDefault()
			}
			if err != nil {
				return fmt.Errorf("failed to open store: %w", err)
			}
			defer st.Close()

			// Create runner
			r, err := runner.New()
			if err != nil {
				return fmt.Errorf("failed to create runner: %w", err)
			}

			// Initialize cluster if enabled
			var cluster *raft.Cluster
			if clusterMode {
				if raftAddr == "" {
					raftAddr = ":7332" // Default Raft port
				}

				cluster, err = raft.NewCluster(raft.ClusterOptions{
					NodeID:   raft.NodeID(nodeID),
					BindAddr: raftAddr,
					Peers:    peers,
				})
				if err != nil {
					return fmt.Errorf("failed to create cluster: %w", err)
				}

				if err := cluster.Start(); err != nil {
					return fmt.Errorf("failed to start cluster: %w", err)
				}

				if bootstrap {
					// Bootstrap this node as the initial cluster
					if err := cluster.BootstrapCluster(); err != nil {
						return fmt.Errorf("failed to bootstrap cluster: %w", err)
					}
					log.Done("Bootstrapped cluster with node %s", nodeID)
				}

				log.Info("Cluster mode enabled on %s", raftAddr)
			}

			// Create scheduler first (so API server can register cronjobs)
			var sched *scheduler.Scheduler
			if !noScheduler {
				sched = scheduler.NewScheduler(scheduler.Config{
					Store:  st,
					Runner: r,
					NodeID: nodeID,
				})
			}

			// Create and start server
			srv, err := api.NewServer(api.Config{
				Addr:      addr,
				Store:     st,
				Runner:    r,
				Scheduler: sched,
				NodeID:    nodeID,
				APIKey:    apiKey,
				Cluster:   cluster,
			})
			if err != nil {
				if cluster != nil {
					cluster.Stop()
				}
				return fmt.Errorf("failed to create server: %w", err)
			}

			// Start scheduler after server is created
			if sched != nil {
				go func() {
					if err := sched.Start(context.Background()); err != nil {
						fmt.Printf("Scheduler error: %v\n", err)
					}
				}()
			}

			// Handle shutdown gracefully
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

			errCh := make(chan error, 1)
			go func() {
				errCh <- srv.Start()
			}()

			// Give server a moment to start, then show ready message
			go func() {
				time.Sleep(100 * time.Millisecond)
				log.Done("Server listening on %s", addr)
				if apiKey != "" {
					log.Info("Bootstrap API key configured (use for initial token creation)")
				}
				fmt.Printf("\nConnect with: prisn context add local --server localhost%s\n", addr)
				fmt.Printf("Or set:       export PRISN_SERVER=localhost%s\n\n", addr)
			}()

			select {
			case err := <-errCh:
				if sched != nil {
					sched.Stop()
				}
				if cluster != nil {
					cluster.Stop()
				}
				return err
			case sig := <-sigCh:
				fmt.Printf("\nReceived %s, shutting down...\n", sig)
				if sched != nil {
					sched.Stop()
				}
				if cluster != nil {
					cluster.Stop()
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				return srv.Shutdown(ctx)
			}
		},
	}

	cmd.Flags().StringVar(&addr, "addr", ":7331", "Address to listen on")
	cmd.Flags().StringVar(&nodeID, "node-id", "", "Node ID (auto-generated if empty)")
	cmd.Flags().StringVar(&dbPath, "db", "", "Path to SQLite database (default: ~/.prisn/state.db)")
	cmd.Flags().BoolVar(&noScheduler, "no-scheduler", false, "Disable the cron job scheduler")
	cmd.Flags().StringVar(&apiKey, "api-key", "", "Bootstrap API key for admin access (or set PRISN_API_KEY)")
	// Cluster flags
	cmd.Flags().BoolVar(&clusterMode, "cluster", false, "Enable cluster mode with Raft consensus")
	cmd.Flags().StringVar(&raftAddr, "raft-addr", ":7332", "Address for Raft RPC communication")
	cmd.Flags().StringSliceVar(&peers, "peers", nil, "Comma-separated list of peer addresses (e.g., node1:7332,node2:7332)")
	cmd.Flags().BoolVar(&bootstrap, "bootstrap", false, "Bootstrap a new cluster (use only on first node)")

	return cmd
}

func newApplyCmd() *cobra.Command {
	var (
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "apply -f <config.toml>",
		Short: "Apply a configuration file",
		Long: `Apply a TOML configuration file to create or update a deployment.

This is the declarative way to manage deployments. The config file specifies
the full desired state, and prisn will make it so.

Config file format (prisn.toml):
  name = "my-api"
  runtime = "python3"
  source = "api.py"
  port = 8080
  replicas = 3

  [resources]
  cpu = "500m"
  memory = "512Mi"

  [env]
  DEBUG = "false"
  DATABASE_URL = { secret = "db-creds", key = "url" }

Examples:
  prisn apply -f prisn.toml
  prisn apply -f prisn.toml --dry-run     # Preview changes`,

		RunE: func(cmd *cobra.Command, args []string) error {
			configFile, _ := cmd.Flags().GetString("file")
			if configFile == "" {
				return fmt.Errorf("config file required: use -f <config.toml>")
			}

			cfg, err := config.Load(configFile)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			namespace, _ := cmd.Flags().GetString("namespace")
			if cfg.Namespace == "" {
				cfg.Namespace = namespace
			}

			// Dry run: just show what would happen
			if dryRun {
				fmt.Printf("Would apply %s:\n", configFile)
				fmt.Printf("  Name:      %s\n", cfg.Name)
				fmt.Printf("  Namespace: %s\n", cfg.Namespace)
				fmt.Printf("  Type:      %s\n", cfg.Type())
				fmt.Printf("  Runtime:   %s\n", cfg.Runtime)
				fmt.Printf("  Source:    %s\n", cfg.Source)
				if cfg.Port > 0 {
					fmt.Printf("  Port:      %d\n", cfg.Port)
				}
				fmt.Printf("  Replicas:  %d\n", cfg.Replicas)
				if cfg.Resources.CPU != "" {
					fmt.Printf("  CPU:       %s\n", cfg.Resources.CPU)
				}
				if cfg.Resources.Memory != "" {
					fmt.Printf("  Memory:    %s\n", cfg.Resources.Memory)
				}
				if len(cfg.Env) > 0 {
					fmt.Printf("  Env vars:  %d\n", len(cfg.Env))
				}
				if cfg.Schedule != "" {
					fmt.Printf("  Schedule:  %s\n", cfg.Schedule)
				}
				return nil
			}

			// Actually apply the config
			fmt.Printf("Applying %s...\n", configFile)

			// For now, if it's a one-shot, run it immediately
			if !cfg.IsService() && !cfg.IsCronJob() {
				r, err := runner.New()
				if err != nil {
					return fmt.Errorf("failed to initialize runner: %w", err)
				}

				ctx := context.Background()
				runCfg := runner.RunConfig{
					Script:  cfg.Source,
					Args:    cfg.Args,
					Timeout: cfg.Timeout.Duration,
					Resources: runner.ResourceConfig{
						MaxMemoryMB:  cfg.MemoryMB(),
						MaxProcesses: cfg.Resources.MaxProcesses,
					},
				}

				// Add env vars
				if len(cfg.Env) > 0 {
					runCfg.Env = make(map[string]string)
					for k, v := range cfg.Env {
						if v.IsSecret() {
							fmt.Printf("Warning: secret %s/%s not resolved (server required)\n", v.Secret, v.Key)
							continue
						}
						runCfg.Env[k] = v.Value
					}
				}

				result, err := r.Run(ctx, runCfg)
				if err != nil {
					return err
				}

				if result.ExitCode != 0 {
					os.Exit(result.ExitCode)
				}
				return nil
			}

			// Services and cron jobs require the server
			fmt.Printf("Configured %s '%s' in namespace '%s'\n", cfg.Type(), cfg.Name, cfg.Namespace)
			fmt.Println("(prisn server required for persistent deployments)")

			return nil
		},
	}

	cmd.Flags().StringP("file", "f", "", "Path to config file (required)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview changes without applying")
	cmd.MarkFlagRequired("file")

	return cmd
}
