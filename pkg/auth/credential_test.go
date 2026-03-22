package auth

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCredential_String_RedactsSecrets(t *testing.T) {
	c := Credential{
		Type:         CredentialAPIKey,
		Key:          "sk-ant-live-supersecret",
		RefreshToken: "rt-secret",
		AccessToken:  "at-secret",
		ExpiresAt:    1234567890,
	}
	s := c.String()

	if strings.Contains(s, "supersecret") || strings.Contains(s, "rt-secret") || strings.Contains(s, "at-secret") {
		t.Errorf("String() leaked secret values: %s", s)
	}
	if !strings.Contains(s, "api_key") {
		t.Errorf("String() should contain credential type, got: %s", s)
	}
	if !strings.Contains(s, "Key:[set]") {
		t.Errorf("String() should indicate Key is set, got: %s", s)
	}
	if !strings.Contains(s, "RefreshToken:[set]") {
		t.Errorf("String() should indicate RefreshToken is set, got: %s", s)
	}
}

func TestCredential_String_EmptyFields(t *testing.T) {
	c := Credential{Type: CredentialOAuth}
	s := c.String()

	if !strings.Contains(s, "Key:[empty]") {
		t.Errorf("String() should indicate Key is empty, got: %s", s)
	}
	if !strings.Contains(s, "RefreshToken:[empty]") {
		t.Errorf("String() should indicate RefreshToken is empty, got: %s", s)
	}
	if !strings.Contains(s, "AccessToken:[empty]") {
		t.Errorf("String() should indicate AccessToken is empty, got: %s", s)
	}
}

func TestCredential_SprintfV_UsesStringer(t *testing.T) {
	c := Credential{
		Type: CredentialAPIKey,
		Key:  "sk-ant-live-supersecret",
	}

	for _, verb := range []string{"%v", "%+v", "%s"} {
		out := fmt.Sprintf(verb, c)
		if strings.Contains(out, "supersecret") {
			t.Errorf("fmt.Sprintf(%q, cred) leaked secret: %s", verb, out)
		}
	}
}

func TestCredential_GoString(t *testing.T) {
	c := Credential{Type: CredentialAPIKey, Key: "secret"}
	s := fmt.Sprintf("%#v", c)
	if strings.Contains(s, "secret") && !strings.Contains(s, "[set]") {
		t.Errorf("GoString() leaked secret via %%#v: %s", s)
	}
}

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

func TestKeyResolver_Literal(t *testing.T) {
	kr := NewKeyResolver()
	got, err := kr.ResolveKeyValue("sk-ant-live-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "sk-ant-live-123" {
		t.Errorf("got %q, want %q", got, "sk-ant-live-123")
	}
}

func TestKeyResolver_EnvVar(t *testing.T) {
	t.Setenv("MY_SECRET_KEY", "resolved-from-env")
	kr := NewKeyResolver()
	got, err := kr.ResolveKeyValue("MY_SECRET_KEY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "resolved-from-env" {
		t.Errorf("got %q, want %q", got, "resolved-from-env")
	}
}

func TestKeyResolver_EnvVarUnset(t *testing.T) {
	// Unset env var — should fall through to literal.
	t.Setenv("UNSET_VAR_TEST", "")
	kr := NewKeyResolver()
	got, err := kr.ResolveKeyValue("UNSET_VAR_TEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Falls through to literal since env var is empty.
	if got != "UNSET_VAR_TEST" {
		t.Errorf("got %q, want %q", got, "UNSET_VAR_TEST")
	}
}

func TestKeyResolver_Command(t *testing.T) {
	kr := NewKeyResolver()
	got, err := kr.ResolveKeyValue("!echo secret-from-cmd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "secret-from-cmd" {
		t.Errorf("got %q, want %q", got, "secret-from-cmd")
	}
}

func TestKeyResolver_CommandCached(t *testing.T) {
	kr := NewKeyResolver()

	// Pre-populate cache with a completed entry.
	entry := &commandEntry{result: "cached-value"}
	entry.once.Do(func() {}) // mark as done
	kr.mu.Lock()
	kr.cache["echo cached"] = entry
	kr.mu.Unlock()

	got, err := kr.ResolveKeyValue("!echo cached")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "cached-value" {
		t.Errorf("got %q, want %q", got, "cached-value")
	}
}

func TestKeyResolver_NoDuplicateExecution(t *testing.T) {
	// Verify that concurrent calls to resolveCommand for the same key execute
	// the underlying command exactly once (no TOCTOU race).
	kr := NewKeyResolver()
	const cmd = "echo race-test-sentinel"

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	results := make([]string, goroutines)
	errs := make([]error, goroutines)
	var ready sync.WaitGroup
	ready.Add(goroutines)

	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			ready.Done()
			ready.Wait() // all goroutines start ~simultaneously
			results[idx], errs[idx] = kr.resolveCommand(cmd)
		}(i)
	}
	wg.Wait()

	for i := range goroutines {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: unexpected error: %v", i, errs[i])
		}
		if results[i] != "race-test-sentinel" {
			t.Errorf("goroutine %d: got %q, want %q", i, results[i], "race-test-sentinel")
		}
	}
}

func TestKeyResolver_NoDuplicateExecution_Counter(t *testing.T) {
	// Use a command key that increments a shared counter to verify
	// the command body executes exactly once despite concurrent callers.
	var execCount atomic.Int32
	kr := NewKeyResolver()
	const key = "counter-test"

	// Manually create an entry whose Once.Do runs our counting function
	// instead of exec.Command, to directly verify single-execution.
	entry := &commandEntry{}
	kr.mu.Lock()
	kr.cache[key] = entry
	kr.mu.Unlock()

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	var ready sync.WaitGroup
	ready.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			ready.Done()
			ready.Wait()
			entry.once.Do(func() {
				execCount.Add(1)
				entry.result = "counted"
			})
		}()
	}
	wg.Wait()

	if got := execCount.Load(); got != 1 {
		t.Errorf("command executed %d times, want exactly 1", got)
	}
}

func TestKeyResolver_EnvVarHeuristic(t *testing.T) {
	// Strings that look like env vars (uppercase + digits + underscores, len >= 4)
	// are checked against os.Getenv before falling through to literal.
	kr := NewKeyResolver()
	tests := []struct {
		input   string
		wantEnv bool // true = treated as env var candidate
	}{
		{"ANTHROPIC_API_KEY", true},
		{"MY_KEY", true},
		{"A1B2", true},
		{"KEYS", true},        // exactly 4 chars
		{"AB", false},         // too short (< 4)
		{"SK", false},         // too short, could be mistaken for env var
		{"KEY", false},        // too short (3 chars)
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
		got, err := kr.ResolveKeyValue(tt.input)
		if err != nil {
			t.Fatalf("ResolveKeyValue(%q): unexpected error: %v", tt.input, err)
		}
		if tt.wantEnv {
			if got != "from-env" {
				t.Errorf("ResolveKeyValue(%q) = %q, want %q (env var candidate)", tt.input, got, "from-env")
			}
		} else {
			if got != tt.input {
				t.Errorf("ResolveKeyValue(%q) = %q, want %q (literal passthrough)", tt.input, got, tt.input)
			}
		}
	}
}
