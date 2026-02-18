package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/lajosnagyuk/prisn/pkg/context"
	"github.com/lajosnagyuk/prisn/pkg/log"
	"github.com/spf13/cobra"
)

func newContextCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage server contexts",
		Long: `Manage connections to multiple prisn servers.

A context stores server address, namespace, and authentication credentials.
Use contexts to switch between local development and production clusters.

Examples:
  prisn context list              # List all contexts
  prisn context current           # Show current context
  prisn context use prod          # Switch to prod context
  prisn context add staging ...   # Add a new context`,
	}

	cmd.AddCommand(
		newContextAddCmd(),
		newContextUseCmd(),
		newContextListCmd(),
		newContextCurrentCmd(),
		newContextDeleteCmd(),
		newContextRenameCmd(),
	)

	return cmd
}

func newContextAddCmd() *cobra.Command {
	var (
		mode        string
		server      string
		kubeContext string
		namespace   string
		token       string
		tlsCA       string
		tlsCert     string
		tlsKey      string
		insecure    bool
		setCurrent  bool
	)

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a new context",
		Long: `Add a new server context to the configuration.

Modes:
  remote      Connect to a prisn API server (default)
  kubernetes  Create CRDs in a Kubernetes cluster
  local       Run scripts locally

Examples:
  prisn context add local --mode local
  prisn context add dev --server localhost:7331
  prisn context add prod --server prod.example.com:7331 --token sk_live_...
  prisn context add k8s --mode kubernetes --kube-context minikube
  prisn context add staging --server staging.internal:7331 --insecure`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			// Validate name
			if err := validateContextName(name); err != nil {
				return err
			}

			// Validate mode
			var ctxMode context.Mode
			switch mode {
			case "", "remote":
				ctxMode = context.ModeRemote
				if server == "" {
					return fmt.Errorf("--server is required for remote mode")
				}
			case "kubernetes", "k8s":
				ctxMode = context.ModeKubernetes
			case "local":
				ctxMode = context.ModeLocal
			default:
				return fmt.Errorf("invalid mode %q: use 'local', 'kubernetes', or 'remote'", mode)
			}

			cfg, err := context.Load("")
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if cfg.HasContext(name) {
				return fmt.Errorf("context %q already exists\nUse 'prisn context delete %s' first, or choose a different name", name, name)
			}

			ctx := &context.Context{
				Name:        name,
				Mode:        ctxMode,
				Server:      server,
				KubeContext: kubeContext,
				Namespace:   namespace,
				Token:       token,
			}

			// Configure TLS if any TLS options are set
			if tlsCA != "" || tlsCert != "" || insecure {
				ctx.TLS = context.TLSConfig{
					Enabled:  true,
					CAFile:   tlsCA,
					CertFile: tlsCert,
					KeyFile:  tlsKey,
					Insecure: insecure,
				}
			}

			cfg.SetContext(ctx)

			// Set as current if requested or if this is the first context
			if setCurrent || cfg.CurrentContext == "" {
				cfg.CurrentContext = name
			}

			if err := context.Save(cfg, ""); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			log.OK("Added context %q", name)
			log.Info("  Mode:      %s", ctxMode)
			if server != "" {
				log.Info("  Server:    %s", server)
			}
			if kubeContext != "" {
				log.Info("  KubeCtx:   %s", kubeContext)
			}
			log.Info("  Namespace: %s", namespace)
			if token != "" {
				log.Info("  Token:     %s", maskToken(token))
			}
			if cfg.CurrentContext == name {
				log.Info("  Status:    current")
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&mode, "mode", "", "Connection mode: local, kubernetes, remote (default: remote)")
	cmd.Flags().StringVar(&server, "server", "", "Server address (required for remote mode)")
	cmd.Flags().StringVar(&kubeContext, "kube-context", "", "Kubeconfig context to use (for kubernetes mode)")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "Default namespace")
	cmd.Flags().StringVar(&token, "token", "", "Authentication token")
	cmd.Flags().StringVar(&tlsCA, "tls-ca", "", "TLS CA certificate file")
	cmd.Flags().StringVar(&tlsCert, "tls-cert", "", "TLS client certificate file (for mTLS)")
	cmd.Flags().StringVar(&tlsKey, "tls-key", "", "TLS client key file (for mTLS)")
	cmd.Flags().BoolVar(&insecure, "insecure", false, "Skip TLS certificate verification")
	cmd.Flags().BoolVar(&setCurrent, "set-current", false, "Set as current context")

	return cmd
}

func newContextUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Switch to a different context",
		Long: `Set the current context for subsequent commands.

Examples:
  prisn context use prod
  prisn context use local`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			cfg, err := context.Load("")
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			ctx := cfg.GetContext(name)
			if ctx == nil {
				names := cfg.ContextNames()
				if len(names) == 0 {
					return fmt.Errorf("context %q not found (no contexts configured)\nAdd one with: prisn context add %s --server <address>", name, name)
				}
				return fmt.Errorf("context %q not found\nAvailable contexts: %s", name, strings.Join(names, ", "))
			}

			cfg.CurrentContext = name
			if err := context.Save(cfg, ""); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			log.OK("Switched to context %q", name)
			// Determine effective mode
			mode := ctx.Mode
			if mode == "" {
				// Infer mode from other fields for backward compatibility
				if ctx.Server != "" {
					mode = context.ModeRemote
				} else if ctx.KubeContext != "" {
					mode = context.ModeKubernetes
				} else {
					mode = context.ModeLocal
				}
			}
			log.Info("  Mode:      %s", mode)
			if mode == context.ModeKubernetes {
				kubeCtx := ctx.KubeContext
				if kubeCtx == "" {
					kubeCtx = "(current kubeconfig context)"
				}
				log.Info("  KubeCtx:   %s", kubeCtx)
			} else if ctx.Server != "" {
				log.Info("  Server:    %s", ctx.Server)
			}
			log.Info("  Namespace: %s", ctx.Namespace)
			return nil
		},
	}
}

func newContextListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List all contexts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := context.Load("")
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if len(cfg.Contexts) == 0 {
				fmt.Println("No contexts configured.")
				fmt.Println()
				fmt.Println("Add one with:")
				fmt.Println("  prisn context add local --server localhost:7331")
				fmt.Println("  prisn context add prod --server prod.example.com:7331 --token <token>")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "CURRENT\tNAME\tMODE\tSERVER/KUBE-CONTEXT\tNAMESPACE")

			for _, name := range cfg.ContextNames() {
				ctx := cfg.Contexts[name]
				current := ""
				if name == cfg.CurrentContext {
					current = "*"
				}
				mode := string(ctx.Mode)
				if mode == "" {
					mode = "remote"
				}
				target := ctx.Server
				if ctx.Mode == context.ModeKubernetes {
					target = ctx.KubeContext
					if target == "" {
						target = "(current)"
					}
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", current, name, mode, target, ctx.Namespace)
			}
			w.Flush()
			return nil
		},
	}
}

func newContextCurrentCmd() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "current",
		Short: "Show current context",
		Long: `Display the currently active context.

Use -v to see detailed information including resolution source.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Get the --context flag from root if set
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")

			resolved, err := context.Resolve(flagContext, flagNamespace)
			if err != nil {
				return err
			}

			if verbose {
				fmt.Printf("Context:   %s\n", resolved.Name)
				mode := string(resolved.Mode)
				if mode == "" {
					mode = "local"
				}
				fmt.Printf("Mode:      %s\n", mode)
				if resolved.Mode == context.ModeKubernetes {
					kubeCtx := resolved.KubeContext
					if kubeCtx == "" {
						kubeCtx = "(current kubeconfig context)"
					}
					fmt.Printf("KubeCtx:   %s\n", kubeCtx)
				} else if resolved.Server != "" {
					fmt.Printf("Server:    %s\n", resolved.Server)
				}
				fmt.Printf("Namespace: %s\n", resolved.Namespace)
				fmt.Printf("Source:    %s\n", resolved.Source)
				if resolved.Token != "" {
					fmt.Printf("Token:     %s\n", resolved.MaskedToken())
				}
				if resolved.TLS.Enabled {
					fmt.Printf("TLS:       enabled\n")
					if resolved.TLS.Insecure {
						fmt.Printf("           (insecure - cert verification disabled)\n")
					}
					if resolved.TLS.CAFile != "" {
						fmt.Printf("           CA: %s\n", resolved.TLS.CAFile)
					}
				}
			} else {
				fmt.Println(resolved.Name)
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show detailed information")
	return cmd
}

func newContextDeleteCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm", "remove"},
		Short:   "Delete a context",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			cfg, err := context.Load("")
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if !cfg.HasContext(name) {
				return fmt.Errorf("context %q not found", name)
			}

			if !yes {
				fmt.Printf("Delete context %q? [y/N] ", name)
				reader := bufio.NewReader(os.Stdin)
				response, _ := reader.ReadString('\n')
				response = strings.TrimSpace(strings.ToLower(response))
				if response != "y" && response != "yes" {
					fmt.Println("Cancelled")
					return nil
				}
			}

			wasCurrent := cfg.CurrentContext == name
			cfg.DeleteContext(name)

			if wasCurrent {
				cfg.CurrentContext = ""
				// Try to set a new current context
				names := cfg.ContextNames()
				if len(names) > 0 {
					cfg.CurrentContext = names[0]
				}
			}

			if err := context.Save(cfg, ""); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			log.OK("Deleted context %q", name)
			if wasCurrent {
				if cfg.CurrentContext != "" {
					log.Info("Switched to context %q", cfg.CurrentContext)
				} else {
					log.Warn("No contexts remaining. Add one with: prisn context add <name> --server <address>")
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation")
	return cmd
}

func newContextRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <old-name> <new-name>",
		Short: "Rename a context",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldName, newName := args[0], args[1]

			if err := validateContextName(newName); err != nil {
				return err
			}

			cfg, err := context.Load("")
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			if err := cfg.RenameContext(oldName, newName); err != nil {
				return err
			}

			if err := context.Save(cfg, ""); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			log.OK("Renamed context %q to %q", oldName, newName)
			return nil
		},
	}
}

// validateContextName checks if a context name is valid.
func validateContextName(name string) error {
	if name == "" {
		return fmt.Errorf("context name cannot be empty")
	}
	if strings.ContainsAny(name, " \t\n\r./\\:") {
		return fmt.Errorf("invalid context name %q: must not contain spaces, slashes, colons, or path separators", name)
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("invalid context name %q: must not start with a dash", name)
	}
	return nil
}

// maskToken masks the middle of a token for display.
func maskToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) <= 12 {
		return "****"
	}
	return token[:4] + "..." + token[len(token)-4:]
}
