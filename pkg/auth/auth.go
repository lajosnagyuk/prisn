// Package auth provides authentication and authorization for the prisn API.
//
// RBAC Model:
//   - Tokens: API keys with associated roles and namespace scopes
//   - Roles: admin, developer, viewer with predefined permissions
//   - Namespaces: Isolation boundaries for multi-tenant access
//
// Token format: prisn_<random_id>
// Secrets are hashed using bcrypt before storage.
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// TokenPrefix is the prefix for all prisn API tokens.
const TokenPrefix = "prisn_"

// Role defines the permission level of a token.
type Role string

const (
	// RoleAdmin has full access to all resources and operations.
	RoleAdmin Role = "admin"

	// RoleDeveloper can manage deployments and view logs in allowed namespaces.
	RoleDeveloper Role = "developer"

	// RoleViewer has read-only access to allowed namespaces.
	RoleViewer Role = "viewer"
)

// Resource types that can be protected.
type Resource string

const (
	ResourceDeployment Resource = "deployment"
	ResourceJob        Resource = "job"
	ResourceCronJob    Resource = "cronjob"
	ResourceExecution  Resource = "execution"
	ResourceNamespace  Resource = "namespace"
	ResourceToken      Resource = "token"
	ResourceSecret     Resource = "secret"
)

// Action types that can be performed on resources.
type Action string

const (
	ActionCreate Action = "create"
	ActionRead   Action = "read"
	ActionUpdate Action = "update"
	ActionDelete Action = "delete"
	ActionList   Action = "list"
	ActionLogs   Action = "logs"
	ActionScale  Action = "scale"
)

// rolePermissions defines what each role can do.
// Map structure: role -> resource -> allowed actions
var rolePermissions = map[Role]map[Resource][]Action{
	RoleAdmin: {
		ResourceDeployment: {ActionCreate, ActionRead, ActionUpdate, ActionDelete, ActionList, ActionScale},
		ResourceJob:        {ActionCreate, ActionRead, ActionUpdate, ActionDelete, ActionList},
		ResourceCronJob:    {ActionCreate, ActionRead, ActionUpdate, ActionDelete, ActionList},
		ResourceExecution:  {ActionRead, ActionList, ActionLogs},
		ResourceNamespace:  {ActionCreate, ActionRead, ActionUpdate, ActionDelete, ActionList},
		ResourceToken:      {ActionCreate, ActionRead, ActionUpdate, ActionDelete, ActionList},
		ResourceSecret:     {ActionCreate, ActionRead, ActionUpdate, ActionDelete, ActionList},
	},
	RoleDeveloper: {
		ResourceDeployment: {ActionCreate, ActionRead, ActionUpdate, ActionDelete, ActionList, ActionScale},
		ResourceJob:        {ActionCreate, ActionRead, ActionUpdate, ActionDelete, ActionList},
		ResourceCronJob:    {ActionCreate, ActionRead, ActionUpdate, ActionDelete, ActionList},
		ResourceExecution:  {ActionRead, ActionList, ActionLogs},
		ResourceSecret:     {ActionCreate, ActionRead, ActionUpdate, ActionDelete, ActionList},
		// No access to Token or Namespace management
	},
	RoleViewer: {
		ResourceDeployment: {ActionRead, ActionList},
		ResourceJob:        {ActionRead, ActionList},
		ResourceCronJob:    {ActionRead, ActionList},
		ResourceExecution:  {ActionRead, ActionList, ActionLogs},
		// Read-only, no create/update/delete
	},
}

// Can returns true if the role allows the action on the resource.
func (r Role) Can(action Action, resource Resource) bool {
	perms, ok := rolePermissions[r]
	if !ok {
		return false
	}

	actions, ok := perms[resource]
	if !ok {
		return false
	}

	for _, a := range actions {
		if a == action {
			return true
		}
	}
	return false
}

// ParseRole parses a string into a Role, returning an error if invalid.
func ParseRole(s string) (Role, error) {
	switch strings.ToLower(s) {
	case "admin":
		return RoleAdmin, nil
	case "developer":
		return RoleDeveloper, nil
	case "viewer":
		return RoleViewer, nil
	default:
		return "", fmt.Errorf("invalid role %q: must be admin, developer, or viewer", s)
	}
}

