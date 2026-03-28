package auth

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	err := s.WithLock(context.Background(), func() error {
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

func TestStoreLockRespectsContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	s1, _ := NewStore(path)
	s2, _ := NewStore(path)

	// Acquire lock with first store.
	if err := s1.Lock(context.Background()); err != nil {
		t.Fatalf("Lock s1: %v", err)
	}
	defer s1.Unlock()

	// Try to acquire with an already-cancelled context — should fail immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s2.Lock(ctx)
	if err == nil {
		s2.Unlock()
		t.Fatal("Lock with cancelled context should fail")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("expected context canceled error, got: %v", err)
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

func TestStoreAgeKeyPathDefault(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	want := filepath.Join(dir, "age-key.txt")
	if got := s.AgeKeyPath(); got != want {
		t.Errorf("AgeKeyPath() = %q, want %q", got, want)
	}
}

func TestStoreSetAgeKeyPath(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	custom := filepath.Join(t.TempDir(), "custom-key.txt")
	s.SetAgeKeyPath(custom)
	if got := s.AgeKeyPath(); got != custom {
		t.Errorf("AgeKeyPath() after Set = %q, want %q", got, custom)
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

func TestStoreLoadRejectsInsecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission check is Unix-only")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	// Write a valid auth file with secure permissions first.
	if err := os.WriteFile(path, []byte(`{"test":{"type":"api_key","key":"k"}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Widen permissions to group-readable.
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	s, _ := NewStore(path)
	err := s.Load()
	if err == nil {
		t.Fatal("Load should fail with insecure permissions")
	}
	if !strings.Contains(err.Error(), "insecure permissions") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewStoreSopsEnabledKeyMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	// Write sops-config.json with enabled=true but no age key file.
	sopsConfig := []byte(`{"enabled": true}`)
	if err := os.WriteFile(filepath.Join(dir, "sops-config.json"), sopsConfig, 0o600); err != nil {
		t.Fatalf("write sops-config: %v", err)
	}

	// NewStore should succeed (key-not-found is a warning, not an error).
	s, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// sopsKey should be empty since the key doesn't exist yet.
	if s.SopsKey() != "" {
		t.Errorf("SopsKey() = %q, want empty (key not generated yet)", s.SopsKey())
	}
}

func TestNewStoreSopsEnabledKeyCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")

	// Write sops-config.json with enabled=true.
	sopsConfig := []byte(`{"enabled": true}`)
	if err := os.WriteFile(filepath.Join(dir, "sops-config.json"), sopsConfig, 0o600); err != nil {
		t.Fatalf("write sops-config: %v", err)
	}

	// Write a corrupt age key file.
	if err := os.WriteFile(filepath.Join(dir, "age-key.txt"), []byte("not a valid age key"), 0o600); err != nil {
		t.Fatalf("write corrupt key: %v", err)
	}

	// NewStore should fail — the key exists but can't be parsed.
	_, err := NewStore(path)
	if err == nil {
		t.Fatal("NewStore should fail with corrupt age key when SOPS enabled")
	}
	if !strings.Contains(err.Error(), "load age key") {
		t.Errorf("unexpected error: %v", err)
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
