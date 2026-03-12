package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/config"
	"github.com/ejm/go_pi/pkg/tools"
)

func newTestSettingsEnv(t *testing.T) (*config.Config, *agent.AgentLoop, *Header) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		DefaultProvider: "anthropic",
		DefaultModel:    "claude-sonnet-4-20250514",
		ThinkingLevel:   "off",
		MaxTokens:       8192,
		SessionDir:      "/tmp/sessions",
		ConfigDir:       dir,
	}
	provider := &tuiMockProvider{streamFn: tuiTextResponse("ok")}
	reg := tools.NewRegistry()
	a := agent.NewAgentLoop(provider, reg)
	h := NewHeader()
	return cfg, a, h
}

func TestNewSettingsCommand_Metadata(t *testing.T) {
	cfg, a, h := newTestSettingsEnv(t)
	cmd := NewSettingsCommand(cfg, a, h)

	if cmd.Name != "settings" {
		t.Errorf("Name = %q, want %q", cmd.Name, "settings")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestSettingsDisplay_NoArgs(t *testing.T) {
	cfg, a, h := newTestSettingsEnv(t)
	cmd := NewSettingsCommand(cfg, a, h)

	teaCmd := cmd.Execute("")
	msg := teaCmd()

	display, ok := msg.(settingsDisplayMsg)
	if !ok {
		t.Fatalf("expected settingsDisplayMsg, got %T", msg)
	}
	if display.text == "" {
		t.Error("expected non-empty display text")
	}
}

func TestSettingsUpdate_Thinking_Valid(t *testing.T) {
	levels := []string{"off", "low", "medium", "high"}
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			cfg, a, h := newTestSettingsEnv(t)
			cmd := NewSettingsCommand(cfg, a, h)

			teaCmd := cmd.Execute("thinking " + level)
			msg := teaCmd()

			updated, ok := msg.(settingsUpdatedMsg)
			if !ok {
				t.Fatalf("expected settingsUpdatedMsg, got %T: %v", msg, msg)
			}
			if updated.key != "thinking" || updated.value != level {
				t.Errorf("got key=%q value=%q, want thinking/%s", updated.key, updated.value, level)
			}
			if cfg.ThinkingLevel != level {
				t.Errorf("cfg.ThinkingLevel = %q, want %q", cfg.ThinkingLevel, level)
			}
		})
	}
}

func TestSettingsUpdate_Thinking_Invalid(t *testing.T) {
	cfg, a, h := newTestSettingsEnv(t)
	cmd := NewSettingsCommand(cfg, a, h)

	teaCmd := cmd.Execute("thinking extreme")
	msg := teaCmd()

	errMsg, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if !errMsg.IsError {
		t.Error("expected IsError=true")
	}
}

func TestSettingsUpdate_Model(t *testing.T) {
	cfg, a, h := newTestSettingsEnv(t)
	cmd := NewSettingsCommand(cfg, a, h)

	teaCmd := cmd.Execute("model claude-opus-4-20250514")
	msg := teaCmd()

	updated, ok := msg.(settingsUpdatedMsg)
	if !ok {
		t.Fatalf("expected settingsUpdatedMsg, got %T: %v", msg, msg)
	}
	if updated.value != "claude-opus-4-20250514" {
		t.Errorf("value = %q, want claude-opus-4-20250514", updated.value)
	}
	if cfg.DefaultModel != "claude-opus-4-20250514" {
		t.Errorf("cfg.DefaultModel = %q, want claude-opus-4-20250514", cfg.DefaultModel)
	}
}

func TestSettingsUpdate_MaxTokens_Valid(t *testing.T) {
	cfg, a, h := newTestSettingsEnv(t)
	cmd := NewSettingsCommand(cfg, a, h)

	teaCmd := cmd.Execute("max_tokens 16384")
	msg := teaCmd()

	updated, ok := msg.(settingsUpdatedMsg)
	if !ok {
		t.Fatalf("expected settingsUpdatedMsg, got %T: %v", msg, msg)
	}
	if updated.value != "16384" {
		t.Errorf("value = %q, want 16384", updated.value)
	}
	if cfg.MaxTokens != 16384 {
		t.Errorf("cfg.MaxTokens = %d, want 16384", cfg.MaxTokens)
	}
}

