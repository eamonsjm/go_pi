package auth

import (
	"path/filepath"
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

func (m *mockProvider) Login(_ OAuthCallbacks) (*Credential, error) {
	return nil, nil
}

func (m *mockProvider) RefreshToken(_ *Credential) (*Credential, error) {
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

	got, err := r.Resolve("anthropic")
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

	got, err := r.Resolve("anthropic")
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
	got, err := r.Resolve("anthropic")
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

	got, err := r.Resolve("anthropic")
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

	got, err := r.Resolve("anthropic")
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

	got, err := r.Resolve("anthropic")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "new-access-tok" {
		t.Errorf("got %q, want %q", got, "new-access-tok")
	}
}

func TestResolve_Empty(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewStore(filepath.Join(dir, "auth.json"))
	r := NewResolver(s)

	// Clear env vars.
	t.Setenv("ANTHROPIC_API_KEY", "")

	got, err := r.Resolve("anthropic")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty key, got %q", got)
	}
}
