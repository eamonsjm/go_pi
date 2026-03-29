package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// warnUnencryptedOnce ensures the "credentials stored as plaintext" warning
// is emitted at most once per process lifetime.
var warnUnencryptedOnce sync.Once

// Store manages persisted authentication credentials for all providers.
// It reads from and writes to ~/.gi/auth.json with file-level locking
// to prevent concurrent refresh races.
type Store struct {
	path       string                 // path to auth.json
	ageKeyPath string                 // path to age private key for SOPS decrypt; default: age-key.txt beside auth.json
	mu         sync.RWMutex           // protects entries
	entries    map[string]*Credential // provider ID → credential
	lockFile   *os.File               // held while locked; nil when unlocked
	sopsKey    string                 // age public key; non-empty enables SOPS encryption on Save
	encrypted  bool                   // true if loaded file was SOPS-encrypted
}

// NewStore creates a Store backed by the given file path.
// If path is empty, it defaults to ~/.gi/auth.json.
//
// If a sops-config.json exists in the same directory, the store reads it
// and pre-configures SOPS encryption (sopsKey) so that Save encrypts
// even when the file is currently plaintext.
func NewStore(path string) (*Store, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		path = filepath.Join(home, ".gi", "auth.json")
	}
	s := &Store{
		path:       path,
		ageKeyPath: filepath.Join(filepath.Dir(path), "age-key.txt"),
		entries:    make(map[string]*Credential),
	}

	// Apply sops-config.json if present and enabled.
	configDir := filepath.Dir(path)
	cfg, err := LoadSopsConfig(configDir)
	if err != nil {
		return nil, fmt.Errorf("load sops config: %w", err)
	}
	if cfg.Enabled {
		if cfg.AgeKeyPath != "" {
			s.ageKeyPath = cfg.AgeKeyPath
		}
		// Pre-load the public key so Save encrypts.
		id, err := LoadAgeKey(s.ageKeyPath)
		if err == nil {
			s.sopsKey = id.Recipient().String()
		} else if errors.Is(err, os.ErrNotExist) {
			// Key doesn't exist yet — the encrypt command will generate it.
			// Warn so the user knows encryption isn't active yet.
			fmt.Fprintf(os.Stderr, "warning: SOPS enabled but age key not found at %s; encryption inactive until key is generated\n", s.ageKeyPath)
		} else {
			return nil, fmt.Errorf("load age key: %w", err)
		}
	}

	return s, nil
}

// SetAgeKeyPath overrides the path to the age private key used for SOPS
// decryption. By default this is "age-key.txt" in the same directory as
// the auth store file.
func (s *Store) SetAgeKeyPath(p string) { s.ageKeyPath = p }

// AgeKeyPath returns the current age private-key path.
func (s *Store) AgeKeyPath() string { return s.ageKeyPath }

// Path returns the filesystem path of the auth store file.
func (s *Store) Path() string { return s.path }

// Encrypted reports whether the currently-loaded file was SOPS-encrypted.
func (s *Store) Encrypted() bool { return s.encrypted }

// SopsKey returns the age public key used for encryption, or "" if SOPS is not active.
func (s *Store) SopsKey() string { return s.sopsKey }

// SetSopsKey sets the age public key for SOPS encryption.
// A non-empty value causes Save to encrypt; empty disables encryption.
// The encrypted flag is not updated here — it tracks file-on-disk state
// and is only updated after a successful Save.
func (s *Store) SetSopsKey(pub string) {
	s.sopsKey = pub
}

// ConfigDir returns the directory containing the auth store (typically ~/.gi).
func (s *Store) ConfigDir() string { return filepath.Dir(s.path) }

