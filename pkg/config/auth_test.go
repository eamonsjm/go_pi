package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAuthFromEnvVars(t *testing.T) {
	// Set env vars for the test.
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-key")
	t.Setenv("OPENAI_API_KEY", "sk-oai-test-key")

	store, err := LoadAuth()
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}

	if got := store.GetKey("anthropic"); got != "sk-ant-test-key" {
		t.Errorf("expected anthropic key 'sk-ant-test-key', got %q", got)
	}
	if got := store.GetKey("openai"); got != "sk-oai-test-key" {
		t.Errorf("expected openai key 'sk-oai-test-key', got %q", got)
	}
}

func TestGetKeyMissing(t *testing.T) {
	store := &AuthStore{Keys: make(map[string]string)}
	if got := store.GetKey("nonexistent"); got != "" {
		t.Errorf("expected empty string for missing key, got %q", got)
	}
}

func TestSetKey(t *testing.T) {
	store := &AuthStore{Keys: make(map[string]string)}

	store.SetKey("anthropic", "my-key")
	if got := store.GetKey("anthropic"); got != "my-key" {
		t.Errorf("expected 'my-key', got %q", got)
	}

	// Update existing key.
	store.SetKey("anthropic", "new-key")
	if got := store.GetKey("anthropic"); got != "new-key" {
		t.Errorf("expected 'new-key' after update, got %q", got)
	}
}

func TestSaveAndLoadAuthRoundTrip(t *testing.T) {
	// Use a temp dir as HOME so Save writes there.
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// Clear env vars so they don't interfere with the load.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	store := &AuthStore{
		Keys: map[string]string{
			"anthropic": "ant-key-123",
			"openai":    "oai-key-456",
		},
	}

	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists.
	path := filepath.Join(dir, ".gi", "auth.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("auth.json not found: %v", err)
	}

	// Load back.
	loaded, err := LoadAuth()
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}

	if got := loaded.GetKey("anthropic"); got != "ant-key-123" {
		t.Errorf("expected 'ant-key-123', got %q", got)
	}
	if got := loaded.GetKey("openai"); got != "oai-key-456" {
		t.Errorf("expected 'oai-key-456', got %q", got)
	}
}

func TestEnvVarsOverrideFileValues(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// Write an auth file with one key.
	piDir := filepath.Join(dir, ".gi")
	if err := os.MkdirAll(piDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	fileStore := &AuthStore{
		Keys: map[string]string{
			"anthropic": "file-key",
		},
	}
	data, _ := json.MarshalIndent(fileStore, "", "  ")
	if err := os.WriteFile(filepath.Join(piDir, "auth.json"), data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Set env var that should override.
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	// Clear other env vars.
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	store, err := LoadAuth()
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}

	// Env var should win.
	if got := store.GetKey("anthropic"); got != "env-key" {
		t.Errorf("expected env var to override file value: got %q, want 'env-key'", got)
	}
}

func TestLoadAuthNoFileNoEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	// Clear all known env vars.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	store, err := LoadAuth()
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}

	if len(store.Keys) != 0 {
		t.Errorf("expected empty keys, got %d keys", len(store.Keys))
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deep", "nested")
	t.Setenv("HOME", dir)

	store := &AuthStore{
		Keys: map[string]string{"test": "key"},
	}
	if err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, ".gi", "auth.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("auth.json not created: %v", err)
	}
}

func TestMultipleProviders(t *testing.T) {
	store := &AuthStore{Keys: make(map[string]string)}

	store.SetKey("anthropic", "key-1")
	store.SetKey("openai", "key-2")
	store.SetKey("google", "key-3")

	if got := store.GetKey("anthropic"); got != "key-1" {
		t.Errorf("anthropic: got %q, want 'key-1'", got)
	}
	if got := store.GetKey("openai"); got != "key-2" {
		t.Errorf("openai: got %q, want 'key-2'", got)
	}
	if got := store.GetKey("google"); got != "key-3" {
		t.Errorf("google: got %q, want 'key-3'", got)
	}
}
