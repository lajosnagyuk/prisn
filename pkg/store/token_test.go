package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestCreateAndGetToken(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create a token
	secret := "prisn_testsecret123"
	hash, _ := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)

	token := &TokenRecord{
		ID:         "test-token-1",
		Name:       "Test Token",
		SecretHash: string(hash),
		Role:       "developer",
		Namespaces: []string{"default", "staging"},
		CreatedAt:  time.Now(),
	}

	if err := st.CreateToken(token); err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Get the token back
	got, err := st.GetToken("test-token-1")
	if err != nil {
		t.Fatalf("GetToken() error = %v", err)
	}

	if got.ID != token.ID {
		t.Errorf("got.ID = %q, want %q", got.ID, token.ID)
	}
	if got.Name != token.Name {
		t.Errorf("got.Name = %q, want %q", got.Name, token.Name)
	}
	if got.Role != token.Role {
		t.Errorf("got.Role = %q, want %q", got.Role, token.Role)
	}
	if len(got.Namespaces) != 2 {
		t.Errorf("len(got.Namespaces) = %d, want 2", len(got.Namespaces))
	}
}

func TestGetTokenBySecret(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create a token with known secret
	secret := "prisn_findmebysecret"
	hash, _ := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)

	token := &TokenRecord{
		ID:         "find-by-secret",
		Name:       "Find By Secret",
		SecretHash: string(hash),
		Role:       "admin",
		Namespaces: []string{"*"},
		CreatedAt:  time.Now(),
	}

	if err := st.CreateToken(token); err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Find by correct secret
	got, err := st.GetTokenBySecret(secret)
	if err != nil {
		t.Fatalf("GetTokenBySecret() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetTokenBySecret() returned nil for valid secret")
	}
	if got.ID != token.ID {
		t.Errorf("got.ID = %q, want %q", got.ID, token.ID)
	}

	// Wrong secret should return nil
	got, err = st.GetTokenBySecret("wrong-secret")
	if err != nil {
		t.Fatalf("GetTokenBySecret() error = %v", err)
	}
	if got != nil {
		t.Error("GetTokenBySecret() should return nil for wrong secret")
	}
}

func TestListTokens(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create multiple tokens
	tokens := []*TokenRecord{
		{ID: "token-1", Name: "Token 1", SecretHash: "hash1", Role: "admin", Namespaces: []string{"*"}, CreatedAt: time.Now()},
		{ID: "token-2", Name: "Token 2", SecretHash: "hash2", Role: "developer", Namespaces: []string{"default"}, CreatedAt: time.Now()},
		{ID: "token-3", Name: "Token 3", SecretHash: "hash3", Role: "developer", Namespaces: []string{"staging"}, CreatedAt: time.Now()},
		{ID: "token-4", Name: "Token 4", SecretHash: "hash4", Role: "viewer", Namespaces: []string{"default"}, CreatedAt: time.Now()},
	}

	for _, tok := range tokens {
		if err := st.CreateToken(tok); err != nil {
			t.Fatalf("CreateToken() error = %v", err)
		}
	}

	// List all tokens
	all, err := st.ListTokens("")
	if err != nil {
		t.Fatalf("ListTokens() error = %v", err)
	}
	if len(all) != 4 {
		t.Errorf("ListTokens() returned %d tokens, want 4", len(all))
	}

	// List developer tokens only
	devs, err := st.ListTokens("developer")
	if err != nil {
		t.Fatalf("ListTokens(developer) error = %v", err)
	}
	if len(devs) != 2 {
		t.Errorf("ListTokens(developer) returned %d tokens, want 2", len(devs))
	}

	// List admin tokens only
	admins, err := st.ListTokens("admin")
	if err != nil {
		t.Fatalf("ListTokens(admin) error = %v", err)
	}
	if len(admins) != 1 {
		t.Errorf("ListTokens(admin) returned %d tokens, want 1", len(admins))
	}
}

