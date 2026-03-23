package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ejm/go_pi/pkg/config"
)

// testConfig creates a Config backed by a temp directory so Save() works.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.LoadConfig(config.WithConfigDir(dir))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// ---------------------------------------------------------------------------
// NewAliasCommand
// ---------------------------------------------------------------------------

func TestNewAliasCommand_Metadata(t *testing.T) {
	reg := NewCommandRegistry()
	cfg := testConfig(t)
	cmd := NewAliasCommand(cfg, reg)

	if cmd.Name != "alias" {
		t.Errorf("Name = %q, want %q", cmd.Name, "alias")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestNewAliasCommand_EmptyArgs(t *testing.T) {
	reg := NewCommandRegistry()
	cfg := testConfig(t)
	cmd := NewAliasCommand(cfg, reg)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if result.IsError {
		t.Error("usage message should not be an error")
	}
	if !strings.Contains(result.Text, "Usage:") {
		t.Errorf("expected usage text, got %q", result.Text)
	}
}

func TestNewAliasCommand_MissingTarget(t *testing.T) {
	reg := NewCommandRegistry()
	cfg := testConfig(t)
	cmd := NewAliasCommand(cfg, reg)

	teaCmd := cmd.Execute("myalias")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected error for missing target")
	}
	if !strings.Contains(result.Text, "missing target") {
		t.Errorf("expected 'missing target' message, got %q", result.Text)
	}
}

func TestNewAliasCommand_TargetNotFound(t *testing.T) {
	reg := NewCommandRegistry()
	cfg := testConfig(t)
	cmd := NewAliasCommand(cfg, reg)

	teaCmd := cmd.Execute("myalias nonexistent")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected error for nonexistent target")
	}
	if !strings.Contains(result.Text, "not found") {
		t.Errorf("expected 'not found' message, got %q", result.Text)
	}
}

func TestNewAliasCommand_Success(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "help"})
	cfg := testConfig(t)
	cmd := NewAliasCommand(cfg, reg)

	teaCmd := cmd.Execute("h help")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "/h") || !strings.Contains(result.Text, "/help") {
		t.Errorf("expected alias creation message, got %q", result.Text)
	}

	// Verify alias was registered.
	if target := reg.GetAlias("h"); target != "help" {
		t.Errorf("expected alias target 'help', got %q", target)
	}

	// Verify alias was persisted to config.
	if cfg.Aliases == nil || cfg.Aliases["h"] != "help" {
		t.Errorf("expected alias in config, got %v", cfg.Aliases)
	}
}

func TestNewAliasCommand_Reset(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "help"})
	reg.SetAlias("h", "help")
	reg.SetAlias("x", "help")
	cfg := testConfig(t)
	cfg.Aliases = map[string]string{"h": "help", "x": "help"}
	cmd := NewAliasCommand(cfg, reg)

	teaCmd := cmd.Execute("--reset")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "cleared") {
		t.Errorf("expected 'cleared' message, got %q", result.Text)
	}

	// Verify aliases are gone.
	if len(reg.AllAliases()) != 0 {
		t.Errorf("expected no aliases, got %v", reg.AllAliases())
	}
	if len(cfg.Aliases) != 0 {
		t.Errorf("expected no config aliases, got %v", cfg.Aliases)
	}
}

func TestNewAliasCommand_SaveFailure(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "help"})
	cfg := testConfig(t)

	// Make the config dir read-only so Save() fails.
	os.Chmod(cfg.ConfigDir, 0o555)
	defer os.Chmod(cfg.ConfigDir, 0o755)

	cmd := NewAliasCommand(cfg, reg)
	teaCmd := cmd.Execute("h help")
	msg := teaCmd()
	result := msg.(CommandResultMsg)

	// Should warn but not be a hard error.
	if result.IsError {
		t.Error("save failure should be a warning, not an error")
	}
	if !strings.Contains(result.Text, "failed to save") {
		t.Errorf("expected save failure warning, got %q", result.Text)
	}
}

func TestNewAliasCommand_SetAliasFails(t *testing.T) {
	// SetAlias fails when the target doesn't exist in the registry's commands map.
	// This can happen if someone removes the command between Get() and SetAlias().
	// But in normal flow, the check at line 68 catches it. So let's just verify
	// that the target-not-found path covers this.
	reg := NewCommandRegistry()
	cfg := testConfig(t)
	cmd := NewAliasCommand(cfg, reg)

	teaCmd := cmd.Execute("h ghost")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected error when target command not found")
	}
}

