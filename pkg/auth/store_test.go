package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	s.Set("anthropic", &Credential{
		Type: CredentialAPIKey,
		Key:  "sk-ant-123",
	})
	s.Set("openai", &Credential{
		Type:         CredentialOAuth,
		RefreshToken: "refresh-tok",
		AccessToken:  "access-tok",
		ExpiresAt:    1234567890000,
	})

	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load into a fresh store.
	s2, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	ant := s2.Get("anthropic")
	if ant == nil || ant.Key != "sk-ant-123" {
		t.Errorf("anthropic: got %+v", ant)
	}

	oai := s2.Get("openai")
	if oai == nil || oai.AccessToken != "access-tok" || oai.RefreshToken != "refresh-tok" {
		t.Errorf("openai: got %+v", oai)
	}
}

func TestStoreLoadMissing(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Load(); err != nil {
		t.Fatalf("Load of missing file should not error: %v", err)
	}
	if len(s.Providers()) != 0 {
		t.Errorf("expected empty providers, got %v", s.Providers())
	}
}

func TestStoreDelete(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(filepath.Join(dir, "auth.json"))
	s.Set("test", &Credential{Type: CredentialAPIKey, Key: "k"})
	s.Delete("test")
	if s.Get("test") != nil {
		t.Error("credential should be deleted")
	}
}

func TestStorePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	s, _ := NewStore(path)
	s.Set("test", &Credential{Type: CredentialAPIKey, Key: "k"})
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("auth.json permissions: got %o, want 600", perm)
	}
}

func TestStoreWithLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	s, _ := NewStore(path)

	called := false
	err := s.WithLock(func() error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("WithLock: %v", err)
	}
	if !called {
		t.Error("lock callback was not executed")
	}

	// Lock file should exist.
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Errorf("lock file not created: %v", err)
	}
}

func TestStoreDefaultPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	s, err := NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	expected := filepath.Join(dir, ".gi", "auth.json")
	if s.path != expected {
		t.Errorf("default path: got %q, want %q", s.path, expected)
	}
}

func TestStoreLoadLegacyFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	// Write legacy format: {"keys": {"anthropic": "sk-ant-legacy"}}
	legacy := []byte(`{"keys": {"anthropic": "sk-ant-legacy", "openai": "sk-oai-legacy"}}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Should have converted to Credential entries.
	ant := s.Get("anthropic")
	if ant == nil || ant.Type != CredentialAPIKey || ant.Key != "sk-ant-legacy" {
		t.Errorf("anthropic: got %+v", ant)
	}

	oai := s.Get("openai")
	if oai == nil || oai.Type != CredentialAPIKey || oai.Key != "sk-oai-legacy" {
		t.Errorf("openai: got %+v", oai)
	}

	// File should have been migrated to new format.
	s2, _ := NewStore(path)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load migrated: %v", err)
	}
	ant2 := s2.Get("anthropic")
	if ant2 == nil || ant2.Key != "sk-ant-legacy" {
		t.Errorf("migrated anthropic: got %+v", ant2)
	}
}

func TestStoreLoadLegacyFormatMigrationPreservesData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	legacy := []byte(`{"keys": {"anthropic": "sk-key-123"}}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s, _ := NewStore(path)
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// After migration, re-read should show new format.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Should NOT contain "keys" at top level anymore.
	content := string(data)
	if content == "" {
		t.Fatal("migrated file is empty")
	}

	// Load again to verify round-trip.
	s2, _ := NewStore(path)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load round-trip: %v", err)
	}
	cred := s2.Get("anthropic")
	if cred == nil || cred.Key != "sk-key-123" || cred.Type != CredentialAPIKey {
		t.Errorf("round-trip: got %+v", cred)
	}
}

func TestStoreLoadJSONNull(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	// Write JSON null — this previously caused a nil map panic on Set().
	if err := os.WriteFile(path, []byte("null"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := s.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Must not panic — entries should be an initialized empty map.
	s.Set("anthropic", &Credential{Type: CredentialAPIKey, Key: "sk-test"})

	cred := s.Get("anthropic")
	if cred == nil || cred.Key != "sk-test" {
		t.Errorf("after Set: got %+v", cred)
	}
}

func TestStoreProvidersSorted(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(filepath.Join(dir, "auth.json"))
	s.Set("openai", &Credential{Type: CredentialAPIKey, Key: "k"})
	s.Set("anthropic", &Credential{Type: CredentialAPIKey, Key: "k"})
	s.Set("openrouter", &Credential{Type: CredentialAPIKey, Key: "k"})

	providers := s.Providers()
	if len(providers) != 3 {
		t.Fatalf("len(Providers()) = %d, want 3", len(providers))
	}
	if providers[0] != "anthropic" || providers[1] != "openai" || providers[2] != "openrouter" {
		t.Errorf("Providers() not sorted: %v", providers)
	}
}
