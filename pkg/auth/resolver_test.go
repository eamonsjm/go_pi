package auth

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockProvider implements OAuthProvider for testing.
type mockProvider struct {
	id          string
	apiKey      string
	refreshErr  error
	refreshCred *Credential
}

func (m *mockProvider) ID() string   { return m.id }
func (m *mockProvider) Name() string { return m.id }

func (m *mockProvider) Login(_ context.Context, _ OAuthCallbacks) (*Credential, error) {
	return nil, nil
}

func (m *mockProvider) RefreshToken(_ context.Context, _ *Credential) (*Credential, error) {
	if m.refreshErr != nil {
		return nil, m.refreshErr
	}
	return m.refreshCred, nil
}

func (m *mockProvider) GetAPIKey(cred *Credential) string {
	if m.apiKey != "" {
		return m.apiKey
	}
	return cred.AccessToken
}

func TestResolve_Override(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(filepath.Join(dir, "auth.json"))
	r := NewResolver(s)
	r.SetOverride("anthropic", "override-key")

	got, err := r.Resolve(context.Background(), "anthropic")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "override-key" {
		t.Errorf("got %q, want %q", got, "override-key")
	}
}

func TestResolve_StoredAPIKey(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(filepath.Join(dir, "auth.json"))
	s.Set("anthropic", &Credential{Type: CredentialAPIKey, Key: "stored-key"})
	r := NewResolver(s)

	got, err := r.Resolve(context.Background(), "anthropic")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "stored-key" {
		t.Errorf("got %q, want %q", got, "stored-key")
	}
}

func TestResolve_EnvVar(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(filepath.Join(dir, "auth.json"))
	r := NewResolver(s)

	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	got, err := r.Resolve(context.Background(), "anthropic")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "env-key" {
		t.Errorf("got %q, want %q", got, "env-key")
	}
}

func TestResolve_Precedence(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(filepath.Join(dir, "auth.json"))
	s.Set("anthropic", &Credential{Type: CredentialAPIKey, Key: "stored-key"})
	r := NewResolver(s)

	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	r.SetOverride("anthropic", "override-key")

	got, err := r.Resolve(context.Background(), "anthropic")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Override should win.
	if got != "override-key" {
		t.Errorf("got %q, want %q", got, "override-key")
	}
}

func TestResolve_OAuthValid(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(filepath.Join(dir, "auth.json"))
	s.Set("anthropic", &Credential{
		Type:        CredentialOAuth,
		AccessToken: "valid-access-tok",
		ExpiresAt:   time.Now().Add(time.Hour).UnixMilli(),
	})

	r := NewResolver(s)
	r.RegisterProvider(&mockProvider{
		id:     "anthropic",
		apiKey: "derived-api-key",
	})

	got, err := r.Resolve(context.Background(), "anthropic")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "derived-api-key" {
		t.Errorf("got %q, want %q", got, "derived-api-key")
	}
}

func TestResolve_OAuthExpiredRefresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	s, _ := NewStore(path)
	s.Set("anthropic", &Credential{
		Type:         CredentialOAuth,
		AccessToken:  "expired-tok",
		RefreshToken: "refresh-tok",
		ExpiresAt:    time.Now().Add(-time.Hour).UnixMilli(),
	})
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	r := NewResolver(s)
	r.RegisterProvider(&mockProvider{
		id: "anthropic",
		refreshCred: &Credential{
			Type:         CredentialOAuth,
			AccessToken:  "new-access-tok",
			RefreshToken: "new-refresh-tok",
			ExpiresAt:    time.Now().Add(time.Hour).UnixMilli(),
		},
	})

	got, err := r.Resolve(context.Background(), "anthropic")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "new-access-tok" {
		t.Errorf("got %q, want %q", got, "new-access-tok")
	}
}

func TestResolve_GetOAuthProvider(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(filepath.Join(dir, "auth.json"))
	r := NewResolver(s)

	// Not registered.
	if got := r.GetOAuthProvider("anthropic"); got != nil {
		t.Errorf("expected nil, got %v", got)
	}

	// Register and retrieve.
	mp := &mockProvider{id: "anthropic"}
	r.RegisterProvider(mp)
	got := r.GetOAuthProvider("anthropic")
	if got == nil || got.ID() != "anthropic" {
		t.Errorf("expected anthropic provider, got %v", got)
	}
}

func TestResolve_OAuthRefreshError_SurfacedNotSwallowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	s, _ := NewStore(path)
	s.Set("anthropic", &Credential{
		Type:         CredentialOAuth,
		AccessToken:  "expired-tok",
		RefreshToken: "bad-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).UnixMilli(),
	})
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	r := NewResolver(s)
	r.RegisterProvider(&mockProvider{
		id:         "anthropic",
		refreshErr: fmt.Errorf("refresh failed (400): invalid_grant"),
	})

	// OAuth refresh errors must be surfaced, not silently swallowed.
	_, err := r.Resolve(context.Background(), "anthropic")
	if err == nil {
		t.Fatal("expected error when OAuth refresh fails, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error should contain refresh failure detail, got: %v", err)
	}
	if !strings.Contains(err.Error(), "/login") {
		t.Errorf("error should suggest /login re-auth, got: %v", err)
	}
}

func TestResolve_OAuthRefreshError_EnvVarNotUsedAsFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	s, _ := NewStore(path)
	s.Set("anthropic", &Credential{
		Type:         CredentialOAuth,
		AccessToken:  "expired-tok",
		RefreshToken: "bad-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).UnixMilli(),
	})
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	r := NewResolver(s)
	r.RegisterProvider(&mockProvider{
		id:         "anthropic",
		refreshErr: fmt.Errorf("refresh failed (400): token revoked"),
	})

	// Even with an env var set, OAuth errors must not silently fall through.
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	_, err := r.Resolve(context.Background(), "anthropic")
	if err == nil {
		t.Fatal("expected error — OAuth refresh failure should not fall through to env var")
	}
}

func TestResolve_Empty(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(filepath.Join(dir, "auth.json"))
	r := NewResolver(s)

	// Clear env vars.
	t.Setenv("ANTHROPIC_API_KEY", "")

	got, err := r.Resolve(context.Background(), "anthropic")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty key, got %q", got)
	}
}
