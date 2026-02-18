package store

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// NewID generates a new unique ID.
// Format: <prefix>-<timestamp>-<random>
// Example: dep-20240121-a1b2c3
func NewID(prefix string) string {
	ts := time.Now().Format("20060102")
	random := make([]byte, 3)
	rand.Read(random)
	return prefix + "-" + ts + "-" + hex.EncodeToString(random)
}

// NewDeploymentID generates a new deployment ID.
func NewDeploymentID() string {
	return NewID("dep")
}

// NewExecutionID generates a new execution ID.
func NewExecutionID() string {
	return NewID("exec")
}

// NewSecretID generates a new secret ID.
func NewSecretID() string {
	return NewID("sec")
}

// NewCronID generates a new cron schedule ID.
func NewCronID() string {
	return NewID("cron")
}
