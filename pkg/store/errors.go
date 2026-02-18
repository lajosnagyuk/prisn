package store

import "errors"

// Sentinel errors for store operations.
// Use errors.Is() to check for these errors.
var (
	// ErrNotFound is returned when a requested resource doesn't exist.
	ErrNotFound = errors.New("not found")

	// ErrAlreadyExists is returned when creating a resource that already exists.
	ErrAlreadyExists = errors.New("already exists")

	// ErrAlreadyRevoked is returned when trying to revoke an already-revoked token.
	ErrAlreadyRevoked = errors.New("already revoked")

	// ErrInvalidCredentials is returned when token authentication fails.
	ErrInvalidCredentials = errors.New("invalid credentials")

	// ErrExpired is returned when a token has expired.
	ErrExpired = errors.New("token expired")
)

// NotFoundError wraps ErrNotFound with additional context.
type NotFoundError struct {
	Resource string
	ID       string
}

func (e *NotFoundError) Error() string {
	if e.ID != "" {
		return e.Resource + " not found: " + e.ID
	}
	return e.Resource + " not found"
}

func (e *NotFoundError) Is(target error) bool {
	return target == ErrNotFound
}

// AlreadyExistsError wraps ErrAlreadyExists with additional context.
type AlreadyExistsError struct {
	Resource string
	ID       string
}

func (e *AlreadyExistsError) Error() string {
	if e.ID != "" {
		return e.Resource + " already exists: " + e.ID
	}
	return e.Resource + " already exists"
}

func (e *AlreadyExistsError) Is(target error) bool {
	return target == ErrAlreadyExists
}