// Load reads credentials from the backing file. If the file does not exist,
// the store starts empty (not an error).
//
// Supports two formats:
//   - New: {"provider": {"type":"api_key","key":"sk-..."}, ...}
//   - Legacy: {"keys": {"provider": "sk-...", ...}}  (auto-migrated on load)
func (s *Store) Load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read auth store: %w", err)
	}

	// The auth store may contain !command values that are executed via sh -c.
	// Verify file permissions before trusting its contents.
	if err := checkFilePermissions(s.path); err != nil {
		return fmt.Errorf("check auth store permissions: %w", err)
	}

	// Detect and decrypt SOPS-encrypted files transparently.
	if isSopsEncrypted(data) {
		keyPath := s.ageKeyPath
		plain, err := decryptSops(data, keyPath)
		if err != nil {
			return fmt.Errorf("decrypt auth store: %w", err)
		}
		data = plain
		s.encrypted = true

		// Extract the public key so Save re-encrypts automatically.
		id, err := LoadAgeKey(keyPath)
		if err != nil {
			return fmt.Errorf("load age key for re-encryption: %w", err)
		}
		s.sopsKey = id.Recipient().String()
	}

	// Probe the JSON structure to detect format.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("parse auth store: %w", err)
	}

	// Legacy format: top-level "keys" field with string values.
	if _, hasKeys := probe["keys"]; hasKeys {
		return s.loadLegacy(data)
	}

	// New format: map[string]*Credential.
	var entries map[string]*Credential
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse auth store: %w", err)
	}
	if entries == nil {
		entries = make(map[string]*Credential)
	}
	s.mu.Lock()
	s.entries = entries
	s.mu.Unlock()

	// Warn once per session if credentials are stored as plaintext.
	if !s.encrypted && len(entries) > 0 {
		warnUnencryptedOnce.Do(func() {
			fmt.Fprintf(os.Stderr, "warning: credentials in %s are not encrypted. Run /encrypt or `gi auth encrypt` to enable SOPS encryption.\n", s.path)
		})
	}

	return nil
}

// loadLegacy converts the old {"keys":{"provider":"key"}} format
// to Credential entries and saves in the new format.
func (s *Store) loadLegacy(data []byte) error {
	var legacy struct {
		Keys map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("parse legacy auth store: %w", err)
	}
	entries := make(map[string]*Credential, len(legacy.Keys))
	for provider, key := range legacy.Keys {
		entries[provider] = &Credential{
			Type: CredentialAPIKey,
			Key:  key,
		}
	}
	s.mu.Lock()
	s.entries = entries
	s.mu.Unlock()
	return s.Save()
}

// Save writes the current credentials to disk with strict permissions (0600).
// Creates parent directories (0700) if needed. If SOPS encryption is enabled
// (sopsKey is set), the file is re-encrypted before writing.
func (s *Store) Save() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create auth dir: %w", err)
	}

	s.mu.RLock()
	data, err := json.MarshalIndent(s.entries, "", "  ")
	s.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshal auth store: %w", err)
	}

	if s.sopsKey != "" {
		data, err = encryptSops(data, s.sopsKey)
		if err != nil {
			return fmt.Errorf("encrypt auth store: %w", err)
		}
	} else if s.encrypted {
		fmt.Fprintf(os.Stderr, "warning: auth store was encrypted but no SOPS key configured; saving as plaintext\n")
	}

	if err := os.WriteFile(s.path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write auth store: %w", err)
	}

	// Update encrypted flag to reflect what was actually written to disk.
	s.encrypted = s.sopsKey != ""

	return nil
}

// Get returns the credential for a provider, or nil if not stored.
func (s *Store) Get(provider string) *Credential {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entries[provider]
}

// HasCredential reports whether credentials are stored for a provider.
func (s *Store) HasCredential(provider string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entries[provider] != nil
}

// Set stores a credential for a provider (in memory; call Save to persist).
func (s *Store) Set(provider string, cred *Credential) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[provider] = cred
}

// Delete removes a provider's credential (in memory; call Save to persist).
func (s *Store) Delete(provider string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, provider)
}

// Providers returns the sorted list of provider IDs that have stored credentials.
func (s *Store) Providers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.entries))
	for k := range s.entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Lock and Unlock are implemented in platform-specific files:
// - store_unix.go: Uses syscall.Flock for file-based locking
// - store_windows.go: Uses LockFileEx for file-based locking

// WithLock executes fn while holding an exclusive file lock on the auth store.
// This prevents concurrent processes from racing on token refresh.
// The context controls lock acquisition timeout/cancellation.
func (s *Store) WithLock(ctx context.Context, fn func() error) error {
	if err := s.Lock(ctx); err != nil {
		return fmt.Errorf("lock auth store: %w", err)
	}
	defer s.Unlock()
	return fn()
}
