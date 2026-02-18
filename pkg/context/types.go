// Package context manages server connection contexts for the prisn CLI.
//
// A context stores connection details for a prisn server: address, namespace,
// authentication token, and TLS settings. Users can define multiple contexts
// and switch between them, similar to kubectl contexts.
package context

// Mode specifies how the CLI connects to the backend.
type Mode string

const (
	// ModeLocal runs scripts directly on the local machine (default for development)
	ModeLocal Mode = "local"

	// ModeKubernetes creates CRDs in a Kubernetes cluster via kubeconfig
	ModeKubernetes Mode = "kubernetes"

	// ModeRemote connects to a prisn API server over HTTP
	ModeRemote Mode = "remote"
)

// Context represents a single server connection configuration.
type Context struct {
	// Name is the context identifier (e.g., "local", "prod", "staging")
	Name string `toml:"name"`

	// Mode specifies how to connect: "local", "kubernetes", or "remote"
	// Default: "local" for localhost, "remote" for other servers
	Mode Mode `toml:"mode,omitempty"`

	// Server is the prisn server address for remote mode
	// (e.g., "localhost:7331", "prod.example.com:7331")
	Server string `toml:"server,omitempty"`

	// KubeContext is the kubeconfig context name for kubernetes mode
	// If empty, uses the current kubeconfig context
	KubeContext string `toml:"kube_context,omitempty"`

	// Namespace is the default namespace for this context
	Namespace string `toml:"namespace,omitempty"`

	// Token is the authentication token for remote servers
	Token string `toml:"token,omitempty"`

	// TLS contains TLS/SSL settings for remote connections
	TLS TLSConfig `toml:"tls,omitempty"`
}

// TLSConfig holds TLS/SSL settings for a context.
type TLSConfig struct {
	// Enabled forces TLS on (auto-detected from server address if not set)
	Enabled bool `toml:"enabled,omitempty"`

	// Insecure skips TLS certificate verification (development only)
	Insecure bool `toml:"insecure,omitempty"`

	// CAFile is the path to a custom CA certificate
	CAFile string `toml:"ca_file,omitempty"`

	// CertFile is the path to a client certificate (for mTLS)
	CertFile string `toml:"cert_file,omitempty"`

	// KeyFile is the path to a client key (for mTLS)
	KeyFile string `toml:"key_file,omitempty"`
}

// Config represents the full ~/.prisn/config.toml file.
type Config struct {
	// CurrentContext is the name of the active context
	CurrentContext string `toml:"current_context"`

	// Contexts maps context names to their configurations
	Contexts map[string]*Context `toml:"contexts"`
}

// Resolved holds the final resolved context settings after applying
// all override sources (flags, env vars, config file).
type Resolved struct {
	// Name is the context name (or "default" if using fallback)
	Name string

	// Mode is the resolved connection mode
	Mode Mode

	// Server is the resolved server address (for remote mode)
	Server string

	// KubeContext is the kubeconfig context (for kubernetes mode)
	KubeContext string

	// Namespace is the resolved namespace
	Namespace string

	// Token is the resolved auth token (for remote mode)
	Token string

	// TLS is the resolved TLS configuration (for remote mode)
	TLS TLSConfig

	// Source describes how the context was resolved (for debugging)
	Source string
}

// IsKubernetes returns true if the context uses Kubernetes mode.
func (r *Resolved) IsKubernetes() bool {
	return r.Mode == ModeKubernetes
}

// IsLocal returns true if the context uses local mode.
func (r *Resolved) IsLocal() bool {
	return r.Mode == ModeLocal || r.Mode == ""
}

// IsRemote returns true if the context uses remote server mode.
func (r *Resolved) IsRemote() bool {
	return r.Mode == ModeRemote
}

// Clone returns a deep copy of the context.
func (c *Context) Clone() *Context {
	if c == nil {
		return nil
	}
	return &Context{
		Name:        c.Name,
		Mode:        c.Mode,
		Server:      c.Server,
		KubeContext: c.KubeContext,
		Namespace:   c.Namespace,
		Token:       c.Token,
		TLS:         c.TLS,
	}
}

// HasAuth returns true if the context has authentication configured.
func (c *Context) HasAuth() bool {
	return c.Token != ""
}

// HasTLS returns true if TLS is explicitly enabled or configured.
func (c *Context) HasTLS() bool {
	return c.TLS.Enabled || c.TLS.CAFile != "" || c.TLS.CertFile != ""
}
