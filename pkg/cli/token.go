package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/auth"
	"github.com/lajosnagyuk/prisn/pkg/client"
	"github.com/lajosnagyuk/prisn/pkg/log"
	"github.com/spf13/cobra"
)

func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage API tokens",
		Long: `Commands for managing prisn API tokens.

Tokens provide authenticated access to the prisn API with role-based permissions.

Roles:
  admin     - Full access to all resources and operations
  developer - Manage deployments, jobs, and secrets in allowed namespaces
  viewer    - Read-only access to allowed namespaces

Examples:
  prisn token create --name "CI/CD" --role developer --namespace default
  prisn token list
  prisn token revoke <token-id>
  prisn token delete <token-id>`,
	}

	cmd.AddCommand(
		newTokenCreateCmd(),
		newTokenListCmd(),
		newTokenGetCmd(),
		newTokenRevokeCmd(),
		newTokenDeleteCmd(),
	)

	return cmd
}

func newTokenCreateCmd() *cobra.Command {
	var (
		name       string
		role       string
		namespaces []string
		expiresIn  string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new API token",
		Long: `Create a new API token with specified role and namespace access.

The secret is only displayed once at creation time. Store it securely.

Roles:
  admin     - Full access (can manage tokens, namespaces, all resources)
  developer - Create/manage deployments, jobs, secrets (cannot manage tokens)
  viewer    - Read-only access

Examples:
  prisn token create --name "CI/CD" --role developer --namespace default
  prisn token create --name "Admin" --role admin --namespace "*"
  prisn token create --name "Temp" --role viewer --namespace staging --expires-in 24h
  prisn token create --name dev-team -r developer --namespace default --namespace staging`,

		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate required flags
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if role == "" {
				return fmt.Errorf("--role is required")
			}
			if len(namespaces) == 0 {
				return fmt.Errorf("at least one --namespace is required")
			}

			// Validate role
			if _, err := auth.ParseRole(role); err != nil {
				return err
			}

			// Validate expiration format
			if expiresIn != "" {
				if _, err := parseExpiration(expiresIn); err != nil {
					return err
				}
			}

			c, err := getClient(cmd)
			if err != nil {
				return err
			}

			req := map[string]any{
				"name":       name,
				"role":       role,
				"namespaces": namespaces,
			}
			if expiresIn != "" {
				req["expires_in"] = expiresIn
			}

			var resp struct {
				ID         string    `json:"id"`
				Name       string    `json:"name"`
				Secret     string    `json:"secret"`
				Role       string    `json:"role"`
				Namespaces []string  `json:"namespaces"`
				CreatedAt  time.Time `json:"created_at"`
				ExpiresAt  *time.Time `json:"expires_at"`
			}

			if err := c.PostJSON("/api/v1/tokens", req, &resp); err != nil {
				if client.IsOffline(err) {
					client.HandleOfflineError(err)
					return nil
				}
				return fmt.Errorf("failed to create token: %w", err)
			}

			// Display the result
			log.Done("Token created successfully")
			fmt.Println()
			fmt.Printf("  ID:         %s\n", resp.ID)
			fmt.Printf("  Name:       %s\n", resp.Name)
			fmt.Printf("  Role:       %s\n", resp.Role)
			fmt.Printf("  Namespaces: %s\n", strings.Join(resp.Namespaces, ", "))
			if resp.ExpiresAt != nil {
				fmt.Printf("  Expires:    %s\n", resp.ExpiresAt.Format(time.RFC3339))
			}
			fmt.Println()
			log.Warn("Save this secret - it will not be shown again:")
			fmt.Printf("\n  %s\n\n", resp.Secret)

			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Token name (required)")
	cmd.Flags().StringVarP(&role, "role", "r", "", "Token role: admin, developer, viewer (required)")
	cmd.Flags().StringSliceVar(&namespaces, "namespace", nil, "Namespaces the token can access (use '*' for all)")
	cmd.Flags().StringVar(&expiresIn, "expires-in", "", "Token expiration (e.g., 24h, 7d, 30d)")

	return cmd
}

func newTokenListCmd() *cobra.Command {
	var role string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List API tokens",
		Long: `List all API tokens with optional role filter.

Examples:
  prisn token list
  prisn token list --role admin
  prisn token list --role developer`,

		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := getClient(cmd)
			if err != nil {
				return err
			}

			path := "/api/v1/tokens"
			if role != "" {
				path += "?role=" + role
			}

			var resp struct {
				Items []struct {
					ID         string     `json:"id"`
					Name       string     `json:"name"`
					Role       string     `json:"role"`
					Namespaces []string   `json:"namespaces"`
					CreatedAt  time.Time  `json:"created_at"`
					ExpiresAt  *time.Time `json:"expires_at"`
					RevokedAt  *time.Time `json:"revoked_at"`
					LastUsedAt *time.Time `json:"last_used_at"`
				} `json:"items"`
				Count int `json:"count"`
			}

			if err := c.GetJSON(path, &resp); err != nil {
				if client.IsOffline(err) {
					client.HandleOfflineError(err)
					return nil
				}
				return fmt.Errorf("failed to list tokens: %w", err)
			}

			printer := NewPrinter(cmd)
			return printer.PrintList(resp.Items, func() {
				if resp.Count == 0 {
					fmt.Println("No tokens found")
					return
				}

				// Print table header
				fmt.Printf("%-20s %-20s %-12s %-20s %-12s\n", "ID", "NAME", "ROLE", "NAMESPACES", "STATUS")
				fmt.Println(strings.Repeat("-", 86))

				for _, t := range resp.Items {
					status := "active"
					if t.RevokedAt != nil {
						status = "revoked"
					} else if t.ExpiresAt != nil && t.ExpiresAt.Before(time.Now()) {
						status = "expired"
					}

					ns := strings.Join(t.Namespaces, ",")
					if len(ns) > 20 {
						ns = ns[:17] + "..."
					}

					id := t.ID
					if len(id) > 20 {
						id = id[:17] + "..."
					}

					name := t.Name
					if len(name) > 20 {
						name = name[:17] + "..."
					}

					fmt.Printf("%-20s %-20s %-12s %-20s %-12s\n", id, name, t.Role, ns, status)
				}

				fmt.Printf("\nTotal: %d tokens\n", resp.Count)
			})
		},
	}

	cmd.Flags().StringVar(&role, "role", "", "Filter by role (admin, developer, viewer)")

	return cmd
}

func newTokenGetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <token-id>",
		Short: "Get token details",
		Long: `Get detailed information about a specific token.

Examples:
  prisn token get abc123`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			c, err := getClient(cmd)
			if err != nil {
				return err
			}

			var resp struct {
				ID         string     `json:"id"`
				Name       string     `json:"name"`
				Role       string     `json:"role"`
				Namespaces []string   `json:"namespaces"`
				CreatedAt  time.Time  `json:"created_at"`
				ExpiresAt  *time.Time `json:"expires_at"`
				RevokedAt  *time.Time `json:"revoked_at"`
				LastUsedAt *time.Time `json:"last_used_at"`
			}

			if err := c.GetJSON("/api/v1/tokens/"+id, &resp); err != nil {
				if client.IsOffline(err) {
					client.HandleOfflineError(err)
					return nil
				}
				return fmt.Errorf("failed to get token: %w", err)
			}

			fmt.Printf("ID:         %s\n", resp.ID)
			fmt.Printf("Name:       %s\n", resp.Name)
			fmt.Printf("Role:       %s\n", resp.Role)
			fmt.Printf("Namespaces: %s\n", strings.Join(resp.Namespaces, ", "))
			fmt.Printf("Created:    %s\n", resp.CreatedAt.Format(time.RFC3339))

			if resp.ExpiresAt != nil {
				fmt.Printf("Expires:    %s\n", resp.ExpiresAt.Format(time.RFC3339))
			} else {
				fmt.Printf("Expires:    never\n")
			}

			if resp.RevokedAt != nil {
				fmt.Printf("Revoked:    %s\n", resp.RevokedAt.Format(time.RFC3339))
			}

			if resp.LastUsedAt != nil {
				fmt.Printf("Last Used:  %s\n", resp.LastUsedAt.Format(time.RFC3339))
			} else {
				fmt.Printf("Last Used:  never\n")
			}

			// Show status
			status := "active"
			if resp.RevokedAt != nil {
				status = "revoked"
			} else if resp.ExpiresAt != nil && resp.ExpiresAt.Before(time.Now()) {
				status = "expired"
			}
			fmt.Printf("Status:     %s\n", status)

			return nil
		},
	}

	return cmd
}

func newTokenRevokeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <token-id>",
		Short: "Revoke an API token",
		Long: `Revoke an API token, preventing further use.

Revoked tokens remain in the system for audit purposes but cannot be used
for authentication. Use 'token delete' to permanently remove a token.

Examples:
  prisn token revoke abc123`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			c, err := getClient(cmd)
			if err != nil {
				return err
			}

			var resp map[string]string
			if err := c.PostJSON("/api/v1/tokens/"+id+"/revoke", nil, &resp); err != nil {
				if client.IsOffline(err) {
					client.HandleOfflineError(err)
					return nil
				}
				return fmt.Errorf("failed to revoke token: %w", err)
			}

			log.Done("Token %s revoked", id)
			return nil
		},
	}

	return cmd
}

func newTokenDeleteCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete <token-id>",
		Short: "Delete an API token",
		Long: `Permanently delete an API token.

This action cannot be undone. For audit purposes, consider revoking
tokens instead of deleting them.

Examples:
  prisn token delete abc123
  prisn token delete abc123 --yes  # Skip confirmation`,

		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

			// Confirm deletion unless --yes
			if !yes {
				fmt.Printf("Delete token %s? This cannot be undone. [y/N]: ", id)
				var input string
				fmt.Scanln(&input)
				input = strings.ToLower(strings.TrimSpace(input))
				if input != "y" && input != "yes" {
					fmt.Println("Cancelled")
					return nil
				}
			}

			c, err := getClient(cmd)
			if err != nil {
				return err
			}

			if _, err := c.Delete("/api/v1/tokens/" + id); err != nil {
				if client.IsOffline(err) {
					client.HandleOfflineError(err)
					return nil
				}
				return fmt.Errorf("failed to delete token: %w", err)
			}

			log.Done("Token %s deleted", id)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation prompt")

	return cmd
}

// parseExpiration parses expiration format (e.g., "24h", "7d", "30d").
func parseExpiration(s string) (time.Duration, error) {
	// Try standard duration format first
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}

	// Try days format (e.g., "7d")
	if len(s) > 1 && s[len(s)-1] == 'd' {
		days := s[:len(s)-1]
		var d int
		if _, err := fmt.Sscanf(days, "%d", &d); err == nil {
			return time.Duration(d) * 24 * time.Hour, nil
		}
	}

	return 0, fmt.Errorf("invalid expiration format: %s (use e.g., 24h, 7d, 30d)", s)
}
