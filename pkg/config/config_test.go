package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.DefaultProvider != "anthropic" {
		t.Errorf("expected DefaultProvider 'anthropic', got %q", cfg.DefaultProvider)
	}
	if cfg.DefaultModel != "claude-sonnet-4-20250514" {
		t.Errorf("expected DefaultModel 'claude-sonnet-4-20250514', got %q", cfg.DefaultModel)
	}
	if cfg.ThinkingLevel != "off" {
		t.Errorf("expected ThinkingLevel 'off', got %q", cfg.ThinkingLevel)
	}
	if cfg.MaxTokens != 8192 {
		t.Errorf("expected MaxTokens 8192, got %d", cfg.MaxTokens)
	}
	if cfg.SessionDir == "" {
		t.Error("expected non-empty SessionDir")
	}
	if cfg.ConfigDir == "" {
		t.Error("expected non-empty ConfigDir")
	}
}

func TestLoadConfigNoFiles(t *testing.T) {
	// LoadConfig should succeed even when no config files exist — it just
	// returns defaults. We can't easily isolate it from the real home dir,
	// but we can verify it doesn't error.
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.DefaultProvider == "" {
		t.Error("expected non-empty DefaultProvider from defaults")
	}
}

func TestMergeFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Write a partial config file — only override some fields.
	partial := map[string]any{
		"default_model": "gpt-4o",
		"max_tokens":    4096,
	}
	data, _ := json.Marshal(partial)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := DefaultConfig()
	if err := mergeFromFile(cfg, path); err != nil {
		t.Fatalf("mergeFromFile: %v", err)
	}

	// Overridden fields.
	if cfg.DefaultModel != "gpt-4o" {
		t.Errorf("expected DefaultModel 'gpt-4o', got %q", cfg.DefaultModel)
	}
	if cfg.MaxTokens != 4096 {
		t.Errorf("expected MaxTokens 4096, got %d", cfg.MaxTokens)
	}

	// Non-overridden fields should retain defaults.
	if cfg.DefaultProvider != "anthropic" {
		t.Errorf("expected DefaultProvider to remain 'anthropic', got %q", cfg.DefaultProvider)
	}
	if cfg.ThinkingLevel != "off" {
		t.Errorf("expected ThinkingLevel to remain 'off', got %q", cfg.ThinkingLevel)
	}
}

func TestMergeFromFileMissing(t *testing.T) {
	cfg := DefaultConfig()
	err := mergeFromFile(cfg, "/nonexistent/path/settings.json")
	if err != nil {
		t.Errorf("mergeFromFile should return nil for missing files, got: %v", err)
	}
}

func TestMergeFromFileInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := DefaultConfig()
	err := mergeFromFile(cfg, path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestMergeOnlySetFieldsOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Write a file with only thinking_level set.
	data := []byte(`{"thinking_level": "high"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := DefaultConfig()
	originalModel := cfg.DefaultModel
	originalProvider := cfg.DefaultProvider
	originalMaxTokens := cfg.MaxTokens

	if err := mergeFromFile(cfg, path); err != nil {
		t.Fatalf("mergeFromFile: %v", err)
	}

	if cfg.ThinkingLevel != "high" {
		t.Errorf("expected ThinkingLevel 'high', got %q", cfg.ThinkingLevel)
	}
	if cfg.DefaultModel != originalModel {
		t.Errorf("DefaultModel should not change: got %q, want %q", cfg.DefaultModel, originalModel)
	}
	if cfg.DefaultProvider != originalProvider {
		t.Errorf("DefaultProvider should not change: got %q, want %q", cfg.DefaultProvider, originalProvider)
	}
	if cfg.MaxTokens != originalMaxTokens {
		t.Errorf("MaxTokens should not change: got %d, want %d", cfg.MaxTokens, originalMaxTokens)
	}
}

func TestSaveAndReload(t *testing.T) {
	dir := t.TempDir()

	cfg := DefaultConfig()
	cfg.ConfigDir = dir
	cfg.DefaultModel = "custom-model"
	cfg.MaxTokens = 2048
	cfg.ThinkingLevel = "medium"

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify the file exists.
	path := filepath.Join(dir, "settings.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("settings.json not found after Save: %v", err)
	}

	// Reload into a fresh config (starting from defaults).
	cfg2 := DefaultConfig()
	cfg2.ConfigDir = dir
	if err := mergeFromFile(cfg2, path); err != nil {
		t.Fatalf("mergeFromFile on saved file: %v", err)
	}

	if cfg2.DefaultModel != "custom-model" {
		t.Errorf("expected DefaultModel 'custom-model', got %q", cfg2.DefaultModel)
	}
	if cfg2.MaxTokens != 2048 {
		t.Errorf("expected MaxTokens 2048, got %d", cfg2.MaxTokens)
	}
	if cfg2.ThinkingLevel != "medium" {
		t.Errorf("expected ThinkingLevel 'medium', got %q", cfg2.ThinkingLevel)
	}
}

func TestSaveCreatesConfigDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "config")

	cfg := DefaultConfig()
	cfg.ConfigDir = dir

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	path := filepath.Join(dir, "settings.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestMergeEmptyStringDoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// An empty string value should not override the default.
	data := []byte(`{"default_provider": ""}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := DefaultConfig()
	if err := mergeFromFile(cfg, path); err != nil {
		t.Fatalf("mergeFromFile: %v", err)
	}

	if cfg.DefaultProvider != "anthropic" {
		t.Errorf("empty string should not override default, got %q", cfg.DefaultProvider)
	}
}

func TestMergeZeroMaxTokensDoesNotOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	data := []byte(`{"max_tokens": 0}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := DefaultConfig()
	if err := mergeFromFile(cfg, path); err != nil {
		t.Fatalf("mergeFromFile: %v", err)
	}

	if cfg.MaxTokens != 8192 {
		t.Errorf("zero max_tokens should not override default, got %d", cfg.MaxTokens)
	}
}
