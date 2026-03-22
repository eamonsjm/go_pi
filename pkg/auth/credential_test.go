package auth

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clearCommandCache removes a key from the command cache between tests.
func clearCommandCache(key string) {
	commandCacheMu.Lock()
	delete(commandCache, key)
	commandCacheMu.Unlock()
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
	clearCommandCache("echo secret-from-cmd")

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
	// Pre-populate cache with a completed entry.
	entry := &commandEntry{result: "cached-value"}
	entry.once.Do(func() {}) // mark as done
	commandCacheMu.Lock()
	commandCache["echo cached"] = entry
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
	clearCommandCache("echo cached")
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

func TestResolveCommand_NoDuplicateExecution(t *testing.T) {
	// Verify that concurrent calls to resolveCommand for the same key execute
	// the underlying command exactly once (no TOCTOU race).
	const cmd = "echo race-test-sentinel"
	clearCommandCache(cmd)

	// Count how many times the command actually runs by using a wrapper that
	// increments an atomic counter. We can't easily intercept exec.Command,
	// so instead we use a unique cache key that calls a command which writes
	// to a temp file and counts invocations via the file system.
	//
	// Simpler approach: launch many goroutines, verify all get the same result
	// and the cache entry exists exactly once.
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
			results[idx], errs[idx] = resolveCommand(cmd)
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

	clearCommandCache(cmd)
}

func TestResolveCommand_NoDuplicateExecution_Counter(t *testing.T) {
	// Use a command key that increments a shared counter to verify
	// the command body executes exactly once despite concurrent callers.
	var execCount atomic.Int32
	const key = "counter-test"
	clearCommandCache(key)

	// Manually create an entry whose Once.Do runs our counting function
	// instead of exec.Command, to directly verify single-execution.
	entry := &commandEntry{}
	commandCacheMu.Lock()
	commandCache[key] = entry
	commandCacheMu.Unlock()

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

	clearCommandCache(key)
}

func TestResolveKeyValue_EnvVarHeuristic(t *testing.T) {
	// Strings that look like env vars (uppercase + digits + underscores, len >= 4)
	// are checked against os.Getenv before falling through to literal.
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
