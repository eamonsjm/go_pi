package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestIsSopsEncrypted(t *testing.T) {
	tests := []struct {
		name string
		data string
		want bool
	}{
		{"plain credentials", `{"anthropic":{"type":"api_key","key":"sk-123"}}`, false},
		{"sops metadata present", `{"anthropic":{"type":"ENC[AES256_GCM,data:xx]"},"sops":{"version":"3.12.2"}}`, true},
		{"empty object", `{}`, false},
		{"invalid json", `not json`, false},
		{"legacy format", `{"keys":{"anthropic":"sk-123"}}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSopsEncrypted([]byte(tt.data)); got != tt.want {
				t.Errorf("isSopsEncrypted() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateAndLoadAgeKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "age-key.txt")

	// Generate a new key.
	id, err := generateAgeKey(keyPath)
	if err != nil {
		t.Fatalf("generateAgeKey: %v", err)
	}

	// Verify the file exists with correct permissions.
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file permissions: got %o, want 600", perm)
	}

	// Load the key back.
	loaded, err := loadAgeKey(keyPath)
	if err != nil {
		t.Fatalf("loadAgeKey: %v", err)
	}

	if loaded.Recipient().String() != id.Recipient().String() {
		t.Errorf("recipient mismatch: got %s, want %s",
			loaded.Recipient().String(), id.Recipient().String())
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "age-key.txt")

	// Generate age key.
	id, err := generateAgeKey(keyPath)
	if err != nil {
		t.Fatalf("generateAgeKey: %v", err)
	}

	plaintext := []byte(`{"anthropic":{"type":"api_key","key":"sk-ant-123"},"openai":{"type":"oauth","refresh_token":"rt-xyz"}}`)

	// Encrypt.
	encrypted, err := encryptSops(plaintext, id.Recipient().String())
	if err != nil {
		t.Fatalf("encryptSops: %v", err)
	}

	// Encrypted data should contain SOPS metadata.
	if !isSopsEncrypted(encrypted) {
		t.Fatal("encrypted data does not contain SOPS metadata")
	}

	// Decrypt.
	decrypted, err := decryptSops(encrypted, keyPath)
	if err != nil {
		t.Fatalf("decryptSops: %v", err)
	}

	// Values should match (JSON may reformat, so compare parsed).
	var original, result map[string]interface{}
	if err := json.Unmarshal(plaintext, &original); err != nil {
		t.Fatalf("parse original: %v", err)
	}
	if err := json.Unmarshal(decrypted, &result); err != nil {
		t.Fatalf("parse decrypted: %v", err)
	}

	// Compare key credential values.
	origAnt := original["anthropic"].(map[string]interface{})
	resAnt := result["anthropic"].(map[string]interface{})
	if resAnt["key"] != origAnt["key"] {
		t.Errorf("anthropic key: got %q, want %q", resAnt["key"], origAnt["key"])
	}

	origOai := original["openai"].(map[string]interface{})
	resOai := result["openai"].(map[string]interface{})
	if resOai["refresh_token"] != origOai["refresh_token"] {
		t.Errorf("openai refresh_token: got %q, want %q", resOai["refresh_token"], origOai["refresh_token"])
	}
}

func TestStoreLoadDecryptsSops(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "age-key.txt")
	authPath := filepath.Join(dir, "auth.json")

	// Generate age key.
	id, err := generateAgeKey(keyPath)
	if err != nil {
		t.Fatalf("generateAgeKey: %v", err)
	}

	// Create a plaintext auth file, then encrypt it.
	plain := []byte(`{"anthropic":{"type":"api_key","key":"sk-ant-secret"}}`)
	encrypted, err := encryptSops(plain, id.Recipient().String())
	if err != nil {
		t.Fatalf("encryptSops: %v", err)
	}
	if err := os.WriteFile(authPath, encrypted, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Load via Store — should decrypt transparently.
	s, err := NewStore(authPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cred := s.Get("anthropic")
	if cred == nil || cred.Key != "sk-ant-secret" {
		t.Errorf("anthropic: got %+v, want key=sk-ant-secret", cred)
	}

	if !s.encrypted {
		t.Error("encrypted flag should be true after loading SOPS file")
	}
	if s.sopsKey == "" {
		t.Error("sopsKey should be set after loading SOPS file")
	}
}

func TestStoreBackwardCompatPlaintext(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")

	// Write a plain (non-SOPS) auth file.
	plain := []byte(`{"anthropic":{"type":"api_key","key":"sk-ant-plain"}}`)
	if err := os.WriteFile(authPath, plain, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s, err := NewStore(authPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cred := s.Get("anthropic")
	if cred == nil || cred.Key != "sk-ant-plain" {
		t.Errorf("anthropic: got %+v", cred)
	}

	if s.encrypted {
		t.Error("encrypted flag should be false for plaintext file")
	}
}

func TestStoreSaveReEncrypts(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "age-key.txt")
	authPath := filepath.Join(dir, "auth.json")

	// Generate age key.
	id, err := generateAgeKey(keyPath)
	if err != nil {
		t.Fatalf("generateAgeKey: %v", err)
	}

	// Create encrypted auth file.
	plain := []byte(`{"anthropic":{"type":"api_key","key":"sk-ant-original"}}`)
	encrypted, err := encryptSops(plain, id.Recipient().String())
	if err != nil {
		t.Fatalf("encryptSops: %v", err)
	}
	if err := os.WriteFile(authPath, encrypted, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Load (decrypts), modify, save (re-encrypts).
	s, err := NewStore(authPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	s.Set("openai", &Credential{Type: CredentialAPIKey, Key: "sk-oai-new"})
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify saved file is still SOPS-encrypted.
	savedData, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !isSopsEncrypted(savedData) {
		t.Fatal("saved file should be SOPS-encrypted")
	}

	// Re-load and verify both credentials.
	s2, err := NewStore(authPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ant := s2.Get("anthropic")
	if ant == nil || ant.Key != "sk-ant-original" {
		t.Errorf("anthropic: got %+v", ant)
	}
	oai := s2.Get("openai")
	if oai == nil || oai.Key != "sk-oai-new" {
		t.Errorf("openai: got %+v", oai)
	}
}

func TestSetSopsKeyDoesNotClearEncryptedBeforeSave(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "age-key.txt")
	authPath := filepath.Join(dir, "auth.json")

	// Generate age key and create an encrypted auth file.
	id, err := generateAgeKey(keyPath)
	if err != nil {
		t.Fatalf("generateAgeKey: %v", err)
	}
	plain := []byte(`{"anthropic":{"type":"api_key","key":"sk-ant-test"}}`)
	encrypted, err := encryptSops(plain, id.Recipient().String())
	if err != nil {
		t.Fatalf("encryptSops: %v", err)
	}
	if err := os.WriteFile(authPath, encrypted, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Load the encrypted store.
	s, err := NewStore(authPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !s.encrypted {
		t.Fatal("expected encrypted=true after loading SOPS file")
	}

	// Clear the sops key (as /decrypt does) — encrypted flag should NOT change yet.
	s.SetSopsKey("")
	if !s.encrypted {
		t.Error("SetSopsKey('') must not clear encrypted flag before Save succeeds")
	}

	// Make Save fail by pointing to a read-only directory.
	roDir := filepath.Join(dir, "readonly")
	if err := os.Mkdir(roDir, 0o500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { os.Chmod(roDir, 0o700) })
	s.path = filepath.Join(roDir, "auth.json")
	if err := s.Save(); err == nil {
		t.Fatal("Save should have failed with read-only directory")
	}

	// encrypted should still be true because Save failed.
	if !s.encrypted {
		t.Error("encrypted flag should remain true after failed Save")
	}
}

func TestSetSopsKeyDoesNotSetEncryptedBeforeSave(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")

	// Create a plaintext auth file.
	plain := []byte(`{"anthropic":{"type":"api_key","key":"sk-ant-plain"}}`)
	if err := os.WriteFile(authPath, plain, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s, err := NewStore(authPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.encrypted {
		t.Fatal("expected encrypted=false for plaintext file")
	}

	// Set a sops key (as /encrypt does) — encrypted should NOT change yet.
	s.SetSopsKey("age1somekey")
	if s.encrypted {
		t.Error("SetSopsKey(key) must not set encrypted flag before Save succeeds")
	}
}

