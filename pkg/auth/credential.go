package auth

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// CredentialType identifies the kind of credential stored.
type CredentialType string

const (
	CredentialAPIKey CredentialType = "api_key"
	CredentialOAuth  CredentialType = "oauth"
)

// Credential is the stored authentication data for a single provider.
// Only fields relevant to the Type are used.
type Credential struct {
	Type         CredentialType `json:"type"`
	Key          string         `json:"key,omitempty"`           // API key value (literal, env ref, or !command)
	RefreshToken string         `json:"refresh_token,omitempty"` // OAuth refresh token
	AccessToken  string         `json:"access_token,omitempty"`  // OAuth access token
	ExpiresAt    int64          `json:"expires_at,omitempty"`    // Unix ms when access token expires
}

// String implements fmt.Stringer. It returns a human-readable representation
// that redacts sensitive fields (Key, RefreshToken, AccessToken), showing only
// whether each is set. This prevents accidental secret leakage via %v or %+v.
func (c Credential) String() string {
	return fmt.Sprintf("Credential{Type:%s Key:%s RefreshToken:%s AccessToken:%s ExpiresAt:%d}",
		c.Type, redact(c.Key), redact(c.RefreshToken), redact(c.AccessToken), c.ExpiresAt)
}

// GoString implements fmt.GoStringer for %#v formatting.
func (c Credential) GoString() string {
	return c.String()
}

// redact returns "[set]" or "[empty]" for a secret field value.
func redact(s string) string {
	if s != "" {
		return "[set]"
	}
	return "[empty]"
}

// IsExpired reports whether an OAuth credential's access token has expired.
// Includes a 5-minute buffer to avoid edge-case failures.
func (c *Credential) IsExpired() bool {
	if c.Type != CredentialOAuth || c.ExpiresAt == 0 {
		return false
	}
	const bufferMs = 5 * 60 * 1000 // 5 minutes
	return time.Now().UnixMilli() >= c.ExpiresAt-bufferMs
}

// --- API key value resolution ---
//
// An API key value can be:
//   - A literal string (used as-is)
//   - An env var name without prefix (resolved at runtime via os.Getenv)
//   - A shell command prefixed with "!" (executed, stdout cached)

// commandEntry holds the cached result of a single !command execution.
// sync.Once ensures at most one goroutine executes the command; all others
// block until the result is available.
type commandEntry struct {
	once   sync.Once
	result string
	err    error
}

// KeyResolver resolves API key values, caching !command results.
// Use NewKeyResolver to create an instance; inject it via the Resolver
// rather than relying on package-level global state.
type KeyResolver struct {
	mu    sync.Mutex
	cache map[string]*commandEntry
}

// NewKeyResolver creates a KeyResolver with an empty command cache.
func NewKeyResolver() *KeyResolver {
	return &KeyResolver{
		cache: make(map[string]*commandEntry),
	}
}

// ResolveKeyValue interprets a key value string:
//
//	"!cmd args"  → run shell command, cache result
//	"ENV_NAME"   → os.Getenv if it looks like an env var (all-caps + underscores)
//	otherwise    → literal
func (kr *KeyResolver) ResolveKeyValue(val string) (string, error) {
	if val == "" {
		return "", nil
	}

	// Shell command.
	if strings.HasPrefix(val, "!") {
		return kr.resolveCommand(val[1:])
	}

	// Env var heuristic: all uppercase letters, digits, and underscores, at least 4 chars.
	// Minimum of 4 prevents short uppercase literals (e.g. "SK", "OK") from accidentally
	// resolving as environment variables.
	envVar := len(val) >= 4
	for _, r := range val {
		if r != '_' && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			envVar = false
			break
		}
	}
	if envVar {
		if v := os.Getenv(val); v != "" {
			return v, nil
		}
		// Fall through to return as literal if env var is unset.
	}

	return val, nil
}

// resolveCommand executes a shell command and returns its trimmed stdout.
// Results are cached for the lifetime of the KeyResolver.
//
// Security boundary: cmd comes from the user's auth config file (~/.gi/auth.json),
// which is written with 0600 permissions and verified on load (see Store.Load).
// The config file IS the trust boundary — if an attacker can write to it, they
// already have the user's credentials. The !command feature is intentional for
// password-manager integration (e.g., "!pass show anthropic-key").
func (kr *KeyResolver) resolveCommand(cmd string) (string, error) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", fmt.Errorf("empty key command")
	}

	kr.mu.Lock()
	entry, ok := kr.cache[cmd]
	if !ok {
		entry = &commandEntry{}
		kr.cache[cmd] = entry
	}
	kr.mu.Unlock()

	entry.once.Do(func() {
		out, err := exec.Command("sh", "-c", cmd).Output()
		if err != nil {
			entry.err = fmt.Errorf("key command %q: %w", cmd, err)
			return
		}
		entry.result = strings.TrimSpace(string(out))
	})

	return entry.result, entry.err
}
