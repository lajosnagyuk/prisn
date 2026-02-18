// Package api provides the HTTP API for prisn server.
//
// This file contains token management endpoints.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/lajosnagyuk/prisn/pkg/auth"
	"github.com/lajosnagyuk/prisn/pkg/log"
	"github.com/lajosnagyuk/prisn/pkg/store"
)

// CreateTokenRequest is the request body for creating a token.
type CreateTokenRequest struct {
	Name       string   `json:"name"`
	Role       string   `json:"role"`
	Namespaces []string `json:"namespaces"`
	ExpiresIn  string   `json:"expires_in,omitempty"` // e.g., "24h", "7d", "30d"
}

// CreateTokenResponse is the response for creating a token.
type CreateTokenResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Secret     string    `json:"secret"` // Only returned once at creation
	Role       string    `json:"role"`
	Namespaces []string  `json:"namespaces"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

// TokenResponse is the response for getting a token (without secret).
type TokenResponse struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Role       string     `json:"role"`
	Namespaces []string   `json:"namespaces"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	// Check permission: only admins can list tokens
	token := GetToken(r.Context())
	if token == nil || !token.Role.Can(auth.ActionList, auth.ResourceToken) {
		s.writeError(w, http.StatusForbidden, fmt.Errorf("permission denied: cannot list tokens"))
		return
	}

	role := r.URL.Query().Get("role")
	limit, offset := parsePagination(r)

	records, err := s.store.ListTokens(role)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Convert to response format (hide secrets)
	tokens := make([]TokenResponse, len(records))
	for i, rec := range records {
		tokens[i] = TokenResponse{
			ID:         rec.ID,
			Name:       rec.Name,
			Role:       rec.Role,
			Namespaces: rec.Namespaces,
			CreatedAt:  rec.CreatedAt,
			ExpiresAt:  rec.ExpiresAt,
			RevokedAt:  rec.RevokedAt,
			LastUsedAt: rec.LastUsedAt,
		}
	}

	// Apply pagination
	total := len(tokens)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	page := tokens[offset:end]

	s.writeJSON(w, map[string]any{
		"items":  page,
		"count":  len(page),
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	// Check permission: only admins can create tokens
	token := GetToken(r.Context())
	if token == nil || !token.Role.Can(auth.ActionCreate, auth.ResourceToken) {
		s.writeError(w, http.StatusForbidden, fmt.Errorf("permission denied: cannot create tokens"))
		return
	}

	var req CreateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	// Validate required fields
	if req.Name == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("name is required"))
		return
	}
	if req.Role == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("role is required"))
		return
	}

	// Parse and validate role
	role, err := auth.ParseRole(req.Role)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	// Validate namespaces
	if len(req.Namespaces) == 0 {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("at least one namespace is required"))
		return
	}

	// Only admins can create tokens with wildcard namespace access
	for _, ns := range req.Namespaces {
		if ns == "*" && role != auth.RoleAdmin {
			s.writeError(w, http.StatusBadRequest, fmt.Errorf("only admin tokens can have wildcard namespace access"))
			return
		}
	}

	// Generate the token
	newToken, secret, err := auth.GenerateToken(req.Name, role, req.Namespaces)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Parse expiration if provided
	if req.ExpiresIn != "" {
		dur, err := time.ParseDuration(req.ExpiresIn)
		if err != nil {
			// Try parsing as days (e.g., "7d")
			if len(req.ExpiresIn) > 1 && req.ExpiresIn[len(req.ExpiresIn)-1] == 'd' {
				days := req.ExpiresIn[:len(req.ExpiresIn)-1]
				var d int
				if _, err := fmt.Sscanf(days, "%d", &d); err == nil {
					dur = time.Duration(d) * 24 * time.Hour
				} else {
					s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid expires_in format: %s", req.ExpiresIn))
					return
				}
			} else {
				s.writeError(w, http.StatusBadRequest, fmt.Errorf("invalid expires_in format: %s", req.ExpiresIn))
				return
			}
		}
		expires := time.Now().Add(dur)
		newToken.ExpiresAt = &expires
	}

	// Store the token
	record := &store.TokenRecord{
		ID:         newToken.ID,
		Name:       newToken.Name,
		SecretHash: newToken.SecretHash,
		Role:       string(newToken.Role),
		Namespaces: newToken.Namespaces,
		CreatedAt:  newToken.CreatedAt,
		ExpiresAt:  newToken.ExpiresAt,
		CreatedBy:  token.ID, // Track who created this token
	}

	if err := s.store.CreateToken(record); err != nil {
		s.writeError(w, http.StatusInternalServerError, err)
		return
	}

	// Audit log: token created
	log.Info("audit: token created id=%s name=%q role=%s by=%s", newToken.ID, newToken.Name, newToken.Role, token.ID)

	// Return the token with secret (only time the secret is shown)
	w.WriteHeader(http.StatusCreated)
	s.writeJSON(w, CreateTokenResponse{
		ID:         newToken.ID,
		Name:       newToken.Name,
		Secret:     secret,
		Role:       string(newToken.Role),
		Namespaces: newToken.Namespaces,
		CreatedAt:  newToken.CreatedAt,
		ExpiresAt:  newToken.ExpiresAt,
	})
}

