// Package api provides the HTTP API for prisn server.
//
// This file contains RBAC authentication and authorization middleware.
package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/lajosnagyuk/prisn/pkg/auth"
	"github.com/lajosnagyuk/prisn/pkg/store"
)

// contextKey is used for storing values in request context.
type contextKey string

const (
	// tokenContextKey is the context key for the authenticated token.
	tokenContextKey contextKey = "prisn_token"
)

// AuthMiddleware provides RBAC authentication and authorization for API requests.
type AuthMiddleware struct {
	store         *store.Store
	legacyAPIKey  string // Backward compatibility: if set, allow this key as admin
}

// NewAuthMiddleware creates a new auth middleware.
func NewAuthMiddleware(st *store.Store, legacyAPIKey string) *AuthMiddleware {
	return &AuthMiddleware{
		store:        st,
		legacyAPIKey: legacyAPIKey,
	}
}

// Authenticate validates the request token and stores it in context.
// Returns nil if authentication succeeds, error response otherwise.
func (m *AuthMiddleware) Authenticate(r *http.Request) (*auth.Token, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, nil
	}

	// Extract token from header
	token := authHeader
	if strings.HasPrefix(authHeader, "Bearer ") {
		token = strings.TrimPrefix(authHeader, "Bearer ")
	}

	// Check legacy API key first (backward compatibility)
	if m.legacyAPIKey != "" && token == m.legacyAPIKey {
		// Legacy key gets admin access to all namespaces
		return &auth.Token{
			ID:         "legacy-api-key",
			Name:       "Legacy API Key",
			Role:       auth.RoleAdmin,
			Namespaces: []string{"*"},
		}, nil
	}

	// Look up token by secret
	record, err := m.store.GetTokenBySecret(token)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, nil // Invalid token
	}

	// Convert store record to auth.Token
	t := &auth.Token{
		ID:         record.ID,
		Name:       record.Name,
		SecretHash: record.SecretHash,
		Role:       auth.Role(record.Role),
		Namespaces: record.Namespaces,
		CreatedAt:  record.CreatedAt,
		ExpiresAt:  record.ExpiresAt,
		RevokedAt:  record.RevokedAt,
		LastUsedAt: record.LastUsedAt,
	}

	// Check if token is valid
	if t.IsRevoked() {
		return nil, nil
	}
	if t.IsExpired() {
		return nil, nil
	}

	return t, nil
}

// RequireAuth wraps a handler to require authentication.
func (m *AuthMiddleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for public endpoints
		if isPublicEndpoint(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		token, err := m.Authenticate(r)
		if err != nil {
			writeAuthError(w, http.StatusInternalServerError, "Authentication error")
			return
		}
		if token == nil {
			writeAuthError(w, http.StatusUnauthorized, "Invalid or missing API token. Use Authorization: Bearer <token> header.")
			return
		}

		// Store token in context for handlers
		ctx := context.WithValue(r.Context(), tokenContextKey, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequirePermission returns middleware that checks if the token has permission.
func (m *AuthMiddleware) RequirePermission(action auth.Action, resource auth.Resource) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := GetToken(r.Context())
			if token == nil {
				writeAuthError(w, http.StatusUnauthorized, "Authentication required")
				return
			}

			// Get namespace from request (query param or path)
			namespace := extractNamespace(r)

			if !token.CanPerform(action, resource, namespace) {
				writeAuthError(w, http.StatusForbidden,
					"Permission denied: cannot perform "+string(action)+" on "+string(resource)+" in namespace "+namespace)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// GetToken retrieves the authenticated token from the request context.
func GetToken(ctx context.Context) *auth.Token {
	token, ok := ctx.Value(tokenContextKey).(*auth.Token)
	if !ok {
		return nil
	}
	return token
}

// extractNamespace gets the namespace from the request.
// Priority: query param > path param > default
func extractNamespace(r *http.Request) string {
	// Check query param first
	ns := r.URL.Query().Get("namespace")
	if ns != "" {
		return ns
	}

	// Default namespace
	return "default"
}

// writeAuthError writes an authentication/authorization error response.
func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Simple JSON encoding to avoid import cycle
	w.Write([]byte(`{"error":"` + http.StatusText(status) + `","message":"` + message + `"}`))
}
