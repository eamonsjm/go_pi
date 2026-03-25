package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RTKConfig holds RTK-specific settings.
type RTKConfig struct {
	Enabled           bool              `json:"enabled"`
	MetricsEnabled    bool              `json:"metrics_enabled"`
	CompressionLevels map[string]string `json:"compression_levels"` // category -> level (low/medium/high)
	EnabledCategories map[string]bool   `json:"enabled_categories"`
	ExportPath        string            `json:"export_path"`
}

// MCPServerConfig describes a single MCP server connection.
type MCPServerConfig struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`     // Streamable HTTP endpoint
	Headers map[string]string `json:"headers,omitempty"` // for Streamable HTTP auth

	// Permission overrides
	Permissions *MCPPermissionConfig `json:"permissions,omitempty"`

	// Sampling limits
	Sampling *SamplingConfig `json:"sampling,omitempty"`

	// Instruction handling: "use" (default) or "ignore"
	Instructions string `json:"instructions,omitempty"`

	// Origin indicates where this config was loaded from ("global" or
	// "project"). Not serialized — set at runtime by LoadConfig.
	Origin string `json:"-"`
}

// MCPPermissionConfig controls per-server tool permission overrides.
type MCPPermissionConfig struct {
	AutoApprove []string `json:"autoApprove,omitempty"` // tool names auto-approved
	Deny        []string `json:"deny,omitempty"`        // tool names always denied
}

// SamplingConfig controls MCP sampling (LLM invocation by servers).
type SamplingConfig struct {
	Enabled         bool `json:"enabled"`
	MaxTokens       int  `json:"maxTokens"`
	SkipApproval bool `json:"skipApproval,omitempty"` // default: false (approval required)
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

	// MCP server definitions. Keys are server names.
	MCPServers map[string]*MCPServerConfig `json:"mcpServers,omitempty"`

	// AllowProjectEnvVars lists environment variable names that project-level
	// MCP configs may interpolate. Global config has full interpolation access;
	// project-level is restricted to this allowlist for security.
	AllowProjectEnvVars []string `json:"allowProjectEnvVars,omitempty"`
}

// DefaultConfig returns a Config populated with sensible defaults.
func DefaultConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	giDir := filepath.Join(home, ".gi")
	return &Config{
		DefaultProvider: "anthropic",
		DefaultModel:    "claude-sonnet-4-6",
		ThinkingLevel:   "off",
		MaxTokens:       8192,
		Theme:           "auto",
		SessionDir:      filepath.Join(giDir, "sessions"),
		ConfigDir:       giDir,
		RTK: &RTKConfig{
			Enabled:           true,
			MetricsEnabled:    true,
			CompressionLevels: map[string]string{
				"go-test":  "medium",
				"go-build": "high",
				"git-log":  "medium",
				"linter":   "medium",
				"generic":  "low",
			},
			EnabledCategories: map[string]bool{
				"git":     true,
				"docker":  true,
				"build":   true,
				"package": true,
				"test":    true,
				"file":    true,
				"other":   true,
			},
			ExportPath:        filepath.Join(giDir, "metrics.json"),
		},
	}, nil
}


// LoadConfigOption customises LoadConfig behaviour.
type LoadConfigOption func(*loadConfigOptions)

type loadConfigOptions struct {
	configDir      string // override global config directory
	localConfigPath string // override project-local config path
}

// WithConfigDir overrides the global configuration directory
// (default: ~/.gi). The global settings.json is read from this directory.
func WithConfigDir(dir string) LoadConfigOption {
	return func(o *loadConfigOptions) { o.configDir = dir }
}

// WithLocalConfigPath overrides the project-local configuration file path
// (default: .gi/settings.json relative to cwd).
func WithLocalConfigPath(path string) LoadConfigOption {
	return func(o *loadConfigOptions) { o.localConfigPath = path }
}

// LoadConfig loads configuration by merging (in order):
//  1. Built-in defaults
//  2. Global settings from ~/.gi/settings.json
//  3. Project-local settings from .gi/settings.json (cwd)
//
// Later sources override earlier ones. Missing files are silently ignored.
// Pass LoadConfigOption values to customise config directory or local path.
func LoadConfig(opts ...LoadConfigOption) (*Config, error) {
	var o loadConfigOptions
	for _, fn := range opts {
		fn(&o)
	}

	cfg, err := DefaultConfig()
	if err != nil {
		return nil, fmt.Errorf("default config: %w", err)
	}

	// Apply config dir override before resolving paths.
	if o.configDir != "" {
		cfg.ConfigDir = o.configDir
		cfg.SessionDir = filepath.Join(o.configDir, "sessions")
		if cfg.RTK != nil {
			cfg.RTK.ExportPath = filepath.Join(o.configDir, "metrics.json")
		}
	}

	// Global config.
	globalPath := filepath.Join(cfg.ConfigDir, "settings.json")
	if err := mergeFromFile(cfg, globalPath); err != nil {
		return nil, fmt.Errorf("global config: %w", err)
	}

	// Mark all servers loaded so far as global-origin.
	for _, srv := range cfg.MCPServers {
		if srv != nil {
			srv.Origin = "global"
		}
	}

	// Project-local config.
	localPath := filepath.Join(".gi", "settings.json")
	if o.localConfigPath != "" {
		localPath = o.localConfigPath
	}
	if err := mergeFromFile(cfg, localPath); err != nil {
		return nil, fmt.Errorf("local config: %w", err)
	}

	// Servers added or replaced by the project-local config have Origin == ""
	// (freshly unmarshaled). Mark them as project-origin.
	for _, srv := range cfg.MCPServers {
		if srv != nil && srv.Origin == "" {
			srv.Origin = "project"
		}
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
		return fmt.Errorf("read config %s: %w", path, err)
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

	// Command aliases (per-key merge with null-deletion, like MCP servers)
	if v, ok := raw["aliases"]; ok {
		var aliases map[string]*string
		if json.Unmarshal(v, &aliases) == nil && aliases != nil {
			if cfg.Aliases == nil {
				cfg.Aliases = make(map[string]string)
			}
			for name, val := range aliases {
				if val == nil {
					// null value disables an alias from a higher tier
					delete(cfg.Aliases, name)
				} else {
					cfg.Aliases[name] = *val
				}
			}
		}
	}

	// RTK configuration — merge per-field, not replace
	if v, ok := raw["rtk"]; ok {
		var rtkCfg RTKConfig
		if json.Unmarshal(v, &rtkCfg) == nil {
			if cfg.RTK == nil {
				cfg.RTK = &rtkCfg
			} else {
				// Merge scalar fields only when explicitly set in the file.
				// Unmarshal into a raw map to detect which keys were present.
				var rtkRaw map[string]json.RawMessage
				if json.Unmarshal(v, &rtkRaw) == nil {
					if _, ok := rtkRaw["enabled"]; ok {
						cfg.RTK.Enabled = rtkCfg.Enabled
					}
					if _, ok := rtkRaw["metrics_enabled"]; ok {
						cfg.RTK.MetricsEnabled = rtkCfg.MetricsEnabled
					}
					if _, ok := rtkRaw["export_path"]; ok {
						cfg.RTK.ExportPath = rtkCfg.ExportPath
					}

					// Merge maps per-key
					if rtkCfg.CompressionLevels != nil {
						if cfg.RTK.CompressionLevels == nil {
							cfg.RTK.CompressionLevels = make(map[string]string)
						}
						for k, v := range rtkCfg.CompressionLevels {
							cfg.RTK.CompressionLevels[k] = v
						}
					}
					if rtkCfg.EnabledCategories != nil {
						if cfg.RTK.EnabledCategories == nil {
							cfg.RTK.EnabledCategories = make(map[string]bool)
						}
						for k, v := range rtkCfg.EnabledCategories {
							cfg.RTK.EnabledCategories[k] = v
						}
					}
				}
			}
		}
	}

	// MCP servers
	if v, ok := raw["mcpServers"]; ok {
		var servers map[string]*MCPServerConfig
		if json.Unmarshal(v, &servers) == nil && servers != nil {
			if cfg.MCPServers == nil {
				cfg.MCPServers = make(map[string]*MCPServerConfig)
			}
			for name, srv := range servers {
				if srv == nil {
					// null value disables a server from a higher tier
					delete(cfg.MCPServers, name)
				} else {
					cfg.MCPServers[name] = srv
				}
			}
		}
	}

	// AllowProjectEnvVars
	if v, ok := raw["allowProjectEnvVars"]; ok {
		var vars []string
		if json.Unmarshal(v, &vars) == nil {
			cfg.AllowProjectEnvVars = vars
		}
	}

	return nil
}