func (s *Server) handleGetToken(w http.ResponseWriter, r *http.Request) {
	// Check permission: only admins can get token details
	token := GetToken(r.Context())
	if token == nil || !token.Role.Can(auth.ActionRead, auth.ResourceToken) {
		s.writeError(w, http.StatusForbidden, fmt.Errorf("permission denied: cannot read tokens"))
		return
	}

	id := r.PathValue("id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("token ID required"))
		return
	}

	record, err := s.store.GetToken(id)
	if err != nil {
		s.writeError(w, http.StatusNotFound, fmt.Errorf("token not found: %s", id))
		return
	}

	s.writeJSON(w, TokenResponse{
		ID:         record.ID,
		Name:       record.Name,
		Role:       record.Role,
		Namespaces: record.Namespaces,
		CreatedAt:  record.CreatedAt,
		ExpiresAt:  record.ExpiresAt,
		RevokedAt:  record.RevokedAt,
		LastUsedAt: record.LastUsedAt,
	})
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	// Check permission: only admins can revoke tokens
	token := GetToken(r.Context())
	if token == nil || !token.Role.Can(auth.ActionUpdate, auth.ResourceToken) {
		s.writeError(w, http.StatusForbidden, fmt.Errorf("permission denied: cannot revoke tokens"))
		return
	}

	id := r.PathValue("id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("token ID required"))
		return
	}

	// Prevent revoking own token
	if token.ID == id {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("cannot revoke your own token"))
		return
	}

	if err := s.store.RevokeToken(id); err != nil {
		if err.Error() == "token already revoked" {
			s.writeError(w, http.StatusConflict, err)
			return
		}
		s.writeError(w, http.StatusNotFound, fmt.Errorf("token not found: %s", id))
		return
	}

	// Audit log: token revoked
	log.Info("audit: token revoked id=%s by=%s", id, token.ID)

	s.writeJSON(w, map[string]string{
		"message": "Token revoked successfully",
		"id":      id,
	})
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	// Check permission: only admins can delete tokens
	token := GetToken(r.Context())
	if token == nil || !token.Role.Can(auth.ActionDelete, auth.ResourceToken) {
		s.writeError(w, http.StatusForbidden, fmt.Errorf("permission denied: cannot delete tokens"))
		return
	}

	id := r.PathValue("id")
	if id == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("token ID required"))
		return
	}

	// Prevent deleting own token
	if token.ID == id {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("cannot delete your own token"))
		return
	}

	if err := s.store.DeleteToken(id); err != nil {
		s.writeError(w, http.StatusNotFound, fmt.Errorf("token not found: %s", id))
		return
	}

	// Audit log: token deleted
	log.Info("audit: token deleted id=%s by=%s", id, token.ID)

	w.WriteHeader(http.StatusNoContent)
}
