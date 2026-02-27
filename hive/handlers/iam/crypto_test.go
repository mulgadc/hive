package handlers_iam

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateMasterKey(t *testing.T) {
	key, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey() error: %v", err)
	}
	if len(key) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(key))
	}

	// Two generated keys should differ
	key2, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey() second call error: %v", err)
	}
	if string(key) == string(key2) {
		t.Fatal("two generated keys should not be identical")
	}
}

func TestSaveLoadMasterKey(t *testing.T) {
	key, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey() error: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")

	if err := SaveMasterKey(path, key); err != nil {
		t.Fatalf("SaveMasterKey() error: %v", err)
	}

	// Check file permissions
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat master.key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected permissions 0600, got %04o", perm)
	}

	loaded, err := LoadMasterKey(path)
	if err != nil {
		t.Fatalf("LoadMasterKey() error: %v", err)
	}
	if string(loaded) != string(key) {
		t.Fatal("loaded key does not match saved key")
	}
}

func TestLoadMasterKeyWrongSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.key")

	if err := os.WriteFile(path, []byte("too-short"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadMasterKey(path)
	if err == nil {
		t.Fatal("LoadMasterKey() should fail for wrong-size key")
	}
}

func TestLoadMasterKeyNotFound(t *testing.T) {
	_, err := LoadMasterKey("/nonexistent/master.key")
	if err == nil {
		t.Fatal("LoadMasterKey() should fail for missing file")
	}
}

func TestSaveMasterKeyWrongSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.key")

	err := SaveMasterKey(path, []byte("too-short"))
	if err == nil {
		t.Fatal("SaveMasterKey() should fail for wrong-size key")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey() error: %v", err)
	}

	secret := "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"

	encrypted, err := EncryptSecret(secret, key)
	if err != nil {
		t.Fatalf("EncryptSecret() error: %v", err)
	}

	if encrypted == secret {
		t.Fatal("encrypted output should differ from plaintext")
	}

	decrypted, err := DecryptSecret(encrypted, key)
	if err != nil {
		t.Fatalf("DecryptSecret() error: %v", err)
	}

	if decrypted != secret {
		t.Fatalf("expected %q, got %q", secret, decrypted)
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	key, _ := GenerateMasterKey()
	secret := "test-secret"

	ct1, _ := EncryptSecret(secret, key)
	ct2, _ := EncryptSecret(secret, key)

	if ct1 == ct2 {
		t.Fatal("encrypting the same plaintext twice should produce different ciphertexts (random nonce)")
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key1, _ := GenerateMasterKey()
	key2, _ := GenerateMasterKey()
	secret := "test-secret"

	encrypted, err := EncryptSecret(secret, key1)
	if err != nil {
		t.Fatalf("EncryptSecret() error: %v", err)
	}

	_, err = DecryptSecret(encrypted, key2)
	if err == nil {
		t.Fatal("DecryptSecret() should fail with wrong key")
	}
}

func TestDecryptTamperedCiphertext(t *testing.T) {
	key, _ := GenerateMasterKey()
	secret := "test-secret"

	encrypted, _ := EncryptSecret(secret, key)

	// Tamper with the ciphertext by flipping a character
	tampered := []byte(encrypted)
	if tampered[len(tampered)-2] == 'A' {
		tampered[len(tampered)-2] = 'B'
	} else {
		tampered[len(tampered)-2] = 'A'
	}

	_, err := DecryptSecret(string(tampered), key)
	if err == nil {
		t.Fatal("DecryptSecret() should fail with tampered ciphertext")
	}
}

func TestDecryptInvalidBase64(t *testing.T) {
	key, _ := GenerateMasterKey()
	_, err := DecryptSecret("not-valid-base64!!!", key)
	if err == nil {
		t.Fatal("DecryptSecret() should fail with invalid base64")
	}
}

func TestDecryptTooShort(t *testing.T) {
	key, _ := GenerateMasterKey()
	// Valid base64 but too short for nonce + ciphertext
	_, err := DecryptSecret("AQID", key)
	if err == nil {
		t.Fatal("DecryptSecret() should fail with too-short ciphertext")
	}
}

func TestLoadBootstrapData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap.json")

	bd := BootstrapData{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		EncryptedSecret: "dGVzdC1lbmNyeXB0ZWQ=",
		AccountID:       "000000000000",
	}
	data, _ := json.Marshal(bd)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadBootstrapData(path)
	if err != nil {
		t.Fatalf("LoadBootstrapData() error: %v", err)
	}

	if loaded.AccessKeyID != bd.AccessKeyID {
		t.Fatalf("AccessKeyID: expected %q, got %q", bd.AccessKeyID, loaded.AccessKeyID)
	}
	if loaded.EncryptedSecret != bd.EncryptedSecret {
		t.Fatalf("EncryptedSecret mismatch")
	}
	if loaded.AccountID != bd.AccountID {
		t.Fatalf("AccountID: expected %q, got %q", bd.AccountID, loaded.AccountID)
	}
}

func TestLoadBootstrapDataNotFound(t *testing.T) {
	_, err := LoadBootstrapData("/nonexistent/bootstrap.json")
	if err == nil {
		t.Fatal("LoadBootstrapData() should fail for missing file")
	}
}

func TestLoadBootstrapDataInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bootstrap.json")

	if err := os.WriteFile(path, []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadBootstrapData(path)
	if err == nil {
		t.Fatal("LoadBootstrapData() should fail for invalid JSON")
	}
}
