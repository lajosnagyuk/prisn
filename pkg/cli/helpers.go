package cli

import (
	"github.com/lajosnagyuk/prisn/pkg/client"
	prisnctx "github.com/lajosnagyuk/prisn/pkg/context"
	"github.com/spf13/cobra"
)

// resolveContext extracts context/namespace from command flags and resolves the connection settings.
// This is the single source of truth for context resolution in all CLI commands.
func resolveContext(cmd *cobra.Command) (*prisnctx.Resolved, error) {
	flagContext, _ := cmd.Root().PersistentFlags().GetString("context")
	flagNamespace, _ := cmd.Root().PersistentFlags().GetString("namespace")
	return prisnctx.Resolve(flagContext, flagNamespace)
}

// mustResolveContext is like resolveContext but returns a default local context on error.
// Use this for commands that can work offline.
func mustResolveContext(cmd *cobra.Command) *prisnctx.Resolved {
	resolved, err := resolveContext(cmd)
	if err != nil {
		// Return a default local context
		return &prisnctx.Resolved{
			Name:      "local",
			Server:    "localhost:7331",
			Namespace: "default",
		}
	}
	return resolved
}

// getClientWithContext creates an API client and returns both the client and resolved context.
// Use this when you need the context for Kubernetes mode detection.
func getClientWithContext(cmd *cobra.Command) (*client.Client, *prisnctx.Resolved, error) {
	resolved, err := resolveContext(cmd)
	if err != nil {
		return nil, nil, err
	}
	c, err := client.New(resolved)
	if err != nil {
		return nil, nil, err
	}
	return c, resolved, nil
}
