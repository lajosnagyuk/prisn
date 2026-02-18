package auth

import (
	"testing"
	"time"
)

func TestRolePermissions(t *testing.T) {
	tests := []struct {
		name       string
		role       Role
		resource   Resource
		action     Action
		wantAllow  bool
	}{
		// Admin can do everything
		{"admin can create deployment", RoleAdmin, ResourceDeployment, ActionCreate, true},
		{"admin can delete deployment", RoleAdmin, ResourceDeployment, ActionDelete, true},
		{"admin can create token", RoleAdmin, ResourceToken, ActionCreate, true},
		{"admin can delete token", RoleAdmin, ResourceToken, ActionDelete, true},

		// Developer can manage deployments but not tokens
		{"developer can create deployment", RoleDeveloper, ResourceDeployment, ActionCreate, true},
		{"developer can update deployment", RoleDeveloper, ResourceDeployment, ActionUpdate, true},
		{"developer can delete deployment", RoleDeveloper, ResourceDeployment, ActionDelete, true},
		{"developer can read deployment", RoleDeveloper, ResourceDeployment, ActionRead, true},
		{"developer can view logs", RoleDeveloper, ResourceExecution, ActionLogs, true},
		{"developer cannot create token", RoleDeveloper, ResourceToken, ActionCreate, false},
		{"developer cannot delete token", RoleDeveloper, ResourceToken, ActionDelete, false},

		// Viewer is read-only
		{"viewer can read deployment", RoleViewer, ResourceDeployment, ActionRead, true},
		{"viewer can list deployments", RoleViewer, ResourceDeployment, ActionList, true},
		{"viewer can view logs", RoleViewer, ResourceExecution, ActionLogs, true},
		{"viewer cannot create deployment", RoleViewer, ResourceDeployment, ActionCreate, false},
		{"viewer cannot update deployment", RoleViewer, ResourceDeployment, ActionUpdate, false},
		{"viewer cannot delete deployment", RoleViewer, ResourceDeployment, ActionDelete, false},
		{"viewer cannot create token", RoleViewer, ResourceToken, ActionCreate, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.role.Can(tt.action, tt.resource)
			if got != tt.wantAllow {
				t.Errorf("Role(%s).Can(%s, %s) = %v, want %v",
					tt.role, tt.action, tt.resource, got, tt.wantAllow)
			}
		})
	}
}

func TestTokenGeneration(t *testing.T) {
	// Generate a token
	token, secret, err := GenerateToken("test-token", RoleDeveloper, []string{"default", "staging"})
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	// Token should have correct fields
	if token.Name != "test-token" {
		t.Errorf("token.Name = %q, want %q", token.Name, "test-token")
	}
	if token.Role != RoleDeveloper {
		t.Errorf("token.Role = %q, want %q", token.Role, RoleDeveloper)
	}
	if len(token.Namespaces) != 2 {
		t.Errorf("len(token.Namespaces) = %d, want 2", len(token.Namespaces))
	}

	// Secret should start with prefix
	if len(secret) < 20 {
		t.Errorf("secret too short: %d chars", len(secret))
	}
	if secret[:6] != "prisn_" {
		t.Errorf("secret should start with 'prisn_', got %q", secret[:6])
	}

	// Token ID should be set
	if token.ID == "" {
		t.Error("token.ID should not be empty")
	}

	// Hash should be set and different from secret
	if token.SecretHash == "" {
		t.Error("token.SecretHash should not be empty")
	}
	if token.SecretHash == secret {
		t.Error("token.SecretHash should not equal plaintext secret")
	}
}

func TestTokenValidation(t *testing.T) {
	// Generate a token
	token, secret, err := GenerateToken("test-token", RoleDeveloper, []string{"default"})
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	// Correct secret should validate
	if !token.ValidateSecret(secret) {
		t.Error("ValidateSecret() should return true for correct secret")
	}

	// Wrong secret should not validate
	if token.ValidateSecret("wrong-secret") {
		t.Error("ValidateSecret() should return false for wrong secret")
	}

	// Empty secret should not validate
	if token.ValidateSecret("") {
		t.Error("ValidateSecret() should return false for empty secret")
	}
}

