package crypto

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	enc, err := NewEncryptorFromPassword("test-password-long-enough-long-enough")
	if err != nil {
		t.Fatalf("NewEncryptorFromPassword failed: %v", err)
	}

	tests := []struct {
		name      string
		plaintext string
	}{
		{"empty", ""},
		{"short", "hello"},
		{"long", strings.Repeat("secret data ", 100)},
		{"special chars", "password!@#$%^&*()"},
		{"unicode", "password-"},
		{"json", `{"key": "value", "nested": {"a": 1}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, err := enc.EncryptString(tt.plaintext)
			if err != nil {
				t.Fatalf("Encrypt failed: %v", err)
			}

			// Empty plaintext returns empty string
			if tt.plaintext == "" {
				if encrypted != "" {
					t.Errorf("expected empty encrypted for empty plaintext")
				}
				return
			}

			// Verify encrypted has prefix
			if !strings.HasPrefix(encrypted, EncryptedPrefix) {
				t.Errorf("encrypted should have prefix %s", EncryptedPrefix)
			}

			// Encrypted should be different from plaintext
			if encrypted == tt.plaintext {
				t.Error("encrypted should be different from plaintext")
			}

			// Decrypt should recover original
			decrypted, err := enc.DecryptString(encrypted)
			if err != nil {
				t.Fatalf("Decrypt failed: %v", err)
			}

			if decrypted != tt.plaintext {
				t.Errorf("decrypted %q != original %q", decrypted, tt.plaintext)
			}
		})
	}
}

func TestDifferentEncryptions(t *testing.T) {
	// Same plaintext should produce different ciphertext each time (due to random nonce)
	enc, _ := NewEncryptorFromPassword("test-password-long-enough")

	plain := "same plaintext"
	encrypted1, _ := enc.EncryptString(plain)
	encrypted2, _ := enc.EncryptString(plain)

	if encrypted1 == encrypted2 {
		t.Error("encrypting same plaintext should produce different ciphertext")
	}

	// But both should decrypt to same value
	dec1, _ := enc.DecryptString(encrypted1)
	dec2, _ := enc.DecryptString(encrypted2)

	if dec1 != plain || dec2 != plain {
		t.Error("both encryptions should decrypt to same value")
	}
}

func TestWrongKey(t *testing.T) {
	enc1, _ := NewEncryptorFromPassword("password1-long-enough")
	enc2, _ := NewEncryptorFromPassword("password2-long-enough")

	encrypted, _ := enc1.EncryptString("secret")

	_, err := enc2.DecryptString(encrypted)
	if err == nil {
		t.Error("decrypting with wrong key should fail")
	}
}

func TestCorruptedData(t *testing.T) {
	enc, _ := NewEncryptorFromPassword("password-long-enough")
	encrypted, _ := enc.EncryptString("secret")

	// Corrupt the ciphertext
	corrupted := encrypted[:len(encrypted)-4] + "XXXX"

	_, err := enc.DecryptString(corrupted)
	if err == nil {
		t.Error("decrypting corrupted data should fail")
	}
}

func TestInvalidEncryptedFormat(t *testing.T) {
	enc, _ := NewEncryptorFromPassword("password-long-enough")

	tests := []string{
		"not-encrypted",
		"enc:v1:",        // empty after prefix
		"enc:v1:!!!",     // invalid base64
		"enc:v2:invalid", // wrong version
	}

	for _, invalid := range tests {
		_, err := enc.DecryptString(invalid)
		if err == nil {
			t.Errorf("should fail for %q", invalid)
		}
	}
}

func TestIsEncrypted(t *testing.T) {
	if IsEncrypted("plain text") {
		t.Error("plain text should not be encrypted")
	}

	if IsEncrypted("enc:v1:") {
		t.Error("just prefix should not be valid")
	}

	if !IsEncrypted("enc:v1:someciphertext") {
		t.Error("valid format should be encrypted")
	}
}

func TestGenerateKey(t *testing.T) {
	key1, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}

	if len(key1) != KeySize {
		t.Errorf("key should be %d bytes, got %d", KeySize, len(key1))
	}

	// Should be different each time
	key2, _ := GenerateKey()
	if bytes.Equal(key1, key2) {
		t.Error("keys should be different")
	}
}

func TestEncodeDecodeKey(t *testing.T) {
	key, _ := GenerateKey()
	encoded := EncodeKey(key)
	decoded, err := DecodeKey(encoded)

	if err != nil {
		t.Fatalf("DecodeKey failed: %v", err)
	}

	if !bytes.Equal(key, decoded) {
		t.Error("decoded key should match original")
	}
}

func TestShortPassword(t *testing.T) {
	_, err := NewEncryptor([]byte("short"))
	if err == nil {
		t.Error("should reject password shorter than 16 bytes")
	}
}

func TestEmptyPassword(t *testing.T) {
	_, err := NewEncryptorFromPassword("")
	if err == nil {
		t.Error("should reject empty password")
	}
}

func TestBinaryData(t *testing.T) {
	enc, _ := NewEncryptorFromPassword("test-password-long-enough")

	// Test with binary data (including null bytes)
	binary := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0x00, 0x05}

	encrypted, err := enc.Encrypt(binary)
	if err != nil {
		t.Fatalf("Encrypt binary failed: %v", err)
	}

	decrypted, err := enc.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt binary failed: %v", err)
	}

	if !bytes.Equal(binary, decrypted) {
		t.Error("binary data should round-trip correctly")
	}
}
