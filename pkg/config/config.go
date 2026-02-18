// Package config handles TOML configuration parsing for prisn deployments.
//
// TOML was chosen over YAML for simplicity and fewer footguns.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// DeployConfig represents a deployment configuration.
type DeployConfig struct {
	// Name is the deployment name (required)
	Name string `toml:"name"`

	// Runtime is the execution runtime (python3, node, bash)
	Runtime string `toml:"runtime"`

	// Source is the path to the main script file
	Source string `toml:"source"`

	// Args are command-line arguments to pass to the script
	Args []string `toml:"args"`

	// Port is the port to expose (for services)
	Port int `toml:"port"`

	// Replicas is the number of instances to run
	Replicas int `toml:"replicas"`

	// Resources are resource limits
	Resources ResourceConfig `toml:"resources"`

	// Env are environment variables (parsed from TOML into EnvValue)
	Env map[string]EnvValue `toml:"-"`

	// RawEnv holds the raw TOML data for post-processing
	RawEnv map[string]interface{} `toml:"env"`

	// Schedule is a cron expression (for cron jobs)
	Schedule string `toml:"schedule"`

	// Timeout is the execution timeout
	Timeout Duration `toml:"timeout"`

	// HealthCheck is the health check configuration
	HealthCheck *HealthCheckConfig `toml:"healthcheck"`

	// Namespace is the deployment namespace
	Namespace string `toml:"namespace"`

	// Labels are user-defined labels
	Labels map[string]string `toml:"labels"`
}

// ResourceConfig specifies resource limits.
type ResourceConfig struct {
	// CPU limit (e.g., "500m", "2")
	CPU string `toml:"cpu"`

	// Memory limit (e.g., "512Mi", "2Gi")
	Memory string `toml:"memory"`

	// MaxProcesses is the maximum number of child processes
	MaxProcesses int `toml:"max_processes"`
}

// EnvValue represents an environment variable value.
// Can be a simple string or a secret reference.
type EnvValue struct {
	// Value is a plain string value
	Value string

	// Secret is the secret name to read from
	Secret string `toml:"secret"`

	// Key is the key within the secret
	Key string `toml:"key"`
}

// IsSecret returns true if this is a secret reference.
func (e EnvValue) IsSecret() bool {
	return e.Secret != ""
}

// HealthCheckConfig specifies health check settings.
type HealthCheckConfig struct {
	// Path is the HTTP path to check (e.g., "/health")
	Path string `toml:"path"`

	// Port is the port to check (defaults to main port)
	Port int `toml:"port"`

	// Interval is the time between checks
	Interval Duration `toml:"interval"`

	// Timeout is the health check timeout
	Timeout Duration `toml:"timeout"`

	// Retries is the number of failures before marking unhealthy
	Retries int `toml:"retries"`
}

// Duration is a wrapper around time.Duration for TOML parsing.
type Duration struct {
	time.Duration
}

// UnmarshalText implements encoding.TextUnmarshaler for go-toml/v2.
func (d *Duration) UnmarshalText(text []byte) error {
	dur, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", string(text), err)
	}
	d.Duration = dur
	return nil
}

// MarshalText implements encoding.TextMarshaler for go-toml/v2.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.Duration.String()), nil
}

// Load reads a configuration file from the given path.
func Load(path string) (*DeployConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	return Parse(data, filepath.Dir(path))
}

// Parse parses TOML configuration from bytes.
func Parse(data []byte, baseDir string) (*DeployConfig, error) {
	var cfg DeployConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Apply defaults
	if err := cfg.applyDefaults(baseDir); err != nil {
		return nil, err
	}

	// Validate
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// applyDefaults sets default values for unspecified fields.
func (c *DeployConfig) applyDefaults(baseDir string) error {
	// Default replicas
	if c.Replicas == 0 {
		c.Replicas = 1
	}

	// Default namespace
	if c.Namespace == "" {
		c.Namespace = "default"
	}

	// Default timeout
	if c.Timeout.Duration == 0 {
		c.Timeout.Duration = 1 * time.Hour
	}

	// Resolve relative source path
	if c.Source != "" && !filepath.IsAbs(c.Source) {
		c.Source = filepath.Join(baseDir, c.Source)
	}

	// Auto-detect runtime from source if not specified
	if c.Runtime == "" && c.Source != "" {
		c.Runtime = detectRuntime(c.Source)
	}

	// Process raw env into typed EnvValue map
	if len(c.RawEnv) > 0 {
		c.Env = make(map[string]EnvValue)
		for k, v := range c.RawEnv {
			switch val := v.(type) {
			case string:
				c.Env[k] = EnvValue{Value: val}
			case map[string]interface{}:
				ev := EnvValue{}
				if secret, ok := val["secret"].(string); ok {
					ev.Secret = secret
				}
				if key, ok := val["key"].(string); ok {
					ev.Key = key
				}
				c.Env[k] = ev
			default:
				return fmt.Errorf("invalid env value for %s: must be string or {secret, key} table", k)
			}
		}
	}

	// Default health check values
	if c.HealthCheck != nil {
		if c.HealthCheck.Interval.Duration == 0 {
			c.HealthCheck.Interval.Duration = 10 * time.Second
		}
		if c.HealthCheck.Timeout.Duration == 0 {
			c.HealthCheck.Timeout.Duration = 5 * time.Second
		}
		if c.HealthCheck.Retries == 0 {
			c.HealthCheck.Retries = 3
		}
		if c.HealthCheck.Port == 0 {
			c.HealthCheck.Port = c.Port
		}
	}

	return nil
}

// Validate checks the configuration for errors.
func (c *DeployConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}

	if c.Source == "" {
		return fmt.Errorf("source is required")
	}

	if _, err := os.Stat(c.Source); err != nil {
		return fmt.Errorf("source file not found: %s", c.Source)
	}

	if c.Runtime == "" {
		return fmt.Errorf("runtime is required (or use a recognized file extension)")
	}

	validRuntimes := map[string]bool{
		"python":  true,
		"python3": true,
		"node":    true,
		"bash":    true,
	}
	if !validRuntimes[c.Runtime] {
		return fmt.Errorf("unsupported runtime: %s (supported: python3, node, bash)", c.Runtime)
	}

	if c.Replicas < 0 {
		return fmt.Errorf("replicas cannot be negative")
	}

	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("port must be between 0 and 65535")
	}

	// Validate cron schedule if provided
	if c.Schedule != "" {
		if err := validateCronSchedule(c.Schedule); err != nil {
			return fmt.Errorf("invalid schedule: %w", err)
		}
	}

	// Validate resource limits
	if c.Resources.CPU != "" {
		if _, err := parseCPU(c.Resources.CPU); err != nil {
			return fmt.Errorf("invalid cpu limit: %w", err)
		}
	}
	if c.Resources.Memory != "" {
		if _, err := parseMemory(c.Resources.Memory); err != nil {
			return fmt.Errorf("invalid memory limit: %w", err)
		}
	}

	return nil
}

