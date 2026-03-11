package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Known environment variable names for each provider.
var providerEnvVars = map[string]string{
	"anthropic":  "ANTHROPIC_API_KEY",
	"openrouter": "OPENROUTER_API_KEY",
	"openai":     "OPENAI_API_KEY",
	"google":     "GOOGLE_API_KEY",
}

// AuthStore manages API keys for LLM providers.
type AuthStore struct {
	Keys map[string]string `json:"keys"` // provider name -> API key
}

// LoadAuth loads API keys by merging:
//  1. Keys from ~/.pi/auth.json
//  2. Environment variables (override file-based keys)
func LoadAuth() (*AuthStore, error) {
	store := &AuthStore{
		Keys: make(map[string]string),
	}

	// Load from file.
	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, ".pi", "auth.json")
		if data, err := os.ReadFile(path); err == nil {
			var fileStore AuthStore
			if err := json.Unmarshal(data, &fileStore); err != nil {
				return nil, fmt.Errorf("parse auth.json: %w", err)
			}
			for k, v := range fileStore.Keys {
				store.Keys[k] = v
			}
		}
		// Missing file is fine — we'll try env vars.
	}

	// Environment variables take precedence.
	for provider, envVar := range providerEnvVars {
		if val := os.Getenv(envVar); val != "" {
			store.Keys[provider] = val
		}
	}

	return store, nil
}

// GetKey returns the API key for the given provider, or empty string if not found.
func (a *AuthStore) GetKey(provider string) string {
	return a.Keys[provider]
}

// SetKey sets an API key for a provider in the store (does not persist to disk).
func (a *AuthStore) SetKey(provider, key string) {
	a.Keys[provider] = key
}

// Save writes the auth store to ~/.pi/auth.json.
func (a *AuthStore) Save() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}

	dir := filepath.Join(home, ".pi")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal auth: %w", err)
	}

	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write auth.json: %w", err)
	}
	return nil
}
