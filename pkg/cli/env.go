package cli

import (
	"fmt"
	"strings"

	"github.com/lajosnagyuk/prisn/pkg/client"
	prisnctx "github.com/lajosnagyuk/prisn/pkg/context"
	"github.com/lajosnagyuk/prisn/pkg/store"
	"github.com/lajosnagyuk/prisn/pkg/validate"
	"github.com/spf13/cobra"
)

func newEnvCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "env",
		Short: "Manage deployment environment variables",
		Long: `Commands for managing environment variables on deployments.

Environment variables are set per-deployment and can be updated without
redeploying the entire application.

Examples:
  prisn env set api LOG_LEVEL=debug DB_HOST=localhost
  prisn env get api
  prisn env unset api DEBUG`,
	}

	cmd.AddCommand(
		newEnvSetCmd(),
		newEnvGetCmd(),
		newEnvUnsetCmd(),
	)

	return cmd
}

func newEnvSetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <deployment> KEY=value...",
		Short: "Set environment variables for a deployment",
		Long: `Set one or more environment variables on a deployment.

The deployment will be restarted to apply the changes.

Examples:
  prisn env set api LOG_LEVEL=debug
  prisn env set api DB_HOST=localhost DB_PORT=5432`,

		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deploymentName := args[0]
			envVars := args[1:]

			// Parse env vars
			newEnv := make(map[string]string)
			for _, e := range envVars {
				key, val, err := validate.EnvVar(e)
				if err != nil {
					return fmt.Errorf("invalid env var %q: %w", e, err)
				}
				newEnv[key] = val
			}

			// Resolve context
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return fmt.Errorf("failed to resolve context: %w", err)
			}

			// Get deployment
			c, err := client.New(resolved)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			var d store.Deployment
			if err := c.GetJSON("/api/v1/deployments/"+deploymentName, &d); err != nil {
				return fmt.Errorf("deployment %q not found: %w", deploymentName, err)
			}

			// Merge env vars
			if d.Env == nil {
				d.Env = make(map[string]string)
			}
			for k, v := range newEnv {
				d.Env[k] = v
			}

			// Update deployment
			updateReq := map[string]any{
				"env": d.Env,
			}
			if err := c.PutJSON("/api/v1/deployments/"+d.ID, updateReq, nil); err != nil {
				return fmt.Errorf("failed to update deployment: %w", err)
			}

			fmt.Printf("< set %d env var(s) on %s\n", len(newEnv), deploymentName)
			for k := range newEnv {
				fmt.Printf("  %s\n", k)
			}

			return nil
		},
	}

	return cmd
}

func newEnvGetCmd() *cobra.Command {
	var showValues bool

	cmd := &cobra.Command{
		Use:   "get <deployment>",
		Short: "Show environment variables for a deployment",
		Long: `Show all environment variables set on a deployment.

By default, values are masked. Use --show-values to see actual values.

Examples:
  prisn env get api
  prisn env get api --show-values`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deploymentName := args[0]

			// Resolve context
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return fmt.Errorf("failed to resolve context: %w", err)
			}

			// Get deployment
			c, err := client.New(resolved)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			var d store.Deployment
			if err := c.GetJSON("/api/v1/deployments/"+deploymentName, &d); err != nil {
				return fmt.Errorf("deployment %q not found: %w", deploymentName, err)
			}

			if len(d.Env) == 0 {
				fmt.Printf("No environment variables set on %s\n", deploymentName)
				return nil
			}

			fmt.Printf("Environment variables for %s:\n", deploymentName)
			fmt.Println()
			for k, v := range d.Env {
				if showValues {
					fmt.Printf("  %s=%s\n", k, v)
				} else {
					masked := strings.Repeat("*", min(len(v), 8))
					fmt.Printf("  %s=%s\n", k, masked)
				}
			}

			if !showValues {
				fmt.Println()
				fmt.Println("(use --show-values to see actual values)")
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&showValues, "show-values", false, "Show actual values (not masked)")

	return cmd
}

func newEnvUnsetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unset <deployment> KEY...",
		Short: "Remove environment variables from a deployment",
		Long: `Remove one or more environment variables from a deployment.

The deployment will be restarted to apply the changes.

Examples:
  prisn env unset api DEBUG
  prisn env unset api LOG_LEVEL DB_DEBUG`,

		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			deploymentName := args[0]
			keysToRemove := args[1:]

			// Resolve context
			flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
			flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			resolved, err := prisnctx.Resolve(flagContext, flagNamespace)
			if err != nil {
				return fmt.Errorf("failed to resolve context: %w", err)
			}

			// Get deployment
			c, err := client.New(resolved)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			var d store.Deployment
			if err := c.GetJSON("/api/v1/deployments/"+deploymentName, &d); err != nil {
				return fmt.Errorf("deployment %q not found: %w", deploymentName, err)
			}

			// Remove keys
			removed := 0
			for _, k := range keysToRemove {
				if _, exists := d.Env[k]; exists {
					delete(d.Env, k)
					removed++
				}
			}

			if removed == 0 {
				fmt.Printf("No matching env vars found on %s\n", deploymentName)
				return nil
			}

			// Update deployment
			updateReq := map[string]any{
				"env": d.Env,
			}
			if err := c.PutJSON("/api/v1/deployments/"+d.ID, updateReq, nil); err != nil {
				return fmt.Errorf("failed to update deployment: %w", err)
			}

			fmt.Printf("< removed %d env var(s) from %s\n", removed, deploymentName)
			for _, k := range keysToRemove {
				fmt.Printf("  %s\n", k)
			}

			return nil
		},
	}

	return cmd
}
