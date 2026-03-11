package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Store manages persisted authentication credentials for all providers.
// It reads from and writes to ~/.pi/auth.json with file-level locking
// to prevent concurrent refresh races.
type Store struct {
	path    string                 // path to auth.json
	entries map[string]*Credential // provider ID → credential
}

// NewStore creates a Store backed by the given file path.
// If path is empty, it defaults to ~/.pi/auth.json.
func NewStore(path string) (*Store, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		path = filepath.Join(home, ".pi", "auth.json")
	}
	return &Store{
		path:    path,
		entries: make(map[string]*Credential),
	}, nil
}

// Load reads credentials from the backing file. If the file does not exist,
// the store starts empty (not an error).
func (s *Store) Load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read auth store: %w", err)
	}

	var entries map[string]*Credential
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse auth store: %w", err)
	}
	s.entries = entries
	return nil
}

// Save writes the current credentials to disk with strict permissions (0600).
// Creates parent directories (0700) if needed.
func (s *Store) Save() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create auth dir: %w", err)
	}

	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal auth store: %w", err)
	}

	if err := os.WriteFile(s.path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write auth store: %w", err)
	}
	return nil
}

// Get returns the credential for a provider, or nil if not stored.
func (s *Store) Get(provider string) *Credential {
	return s.entries[provider]
}

// Set stores a credential for a provider (in memory; call Save to persist).
func (s *Store) Set(provider string, cred *Credential) {
	s.entries[provider] = cred
}

// Delete removes a provider's credential (in memory; call Save to persist).
func (s *Store) Delete(provider string) {
	delete(s.entries, provider)
}

// Providers returns the list of provider IDs that have stored credentials.
func (s *Store) Providers() []string {
	out := make([]string, 0, len(s.entries))
	for k := range s.entries {
		out = append(out, k)
	}
	return out
}

// WithLock executes fn while holding an exclusive file lock on the auth store.
// This prevents concurrent processes from racing on token refresh.
// The lock file is adjacent to auth.json (auth.json.lock).
func (s *Store) WithLock(fn func() error) error {
	lockPath := s.path + ".lock"

	dir := filepath.Dir(lockPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("acquire lock: %w", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	return fn()
}