func TestSettingsUpdate_MaxTokens_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"not a number", "max_tokens abc"},
		{"zero", "max_tokens 0"},
		{"negative", "max_tokens -100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, a, h := newTestSettingsEnv(t)
			cmd := NewSettingsCommand(cfg, a, h)

			teaCmd := cmd.Execute(tt.value)
			msg := teaCmd()

			errMsg, ok := msg.(CommandResultMsg)
			if !ok {
				t.Fatalf("expected CommandResultMsg, got %T", msg)
			}
			if !errMsg.IsError {
				t.Error("expected IsError=true")
			}
		})
	}
}

func TestSettingsUpdate_Provider_Valid_RequiresRestart(t *testing.T) {
	providers := []string{"anthropic", "openrouter", "openai"}
	for _, p := range providers {
		t.Run(p, func(t *testing.T) {
			cfg, a, h := newTestSettingsEnv(t)
			cmd := NewSettingsCommand(cfg, a, h)

			teaCmd := cmd.Execute("provider " + p)
			msg := teaCmd()

			// Valid providers still return an error message because they require restart.
			errMsg, ok := msg.(CommandResultMsg)
			if !ok {
				t.Fatalf("expected CommandResultMsg, got %T", msg)
			}
			if !errMsg.IsError {
				t.Error("expected IsError=true (provider change requires restart)")
			}
		})
	}
}

func TestSettingsUpdate_Provider_Invalid(t *testing.T) {
	cfg, a, h := newTestSettingsEnv(t)
	cmd := NewSettingsCommand(cfg, a, h)

	teaCmd := cmd.Execute("provider azure")
	msg := teaCmd()

	errMsg, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if !errMsg.IsError {
		t.Error("expected IsError=true")
	}
}

func TestSettingsUpdate_UnknownKey(t *testing.T) {
	cfg, a, h := newTestSettingsEnv(t)
	cmd := NewSettingsCommand(cfg, a, h)

	teaCmd := cmd.Execute("nonexistent_key value")
	msg := teaCmd()

	errMsg, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if !errMsg.IsError {
		t.Error("expected IsError=true for unknown key")
	}
}

func TestSettingsUpdate_MissingValue(t *testing.T) {
	cfg, a, h := newTestSettingsEnv(t)
	cmd := NewSettingsCommand(cfg, a, h)

	teaCmd := cmd.Execute("thinking")
	msg := teaCmd()

	errMsg, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if !errMsg.IsError {
		t.Error("expected IsError=true for missing value")
	}
}

func TestSettingsUpdate_SavesPersistently(t *testing.T) {
	cfg, a, h := newTestSettingsEnv(t)
	cmd := NewSettingsCommand(cfg, a, h)

	teaCmd := cmd.Execute("model test-model")
	teaCmd() // Execute to trigger Save()

	// Check that settings.json was actually written.
	settingsPath := filepath.Join(cfg.ConfigDir, "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("failed to read saved settings: %v", err)
	}
	if len(data) == 0 {
		t.Error("saved settings file is empty")
	}
}

func TestSettingsUpdate_WhitespaceHandling(t *testing.T) {
	cfg, a, h := newTestSettingsEnv(t)
	cmd := NewSettingsCommand(cfg, a, h)

	// Leading/trailing whitespace should be trimmed.
	teaCmd := cmd.Execute("  model   test-model  ")
	msg := teaCmd()

	updated, ok := msg.(settingsUpdatedMsg)
	if !ok {
		t.Fatalf("expected settingsUpdatedMsg, got %T: %v", msg, msg)
	}
	if updated.value != "test-model" {
		t.Errorf("value = %q, want %q", updated.value, "test-model")
	}
}

func TestSettingsDisplay_ShowsCurrentValues(t *testing.T) {
	cfg, a, h := newTestSettingsEnv(t)
	cfg.DefaultProvider = "openai"
	cfg.DefaultModel = "gpt-4"
	cfg.ThinkingLevel = "high"
	cfg.MaxTokens = 4096

	cmd := NewSettingsCommand(cfg, a, h)
	teaCmd := cmd.Execute("")
	msg := teaCmd()

	display := msg.(settingsDisplayMsg)
	if display.text == "" {
		t.Fatal("display text should not be empty")
	}

	// Check that current values appear in the display.
	checks := []string{"openai", "gpt-4", "high", "4096"}
	for _, want := range checks {
		found := false
		if len(display.text) > 0 {
			for _, line := range []string{display.text} {
				if contains(line, want) {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("display text should contain %q", want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
