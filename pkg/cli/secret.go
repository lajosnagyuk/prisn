package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/client"
	"github.com/lajosnagyuk/prisn/pkg/log"
	"github.com/lajosnagyuk/prisn/pkg/store"
	"github.com/spf13/cobra"
)

func newSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage secrets",
		Long: `Commands for managing secrets.

Secrets store sensitive data like API keys, passwords, and certificates.
They can be referenced in prisn.toml or passed to deployments.

Examples:
  prisn secret create db-creds --from-literal url=postgres://...
  prisn secret create app-env --from-env-file .env.prod
  prisn secret list
  prisn secret get db-creds
  prisn secret delete db-creds`,
	}

	cmd.AddCommand(
		newSecretCreateCmd(),
		newSecretListCmd(),
		newSecretGetCmd(),
		newSecretDeleteCmd(),
	)

	return cmd
}

func newSecretCreateCmd() *cobra.Command {
	var (
		fromLiteral []string
		fromFile    []string
		fromEnvFile string
	)

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new secret",
		Long: `Create a new secret with key-value pairs.

Data can be provided via:
  --from-literal KEY=value     Add a key-value pair
  --from-file KEY=path         Add key with file contents as value
  --from-env-file path         Add all key-values from a .env file

Examples:
  prisn secret create db-creds --from-literal url=postgres://user:pass@host/db
  prisn secret create certs --from-file tls.crt=./cert.pem --from-file tls.key=./key.pem
  prisn secret create app-env --from-env-file .env.prod`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			c, err := getClient(cmd)
			if err != nil {
				return err
			}

			// Build data map
			data := make(map[string]string)

			// Process --from-literal
			for _, lit := range fromLiteral {
				key, val, ok := strings.Cut(lit, "=")
				if !ok {
					return fmt.Errorf("invalid --from-literal format: %s (expected KEY=value)", lit)
				}
				data[key] = val
			}

			// Process --from-file
			for _, f := range fromFile {
				key, path, ok := strings.Cut(f, "=")
				if !ok {
					return fmt.Errorf("invalid --from-file format: %s (expected KEY=path)", f)
				}
				content, err := os.ReadFile(path)
				if err != nil {
					return fmt.Errorf("failed to read file %s: %w", path, err)
				}
				data[key] = string(content)
			}

			// Process --from-env-file
			if fromEnvFile != "" {
				envData, err := parseEnvFile(fromEnvFile)
				if err != nil {
					return fmt.Errorf("failed to parse env file: %w", err)
				}
				for k, v := range envData {
					data[k] = v
				}
			}

			if len(data) == 0 {
				return fmt.Errorf("no data provided: use --from-literal, --from-file, or --from-env-file")
			}

			// Get namespace from flag
			namespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			if namespace == "" {
				namespace = "default"
			}

			req := map[string]any{
				"name":      name,
				"namespace": namespace,
				"data":      data,
			}

			var result struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Keys int    `json:"keys"`
			}

			if err := c.PostJSON("/api/v1/secrets", req, &result); err != nil {
				if client.IsOffline(err) {
					client.HandleOfflineError(err)
					return nil
				}
				if strings.Contains(err.Error(), "409") || strings.Contains(err.Error(), "already exists") {
					return fmt.Errorf("secret %s/%s already exists", namespace, name)
				}
				return fmt.Errorf("failed to create secret: %w", err)
			}

			log.Done("created secret %s/%s (%d keys)", namespace, name, len(data))
			return nil
		},
	}

	cmd.Flags().StringArrayVar(&fromLiteral, "from-literal", nil, "Add key-value pair (KEY=value)")
	cmd.Flags().StringArrayVar(&fromFile, "from-file", nil, "Add key from file (KEY=path)")
	cmd.Flags().StringVar(&fromEnvFile, "from-env-file", "", "Add all keys from .env file")

	return cmd
}

func newSecretListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List secrets",
		Long: `List all secrets in the current namespace.

Note: Only shows secret names and keys, not values.

Examples:
  prisn secret list
  prisn secret list -n production`,

		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := getClient(cmd)
			if err != nil {
				return err
			}

			namespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			if namespace == "" {
				namespace = "default"
			}

			path := "/api/v1/secrets"
			if namespace != "" {
				path += "?namespace=" + namespace
			}

			var resp struct {
				Items []struct {
					ID        string    `json:"id"`
					Name      string    `json:"name"`
					Namespace string    `json:"namespace"`
					Keys      []string  `json:"keys"`
					CreatedAt time.Time `json:"created_at"`
				} `json:"items"`
				Count int `json:"count"`
			}

			if err := c.GetJSON(path, &resp); err != nil {
				if client.IsOffline(err) {
					client.HandleOfflineError(err)
					return nil
				}
				return fmt.Errorf("failed to list secrets: %w", err)
			}

			printer := NewPrinter(cmd)
			return printer.PrintList(resp.Items, func() {
				if resp.Count == 0 {
					fmt.Println("No secrets found")
					return
				}

				// Print table header
				fmt.Printf("%-20s %-15s %-30s %-5s\n", "NAME", "NAMESPACE", "KEYS", "AGE")
				fmt.Println(strings.Repeat("-", 72))

				for _, s := range resp.Items {
					keys := strings.Join(s.Keys, ",")
					if len(keys) > 30 {
						keys = keys[:27] + "..."
					}

					name := s.Name
					if len(name) > 20 {
						name = name[:17] + "..."
					}

					age := formatAge(time.Since(s.CreatedAt))
					fmt.Printf("%-20s %-15s %-30s %-5s\n", name, s.Namespace, keys, age)
				}

				fmt.Printf("\nTotal: %d secrets\n", resp.Count)
			})
		},
	}

	return cmd
}

func newSecretGetCmd() *cobra.Command {
	var showValues bool

	cmd := &cobra.Command{
		Use:   "get <name>",
		Short: "Get secret details",
		Long: `Get details of a secret.

By default, only shows key names (not values) for security.
Use --show-values to display actual values.

Examples:
  prisn secret get db-creds
  prisn secret get db-creds --show-values`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			c, err := getClient(cmd)
			if err != nil {
				return err
			}

			namespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			if namespace == "" {
				namespace = "default"
			}

			var sec store.Secret
			endpoint := fmt.Sprintf("/api/v1/secrets/%s?namespace=%s", name, namespace)
			if err := c.GetJSON(endpoint, &sec); err != nil {
				if strings.Contains(err.Error(), "not found") {
					return fmt.Errorf("secret not found: %s/%s\n\n  Create one: prisn secret create %s --from-literal KEY=value", namespace, name, name)
				}
				return err
			}

			fmt.Printf("Name:       %s\n", sec.Name)
			fmt.Printf("Namespace:  %s\n", sec.Namespace)
			fmt.Printf("Created:    %s\n", sec.CreatedAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("Keys:       %d\n", len(sec.Data))
			fmt.Println()

			if showValues {
				fmt.Println("DATA")
				for k, v := range sec.Data {
					fmt.Printf("  %s = %s\n", k, v)
				}
			} else {
				fmt.Println("KEYS (use --show-values to see values)")
				for k := range sec.Data {
					fmt.Printf("  %s\n", k)
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&showValues, "show-values", false, "Show actual secret values")

	return cmd
}

func newSecretDeleteCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a secret",
		Long: `Delete a secret.

Requires confirmation unless --yes is used.

Examples:
  prisn secret delete db-creds
  prisn secret delete db-creds --yes`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			c, err := getClient(cmd)
			if err != nil {
				return err
			}

			namespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
			if namespace == "" {
				namespace = "default"
			}

			if !yes {
				fmt.Printf("Delete secret %s/%s? [y/N]: ", namespace, name)
				var input string
				fmt.Scanln(&input)
				input = strings.ToLower(strings.TrimSpace(input))
				if input != "y" && input != "yes" {
					fmt.Println("Cancelled.")
					return nil
				}
			}

			endpoint := fmt.Sprintf("/api/v1/secrets/%s?namespace=%s", name, namespace)
			if _, err := c.Delete(endpoint); err != nil {
				if client.IsOffline(err) {
					client.HandleOfflineError(err)
					return nil
				}
				if strings.Contains(err.Error(), "not found") {
					return fmt.Errorf("secret not found: %s/%s\n\n  List secrets: prisn secret list", namespace, name)
				}
				return fmt.Errorf("failed to delete secret: %w", err)
			}

			log.Done("deleted secret %s/%s", namespace, name)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation")

	return cmd
}

// parseEnvFile reads a .env file and returns key-value pairs.
func parseEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data := make(map[string]string)
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: invalid format (expected KEY=value)", lineNum)
		}

		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		// Remove surrounding quotes if present
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}

		data[key] = val
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return data, nil
}
