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

	expected := filepath.Join(dir, ".pi", "auth.json")
	if s.path != expected {
		t.Errorf("default path: got %q, want %q", s.path, expected)
	}
}
