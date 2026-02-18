package context

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigLoadSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Test loading non-existent file returns empty config
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load non-existent should not error: %v", err)
	}
	if len(cfg.Contexts) != 0 {
		t.Errorf("expected empty contexts, got %d", len(cfg.Contexts))
	}

	// Add contexts and save
	cfg.SetContext(&Context{
		Name:      "local",
		Server:    "localhost:7331",
		Namespace: "default",
	})
	cfg.SetContext(&Context{
		Name:      "prod",
		Server:    "prod.example.com:7331",
		Namespace: "production",
		Token:     "sk_test_123",
	})
	cfg.CurrentContext = "local"

	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Check file permissions (should be 0600 for security)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600 permissions, got %o", info.Mode().Perm())
	}

	// Reload and verify
	cfg2, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg2.CurrentContext != "local" {
		t.Errorf("expected current context 'local', got %q", cfg2.CurrentContext)
	}
	if len(cfg2.Contexts) != 2 {
		t.Errorf("expected 2 contexts, got %d", len(cfg2.Contexts))
	}
	if cfg2.Contexts["prod"].Token != "sk_test_123" {
		t.Errorf("expected token preserved")
	}
}

func TestConfigContextOperations(t *testing.T) {
	cfg := &Config{Contexts: make(map[string]*Context)}

	// Set context
	cfg.SetContext(&Context{Name: "test", Server: "test:7331"})
	if !cfg.HasContext("test") {
		t.Error("expected context 'test' to exist")
	}

	// Get context
	ctx := cfg.GetContext("test")
	if ctx == nil {
		t.Fatal("GetContext returned nil")
	}
	if ctx.Server != "test:7331" {
		t.Errorf("expected server 'test:7331', got %q", ctx.Server)
	}

	// Get non-existent
	if cfg.GetContext("nonexistent") != nil {
		t.Error("expected nil for non-existent context")
	}

	// Delete context
	if !cfg.DeleteContext("test") {
		t.Error("DeleteContext should return true for existing context")
	}
	if cfg.HasContext("test") {
		t.Error("context should be deleted")
	}
	if cfg.DeleteContext("test") {
		t.Error("DeleteContext should return false for non-existent context")
	}
}

func TestConfigRenameContext(t *testing.T) {
	cfg := &Config{
		Contexts:       make(map[string]*Context),
		CurrentContext: "old",
	}
	cfg.SetContext(&Context{Name: "old", Server: "old:7331"})

	// Rename
	if err := cfg.RenameContext("old", "new"); err != nil {
		t.Fatalf("RenameContext failed: %v", err)
	}

	// Check old is gone, new exists
	if cfg.HasContext("old") {
		t.Error("old context should not exist")
	}
	if !cfg.HasContext("new") {
		t.Error("new context should exist")
	}

	// Current context should be updated
	if cfg.CurrentContext != "new" {
		t.Errorf("expected current context 'new', got %q", cfg.CurrentContext)
	}

	// Context name should be updated
	ctx := cfg.GetContext("new")
	if ctx.Name != "new" {
		t.Errorf("expected context name 'new', got %q", ctx.Name)
	}

	// Rename non-existent should fail
	if err := cfg.RenameContext("nonexistent", "whatever"); err == nil {
		t.Error("expected error for non-existent context")
	}

	// Rename to existing should fail
	cfg.SetContext(&Context{Name: "other", Server: "other:7331"})
	if err := cfg.RenameContext("new", "other"); err == nil {
		t.Error("expected error when renaming to existing name")
	}
}

func TestContextNames(t *testing.T) {
	cfg := &Config{Contexts: make(map[string]*Context)}
	cfg.SetContext(&Context{Name: "zebra", Server: "z:7331"})
	cfg.SetContext(&Context{Name: "alpha", Server: "a:7331"})
	cfg.SetContext(&Context{Name: "beta", Server: "b:7331"})

	names := cfg.ContextNames()
	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d", len(names))
	}

	// Should be sorted
	if names[0] != "alpha" || names[1] != "beta" || names[2] != "zebra" {
		t.Errorf("names should be sorted, got %v", names)
	}
}

