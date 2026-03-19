package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RTKConfig holds RTK-specific settings.
type RTKConfig struct {
	Enabled               bool              `json:"enabled"`
	MetricsEnabled        bool              `json:"metrics_enabled"`
	CompressionLevels     map[string]string `json:"compression_levels"` // category -> level (low/medium/high)
	EnabledCategories     map[string]bool   `json:"enabled_categories"`
	ExportPath            string            `json:"export_path"`
}

// Config holds all gi settings.
type Config struct {
	// Provider settings
	DefaultProvider string `json:"default_provider"`
	DefaultModel    string `json:"default_model"`

	// Behavior
	ThinkingLevel string `json:"thinking_level"`
	MaxTokens     int    `json:"max_tokens"`

	// Appearance
	Theme string `json:"theme"`

	// Paths
	SessionDir string `json:"session_dir"`
	ConfigDir  string `json:"config_dir"`

	// Command aliases
	Aliases map[string]string `json:"aliases"`

	// RTK (Rust Token Killer) compression settings
	RTK *RTKConfig `json:"rtk"`
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	giDir := filepath.Join(home, ".gi")
	return &Config{
		DefaultProvider: "anthropic",
		DefaultModel:    "claude-sonnet-4-20250514",
		ThinkingLevel:   "off",
		MaxTokens:       8192,
		Theme:           "auto",
		SessionDir:      filepath.Join(giDir, "sessions"),
		ConfigDir:       giDir,
		RTK: &RTKConfig{
			Enabled:           true,
			MetricsEnabled:    true,
			CompressionLevels: defaultCompressionLevels(),
			EnabledCategories: defaultEnabledCategories(),
			ExportPath:        filepath.Join(giDir, "metrics.json"),
		},
	}
}

// defaultCompressionLevels returns default compression settings.
func defaultCompressionLevels() map[string]string {
	return map[string]string{
		"go-test":   "medium",
		"go-build":  "high",
		"git-log":   "medium",
		"linter":    "medium",
		"generic":   "low",
	}
}

// defaultEnabledCategories returns which categories are enabled by default.
func defaultEnabledCategories() map[string]bool {
	return map[string]bool{
		"git":     true,
		"docker":  true,
		"build":   true,
		"package": true,
		"test":    true,
		"file":    true,
		"other":   true,
	}
}

// LoadConfig loads configuration by merging (in order):
//  1. Built-in defaults
//  2. Global settings from ~/.gi/settings.json
//  3. Project-local settings from .gi/settings.json (cwd)
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
	localPath := filepath.Join(".gi", "settings.json")
	if err := mergeFromFile(cfg, localPath); err != nil {
		return nil, fmt.Errorf("local config: %w", err)
	}

	return cfg, nil
}

// Save writes the config to ~/.gi/settings.json.
func (c *Config) Save() error {
	dir := c.ConfigDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home dir: %w", err)
		}
		dir = filepath.Join(home, ".gi")
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
	if v, ok := raw["theme"]; ok {
		var s string
		if json.Unmarshal(v, &s) == nil && s != "" {
			cfg.Theme = s
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

	// Command aliases
	if v, ok := raw["aliases"]; ok {
		var aliases map[string]string
		if json.Unmarshal(v, &aliases) == nil && aliases != nil {
			cfg.Aliases = aliases
		}
	}

	// RTK configuration
	if v, ok := raw["rtk"]; ok {
		var rtkCfg RTKConfig
		if json.Unmarshal(v, &rtkCfg) == nil {
			cfg.RTK = &rtkCfg
		}
	}

	return nil
}