func TestTokenExpiration(t *testing.T) {
	// Generate a token with expiration
	token, _, err := GenerateToken("test-token", RoleDeveloper, []string{"default"})
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	// Token without expiration should not be expired
	if token.IsExpired() {
		t.Error("Token without expiration should not be expired")
	}

	// Set expiration in the past
	past := time.Now().Add(-1 * time.Hour)
	token.ExpiresAt = &past
	if !token.IsExpired() {
		t.Error("Token with past expiration should be expired")
	}

	// Set expiration in the future
	future := time.Now().Add(1 * time.Hour)
	token.ExpiresAt = &future
	if token.IsExpired() {
		t.Error("Token with future expiration should not be expired")
	}
}

func TestTokenNamespaceAccess(t *testing.T) {
	tests := []struct {
		name        string
		namespaces  []string
		checkNS     string
		wantAllowed bool
	}{
		{"wildcard allows all", []string{"*"}, "any-namespace", true},
		{"specific namespace allowed", []string{"default", "staging"}, "default", true},
		{"specific namespace allowed 2", []string{"default", "staging"}, "staging", true},
		{"unlisted namespace denied", []string{"default", "staging"}, "production", false},
		{"empty namespaces denies all", []string{}, "default", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := &Token{Namespaces: tt.namespaces}
			got := token.HasNamespaceAccess(tt.checkNS)
			if got != tt.wantAllowed {
				t.Errorf("HasNamespaceAccess(%q) = %v, want %v", tt.checkNS, got, tt.wantAllowed)
			}
		})
	}
}

func TestTokenCanPerform(t *testing.T) {
	// Developer token with access to "default" namespace
	token := &Token{
		Role:       RoleDeveloper,
		Namespaces: []string{"default"},
	}

	tests := []struct {
		name      string
		action    Action
		resource  Resource
		namespace string
		want      bool
	}{
		// Can perform allowed actions in allowed namespace
		{"create deployment in default", ActionCreate, ResourceDeployment, "default", true},
		{"read deployment in default", ActionRead, ResourceDeployment, "default", true},

		// Cannot perform disallowed actions
		{"create token", ActionCreate, ResourceToken, "default", false},

		// Cannot access other namespaces
		{"create in production", ActionCreate, ResourceDeployment, "production", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := token.CanPerform(tt.action, tt.resource, tt.namespace)
			if got != tt.want {
				t.Errorf("CanPerform(%s, %s, %s) = %v, want %v",
					tt.action, tt.resource, tt.namespace, got, tt.want)
			}
		})
	}
}

func TestParseRole(t *testing.T) {
	tests := []struct {
		input   string
		want    Role
		wantErr bool
	}{
		{"admin", RoleAdmin, false},
		{"developer", RoleDeveloper, false},
		{"viewer", RoleViewer, false},
		{"ADMIN", RoleAdmin, false},    // case insensitive
		{"Admin", RoleAdmin, false},    // case insensitive
		{"invalid", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseRole(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseRole(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseRole(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTokenPrefix(t *testing.T) {
	// Extract token ID from a full secret
	secret := "prisn_abc123def456"
	id := ExtractTokenID(secret)
	if id != "abc123def456" {
		t.Errorf("ExtractTokenID(%q) = %q, want %q", secret, id, "abc123def456")
	}

	// Invalid prefix returns empty
	id = ExtractTokenID("invalid_token")
	if id != "" {
		t.Errorf("ExtractTokenID(invalid) = %q, want empty", id)
	}
}

func TestRevokedToken(t *testing.T) {
	token := &Token{
		Role:       RoleDeveloper,
		Namespaces: []string{"*"},
	}

	// Not revoked by default
	if token.IsRevoked() {
		t.Error("New token should not be revoked")
	}

	// Can perform actions when not revoked
	if !token.CanPerform(ActionCreate, ResourceDeployment, "default") {
		t.Error("Non-revoked token should be able to perform actions")
	}

	// Revoke the token
	token.Revoke()

	// Should be revoked now
	if !token.IsRevoked() {
		t.Error("Token should be revoked after Revoke()")
	}

	// Cannot perform actions when revoked
	if token.CanPerform(ActionCreate, ResourceDeployment, "default") {
		t.Error("Revoked token should not be able to perform actions")
	}
}