func TestResolve(t *testing.T) {
	// Set up test config
	dir := t.TempDir()
	path := filepath.Join(dir, ".prisn", "config.toml")
	os.MkdirAll(filepath.Dir(path), 0755)

	cfg := &Config{
		CurrentContext: "saved",
		Contexts:       make(map[string]*Context),
	}
	cfg.SetContext(&Context{
		Name:      "saved",
		Server:    "saved.example.com:7331",
		Namespace: "saved-ns",
	})
	cfg.SetContext(&Context{
		Name:      "other",
		Server:    "other.example.com:7331",
		Namespace: "other-ns",
	})
	Save(cfg, path)

	// Override config path for testing
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", originalHome)

	// Clear env vars
	os.Unsetenv("PRISN_CONTEXT")
	os.Unsetenv("PRISN_SERVER")
	os.Unsetenv("PRISN_NAMESPACE")
	os.Unsetenv("PRISN_TOKEN")

	t.Run("flag override", func(t *testing.T) {
		resolved, err := Resolve("other", "")
		if err != nil {
			t.Fatalf("Resolve failed: %v", err)
		}
		if resolved.Name != "other" {
			t.Errorf("expected 'other', got %q", resolved.Name)
		}
		if resolved.Source != "flag --context" {
			t.Errorf("expected source 'flag --context', got %q", resolved.Source)
		}
	})

	t.Run("config current context", func(t *testing.T) {
		resolved, err := Resolve("", "")
		if err != nil {
			t.Fatalf("Resolve failed: %v", err)
		}
		if resolved.Name != "saved" {
			t.Errorf("expected 'saved', got %q", resolved.Name)
		}
		if resolved.Namespace != "saved-ns" {
			t.Errorf("expected namespace 'saved-ns', got %q", resolved.Namespace)
		}
	})

	t.Run("namespace flag override", func(t *testing.T) {
		resolved, err := Resolve("", "override-ns")
		if err != nil {
			t.Fatalf("Resolve failed: %v", err)
		}
		if resolved.Namespace != "override-ns" {
			t.Errorf("expected namespace 'override-ns', got %q", resolved.Namespace)
		}
	})

	t.Run("env var PRISN_CONTEXT", func(t *testing.T) {
		os.Setenv("PRISN_CONTEXT", "other")
		defer os.Unsetenv("PRISN_CONTEXT")

		resolved, err := Resolve("", "")
		if err != nil {
			t.Fatalf("Resolve failed: %v", err)
		}
		if resolved.Name != "other" {
			t.Errorf("expected 'other', got %q", resolved.Name)
		}
		if resolved.Source != "env PRISN_CONTEXT" {
			t.Errorf("expected source 'env PRISN_CONTEXT', got %q", resolved.Source)
		}
	})

	t.Run("non-existent context error", func(t *testing.T) {
		_, err := Resolve("nonexistent", "")
		if err == nil {
			t.Error("expected error for non-existent context")
		}
	})
}

func TestResolveDefaultFallback(t *testing.T) {
	// Use empty temp dir (no config)
	dir := t.TempDir()
	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", originalHome)

	os.Unsetenv("PRISN_CONTEXT")
	os.Unsetenv("PRISN_SERVER")

	t.Run("default localhost", func(t *testing.T) {
		resolved, err := Resolve("", "")
		if err != nil {
			t.Fatalf("Resolve failed: %v", err)
		}
		if resolved.Server != "localhost:7331" {
			t.Errorf("expected 'localhost:7331', got %q", resolved.Server)
		}
		if resolved.Source != "default" {
			t.Errorf("expected source 'default', got %q", resolved.Source)
		}
	})

	t.Run("PRISN_SERVER env", func(t *testing.T) {
		os.Setenv("PRISN_SERVER", "custom.server:9999")
		defer os.Unsetenv("PRISN_SERVER")

		resolved, err := Resolve("", "")
		if err != nil {
			t.Fatalf("Resolve failed: %v", err)
		}
		if resolved.Server != "custom.server:9999" {
			t.Errorf("expected 'custom.server:9999', got %q", resolved.Server)
		}
		if resolved.Source != "env PRISN_SERVER" {
			t.Errorf("expected source 'env PRISN_SERVER', got %q", resolved.Source)
		}
	})
}

