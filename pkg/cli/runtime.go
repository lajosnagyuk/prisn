// Package cli provides the command-line interface for prisn.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/lajosnagyuk/prisn/pkg/log"
	"github.com/lajosnagyuk/prisn/pkg/runtime"
	"github.com/spf13/cobra"
)

func newRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Manage script runtimes",
		Long: `Manage available script runtimes.

prisn supports any interpreter through configurable runtimes.
Built-in runtimes include Python, Node.js, Bash, Ruby, Perl, PHP, and more.

Custom runtimes can be added via ~/.prisn/runtimes.toml.

Examples:
  prisn runtime list                   # List available runtimes
  prisn runtime info python3           # Show runtime details
  prisn runtime check python3          # Check if runtime is installed
  prisn runtime detect script.py       # Detect runtime for a script
  prisn runtime push zig               # Push runtime to cluster
  prisn runtime pull                   # Pull runtimes from cluster`,
	}

	cmd.AddCommand(newRuntimeListCmd())
	cmd.AddCommand(newRuntimeInfoCmd())
	cmd.AddCommand(newRuntimeCheckCmd())
	cmd.AddCommand(newRuntimeDetectCmd())
	cmd.AddCommand(newRuntimePushCmd())
	cmd.AddCommand(newRuntimePullCmd())
	cmd.AddCommand(newRuntimeRemoteCmd())

	return cmd
}

func newRuntimeListCmd() *cobra.Command {
	var showBuiltin bool
	var showCustom bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available runtimes",
		Long: `List all available script runtimes.

Shows built-in runtimes and any custom runtimes defined in ~/.prisn/runtimes.toml.

Examples:
  prisn runtime list              # List all runtimes
  prisn runtime list --builtin    # List only built-in runtimes
  prisn runtime list --custom     # List only custom runtimes`,

		RunE: func(cmd *cobra.Command, args []string) error {
			registry := runtime.DefaultRegistry

			runtimes := registry.List()
			sort.Slice(runtimes, func(i, j int) bool {
				return runtimes[i].Name < runtimes[j].Name
			})

			if len(runtimes) == 0 {
				fmt.Println("No runtimes available")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tEXTENSIONS\tSTATUS")

			for _, rt := range runtimes {
				// Filter based on flags
				if showBuiltin && !rt.Builtin {
					continue
				}
				if showCustom && rt.Builtin {
					continue
				}

				// Check if installed
				status := "not installed"
				if _, err := registry.Check(rt); err == nil {
					status = "installed"
				}

				exts := strings.Join(rt.Extensions, ", ")
				if len(exts) > 20 {
					exts = exts[:17] + "..."
				}

				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", rt.ID, rt.Name, exts, status)
			}
			w.Flush()

			return nil
		},
	}

	cmd.Flags().BoolVar(&showBuiltin, "builtin", false, "Show only built-in runtimes")
	cmd.Flags().BoolVar(&showCustom, "custom", false, "Show only custom runtimes")

	return cmd
}

func newRuntimeInfoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "info <runtime>",
		Short: "Show runtime details",
		Long: `Show detailed information about a runtime.

Examples:
  prisn runtime info python3
  prisn runtime info babashka
  prisn runtime info node`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := runtime.DefaultRegistry
			runtimeID := args[0]

			rt, ok := registry.Get(runtimeID)
			if !ok {
				return fmt.Errorf("unknown runtime: %s\n\n  Run 'prisn runtime list' to see available runtimes", runtimeID)
			}

			fmt.Printf("Runtime: %s\n", rt.Name)
			fmt.Printf("ID: %s\n", rt.ID)
			fmt.Printf("Command: %s\n", rt.Command)
			fmt.Printf("Args: %s\n", strings.Join(rt.Args, " "))
			fmt.Printf("Extensions: %s\n", strings.Join(rt.Extensions, ", "))

			if len(rt.ShebangPatterns) > 0 {
				fmt.Printf("Shebang Patterns: %s\n", strings.Join(rt.ShebangPatterns, ", "))
			}

			if rt.Builtin {
				fmt.Printf("Type: built-in\n")
			} else {
				fmt.Printf("Type: custom\n")
			}

			// Check installation status
			fmt.Printf("\nInstallation Status:\n")
			if path, err := registry.Check(rt); err == nil {
				fmt.Printf("  Status: installed\n")
				fmt.Printf("  Path: %s\n", path)
			} else {
				fmt.Printf("  Status: not installed\n")
				if rt.Detect.Check != "" {
					fmt.Printf("  Check: %s\n", rt.Detect.Check)
				}

				// Show install hints
				hints := registry.GetInstallHints(rt)
				if len(hints) > 0 {
					fmt.Printf("\nInstallation hints:\n")
					for _, hint := range hints {
						fmt.Printf("  %s\n", hint)
					}
				}
			}

			// Dependency management
			fmt.Printf("\nDependency Management:\n")
			if !rt.Deps.Enabled {
				fmt.Printf("  Enabled: no\n")
			} else {
				fmt.Printf("  Enabled: yes\n")
				if rt.Deps.SelfManaged {
					fmt.Printf("  Mode: self-managed (runtime handles deps)\n")
				} else {
					fmt.Printf("  Mode: prisn-managed\n")
				}
				if len(rt.Deps.ManifestFiles) > 0 {
					fmt.Printf("  Manifest files: %s\n", strings.Join(rt.Deps.ManifestFiles, ", "))
				}
				if rt.Deps.EnvType != "" && rt.Deps.EnvType != "none" {
					fmt.Printf("  Environment type: %s\n", rt.Deps.EnvType)
				}
			}

			return nil
		},
	}

	return cmd
}

func newRuntimeCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check <runtime>",
		Short: "Check if a runtime is installed",
		Long: `Check if a runtime is installed and working.

Examples:
  prisn runtime check python3
  prisn runtime check node
  prisn runtime check babashka`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := runtime.DefaultRegistry
			runtimeID := args[0]

			rt, ok := registry.Get(runtimeID)
			if !ok {
				return fmt.Errorf("unknown runtime: %s\n\n  Run 'prisn runtime list' to see available runtimes", runtimeID)
			}

			path, err := registry.Check(rt)
			if err != nil {
				log.Fail("%s is not installed", rt.Name)

				// Show install hints
				hints := registry.GetInstallHints(rt)
				if len(hints) > 0 {
					fmt.Fprintf(os.Stderr, "\nTo install:\n")
					for _, hint := range hints {
						fmt.Fprintf(os.Stderr, "  %s\n", hint)
					}
				}

				return fmt.Errorf("runtime not found: %s", runtimeID)
			}

			log.Done("%s is installed: %s", rt.Name, path)
			return nil
		},
	}

	return cmd
}

func newRuntimeDetectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "detect <script>",
		Short: "Detect runtime for a script",
		Long: `Detect which runtime would be used for a script.

This shows the runtime that would be selected based on file extension
or shebang line.

Examples:
  prisn runtime detect script.py
  prisn runtime detect server.js
  prisn runtime detect task.bb`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := runtime.DefaultRegistry
			scriptPath := args[0]

			// Resolve path
			if !filepath.IsAbs(scriptPath) {
				cwd, _ := os.Getwd()
				scriptPath = filepath.Join(cwd, scriptPath)
			}

			// Check file exists
			if _, err := os.Stat(scriptPath); err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("script not found: %s", args[0])
				}
				return fmt.Errorf("cannot access script: %w", err)
			}

			rt, err := registry.GetByFile(scriptPath)
			if err != nil {
				return fmt.Errorf("cannot detect runtime: %w", err)
			}

			fmt.Printf("Script: %s\n", args[0])
			fmt.Printf("Runtime: %s (%s)\n", rt.Name, rt.ID)
			fmt.Printf("Command: %s %s\n", rt.Command, strings.Join(rt.Args, " "))

			// Check if installed
			if _, err := registry.Check(rt); err != nil {
				fmt.Printf("\nWarning: %s is not installed\n", rt.Name)
				hints := registry.GetInstallHints(rt)
				if len(hints) > 0 {
					fmt.Printf("\nTo install:\n")
					for _, hint := range hints {
						fmt.Printf("  %s\n", hint)
					}
				}
			}

			return nil
		},
	}

	return cmd
}

func newRuntimePushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push <runtime-id>",
		Short: "Push a local runtime to the cluster",
		Long: `Push a local runtime definition to the cluster.

This uploads a runtime from ~/.prisn/runtimes.toml (or built-in) to the
connected server, making it available for remote executions.

Examples:
  prisn runtime push zig          # Push custom runtime
  prisn runtime push python3      # Push built-in (for customization)`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := runtime.DefaultRegistry
			runtimeID := args[0]

			rt, ok := registry.Get(runtimeID)
			if !ok {
				return fmt.Errorf("unknown runtime: %s\n\n  Run 'prisn runtime list' to see available runtimes", runtimeID)
			}

			// Get client
			c, err := getClient(cmd)
			if err != nil {
				return fmt.Errorf("server connection required: %w", err)
			}

			// Build request
			req := map[string]any{
				"id":               rt.ID,
				"name":             rt.Name,
				"command":          rt.Command,
				"args":             rt.Args,
				"extensions":       rt.Extensions,
				"env":              rt.Env,
				"shebang_patterns": rt.ShebangPatterns,
				"detect": map[string]any{
					"check":         rt.Detect.Check,
					"version_regex": rt.Detect.VersionRegex,
					"min_version":   rt.Detect.MinVersion,
				},
				"install": map[string]any{
					"macos":   rt.Install.Commands["macos"],
					"linux":   rt.Install.Commands["linux"],
					"windows": rt.Install.Commands["windows"],
					"script":  rt.Install.Script,
					"doc_url": rt.Install.DocURL,
					"note":    rt.Install.Note,
				},
				"deps": map[string]any{
					"enabled":         rt.Deps.Enabled,
					"manifest_files":  rt.Deps.ManifestFiles,
					"parser":          rt.Deps.Parser,
					"install_command": rt.Deps.InstallCommand,
					"self_managed":    rt.Deps.SelfManaged,
					"env_type":        rt.Deps.EnvType,
				},
			}

			var resp map[string]any
			if err := c.PostJSON("/api/v1/runtimes", req, &resp); err != nil {
				return fmt.Errorf("failed to push runtime: %w", err)
			}

			log.Done("Runtime %s pushed to cluster", rt.Name)
			return nil
		},
	}

	return cmd
}

func newRuntimePullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull",
		Short: "List runtimes available on the cluster",
		Long: `List runtime definitions stored in the cluster.

These are custom runtimes that have been pushed to the cluster and
are available for remote executions.

Examples:
  prisn runtime pull              # List cluster runtimes`,

		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := getClient(cmd)
			if err != nil {
				return fmt.Errorf("server connection required: %w", err)
			}

			var resp struct {
				Items []struct {
					ID         string   `json:"id"`
					Name       string   `json:"name"`
					Command    string   `json:"command"`
					Extensions []string `json:"extensions"`
				} `json:"items"`
				Count int `json:"count"`
			}

			if err := c.GetJSON("/api/v1/runtimes", &resp); err != nil {
				return fmt.Errorf("failed to list cluster runtimes: %w", err)
			}

			if resp.Count == 0 {
				fmt.Println("No custom runtimes in cluster")
				fmt.Println("\nPush a runtime with: prisn runtime push <runtime-id>")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tCOMMAND\tEXTENSIONS")

			for _, rt := range resp.Items {
				exts := strings.Join(rt.Extensions, ", ")
				if len(exts) > 20 {
					exts = exts[:17] + "..."
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", rt.ID, rt.Name, rt.Command, exts)
			}
			w.Flush()

			return nil
		},
	}

	return cmd
}

func newRuntimeRemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote <subcommand>",
		Short: "Manage runtimes on the cluster",
		Long: `Manage runtime definitions stored in the cluster.

Subcommands:
  list    - List runtimes on the cluster (alias for 'prisn runtime pull')
  delete  - Delete a runtime from the cluster`,
	}

	cmd.AddCommand(newRuntimeRemoteDeleteCmd())

	return cmd
}

func newRuntimeRemoteDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <runtime-id>",
		Short: "Delete a runtime from the cluster",
		Long: `Delete a custom runtime definition from the cluster.

This removes the runtime from the cluster. It does not affect local runtimes.

Examples:
  prisn runtime remote delete zig`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runtimeID := args[0]

			c, err := getClient(cmd)
			if err != nil {
				return fmt.Errorf("server connection required: %w", err)
			}

			if _, err := c.Delete("/api/v1/runtimes/" + runtimeID); err != nil {
				return fmt.Errorf("failed to delete runtime: %w", err)
			}

			log.Done("Runtime %s deleted from cluster", runtimeID)
			return nil
		},
	}

	return cmd
}
