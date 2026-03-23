package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ejm/go_pi/pkg/auth"
)

// ---------------------------------------------------------------------------
// NewLoginCommand
// ---------------------------------------------------------------------------

func TestNewLoginCommand_Metadata(t *testing.T) {
	store := testAuthStore(t)
	resolver := auth.NewResolver(store)
	cmd := NewLoginCommand(store, resolver)

	if cmd.Name != "login" {
		t.Errorf("Name = %q, want %q", cmd.Name, "login")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestNewLoginCommand_EmptyProvider(t *testing.T) {
	store := testAuthStore(t)
	resolver := auth.NewResolver(store)
	cmd := NewLoginCommand(store, resolver)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if !result.IsError {
		t.Error("expected error for empty provider")
	}
	if !strings.Contains(result.Text, "Usage:") {
		t.Errorf("expected usage message, got %q", result.Text)
	}
}

func TestNewLoginCommand_UnknownProvider(t *testing.T) {
	store := testAuthStore(t)
	resolver := auth.NewResolver(store)
	cmd := NewLoginCommand(store, resolver)

	teaCmd := cmd.Execute("nonexistent")
	msg := teaCmd()
	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if !result.IsError {
		t.Error("expected error for unknown provider")
	}
	if !strings.Contains(result.Text, "No OAuth support") {
		t.Errorf("expected 'No OAuth support' message, got %q", result.Text)
	}
}

func TestNewLoginCommand_WhitespaceArgs(t *testing.T) {
	store := testAuthStore(t)
	resolver := auth.NewResolver(store)
	cmd := NewLoginCommand(store, resolver)

	teaCmd := cmd.Execute("   ")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected error for whitespace-only args")
	}
}

// ---------------------------------------------------------------------------
// NewLogoutCommand
// ---------------------------------------------------------------------------

func TestNewLogoutCommand_Metadata(t *testing.T) {
	store := testAuthStore(t)
	cmd := NewLogoutCommand(store)

	if cmd.Name != "logout" {
		t.Errorf("Name = %q, want %q", cmd.Name, "logout")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestNewLogoutCommand_EmptyProvider(t *testing.T) {
	store := testAuthStore(t)
	cmd := NewLogoutCommand(store)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if !result.IsError {
		t.Error("expected error for empty provider")
	}
	if !strings.Contains(result.Text, "Usage:") {
		t.Errorf("expected usage message, got %q", result.Text)
	}
}

func TestNewLogoutCommand_Success(t *testing.T) {
	store := testAuthStore(t)
	store.Set("testprovider", &auth.Credential{
		Type: auth.CredentialAPIKey,
		Key:  "test-key",
	})
	cmd := NewLogoutCommand(store)

	teaCmd := cmd.Execute("testprovider")
	msg := teaCmd()
	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "Logged out") {
		t.Errorf("expected 'Logged out' message, got %q", result.Text)
	}
	if !strings.Contains(result.Text, "testprovider") {
		t.Errorf("expected provider name in message, got %q", result.Text)
	}

	// Verify credential was removed.
	if cred := store.Get("testprovider"); cred != nil {
		t.Error("expected credential to be deleted after logout")
	}
}

func TestNewLogoutCommand_NonexistentProvider(t *testing.T) {
	store := testAuthStore(t)
	cmd := NewLogoutCommand(store)

	// Logging out from a provider that doesn't exist should still succeed.
	teaCmd := cmd.Execute("nonexistent")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Errorf("expected success even for nonexistent provider: %s", result.Text)
	}
}

func TestNewLogoutCommand_WhitespaceArgs(t *testing.T) {
	store := testAuthStore(t)
	cmd := NewLogoutCommand(store)

	teaCmd := cmd.Execute("   ")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected error for whitespace-only args")
	}
}

// ---------------------------------------------------------------------------
// NewAuthStatusCommand
// ---------------------------------------------------------------------------

