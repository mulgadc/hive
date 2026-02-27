package handlers_iam

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
)

const masterKeySize = 32 // AES-256

// GenerateMasterKey returns 32 cryptographically random bytes suitable
// for use as an AES-256-GCM key.
func GenerateMasterKey() ([]byte, error) {
	key := make([]byte, masterKeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	return key, nil
}

// LoadMasterKey reads a master key from disk and validates it is exactly 32 bytes.
func LoadMasterKey(path string) ([]byte, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read master key: %w", err)
	}
	if len(key) != masterKeySize {
		return nil, fmt.Errorf("master key must be %d bytes, got %d", masterKeySize, len(key))
	}
	return key, nil
}

// SaveMasterKey writes a master key to disk with 0600 permissions.
func SaveMasterKey(path string, key []byte) error {
	if len(key) != masterKeySize {
		return fmt.Errorf("master key must be %d bytes, got %d", masterKeySize, len(key))
	}
	if err := os.WriteFile(path, key, 0600); err != nil {
		return fmt.Errorf("write master key: %w", err)
	}
	return nil
}

// EncryptSecret encrypts a plaintext secret with AES-256-GCM.
// Returns base64(nonce + ciphertext + tag).
func EncryptSecret(plaintext string, key []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	// Seal appends ciphertext+tag to nonce
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptSecret decrypts a base64-encoded AES-256-GCM ciphertext.
func DecryptSecret(ciphertext string, key []byte) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, sealed := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plaintext), nil
}

// SaveBootstrapData writes bootstrap data as JSON to disk with 0600 permissions.
func SaveBootstrapData(path string, data *BootstrapData) error {
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal bootstrap data: %w", err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		return fmt.Errorf("write bootstrap data: %w", err)
	}
	return nil
}

// LoadBootstrapData reads and parses a bootstrap JSON file from disk.
func LoadBootstrapData(path string) (*BootstrapData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bd BootstrapData
	if err := json.Unmarshal(data, &bd); err != nil {
		return nil, fmt.Errorf("parse bootstrap data: %w", err)
	}
	return &bd, nil
}
