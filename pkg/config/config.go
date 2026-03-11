package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds all pi settings.
type Config struct {
	// Provider settings
	DefaultProvider string `json:"default_provider"`
	DefaultModel    string `json:"default_model"`

	// Behavior
	ThinkingLevel string `json:"thinking_level"`
	MaxTokens     int    `json:"max_tokens"`

	// Paths
	SessionDir string `json:"session_dir"`
	ConfigDir  string `json:"config_dir"`
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	piDir := filepath.Join(home, ".pi")
	return &Config{
		DefaultProvider: "anthropic",
		DefaultModel:    "claude-sonnet-4-20250514",
		ThinkingLevel:   "off",
		MaxTokens:       8192,
		SessionDir:      filepath.Join(piDir, "sessions"),
		ConfigDir:       piDir,
	}
}

// LoadConfig loads configuration by merging (in order):
//  1. Built-in defaults
//  2. Global settings from ~/.pi/settings.json
//  3. Project-local settings from .pi/settings.json (cwd)
//
// Later sources override earlier ones. Missing files are silently ignored.
func LoadConfig() (*Config, error) {
	cfg := DefaultConfig()

	// Global config.
	globalPath := filepath.Join(cfg.ConfigDir, "settings.json")
	if err := mergeFromFile(cfg, globalPath); err != nil {
		return nil, fmt.Errorf("global config: %w", err)
	}

	// Project-local config.
	localPath := filepath.Join(".pi", "settings.json")
	if err := mergeFromFile(cfg, localPath); err != nil {
		return nil, fmt.Errorf("local config: %w", err)
	}

	return cfg, nil
}

// Save writes the config to ~/.pi/settings.json.
func (c *Config) Save() error {
	dir := c.ConfigDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home dir: %w", err)
		}
		dir = filepath.Join(home, ".pi")
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	path := filepath.Join(dir, "settings.json")
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// mergeFromFile reads a JSON file and merges its values into cfg.
// If the file does not exist, it returns nil (not an error).
func mergeFromFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	// Unmarshal into a temporary struct so that zero-value fields in the file
	// don't overwrite non-zero defaults. We use a map to detect which fields
	// are actually present.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	if v, ok := raw["default_provider"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			cfg.DefaultProvider = s
		}
	}
	if v, ok := raw["default_model"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			cfg.DefaultModel = s
		}
	}
	if v, ok := raw["thinking_level"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			cfg.ThinkingLevel = s
		}
	}
	if v, ok := raw["max_tokens"]; ok {
		var n int
		if json.Unmarshal(v, &n) == nil && n > 0 {
			cfg.MaxTokens = n
		}
	}
	if v, ok := raw["session_dir"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			cfg.SessionDir = s
		}
	}
	if v, ok := raw["config_dir"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			cfg.ConfigDir = s
		}
	}

	return nil
}
