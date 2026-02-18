// Package crypto provides encryption utilities for secrets at rest.
//
// Uses AES-256-GCM for authenticated encryption. Keys are derived from
// a master password using Argon2id.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/argon2"
)

const (
	// SaltSize is the size of the salt used for key derivation.
	SaltSize = 16

	// NonceSize is the size of the GCM nonce (12 bytes recommended).
	NonceSize = 12

	// KeySize is the size of the AES key (32 bytes = 256 bits).
	KeySize = 32

	// EncryptedPrefix identifies encrypted values.
	EncryptedPrefix = "enc:v1:"
)

// Encryptor provides encryption and decryption using AES-256-GCM.
type Encryptor struct {
	key []byte
}

// NewEncryptor creates a new encryptor from a master key.
// The key should be a secure random value (32 bytes recommended).
func NewEncryptor(masterKey []byte) (*Encryptor, error) {
	if len(masterKey) < 16 {
		return nil, fmt.Errorf("master key too short (need at least 16 bytes)")
	}

	// If key is not 32 bytes, derive it using Argon2id
	key := masterKey
	if len(masterKey) != KeySize {
		// Use a fixed salt for key derivation (we're just normalizing the key length)
		salt := []byte("prisn-secret-key")
		key = argon2.IDKey(masterKey, salt, 1, 64*1024, 4, KeySize)
	}

	return &Encryptor{key: key}, nil
}

// NewEncryptorFromPassword creates an encryptor from a password string.
func NewEncryptorFromPassword(password string) (*Encryptor, error) {
	if password == "" {
		return nil, fmt.Errorf("password cannot be empty")
	}
	return NewEncryptor([]byte(password))
}

// Encrypt encrypts plaintext and returns a base64-encoded ciphertext
// with the format "enc:v1:<base64(salt || nonce || ciphertext)>"
func (e *Encryptor) Encrypt(plaintext []byte) (string, error) {
	if len(plaintext) == 0 {
		return "", nil
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt with authentication
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	// Combine nonce and ciphertext
	combined := make([]byte, NonceSize+len(ciphertext))
	copy(combined[:NonceSize], nonce)
	copy(combined[NonceSize:], ciphertext)

	// Return with prefix for identification
	return EncryptedPrefix + base64.StdEncoding.EncodeToString(combined), nil
}

// EncryptString encrypts a string and returns the encrypted form.
func (e *Encryptor) EncryptString(s string) (string, error) {
	return e.Encrypt([]byte(s))
}

// Decrypt decrypts a value that was encrypted with Encrypt.
func (e *Encryptor) Decrypt(encrypted string) ([]byte, error) {
	if encrypted == "" {
		return nil, nil
	}

	// Check prefix
	if len(encrypted) < len(EncryptedPrefix) || encrypted[:len(EncryptedPrefix)] != EncryptedPrefix {
		return nil, fmt.Errorf("not an encrypted value (missing prefix)")
	}

	// Decode base64
	combined, err := base64.StdEncoding.DecodeString(encrypted[len(EncryptedPrefix):])
	if err != nil {
		return nil, fmt.Errorf("failed to decode: %w", err)
	}

	if len(combined) < NonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}

	// Extract nonce and ciphertext
	nonce := combined[:NonceSize]
	ciphertext := combined[NonceSize:]

	// Decrypt
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (wrong key or corrupted data)")
	}

	return plaintext, nil
}

// DecryptString decrypts an encrypted string.
func (e *Encryptor) DecryptString(encrypted string) (string, error) {
	plaintext, err := e.Decrypt(encrypted)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// IsEncrypted returns true if the value appears to be encrypted.
func IsEncrypted(value string) bool {
	return len(value) > len(EncryptedPrefix) && value[:len(EncryptedPrefix)] == EncryptedPrefix
}

// GenerateKey generates a random 32-byte key suitable for encryption.
func GenerateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}
	return key, nil
}

// EncodeKey encodes a key to a base64 string for storage/display.
func EncodeKey(key []byte) string {
	return base64.StdEncoding.EncodeToString(key)
}

// DecodeKey decodes a base64-encoded key.
func DecodeKey(encoded string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(encoded)
}