func TestNewAuthStatusCommand_Metadata(t *testing.T) {
	store := testAuthStore(t)
	resolver := auth.NewResolver(store)
	ctx := context.Background()
	cmd := NewAuthStatusCommand(ctx, store, resolver)

	if cmd.Name != "auth" {
		t.Errorf("Name = %q, want %q", cmd.Name, "auth")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestNewAuthStatusCommand_ProducesOutput(t *testing.T) {
	store := testAuthStore(t)
	resolver := auth.NewResolver(store)
	ctx := context.Background()
	cmd := NewAuthStatusCommand(ctx, store, resolver)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "Authentication Status:") {
		t.Errorf("expected 'Authentication Status:' header, got %q", result.Text)
	}
}

func TestNewAuthStatusCommand_ShowsProviders(t *testing.T) {
	store := testAuthStore(t)
	resolver := auth.NewResolver(store)
	ctx := context.Background()
	cmd := NewAuthStatusCommand(ctx, store, resolver)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result := msg.(CommandResultMsg)

	// Should mention at least "anthropic" — a valid provider.
	if !strings.Contains(result.Text, "anthropic") {
		t.Errorf("expected 'anthropic' in status output, got %q", result.Text)
	}
}

func TestNewAuthStatusCommand_WithOAuthCredential(t *testing.T) {
	store := testAuthStore(t)
	store.Set("anthropic", &auth.Credential{
		Type:        auth.CredentialOAuth,
		AccessToken: "test-token",
		ExpiresAt:   0,
	})

	resolver := auth.NewResolver(store)
	// Set an override so Resolve returns a key for anthropic.
	resolver.SetOverride("anthropic", "test-key")

	ctx := context.Background()
	cmd := NewAuthStatusCommand(ctx, store, resolver)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result := msg.(CommandResultMsg)

	if !strings.Contains(result.Text, "OAuth") {
		t.Errorf("expected OAuth indicator in status, got %q", result.Text)
	}
}

func TestNewAuthStatusCommand_WithAPIKeyCredential(t *testing.T) {
	store := testAuthStore(t)
	store.Set("anthropic", &auth.Credential{
		Type: auth.CredentialAPIKey,
		Key:  "sk-test",
	})

	resolver := auth.NewResolver(store)
	resolver.SetOverride("anthropic", "sk-test")

	ctx := context.Background()
	cmd := NewAuthStatusCommand(ctx, store, resolver)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result := msg.(CommandResultMsg)

	if !strings.Contains(result.Text, "API key") {
		t.Errorf("expected 'API key' indicator in status, got %q", result.Text)
	}
}

// ---------------------------------------------------------------------------
// authOAuthMsg / authLoginSuccessMsg message types
// ---------------------------------------------------------------------------

func TestAuthOAuthMsg_Fields(t *testing.T) {
	codeCh := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msg := authOAuthMsg{
		providerName: "test",
		url:          "https://example.com/auth",
		codeCh:       codeCh,
		cancelAuth:   cancel,
	}
	_ = ctx

	if msg.providerName != "test" {
		t.Errorf("providerName = %q, want %q", msg.providerName, "test")
	}
	if msg.url != "https://example.com/auth" {
		t.Errorf("url = %q, want %q", msg.url, "https://example.com/auth")
	}
	if msg.codeCh == nil {
		t.Error("expected codeCh to be non-nil")
	}
}

func TestAuthLoginSuccessMsg_Fields(t *testing.T) {
	msg := authLoginSuccessMsg{
		providerName: "anthropic",
		text:         "Logged in to Anthropic!",
	}

	if msg.providerName != "anthropic" {
		t.Errorf("providerName = %q, want %q", msg.providerName, "anthropic")
	}
	if msg.text != "Logged in to Anthropic!" {
		t.Errorf("text = %q, want %q", msg.text, "Logged in to Anthropic!")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func testAuthStore(t *testing.T) *auth.Store {
	t.Helper()
	dir := t.TempDir()
	store, err := auth.NewStore(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}