// Token represents an API token with associated permissions.
type Token struct {
	// ID is the unique identifier for the token (not the secret).
	ID string `json:"id"`

	// Name is a human-readable description of the token's purpose.
	Name string `json:"name"`

	// SecretHash is the bcrypt hash of the token secret.
	// The plaintext secret is only returned once at creation time.
	SecretHash string `json:"secret_hash"`

	// Role defines the permission level.
	Role Role `json:"role"`

	// Namespaces lists which namespaces this token can access.
	// Use ["*"] for access to all namespaces (admin only).
	Namespaces []string `json:"namespaces"`

	// CreatedAt is when the token was created.
	CreatedAt time.Time `json:"created_at"`

	// ExpiresAt is when the token expires. Nil means never expires.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	// RevokedAt is when the token was revoked. Nil means not revoked.
	RevokedAt *time.Time `json:"revoked_at,omitempty"`

	// LastUsedAt is when the token was last used for authentication.
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`

	// CreatedBy is the token ID that created this token (for audit).
	CreatedBy string `json:"created_by,omitempty"`
}

// GenerateToken creates a new token with the given parameters.
// Returns the Token (with hashed secret) and the plaintext secret.
// The plaintext secret is only available at creation time.
func GenerateToken(name string, role Role, namespaces []string) (*Token, string, error) {
	// Generate random ID (16 bytes = 32 hex chars)
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, "", fmt.Errorf("failed to generate token ID: %w", err)
	}
	id := hex.EncodeToString(idBytes)

	// Generate random secret (32 bytes = 64 hex chars)
	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, "", fmt.Errorf("failed to generate token secret: %w", err)
	}
	secret := TokenPrefix + hex.EncodeToString(secretBytes)

	// Hash the secret for storage
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return nil, "", fmt.Errorf("failed to hash secret: %w", err)
	}

	token := &Token{
		ID:         id,
		Name:       name,
		SecretHash: string(hash),
		Role:       role,
		Namespaces: namespaces,
		CreatedAt:  time.Now(),
	}

	return token, secret, nil
}

// ValidateSecret checks if the provided secret matches the token's hash.
func (t *Token) ValidateSecret(secret string) bool {
	if secret == "" {
		return false
	}
	err := bcrypt.CompareHashAndPassword([]byte(t.SecretHash), []byte(secret))
	return err == nil
}

// IsExpired returns true if the token has expired.
func (t *Token) IsExpired() bool {
	if t.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*t.ExpiresAt)
}

// IsRevoked returns true if the token has been revoked.
func (t *Token) IsRevoked() bool {
	return t.RevokedAt != nil
}

// Revoke marks the token as revoked.
func (t *Token) Revoke() {
	now := time.Now()
	t.RevokedAt = &now
}

// HasNamespaceAccess returns true if the token has access to the given namespace.
func (t *Token) HasNamespaceAccess(namespace string) bool {
	for _, ns := range t.Namespaces {
		if ns == "*" || ns == namespace {
			return true
		}
	}
	return false
}

// CanPerform returns true if the token can perform the action on the resource in the namespace.
func (t *Token) CanPerform(action Action, resource Resource, namespace string) bool {
	// Check if token is valid
	if t.IsRevoked() || t.IsExpired() {
		return false
	}

	// Check namespace access
	if !t.HasNamespaceAccess(namespace) {
		return false
	}

	// Check role permission
	return t.Role.Can(action, resource)
}

// ExtractTokenID extracts the token ID from a full secret string.
// Returns empty string if the secret doesn't have the correct prefix.
func ExtractTokenID(secret string) string {
	if !strings.HasPrefix(secret, TokenPrefix) {
		return ""
	}
	return strings.TrimPrefix(secret, TokenPrefix)
}

// MaskedSecret returns a masked version of the secret for display.
// Shows first 10 and last 4 characters only.
func MaskedSecret(secret string) string {
	if len(secret) <= 14 {
		return "****"
	}
	return secret[:10] + "..." + secret[len(secret)-4:]
}

// ValidRoles returns all valid role names.
func ValidRoles() []Role {
	return []Role{RoleAdmin, RoleDeveloper, RoleViewer}
}
