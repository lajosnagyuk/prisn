package context

import (
	"fmt"
	"os"
	"strings"
)

const (
	// DefaultServer is the fallback server address
	DefaultServer = "localhost:7331"

	// DefaultNamespace is the fallback namespace
	DefaultNamespace = "default"
)

// Environment variable names
const (
	EnvContext   = "PRISN_CONTEXT"
	EnvServer    = "PRISN_SERVER"
	EnvNamespace = "PRISN_NAMESPACE"
	EnvToken     = "PRISN_TOKEN"
)

// Resolve determines the active context from flags, environment variables,
// and the config file.
//
// Resolution priority (highest to lowest):
//  1. --context flag (flagContext parameter)
//  2. PRISN_CONTEXT environment variable
//  3. current_context in config file
//  4. PRISN_SERVER environment variable (creates implicit context)
//  5. Default: localhost:7331
//
// The flagNamespace parameter overrides the context's namespace if set.
func Resolve(flagContext, flagNamespace string) (*Resolved, error) {
	cfg, err := Load("")
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	var contextName string
	var source string
	var ctx *Context

	// Priority 1: --context flag
	if flagContext != "" {
		contextName = flagContext
		source = "flag --context"
	} else if envContext := os.Getenv(EnvContext); envContext != "" {
		// Priority 2: PRISN_CONTEXT env var
		contextName = envContext
		source = "env PRISN_CONTEXT"
	} else if cfg.CurrentContext != "" {
		// Priority 3: current_context in config
		contextName = cfg.CurrentContext
		source = "config current_context"
	}

	// If we have a context name, look it up
	if contextName != "" {
		ctx = cfg.GetContext(contextName)
		if ctx == nil {
			names := cfg.ContextNames()
			if len(names) == 0 {
				return nil, fmt.Errorf("context %q not found (no contexts configured)\nAdd one with: prisn context add %s --server <address>", contextName, contextName)
			}
			return nil, fmt.Errorf("context %q not found\nAvailable contexts: %s", contextName, strings.Join(names, ", "))
		}
	} else {
		// Priority 4/5: Fall back to PRISN_SERVER or default
		server := os.Getenv(EnvServer)
		if server != "" {
			source = "env PRISN_SERVER"
		} else {
			server = DefaultServer
			source = "default"
		}
		ctx = &Context{
			Name:      "default",
			Server:    server,
			Namespace: DefaultNamespace,
		}
	}

	// Build resolved context
	resolved := &Resolved{
		Name:        ctx.Name,
		Mode:        ctx.Mode,
		Server:      ctx.Server,
		KubeContext: ctx.KubeContext,
		Namespace:   ctx.Namespace,
		Token:       ctx.Token,
		TLS:         ctx.TLS,
		Source:      source,
	}

	// Auto-detect mode if not set
	if resolved.Mode == "" {
		if resolved.Server != "" && resolved.Server != DefaultServer {
			resolved.Mode = ModeRemote
		} else {
			resolved.Mode = ModeLocal
		}
	}

	// Apply overrides

	// --namespace flag overrides context namespace
	if flagNamespace != "" {
		resolved.Namespace = flagNamespace
	} else if envNS := os.Getenv(EnvNamespace); envNS != "" {
		// PRISN_NAMESPACE env var also overrides
		resolved.Namespace = envNS
	}

	// Ensure namespace has a default
	if resolved.Namespace == "" {
		resolved.Namespace = DefaultNamespace
	}

	// PRISN_TOKEN env var overrides context token
	if envToken := os.Getenv(EnvToken); envToken != "" {
		resolved.Token = envToken
	}

	// Auto-detect TLS from server address
	if !resolved.TLS.Enabled && shouldEnableTLS(resolved.Server) {
		resolved.TLS.Enabled = true
	}

	return resolved, nil
}

// shouldEnableTLS returns true if TLS should be auto-enabled for the server.
func shouldEnableTLS(server string) bool {
	// Enable TLS for standard HTTPS port
	if strings.HasSuffix(server, ":443") {
		return true
	}
	// Don't auto-enable for localhost (dev environment)
	if strings.HasPrefix(server, "localhost:") || strings.HasPrefix(server, "127.0.0.1:") {
		return false
	}
	// Enable TLS for any non-localhost address on port 7331 (our default)
	// This is a safety measure - production servers should use TLS
	// Users can explicitly set tls.enabled = false if needed
	return false // Be conservative - don't auto-enable, let users opt-in
}

// Scheme returns "https" if TLS is enabled, otherwise "http".
func (r *Resolved) Scheme() string {
	if r.TLS.Enabled {
		return "https"
	}
	return "http"
}

// BaseURL returns the full base URL for API requests.
func (r *Resolved) BaseURL() string {
	return fmt.Sprintf("%s://%s", r.Scheme(), r.Server)
}

// String returns a human-readable representation.
func (r *Resolved) String() string {
	return fmt.Sprintf("%s (%s)", r.Name, r.Server)
}

// MaskedToken returns the token with middle characters masked.
// Shows first 4 and last 4 characters only.
func (r *Resolved) MaskedToken() string {
	if r.Token == "" {
		return ""
	}
	if len(r.Token) <= 12 {
		return "****"
	}
	return r.Token[:4] + "..." + r.Token[len(r.Token)-4:]
}