func TestResolvedHelpers(t *testing.T) {
	r := &Resolved{
		Name:   "test",
		Server: "test.example.com:7331",
		Token:  "sk_test_1234567890abcdef",
		TLS:    TLSConfig{Enabled: false},
	}

	// Scheme
	if r.Scheme() != "http" {
		t.Errorf("expected http, got %q", r.Scheme())
	}
	r.TLS.Enabled = true
	if r.Scheme() != "https" {
		t.Errorf("expected https, got %q", r.Scheme())
	}

	// BaseURL
	if r.BaseURL() != "https://test.example.com:7331" {
		t.Errorf("expected https://test.example.com:7331, got %q", r.BaseURL())
	}

	// MaskedToken
	masked := r.MaskedToken()
	if masked != "sk_t...cdef" {
		t.Errorf("expected 'sk_t...cdef', got %q", masked)
	}

	// Short token
	r.Token = "short"
	if r.MaskedToken() != "****" {
		t.Errorf("expected '****' for short token, got %q", r.MaskedToken())
	}

	// Empty token
	r.Token = ""
	if r.MaskedToken() != "" {
		t.Errorf("expected empty for empty token, got %q", r.MaskedToken())
	}
}

func TestContextClone(t *testing.T) {
	original := &Context{
		Name:      "test",
		Server:    "server:7331",
		Namespace: "ns",
		Token:     "token",
		TLS: TLSConfig{
			Enabled: true,
			CAFile:  "/path/to/ca",
		},
	}

	clone := original.Clone()

	// Verify values match
	if clone.Name != original.Name {
		t.Errorf("Name mismatch")
	}
	if clone.Server != original.Server {
		t.Errorf("Server mismatch")
	}
	if clone.TLS.CAFile != original.TLS.CAFile {
		t.Errorf("TLS.CAFile mismatch")
	}

	// Verify it's a copy, not same reference
	clone.Name = "modified"
	if original.Name == "modified" {
		t.Error("Clone should not affect original")
	}

	// Nil clone
	var nilCtx *Context
	if nilCtx.Clone() != nil {
		t.Error("Clone of nil should be nil")
	}
}

func TestContextHelpers(t *testing.T) {
	ctx := &Context{}

	if ctx.HasAuth() {
		t.Error("empty context should not have auth")
	}
	ctx.Token = "token"
	if !ctx.HasAuth() {
		t.Error("context with token should have auth")
	}

	if ctx.HasTLS() {
		t.Error("empty TLS should not be enabled")
	}
	ctx.TLS.Enabled = true
	if !ctx.HasTLS() {
		t.Error("TLS.Enabled should indicate HasTLS")
	}

	ctx.TLS.Enabled = false
	ctx.TLS.CAFile = "/path"
	if !ctx.HasTLS() {
		t.Error("TLS.CAFile should indicate HasTLS")
	}
}

// =============================================================================
// Kubernetes Mode Tests
// =============================================================================

func TestModeConstants(t *testing.T) {
	// Verify mode constants are defined correctly
	if ModeLocal != "local" {
		t.Errorf("expected ModeLocal='local', got %q", ModeLocal)
	}
	if ModeKubernetes != "kubernetes" {
		t.Errorf("expected ModeKubernetes='kubernetes', got %q", ModeKubernetes)
	}
	if ModeRemote != "remote" {
		t.Errorf("expected ModeRemote='remote', got %q", ModeRemote)
	}
}

func TestResolvedModeHelpers(t *testing.T) {
	tests := []struct {
		name         string
		mode         Mode
		isLocal      bool
		isKubernetes bool
		isRemote     bool
	}{
		{"empty mode defaults to local", "", true, false, false},
		{"explicit local", ModeLocal, true, false, false},
		{"kubernetes mode", ModeKubernetes, false, true, false},
		{"remote mode", ModeRemote, false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Resolved{Mode: tt.mode}
			if r.IsLocal() != tt.isLocal {
				t.Errorf("IsLocal() = %v, want %v", r.IsLocal(), tt.isLocal)
			}
			if r.IsKubernetes() != tt.isKubernetes {
				t.Errorf("IsKubernetes() = %v, want %v", r.IsKubernetes(), tt.isKubernetes)
			}
			if r.IsRemote() != tt.isRemote {
				t.Errorf("IsRemote() = %v, want %v", r.IsRemote(), tt.isRemote)
			}
		})
	}
}

func TestKubernetesContextSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	cfg := &Config{
		CurrentContext: "k8s-prod",
		Contexts:       make(map[string]*Context),
	}
	cfg.SetContext(&Context{
		Name:        "k8s-prod",
		Mode:        ModeKubernetes,
		KubeContext: "prod-cluster",
		Namespace:   "production",
	})
	cfg.SetContext(&Context{
		Name:        "k8s-default",
		Mode:        ModeKubernetes,
		KubeContext: "", // uses current kubeconfig context
		Namespace:   "default",
	})

	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Reload and verify
	cfg2, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Check k8s-prod context
	ctx := cfg2.GetContext("k8s-prod")
	if ctx == nil {
		t.Fatal("k8s-prod context not found")
	}
	if ctx.Mode != ModeKubernetes {
		t.Errorf("expected mode kubernetes, got %q", ctx.Mode)
	}
	if ctx.KubeContext != "prod-cluster" {
		t.Errorf("expected kube_context 'prod-cluster', got %q", ctx.KubeContext)
	}
	if ctx.Namespace != "production" {
		t.Errorf("expected namespace 'production', got %q", ctx.Namespace)
	}

	// Check k8s-default context
	ctx2 := cfg2.GetContext("k8s-default")
	if ctx2 == nil {
		t.Fatal("k8s-default context not found")
	}
	if ctx2.KubeContext != "" {
		t.Errorf("expected empty kube_context, got %q", ctx2.KubeContext)
	}
}

func TestResolveKubernetesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".prisn", "config.toml")
	os.MkdirAll(filepath.Dir(path), 0755)

	cfg := &Config{
		CurrentContext: "k8s",
		Contexts:       make(map[string]*Context),
	}
	cfg.SetContext(&Context{
		Name:        "k8s",
		Mode:        ModeKubernetes,
		KubeContext: "my-cluster",
		Namespace:   "prisn-test",
	})
	Save(cfg, path)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", originalHome)

	os.Unsetenv("PRISN_CONTEXT")
	os.Unsetenv("PRISN_NAMESPACE")

	resolved, err := Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if !resolved.IsKubernetes() {
		t.Error("expected IsKubernetes() to be true")
	}
	if resolved.Mode != ModeKubernetes {
		t.Errorf("expected mode kubernetes, got %q", resolved.Mode)
	}
	if resolved.KubeContext != "my-cluster" {
		t.Errorf("expected kube_context 'my-cluster', got %q", resolved.KubeContext)
	}
	if resolved.Namespace != "prisn-test" {
		t.Errorf("expected namespace 'prisn-test', got %q", resolved.Namespace)
	}
}

func TestResolveModeAutoDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".prisn", "config.toml")
	os.MkdirAll(filepath.Dir(path), 0755)

	// Context without explicit mode but with server -> should be remote
	cfg := &Config{
		CurrentContext: "remote-implicit",
		Contexts:       make(map[string]*Context),
	}
	cfg.SetContext(&Context{
		Name:      "remote-implicit",
		Server:    "prod.example.com:7331",
		Namespace: "default",
		// Mode not set
	})
	Save(cfg, path)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", originalHome)

	os.Unsetenv("PRISN_CONTEXT")
	os.Unsetenv("PRISN_NAMESPACE")

	resolved, err := Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	// Mode should be auto-detected as remote since server is not localhost
	if resolved.Mode != ModeRemote {
		t.Errorf("expected auto-detected mode remote, got %q", resolved.Mode)
	}
}

func TestContextCloneWithMode(t *testing.T) {
	original := &Context{
		Name:        "k8s-test",
		Mode:        ModeKubernetes,
		KubeContext: "test-cluster",
		Namespace:   "test-ns",
	}

	clone := original.Clone()

	if clone.Mode != ModeKubernetes {
		t.Errorf("Mode not cloned: expected %q, got %q", ModeKubernetes, clone.Mode)
	}
	if clone.KubeContext != "test-cluster" {
		t.Errorf("KubeContext not cloned: expected %q, got %q", "test-cluster", clone.KubeContext)
	}

	// Verify it's a copy
	clone.KubeContext = "modified"
	if original.KubeContext == "modified" {
		t.Error("Clone should not affect original")
	}
}