func TestRevokeToken(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create a token
	token := &TokenRecord{
		ID:         "revoke-me",
		Name:       "Revoke Me",
		SecretHash: "hash",
		Role:       "developer",
		Namespaces: []string{"default"},
		CreatedAt:  time.Now(),
	}

	if err := st.CreateToken(token); err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Token should not be revoked initially
	got, _ := st.GetToken("revoke-me")
	if got.RevokedAt != nil {
		t.Error("Token should not be revoked initially")
	}

	// Revoke the token
	if err := st.RevokeToken("revoke-me"); err != nil {
		t.Fatalf("RevokeToken() error = %v", err)
	}

	// Token should be revoked now
	got, _ = st.GetToken("revoke-me")
	if got.RevokedAt == nil {
		t.Error("Token should be revoked after RevokeToken()")
	}

	// Cannot revoke already revoked token
	if err := st.RevokeToken("revoke-me"); err == nil {
		t.Error("RevokeToken() should fail for already revoked token")
	}

	// Revoked token should not be found by GetTokenBySecret
	secret := "prisn_revokesecret"
	hash, _ := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	token2 := &TokenRecord{
		ID:         "revoke-me-2",
		Name:       "Revoke Me 2",
		SecretHash: string(hash),
		Role:       "developer",
		Namespaces: []string{"default"},
		CreatedAt:  time.Now(),
	}
	st.CreateToken(token2)
	st.RevokeToken("revoke-me-2")

	got, _ = st.GetTokenBySecret(secret)
	if got != nil {
		t.Error("GetTokenBySecret() should not return revoked tokens")
	}
}

func TestDeleteToken(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create a token
	token := &TokenRecord{
		ID:         "delete-me",
		Name:       "Delete Me",
		SecretHash: "hash",
		Role:       "developer",
		Namespaces: []string{"default"},
		CreatedAt:  time.Now(),
	}

	if err := st.CreateToken(token); err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Delete the token
	if err := st.DeleteToken("delete-me"); err != nil {
		t.Fatalf("DeleteToken() error = %v", err)
	}

	// Token should not exist
	_, err = st.GetToken("delete-me")
	if err == nil {
		t.Error("GetToken() should fail for deleted token")
	}

	// Cannot delete non-existent token
	if err := st.DeleteToken("delete-me"); err == nil {
		t.Error("DeleteToken() should fail for non-existent token")
	}
}

func TestTokenCount(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Initially zero tokens
	count, _ := st.TokenCount("")
	if count != 0 {
		t.Errorf("TokenCount() = %d, want 0", count)
	}

	// Create some tokens
	for i := 0; i < 3; i++ {
		st.CreateToken(&TokenRecord{
			ID: fmt.Sprintf("token-%d", i), Name: "Token", SecretHash: "hash",
			Role: "developer", Namespaces: []string{"default"}, CreatedAt: time.Now(),
		})
	}
	st.CreateToken(&TokenRecord{
		ID: "admin-token", Name: "Admin", SecretHash: "hash",
		Role: "admin", Namespaces: []string{"*"}, CreatedAt: time.Now(),
	})

	// Count all
	count, _ = st.TokenCount("")
	if count != 4 {
		t.Errorf("TokenCount() = %d, want 4", count)
	}

	// Count by role
	count, _ = st.TokenCount("developer")
	if count != 3 {
		t.Errorf("TokenCount(developer) = %d, want 3", count)
	}

	count, _ = st.TokenCount("admin")
	if count != 1 {
		t.Errorf("TokenCount(admin) = %d, want 1", count)
	}
}

func TestTokenWithExpiration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	// Create token with expiration
	expires := time.Now().Add(24 * time.Hour)
	token := &TokenRecord{
		ID:         "expires-token",
		Name:       "Expires Token",
		SecretHash: "hash",
		Role:       "developer",
		Namespaces: []string{"default"},
		CreatedAt:  time.Now(),
		ExpiresAt:  &expires,
	}

	if err := st.CreateToken(token); err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}

	// Get token and verify expiration is stored
	got, _ := st.GetToken("expires-token")
	if got.ExpiresAt == nil {
		t.Error("ExpiresAt should not be nil")
	}
	if got.ExpiresAt.Sub(expires) > time.Second {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, expires)
	}
}
