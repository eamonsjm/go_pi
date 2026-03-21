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

// ResolveKey resolves the Key field to a usable API key string.
// Env-var names and !commands are evaluated; literals pass through.
func (c *Credential) ResolveKey() (string, error) {
	if c.Type == CredentialOAuth {
		return c.AccessToken, nil
	}
	return resolveKeyValue(c.Key)
}

// commandCache caches results of !command key values within a process.
var (
	commandCacheMu sync.Mutex
	commandCache   = map[string]string{}
)

// resolveKeyValue interprets a key value string:
//
//	"!cmd args"  → run shell command, cache result
//	"ENV_NAME"   → os.Getenv if it looks like an env var (all-caps + underscores)
//	otherwise    → literal
func resolveKeyValue(val string) (string, error) {
	if val == "" {
		return "", nil
	}

	// Shell command.
	if strings.HasPrefix(val, "!") {
		return resolveCommand(val[1:])
	}

	// Env var heuristic: all uppercase letters, digits, and underscores, at least 2 chars.
	envVar := len(val) >= 2
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
// Results are cached for the lifetime of the process.
//
// Security boundary: cmd comes from the user's auth config file (~/.gi/auth.json),
// which is written with 0600 permissions and verified on load (see Store.Load).
// The config file IS the trust boundary — if an attacker can write to it, they
// already have the user's credentials. The !command feature is intentional for
// password-manager integration (e.g., "!pass show anthropic-key").
func resolveCommand(cmd string) (string, error) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", fmt.Errorf("empty key command")
	}

	commandCacheMu.Lock()
	if cached, ok := commandCache[cmd]; ok {
		commandCacheMu.Unlock()
		return cached, nil
	}
	commandCacheMu.Unlock()

	out, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		return "", fmt.Errorf("key command %q: %w", cmd, err)
	}
	result := strings.TrimSpace(string(out))

	commandCacheMu.Lock()
	commandCache[cmd] = result
	commandCacheMu.Unlock()

	return result, nil
}
