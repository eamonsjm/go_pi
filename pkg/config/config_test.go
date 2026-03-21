package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func mustDefaultConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig: %v", err)
	}
	return cfg
}

func TestDefaultConfig(t *testing.T) {
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig: %v", err)
	}

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

	cfg := mustDefaultConfig(t)
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
	cfg := mustDefaultConfig(t)
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

	cfg := mustDefaultConfig(t)
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

	cfg := mustDefaultConfig(t)
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

	cfg := mustDefaultConfig(t)
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
	cfg2 := mustDefaultConfig(t)
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

	cfg := mustDefaultConfig(t)
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

	cfg := mustDefaultConfig(t)
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

	cfg := mustDefaultConfig(t)
	if err := mergeFromFile(cfg, path); err != nil {
		t.Fatalf("mergeFromFile: %v", err)
	}

	if cfg.MaxTokens != 8192 {
		t.Errorf("zero max_tokens should not override default, got %d", cfg.MaxTokens)
	}
}

func TestMergeFromFileTrailingComma(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Trailing comma is invalid JSON.
	data := []byte(`{"default_model": "gpt-4o",}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := mustDefaultConfig(t)
	err := mergeFromFile(cfg, path)
	if err == nil {
		t.Error("expected error for JSON with trailing comma")
	}
}

func TestMergeFromFileEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// 0-byte file.
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := mustDefaultConfig(t)
	err := mergeFromFile(cfg, path)
	if err == nil {
		t.Error("expected error for empty config file")
	}
}

func TestMergeFromFileAllZeroValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Every field set to its zero value — none should overwrite defaults.
	data := []byte(`{
		"default_provider": "",
		"default_model": "",
		"thinking_level": "",
		"max_tokens": 0,
		"session_dir": "",
		"config_dir": ""
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := mustDefaultConfig(t)
	defaults := mustDefaultConfig(t)

	if err := mergeFromFile(cfg, path); err != nil {
		t.Fatalf("mergeFromFile: %v", err)
	}

	if cfg.DefaultProvider != defaults.DefaultProvider {
		t.Errorf("DefaultProvider changed to %q, want %q", cfg.DefaultProvider, defaults.DefaultProvider)
	}
	if cfg.DefaultModel != defaults.DefaultModel {
		t.Errorf("DefaultModel changed to %q, want %q", cfg.DefaultModel, defaults.DefaultModel)
	}
	if cfg.ThinkingLevel != defaults.ThinkingLevel {
		t.Errorf("ThinkingLevel changed to %q, want %q", cfg.ThinkingLevel, defaults.ThinkingLevel)
	}
	if cfg.MaxTokens != defaults.MaxTokens {
		t.Errorf("MaxTokens changed to %d, want %d", cfg.MaxTokens, defaults.MaxTokens)
	}
	if cfg.SessionDir != defaults.SessionDir {
		t.Errorf("SessionDir changed to %q, want %q", cfg.SessionDir, defaults.SessionDir)
	}
	if cfg.ConfigDir != defaults.ConfigDir {
		t.Errorf("ConfigDir changed to %q, want %q", cfg.ConfigDir, defaults.ConfigDir)
	}
}

func TestMergeFromFileNegativeMaxTokens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	data := []byte(`{"max_tokens": -100}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := mustDefaultConfig(t)
	if err := mergeFromFile(cfg, path); err != nil {
		t.Fatalf("mergeFromFile: %v", err)
	}

	if cfg.MaxTokens != 8192 {
		t.Errorf("negative max_tokens should not override default, got %d", cfg.MaxTokens)
	}
}

func TestMergeFromFileUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Unknown top-level keys and nested objects should be silently ignored.
	data := []byte(`{
		"default_model": "gpt-4o",
		"unknown_field": "should be ignored",
		"nested": {"deep": {"key": "value"}},
		"extra_list": [1, 2, 3]
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := mustDefaultConfig(t)
	if err := mergeFromFile(cfg, path); err != nil {
		t.Fatalf("mergeFromFile: %v", err)
	}

	// Known field should still be applied.
	if cfg.DefaultModel != "gpt-4o" {
		t.Errorf("expected DefaultModel 'gpt-4o', got %q", cfg.DefaultModel)
	}

	// Other defaults should be untouched.
	if cfg.DefaultProvider != "anthropic" {
		t.Errorf("DefaultProvider changed unexpectedly to %q", cfg.DefaultProvider)
	}
}

func TestMergeFromFilePermissionError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("skipping permission test when running as root")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	if err := os.WriteFile(path, []byte(`{"default_model": "x"}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Remove read permission.
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(path, 0o600) })

	cfg := mustDefaultConfig(t)
	err := mergeFromFile(cfg, path)
	if err == nil {
		t.Error("expected error for unreadable config file")
	}
}

func TestMergeFromFileWrongType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// max_tokens as a string instead of int — should not crash, field stays default.
	data := []byte(`{"max_tokens": "not a number", "default_model": "good-model"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := mustDefaultConfig(t)
	if err := mergeFromFile(cfg, path); err != nil {
		t.Fatalf("mergeFromFile: %v", err)
	}

	// Wrong-type field should be silently skipped (unmarshal into int fails).
	if cfg.MaxTokens != 8192 {
		t.Errorf("max_tokens with wrong type should not override default, got %d", cfg.MaxTokens)
	}
	// Correct-type field should still apply.
	if cfg.DefaultModel != "good-model" {
		t.Errorf("expected DefaultModel 'good-model', got %q", cfg.DefaultModel)
	}
}

func TestMergeFromFileNullValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

	// Explicit null values should not overwrite defaults.
	data := []byte(`{"default_provider": null, "max_tokens": null}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := mustDefaultConfig(t)
	if err := mergeFromFile(cfg, path); err != nil {
		t.Fatalf("mergeFromFile: %v", err)
	}

	if cfg.DefaultProvider != "anthropic" {
		t.Errorf("null should not override default_provider, got %q", cfg.DefaultProvider)
	}
	if cfg.MaxTokens != 8192 {
		t.Errorf("null should not override max_tokens, got %d", cfg.MaxTokens)
	}
}
