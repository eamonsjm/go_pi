package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// SopsConfig stores SOPS encryption settings for the auth store.
// Persisted at <configDir>/sops-config.json (typically ~/.gi/sops-config.json).
type SopsConfig struct {
	Enabled    bool   `json:"enabled"`
	AgeKeyPath string `json:"age_key_path"`
	KMSArn     string `json:"kms_arn,omitempty"`
}

// SopsConfigPath returns the filesystem path for sops-config.json
// within the given config directory.
func SopsConfigPath(configDir string) string {
	return filepath.Join(configDir, "sops-config.json")
}

// LoadSopsConfig reads sops-config.json from configDir.
// Returns a zero-value config (Enabled=false) if the file does not exist.
func LoadSopsConfig(configDir string) (*SopsConfig, error) {
	path := SopsConfigPath(configDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &SopsConfig{}, nil
		}
		return nil, fmt.Errorf("read sops config: %w", err)
	}

	var cfg SopsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse sops config: %w", err)
	}
	return &cfg, nil
}

// SaveSopsConfig writes sops-config.json to configDir with 0600 permissions.
// Creates parent directories (0700) if needed.
func SaveSopsConfig(configDir string, cfg *SopsConfig) error {
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal sops config: %w", err)
	}

	path := SopsConfigPath(configDir)
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write sops config: %w", err)
	}
	return nil
}

// RemoveSopsConfig deletes sops-config.json from configDir.
// Returns nil if the file does not exist.
func RemoveSopsConfig(configDir string) error {
	path := SopsConfigPath(configDir)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove sops config: %w", err)
	}
	return nil
}