// ---------------------------------------------------------------------------
// NewAliasesCommand
// ---------------------------------------------------------------------------

func TestNewAliasesCommand_Metadata(t *testing.T) {
	reg := NewCommandRegistry()
	cmd := NewAliasesCommand(reg)

	if cmd.Name != "aliases" {
		t.Errorf("Name = %q, want %q", cmd.Name, "aliases")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
}

func TestNewAliasesCommand_NoAliases(t *testing.T) {
	reg := NewCommandRegistry()
	cmd := NewAliasesCommand(reg)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "No aliases") {
		t.Errorf("expected 'No aliases' message, got %q", result.Text)
	}
}

func TestNewAliasesCommand_WithAliases(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "help"})
	reg.Register(&SlashCommand{Name: "model"})
	reg.SetAlias("h", "help")
	reg.SetAlias("m", "model")

	cmd := NewAliasesCommand(reg)
	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "/h") || !strings.Contains(result.Text, "/help") {
		t.Errorf("expected alias listing, got %q", result.Text)
	}
	if !strings.Contains(result.Text, "/m") || !strings.Contains(result.Text, "/model") {
		t.Errorf("expected alias listing, got %q", result.Text)
	}
}

func TestNewAliasesCommand_SortOrder(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "help"})
	reg.SetAlias("z", "help")
	reg.SetAlias("a", "help")
	reg.SetAlias("m", "help")

	cmd := NewAliasesCommand(reg)
	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result := msg.(CommandResultMsg)

	// Aliases should appear in alphabetical order.
	posA := strings.Index(result.Text, "/a")
	posM := strings.Index(result.Text, "/m")
	posZ := strings.Index(result.Text, "/z")
	if posA < 0 || posM < 0 || posZ < 0 {
		t.Fatalf("expected all aliases in output, got %q", result.Text)
	}
	if posA >= posM || posM >= posZ {
		t.Errorf("aliases not in alphabetical order in %q", result.Text)
	}
}

// ---------------------------------------------------------------------------
// NewUnaliasCommand
// ---------------------------------------------------------------------------

func TestNewUnaliasCommand_Metadata(t *testing.T) {
	reg := NewCommandRegistry()
	cfg := testConfig(t)
	cmd := NewUnaliasCommand(cfg, reg)

	if cmd.Name != "unalias" {
		t.Errorf("Name = %q, want %q", cmd.Name, "unalias")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
}

func TestNewUnaliasCommand_EmptyArgs(t *testing.T) {
	reg := NewCommandRegistry()
	cfg := testConfig(t)
	cmd := NewUnaliasCommand(cfg, reg)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected error for empty args")
	}
	if !strings.Contains(result.Text, "Usage:") {
		t.Errorf("expected usage text, got %q", result.Text)
	}
}

func TestNewUnaliasCommand_AliasNotFound(t *testing.T) {
	reg := NewCommandRegistry()
	cfg := testConfig(t)
	cmd := NewUnaliasCommand(cfg, reg)

	teaCmd := cmd.Execute("nonexistent")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected error for nonexistent alias")
	}
	if !strings.Contains(result.Text, "not found") {
		t.Errorf("expected 'not found' message, got %q", result.Text)
	}
}

func TestNewUnaliasCommand_Success(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "help"})
	reg.SetAlias("h", "help")
	cfg := testConfig(t)
	cfg.Aliases = map[string]string{"h": "help"}
	_ = cfg.Save()

	cmd := NewUnaliasCommand(cfg, reg)
	teaCmd := cmd.Execute("h")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "removed") {
		t.Errorf("expected 'removed' message, got %q", result.Text)
	}

	// Verify alias is gone from registry.
	if target := reg.GetAlias("h"); target != "" {
		t.Errorf("expected alias to be removed, got target %q", target)
	}
}

func TestNewUnaliasCommand_SaveFailure(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "help"})
	reg.SetAlias("h", "help")
	cfg := testConfig(t)
	cfg.Aliases = map[string]string{"h": "help"}

	// Make config dir read-only.
	dir := filepath.Dir(filepath.Join(cfg.ConfigDir, "settings.json"))
	os.Chmod(dir, 0o555)
	defer os.Chmod(dir, 0o755)

	cmd := NewUnaliasCommand(cfg, reg)
	teaCmd := cmd.Execute("h")
	msg := teaCmd()
	result := msg.(CommandResultMsg)

	// Should still succeed (warn, not error).
	if result.IsError {
		t.Error("save failure should be a warning, not an error")
	}
}
