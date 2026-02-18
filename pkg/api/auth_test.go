package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/auth"
	"github.com/lajosnagyuk/prisn/pkg/store"
	"golang.org/x/crypto/bcrypt"
)

func TestAuthMiddleware(t *testing.T) {
	// Create test store
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create a test token
	secret := "prisn_testtoken123456789012345678901234567890123456"
	hash, _ := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	testToken := &store.TokenRecord{
		ID:         "test-token-id",
		Name:       "Test Token",
		SecretHash: string(hash),
		Role:       "admin",
		Namespaces: []string{"*"},
		CreatedAt:  time.Now(),
	}
	if err := st.CreateToken(testToken); err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	mw := NewAuthMiddleware(st, "")

	tests := []struct {
		name       string
		authHeader string
		wantToken  bool
		wantNil    bool
	}{
		{
			name:       "no auth header",
			authHeader: "",
			wantToken:  false,
			wantNil:    true,
		},
		{
			name:       "valid bearer token",
			authHeader: "Bearer " + secret,
			wantToken:  true,
		},
		{
			name:       "valid raw token",
			authHeader: secret,
			wantToken:  true,
		},
		{
			name:       "invalid token",
			authHeader: "Bearer invalid-token",
			wantToken:  false,
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/deployments", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			token, err := mw.Authenticate(req)
			if err != nil {
				t.Errorf("Authenticate() error = %v", err)
				return
			}

			if tt.wantNil && token != nil {
				t.Errorf("expected nil token, got %+v", token)
			}
			if tt.wantToken && token == nil {
				t.Error("expected token, got nil")
			}
			if tt.wantToken && token != nil && token.ID != testToken.ID {
				t.Errorf("token.ID = %q, want %q", token.ID, testToken.ID)
			}
		})
	}
}

func TestAuthMiddlewareLegacyKey(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	legacyKey := "my-legacy-api-key"
	mw := NewAuthMiddleware(st, legacyKey)

	tests := []struct {
		name       string
		authHeader string
		wantAdmin  bool
	}{
		{
			name:       "legacy key as bearer",
			authHeader: "Bearer " + legacyKey,
			wantAdmin:  true,
		},
		{
			name:       "legacy key raw",
			authHeader: legacyKey,
			wantAdmin:  true,
		},
		{
			name:       "wrong key",
			authHeader: "wrong-key",
			wantAdmin:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/deployments", nil)
			req.Header.Set("Authorization", tt.authHeader)

			token, err := mw.Authenticate(req)
			if err != nil {
				t.Errorf("Authenticate() error = %v", err)
				return
			}

			if tt.wantAdmin {
				if token == nil {
					t.Error("expected admin token, got nil")
					return
				}
				if token.Role != auth.RoleAdmin {
					t.Errorf("token.Role = %q, want admin", token.Role)
				}
			}
		})
	}
}

func TestAuthMiddlewareRevokedToken(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create and revoke a token
	secret := "prisn_revokedtoken12345678901234567890123456789012"
	hash, _ := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	testToken := &store.TokenRecord{
		ID:         "revoked-token",
		Name:       "Revoked Token",
		SecretHash: string(hash),
		Role:       "admin",
		Namespaces: []string{"*"},
		CreatedAt:  time.Now(),
	}
	st.CreateToken(testToken)
	st.RevokeToken(testToken.ID)

	mw := NewAuthMiddleware(st, "")

	req := httptest.NewRequest("GET", "/api/v1/deployments", nil)
	req.Header.Set("Authorization", "Bearer "+secret)

	token, err := mw.Authenticate(req)
	if err != nil {
		t.Errorf("Authenticate() error = %v", err)
		return
	}
	if token != nil {
		t.Error("revoked token should return nil")
	}
}

func TestAuthMiddlewareExpiredToken(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create an expired token
	secret := "prisn_expiredtoken12345678901234567890123456789012"
	hash, _ := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	pastTime := time.Now().Add(-1 * time.Hour)
	testToken := &store.TokenRecord{
		ID:         "expired-token",
		Name:       "Expired Token",
		SecretHash: string(hash),
		Role:       "admin",
		Namespaces: []string{"*"},
		CreatedAt:  time.Now().Add(-24 * time.Hour),
		ExpiresAt:  &pastTime,
	}
	st.CreateToken(testToken)

	mw := NewAuthMiddleware(st, "")

	req := httptest.NewRequest("GET", "/api/v1/deployments", nil)
	req.Header.Set("Authorization", "Bearer "+secret)

	token, err := mw.Authenticate(req)
	if err != nil {
		t.Errorf("Authenticate() error = %v", err)
		return
	}
	if token != nil {
		t.Error("expired token should return nil")
	}
}

