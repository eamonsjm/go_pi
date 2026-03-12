package auth

import (
	"testing"
	"time"
)

func TestIsExpired_NotOAuth(t *testing.T) {
	c := &Credential{Type: CredentialAPIKey, Key: "sk-123"}
	if c.IsExpired() {
		t.Error("API key credential should never be expired")
	}
}

func TestIsExpired_FutureToken(t *testing.T) {
	c := &Credential{
		Type:      CredentialOAuth,
		ExpiresAt: time.Now().Add(time.Hour).UnixMilli(),
	}
	if c.IsExpired() {
		t.Error("token expiring in 1h should not be expired")
	}
}

func TestIsExpired_PastToken(t *testing.T) {
	c := &Credential{
		Type:      CredentialOAuth,
		ExpiresAt: time.Now().Add(-time.Hour).UnixMilli(),
	}
	if !c.IsExpired() {
		t.Error("token expired 1h ago should be expired")
	}
}

func TestIsExpired_BufferZone(t *testing.T) {
	// Token "expires" in 3 minutes, but the 5-minute buffer should mark it expired.
	c := &Credential{
		Type:      CredentialOAuth,
		ExpiresAt: time.Now().Add(3 * time.Minute).UnixMilli(),
	}
	if !c.IsExpired() {
		t.Error("token within 5-minute buffer should be considered expired")
	}
}

func TestResolveKey_Literal(t *testing.T) {
	c := &Credential{Type: CredentialAPIKey, Key: "sk-ant-live-123"}
	got, err := c.ResolveKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sk-ant-live-123" {
		t.Errorf("got %q, want %q", got, "sk-ant-live-123")
	}
}

func TestResolveKey_EnvVar(t *testing.T) {
	t.Setenv("MY_SECRET_KEY", "resolved-from-env")
	c := &Credential{Type: CredentialAPIKey, Key: "MY_SECRET_KEY"}
	got, err := c.ResolveKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "resolved-from-env" {
		t.Errorf("got %q, want %q", got, "resolved-from-env")
	}
}

func TestResolveKey_EnvVarUnset(t *testing.T) {
	// Unset env var — should fall through to literal.
	t.Setenv("UNSET_VAR_TEST", "")
	c := &Credential{Type: CredentialAPIKey, Key: "UNSET_VAR_TEST"}
	got, err := c.ResolveKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Falls through to literal since env var is empty.
	if got != "UNSET_VAR_TEST" {
		t.Errorf("got %q, want %q", got, "UNSET_VAR_TEST")
	}
}

func TestResolveKey_Command(t *testing.T) {
	// Clear cache from previous runs.
	commandCacheMu.Lock()
	delete(commandCache, "echo secret-from-cmd")
	commandCacheMu.Unlock()

	c := &Credential{Type: CredentialAPIKey, Key: "!echo secret-from-cmd"}
	got, err := c.ResolveKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "secret-from-cmd" {
		t.Errorf("got %q, want %q", got, "secret-from-cmd")
	}
}

func TestResolveKey_CommandCached(t *testing.T) {
	// Pre-populate cache.
	commandCacheMu.Lock()
	commandCache["echo cached"] = "cached-value"
	commandCacheMu.Unlock()

	c := &Credential{Type: CredentialAPIKey, Key: "!echo cached"}
	got, err := c.ResolveKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "cached-value" {
		t.Errorf("got %q, want %q", got, "cached-value")
	}

	// Cleanup.
	commandCacheMu.Lock()
	delete(commandCache, "echo cached")
	commandCacheMu.Unlock()
}

func TestResolveKey_OAuth(t *testing.T) {
	c := &Credential{
		Type:        CredentialOAuth,
		AccessToken: "access-token-123",
	}
	got, err := c.ResolveKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "access-token-123" {
		t.Errorf("got %q, want %q", got, "access-token-123")
	}
}

func TestResolveKeyValue_EnvVarHeuristic(t *testing.T) {
	// Strings that look like env vars (uppercase + digits + underscores, len >= 2)
	// are checked against os.Getenv before falling through to literal.
	tests := []struct {
		input   string
		wantEnv bool // true = treated as env var candidate
	}{
		{"ANTHROPIC_API_KEY", true},
		{"MY_KEY", true},
		{"AB", true},
		{"A1B2", true},
		{"A", false},          // too short
		{"", false},           // empty
		{"my_key", false},     // lowercase
		{"MY-KEY", false},     // hyphen
		{"sk-ant-123", false}, // mixed
	}
	for _, tt := range tests {
		if tt.wantEnv {
			t.Setenv(tt.input, "from-env")
		}
		got, err := resolveKeyValue(tt.input)
		if err != nil {
			t.Fatalf("resolveKeyValue(%q): unexpected error: %v", tt.input, err)
		}
		if tt.wantEnv {
			if got != "from-env" {
				t.Errorf("resolveKeyValue(%q) = %q, want %q (env var candidate)", tt.input, got, "from-env")
			}
		} else {
			if got != tt.input {
				t.Errorf("resolveKeyValue(%q) = %q, want %q (literal passthrough)", tt.input, got, tt.input)
			}
		}
	}
}
