package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/pelletier/go-toml/v2"
)

const (
	// ConfigDir is the prisn configuration directory name
	ConfigDir = ".prisn"

	// ConfigFile is the context configuration filename
	ConfigFile = "config.toml"
)

// DefaultConfigPath returns the default config file path (~/.prisn/config.toml).
func DefaultConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot find home directory: %w", err)
	}
	return filepath.Join(home, ConfigDir, ConfigFile), nil
}

// Load reads the config file from the given path.
// If path is empty, uses the default path.
// Returns an empty config if the file doesn't exist.
func Load(path string) (*Config, error) {
	if path == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return nil, err
		}
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// No config file - return empty config
		return &Config{
			Contexts: make(map[string]*Context),
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Initialize map if nil
	if cfg.Contexts == nil {
		cfg.Contexts = make(map[string]*Context)
	}

	// Ensure names are set in each context
	for name, ctx := range cfg.Contexts {
		if ctx.Name == "" {
			ctx.Name = name
		}
	}

	return &cfg, nil
}

// Save writes the config to the given path.
// If path is empty, uses the default path.
// Creates the directory if it doesn't exist.
// Uses 0600 permissions since config may contain tokens.
func Save(cfg *Config, path string) error {
	if path == "" {
		var err error
		path, err = DefaultConfigPath()
		if err != nil {
			return err
		}
	}

	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Write with restricted permissions (contains tokens)
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

// GetContext returns the named context, or nil if not found.
func (c *Config) GetContext(name string) *Context {
	if c.Contexts == nil {
		return nil
	}
	return c.Contexts[name]
}

// SetContext adds or updates a context.
func (c *Config) SetContext(ctx *Context) {
	if c.Contexts == nil {
		c.Contexts = make(map[string]*Context)
	}
	c.Contexts[ctx.Name] = ctx
}

// DeleteContext removes a context by name.
// Returns true if the context existed.
func (c *Config) DeleteContext(name string) bool {
	if c.Contexts == nil {
		return false
	}
	_, exists := c.Contexts[name]
	if exists {
		delete(c.Contexts, name)
	}
	return exists
}

// RenameContext renames a context.
// Returns error if old name doesn't exist or new name already exists.
func (c *Config) RenameContext(oldName, newName string) error {
	if c.Contexts == nil {
		return fmt.Errorf("context %q not found", oldName)
	}

	ctx, exists := c.Contexts[oldName]
	if !exists {
		return fmt.Errorf("context %q not found", oldName)
	}

	if _, exists := c.Contexts[newName]; exists {
		return fmt.Errorf("context %q already exists", newName)
	}

	// Update the context
	ctx.Name = newName
	c.Contexts[newName] = ctx
	delete(c.Contexts, oldName)

	// Update current context if it was renamed
	if c.CurrentContext == oldName {
		c.CurrentContext = newName
	}

	return nil
}

// ContextNames returns a sorted list of all context names.
func (c *Config) ContextNames() []string {
	if c.Contexts == nil {
		return nil
	}

	names := make([]string, 0, len(c.Contexts))
	for name := range c.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// HasContext returns true if the named context exists.
func (c *Config) HasContext(name string) bool {
	if c.Contexts == nil {
		return false
	}
	_, exists := c.Contexts[name]
	return exists
}

// CurrentCtx returns the current context, or nil if not set or not found.
func (c *Config) CurrentCtx() *Context {
	if c.CurrentContext == "" {
		return nil
	}
	return c.GetContext(c.CurrentContext)
}