func TestTokensAPI(t *testing.T) {
	// Create test store
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create an admin token for making requests
	adminSecret := "prisn_admintoken12345678901234567890123456789012"
	hash, _ := bcrypt.GenerateFromPassword([]byte(adminSecret), bcrypt.MinCost)
	adminToken := &store.TokenRecord{
		ID:         "admin-token",
		Name:       "Admin Token",
		SecretHash: string(hash),
		Role:       "admin",
		Namespaces: []string{"*"},
		CreatedAt:  time.Now(),
	}
	if err := st.CreateToken(adminToken); err != nil {
		t.Fatalf("failed to create admin token: %v", err)
	}

	// Create test server
	srv, err := NewServer(Config{
		Addr:  ":0",
		Store: st,
	})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("create token", func(t *testing.T) {
		body := map[string]any{
			"name":       "New Developer Token",
			"role":       "developer",
			"namespaces": []string{"default", "staging"},
		}
		jsonBody, _ := json.Marshal(body)

		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/tokens", bytes.NewReader(jsonBody))
		req.Header.Set("Authorization", "Bearer "+adminSecret)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
		}

		var result CreateTokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if result.Name != "New Developer Token" {
			t.Errorf("name = %q, want %q", result.Name, "New Developer Token")
		}
		if result.Role != "developer" {
			t.Errorf("role = %q, want %q", result.Role, "developer")
		}
		if result.Secret == "" {
			t.Error("secret should not be empty")
		}
		if len(result.Namespaces) != 2 {
			t.Errorf("len(namespaces) = %d, want 2", len(result.Namespaces))
		}
	})

	t.Run("create token with expiration", func(t *testing.T) {
		body := map[string]any{
			"name":       "Expiring Token",
			"role":       "viewer",
			"namespaces": []string{"default"},
			"expires_in": "24h",
		}
		jsonBody, _ := json.Marshal(body)

		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/tokens", bytes.NewReader(jsonBody))
		req.Header.Set("Authorization", "Bearer "+adminSecret)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
		}

		var result CreateTokenResponse
		json.NewDecoder(resp.Body).Decode(&result)

		if result.ExpiresAt == nil {
			t.Error("expires_at should not be nil")
		}
	})

	t.Run("list tokens", func(t *testing.T) {
		req, _ := http.NewRequest("GET", ts.URL+"/api/v1/tokens", nil)
		req.Header.Set("Authorization", "Bearer "+adminSecret)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		var result map[string]any
		json.NewDecoder(resp.Body).Decode(&result)

		count := int(result["count"].(float64))
		if count < 1 {
			t.Errorf("count = %d, want at least 1", count)
		}
	})

	t.Run("list tokens by role", func(t *testing.T) {
		req, _ := http.NewRequest("GET", ts.URL+"/api/v1/tokens?role=developer", nil)
		req.Header.Set("Authorization", "Bearer "+adminSecret)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
	})

	t.Run("unauthorized without token", func(t *testing.T) {
		req, _ := http.NewRequest("GET", ts.URL+"/api/v1/tokens", nil)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		// Server without API key requirement allows unauthenticated access
		// but the token handler checks permissions
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
		}
	})

	t.Run("developer cannot create tokens", func(t *testing.T) {
		// Create a developer token
		devSecret := "prisn_devtoken123456789012345678901234567890123"
		hash, _ := bcrypt.GenerateFromPassword([]byte(devSecret), bcrypt.MinCost)
		devToken := &store.TokenRecord{
			ID:         "dev-token",
			Name:       "Dev Token",
			SecretHash: string(hash),
			Role:       "developer",
			Namespaces: []string{"default"},
			CreatedAt:  time.Now(),
		}
		st.CreateToken(devToken)

		body := map[string]any{
			"name":       "Unauthorized Token",
			"role":       "viewer",
			"namespaces": []string{"default"},
		}
		jsonBody, _ := json.Marshal(body)

		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/tokens", bytes.NewReader(jsonBody))
		req.Header.Set("Authorization", "Bearer "+devSecret)
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusForbidden)
		}
	})
}

func TestRevokeTokenAPI(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create admin and target tokens
	adminSecret := "prisn_admintoken12345678901234567890123456789012"
	hash, _ := bcrypt.GenerateFromPassword([]byte(adminSecret), bcrypt.MinCost)
	st.CreateToken(&store.TokenRecord{
		ID:         "admin-token",
		Name:       "Admin Token",
		SecretHash: string(hash),
		Role:       "admin",
		Namespaces: []string{"*"},
		CreatedAt:  time.Now(),
	})

	st.CreateToken(&store.TokenRecord{
		ID:         "target-token",
		Name:       "Target Token",
		SecretHash: "hash",
		Role:       "developer",
		Namespaces: []string{"default"},
		CreatedAt:  time.Now(),
	})

	srv, _ := NewServer(Config{Addr: ":0", Store: st})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("revoke token", func(t *testing.T) {
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/tokens/target-token/revoke", nil)
		req.Header.Set("Authorization", "Bearer "+adminSecret)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}

		// Verify token is revoked
		rec, _ := st.GetToken("target-token")
		if rec.RevokedAt == nil {
			t.Error("token should be revoked")
		}
	})

	t.Run("cannot revoke own token", func(t *testing.T) {
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/tokens/admin-token/revoke", nil)
		req.Header.Set("Authorization", "Bearer "+adminSecret)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
	})
}

func TestDeleteTokenAPI(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	adminSecret := "prisn_admintoken12345678901234567890123456789012"
	hash, _ := bcrypt.GenerateFromPassword([]byte(adminSecret), bcrypt.MinCost)
	st.CreateToken(&store.TokenRecord{
		ID:         "admin-token",
		Name:       "Admin Token",
		SecretHash: string(hash),
		Role:       "admin",
		Namespaces: []string{"*"},
		CreatedAt:  time.Now(),
	})

	st.CreateToken(&store.TokenRecord{
		ID:         "delete-me",
		Name:       "Delete Me",
		SecretHash: "hash",
		Role:       "viewer",
		Namespaces: []string{"default"},
		CreatedAt:  time.Now(),
	})

	srv, _ := NewServer(Config{Addr: ":0", Store: st})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	t.Run("delete token", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/tokens/delete-me", nil)
		req.Header.Set("Authorization", "Bearer "+adminSecret)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
		}

		// Verify token is deleted
		_, err = st.GetToken("delete-me")
		if err == nil {
			t.Error("token should be deleted")
		}
	})

	t.Run("cannot delete own token", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", ts.URL+"/api/v1/tokens/admin-token", nil)
		req.Header.Set("Authorization", "Bearer "+adminSecret)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
		}
	})
}
