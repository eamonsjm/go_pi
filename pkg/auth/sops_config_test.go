package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSopsConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()

	cfg := &SopsConfig{
		Enabled:    true,
		AgeKeyPath: "/home/user/.gi/age-key.txt",
	}

	if err := SaveSopsConfig(dir, cfg); err != nil {
		t.Fatalf("SaveSopsConfig: %v", err)
	}

	// Verify file permissions.
	info, err := os.Stat(SopsConfigPath(dir))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions: got %o, want 600", perm)
	}

	// Load back.
	loaded, err := LoadSopsConfig(dir)
	if err != nil {
		t.Fatalf("LoadSopsConfig: %v", err)
	}
	if !loaded.Enabled {
		t.Error("Enabled should be true")
	}
	if loaded.AgeKeyPath != cfg.AgeKeyPath {
		t.Errorf("AgeKeyPath: got %q, want %q", loaded.AgeKeyPath, cfg.AgeKeyPath)
	}
}

func TestSopsConfigMissing(t *testing.T) {
	dir := t.TempDir()

	cfg, err := LoadSopsConfig(dir)
	if err != nil {
		t.Fatalf("LoadSopsConfig: %v", err)
	}
	if cfg.Enabled {
		t.Error("missing config should default to Enabled=false")
	}
}

func TestRemoveSopsConfig(t *testing.T) {
	dir := t.TempDir()

	// Save then remove.
	if err := SaveSopsConfig(dir, &SopsConfig{Enabled: true}); err != nil {
		t.Fatalf("SaveSopsConfig: %v", err)
	}
	if err := RemoveSopsConfig(dir); err != nil {
		t.Fatalf("RemoveSopsConfig: %v", err)
	}

	// File should be gone.
	if _, err := os.Stat(SopsConfigPath(dir)); !os.IsNotExist(err) {
		t.Errorf("config file should be deleted, got err: %v", err)
	}

	// Removing again should not error.
	if err := RemoveSopsConfig(dir); err != nil {
		t.Errorf("RemoveSopsConfig on missing file: %v", err)
	}
}

func TestNewStoreReadsSopsConfig(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "age-key.txt")
	authPath := filepath.Join(dir, "auth.json")

	// Generate an age key.
	id, err := GenerateAgeKey(keyPath)
	if err != nil {
		t.Fatalf("GenerateAgeKey: %v", err)
	}

	// Write sops-config.json that points to the key.
	cfg := &SopsConfig{
		Enabled:    true,
		AgeKeyPath: keyPath,
	}
	if err := SaveSopsConfig(dir, cfg); err != nil {
		t.Fatalf("SaveSopsConfig: %v", err)
	}

	// Write a plaintext auth file.
	if err := os.WriteFile(authPath, []byte(`{"anthropic":{"type":"api_key","key":"sk-test"}}`), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	// NewStore should pre-load the SOPS key from config.
	s, err := NewStore(authPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	if s.SopsKey() != id.Recipient().String() {
		t.Errorf("sopsKey: got %q, want %q", s.SopsKey(), id.Recipient().String())
	}

	// Load + Save should now encrypt the plaintext file.
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !isSopsEncrypted(data) {
		t.Error("file should be SOPS-encrypted after Save with sopsKey set from config")
	}
}

func TestNewStoreWithoutSopsConfig(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")

	if err := os.WriteFile(authPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// No sops-config.json — store should work without SOPS.
	s, err := NewStore(authPath)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if s.SopsKey() != "" {
		t.Errorf("sopsKey should be empty without config, got %q", s.SopsKey())
	}
}