// detectRuntime guesses the runtime from the file extension.
func detectRuntime(source string) string {
	ext := strings.ToLower(filepath.Ext(source))
	switch ext {
	case ".py":
		return "python3"
	case ".js", ".mjs":
		return "node"
	case ".sh", ".bash":
		return "bash"
	default:
		return ""
	}
}

// validateCronSchedule validates a cron expression.
func validateCronSchedule(schedule string) error {
	parts := strings.Fields(schedule)
	if len(parts) != 5 {
		return fmt.Errorf("must have 5 fields (min hour dom mon dow)")
	}
	// Basic validation - full validation happens at runtime
	return nil
}

// parseCPU parses a CPU limit string (e.g., "500m", "2").
func parseCPU(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	if strings.HasSuffix(s, "m") {
		// Millicores
		var millis float64
		_, err := fmt.Sscanf(s, "%fm", &millis)
		if err != nil {
			return 0, fmt.Errorf("invalid cpu format: %s", s)
		}
		return millis / 1000, nil
	}

	// Cores
	var cores float64
	_, err := fmt.Sscanf(s, "%f", &cores)
	if err != nil {
		return 0, fmt.Errorf("invalid cpu format: %s", s)
	}
	return cores, nil
}

// parseMemory parses a memory limit string (e.g., "512Mi", "2Gi").
func parseMemory(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	multipliers := map[string]int64{
		"Ki": 1024,
		"Mi": 1024 * 1024,
		"Gi": 1024 * 1024 * 1024,
		"Ti": 1024 * 1024 * 1024 * 1024,
		"K":  1000,
		"M":  1000 * 1000,
		"G":  1000 * 1000 * 1000,
		"T":  1000 * 1000 * 1000 * 1000,
	}

	for suffix, mult := range multipliers {
		if strings.HasSuffix(s, suffix) {
			var value int64
			_, err := fmt.Sscanf(s, "%d"+suffix, &value)
			if err != nil {
				return 0, fmt.Errorf("invalid memory format: %s", s)
			}
			return value * mult, nil
		}
	}

	// Plain bytes
	var bytes int64
	_, err := fmt.Sscanf(s, "%d", &bytes)
	if err != nil {
		return 0, fmt.Errorf("invalid memory format: %s", s)
	}
	return bytes, nil
}

// CPUMillicores returns the CPU limit in millicores.
func (c *DeployConfig) CPUMillicores() int {
	if c.Resources.CPU == "" {
		return 0
	}
	cores, _ := parseCPU(c.Resources.CPU)
	return int(cores * 1000)
}

// MemoryBytes returns the memory limit in bytes.
func (c *DeployConfig) MemoryBytes() int64 {
	if c.Resources.Memory == "" {
		return 0
	}
	bytes, _ := parseMemory(c.Resources.Memory)
	return bytes
}

// MemoryMB returns the memory limit in megabytes.
func (c *DeployConfig) MemoryMB() int {
	return int(c.MemoryBytes() / (1024 * 1024))
}

// IsService returns true if this is a long-running service.
func (c *DeployConfig) IsService() bool {
	return c.Port > 0
}

// IsCronJob returns true if this is a scheduled job.
func (c *DeployConfig) IsCronJob() bool {
	return c.Schedule != ""
}

// Type returns the deployment type.
func (c *DeployConfig) Type() string {
	if c.IsCronJob() {
		return "cronjob"
	}
	if c.IsService() {
		return "service"
	}
	return "job"
}
