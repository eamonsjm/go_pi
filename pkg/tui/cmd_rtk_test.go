package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/config"
	"github.com/ejm/go_pi/pkg/tools"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestRTKConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	return &config.Config{
		ConfigDir: dir,
		RTK: &config.RTKConfig{
			Enabled:        true,
			MetricsEnabled: true,
			ExportPath:     filepath.Join(dir, "metrics.json"),
			CompressionLevels: map[string]string{
				"go-test":  "medium",
				"go-build": "low",
				"git-log":  "high",
				"linter":   "medium",
				"generic":  "low",
			},
			EnabledCategories: map[string]bool{
				"git":     true,
				"docker":  false,
				"build":   true,
				"package": true,
				"test":    true,
				"file":    false,
				"other":   true,
			},
		},
	}
}

func metricsWithData() *tools.Metrics {
	m := tools.NewMetrics()
	m.Record(tools.CategoryGit, 1000, 400, 50*time.Millisecond)
	m.Record(tools.CategoryBuild, 2000, 800, 100*time.Millisecond)
	return m
}

// ---------------------------------------------------------------------------
// isValidCategory
// ---------------------------------------------------------------------------

func TestIsValidCategory(t *testing.T) {
	valid := []string{"git", "docker", "build", "package", "test", "file", "other"}
	for _, c := range valid {
		if !isValidCategory(c) {
			t.Errorf("expected %q to be valid", c)
		}
	}

	invalid := []string{"", "GIT", "Git", "unknown", "network", " git"}
	for _, c := range invalid {
		if isValidCategory(c) {
			t.Errorf("expected %q to be invalid", c)
		}
	}
}

// ---------------------------------------------------------------------------
// NewRTKCommand
// ---------------------------------------------------------------------------

func TestNewRTKCommand_Metadata(t *testing.T) {
	cfg := newTestRTKConfig(t)
	m := tools.NewMetrics()
	cmd := NewRTKCommand(cfg, m)

	if cmd.Name != "rtk" {
		t.Errorf("Name = %q, want %q", cmd.Name, "rtk")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestNewRTKCommand_DefaultsToStatus(t *testing.T) {
	cfg := newTestRTKConfig(t)
	m := tools.NewMetrics()
	cmd := NewRTKCommand(cfg, m)

	teaCmd := cmd.Execute("")
	msg := teaCmd()

	display, ok := msg.(rtkDisplayMsg)
	if !ok {
		t.Fatalf("expected rtkDisplayMsg, got %T", msg)
	}
	if !strings.Contains(display.text, "RTK Status:") {
		t.Errorf("expected status output, got %q", display.text)
	}
}

func TestNewRTKCommand_UnknownSubcommand(t *testing.T) {
	cfg := newTestRTKConfig(t)
	m := tools.NewMetrics()
	cmd := NewRTKCommand(cfg, m)

	teaCmd := cmd.Execute("frobnicate")
	msg := teaCmd()

	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
	if !strings.Contains(result.Text, "unknown subcommand") {
		t.Errorf("expected 'unknown subcommand' in error, got %q", result.Text)
	}
}

// ---------------------------------------------------------------------------
// rtkStatus
// ---------------------------------------------------------------------------

func TestRtkStatus_NilRTK(t *testing.T) {
	cfg := &config.Config{}
	m := tools.NewMetrics()

	teaCmd := rtkStatus(cfg, m)
	msg := teaCmd()

	display, ok := msg.(rtkDisplayMsg)
	if !ok {
		t.Fatalf("expected rtkDisplayMsg, got %T", msg)
	}
	if display.text != "RTK is not configured" {
		t.Errorf("expected 'RTK is not configured', got %q", display.text)
	}
}

func TestRtkStatus_EnabledWithMetrics(t *testing.T) {
	cfg := newTestRTKConfig(t)
	m := metricsWithData()

	teaCmd := rtkStatus(cfg, m)
	msg := teaCmd()

	display, ok := msg.(rtkDisplayMsg)
	if !ok {
		t.Fatalf("expected rtkDisplayMsg, got %T", msg)
	}

	for _, want := range []string{
		"enabled",
		"RTK Status:",
		"Total Tokens:",
		"Saved Tokens:",
		"Active Compressors:",
		"Enabled Categories:",
		"go-test",
		"Commands:",
	} {
		if !strings.Contains(display.text, want) {
			t.Errorf("status output missing %q", want)
		}
	}
}

func TestRtkStatus_Disabled(t *testing.T) {
	cfg := newTestRTKConfig(t)
	cfg.RTK.Enabled = false
	cfg.RTK.MetricsEnabled = false
	m := tools.NewMetrics()

	teaCmd := rtkStatus(cfg, m)
	msg := teaCmd()

	display := msg.(rtkDisplayMsg)
	if !strings.Contains(display.text, "disabled") {
		t.Errorf("expected 'disabled' in output, got %q", display.text)
	}
}

func TestRtkStatus_ZeroTokens(t *testing.T) {
	cfg := newTestRTKConfig(t)
	m := tools.NewMetrics()

	teaCmd := rtkStatus(cfg, m)
	msg := teaCmd()

	display := msg.(rtkDisplayMsg)
	// Compression ratio should be 0.0% when no tokens recorded
	if !strings.Contains(display.text, "0.0%") {
		t.Errorf("expected 0.0%% compression with zero tokens, got %q", display.text)
	}
}

// ---------------------------------------------------------------------------
// rtkMetrics
// ---------------------------------------------------------------------------

func TestRtkMetrics_Empty(t *testing.T) {
	m := tools.NewMetrics()

	teaCmd := rtkMetrics(m)
	msg := teaCmd()

	display, ok := msg.(rtkDisplayMsg)
	if !ok {
		t.Fatalf("expected rtkDisplayMsg, got %T", msg)
	}
	if !strings.Contains(display.text, "No metrics recorded yet") {
		t.Errorf("expected empty metrics message, got %q", display.text)
	}
}

func TestRtkMetrics_WithData(t *testing.T) {
	m := metricsWithData()

	teaCmd := rtkMetrics(m)
	msg := teaCmd()

	display := msg.(rtkDisplayMsg)

	for _, want := range []string{
		"RTK Metrics by Category:",
		"git:",
		"build:",
		"Commands:",
		"Total Bytes:",
		"Compressed:",
		"reduction",
		"Total tokens saved:",
	} {
		if !strings.Contains(display.text, want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// rtkShowConfig
// ---------------------------------------------------------------------------

func TestRtkShowConfig_NilRTK(t *testing.T) {
	cfg := &config.Config{}

	teaCmd := rtkShowConfig(cfg)
	msg := teaCmd()

	display := msg.(rtkDisplayMsg)
	if display.text != "RTK is not configured" {
		t.Errorf("expected 'RTK is not configured', got %q", display.text)
	}
}

func TestRtkShowConfig_WithConfig(t *testing.T) {
	cfg := newTestRTKConfig(t)

	teaCmd := rtkShowConfig(cfg)
	msg := teaCmd()

	display := msg.(rtkDisplayMsg)

	for _, want := range []string{
		"RTK Configuration:",
		"enabled: true",
		"metrics_enabled: true",
		"compression_levels:",
		"enabled_categories:",
	} {
		if !strings.Contains(display.text, want) {
			t.Errorf("config output missing %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// rtkEnableCategory / rtkDisableCategory
// ---------------------------------------------------------------------------

func TestRtkEnableCategory_Valid(t *testing.T) {
	cfg := newTestRTKConfig(t)
	cfg.RTK.EnabledCategories["docker"] = false

	teaCmd := rtkEnableCategory("docker", cfg)
	msg := teaCmd()

	updated, ok := msg.(rtkUpdatedMsg)
	if !ok {
		t.Fatalf("expected rtkUpdatedMsg, got %T", msg)
	}
	if updated.setting != "enable_docker" || updated.value != "true" {
		t.Errorf("got setting=%q value=%q, want enable_docker/true", updated.setting, updated.value)
	}
	if !cfg.RTK.EnabledCategories["docker"] {
		t.Error("expected docker to be enabled in config")
	}
}

func TestRtkEnableCategory_InvalidCategory(t *testing.T) {
	cfg := newTestRTKConfig(t)

	teaCmd := rtkEnableCategory("invalid", cfg)
	msg := teaCmd()

	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
	if !strings.Contains(result.Text, "invalid category") {
		t.Errorf("expected 'invalid category' error, got %q", result.Text)
	}
}

func TestRtkEnableCategory_NilRTK(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	teaCmd := rtkEnableCategory("git", cfg)
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected IsError=true for nil RTK")
	}
	if !strings.Contains(result.Text, "RTK not configured") {
		t.Errorf("expected 'RTK not configured', got %q", result.Text)
	}
}

func TestRtkEnableCategory_NilEnabledCategories(t *testing.T) {
	cfg := newTestRTKConfig(t)
	cfg.RTK.EnabledCategories = nil

	teaCmd := rtkEnableCategory("git", cfg)
	msg := teaCmd()

	_, ok := msg.(rtkUpdatedMsg)
	if !ok {
		t.Fatalf("expected rtkUpdatedMsg, got %T", msg)
	}
	if !cfg.RTK.EnabledCategories["git"] {
		t.Error("expected git to be enabled after init")
	}
}

func TestRtkDisableCategory_Valid(t *testing.T) {
	cfg := newTestRTKConfig(t)
	cfg.RTK.EnabledCategories["git"] = true

	teaCmd := rtkDisableCategory("git", cfg)
	msg := teaCmd()

	updated, ok := msg.(rtkUpdatedMsg)
	if !ok {
		t.Fatalf("expected rtkUpdatedMsg, got %T", msg)
	}
	if updated.setting != "disable_git" || updated.value != "true" {
		t.Errorf("got setting=%q value=%q, want disable_git/true", updated.setting, updated.value)
	}
	if cfg.RTK.EnabledCategories["git"] {
		t.Error("expected git to be disabled in config")
	}
}

func TestRtkDisableCategory_InvalidCategory(t *testing.T) {
	cfg := newTestRTKConfig(t)

	teaCmd := rtkDisableCategory("bogus", cfg)
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected IsError=true")
	}
}

func TestRtkDisableCategory_NilRTK(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	teaCmd := rtkDisableCategory("git", cfg)
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if !result.IsError || !strings.Contains(result.Text, "RTK not configured") {
		t.Errorf("expected RTK not configured error, got %q", result.Text)
	}
}

func TestRtkDisableCategory_NilEnabledCategories(t *testing.T) {
	cfg := newTestRTKConfig(t)
	cfg.RTK.EnabledCategories = nil

	teaCmd := rtkDisableCategory("build", cfg)
	msg := teaCmd()

	_, ok := msg.(rtkUpdatedMsg)
	if !ok {
		t.Fatalf("expected rtkUpdatedMsg, got %T", msg)
	}
	if cfg.RTK.EnabledCategories["build"] {
		t.Error("expected build to be false after disable")
	}
}

// ---------------------------------------------------------------------------
// rtkSetCompressionLevel
// ---------------------------------------------------------------------------

func TestRtkSetCompressionLevel_Valid(t *testing.T) {
	tests := []struct {
		compressor string
		level      string
	}{
		{"go-test", "low"},
		{"go-build", "medium"},
		{"git-log", "high"},
		{"linter", "low"},
		{"generic", "high"},
	}
	for _, tt := range tests {
		t.Run(tt.compressor+"_"+tt.level, func(t *testing.T) {
			cfg := newTestRTKConfig(t)

			teaCmd := rtkSetCompressionLevel(tt.compressor, tt.level, cfg)
			msg := teaCmd()

			updated, ok := msg.(rtkUpdatedMsg)
			if !ok {
				t.Fatalf("expected rtkUpdatedMsg, got %T", msg)
			}
			if updated.setting != tt.compressor || updated.value != tt.level {
				t.Errorf("got setting=%q value=%q, want %q/%q", updated.setting, updated.value, tt.compressor, tt.level)
			}
			if cfg.RTK.CompressionLevels[tt.compressor] != tt.level {
				t.Errorf("config not updated: got %q", cfg.RTK.CompressionLevels[tt.compressor])
			}
		})
	}
}

func TestRtkSetCompressionLevel_InvalidLevel(t *testing.T) {
	cfg := newTestRTKConfig(t)

	teaCmd := rtkSetCompressionLevel("go-test", "extreme", cfg)
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected IsError=true for invalid level")
	}
	if !strings.Contains(result.Text, "invalid level") {
		t.Errorf("expected 'invalid level' error, got %q", result.Text)
	}
}

func TestRtkSetCompressionLevel_InvalidCompressor(t *testing.T) {
	cfg := newTestRTKConfig(t)

	teaCmd := rtkSetCompressionLevel("unknown-comp", "low", cfg)
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected IsError=true for invalid compressor")
	}
	if !strings.Contains(result.Text, "invalid compressor") {
		t.Errorf("expected 'invalid compressor' error, got %q", result.Text)
	}
}

func TestRtkSetCompressionLevel_NilRTK(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	teaCmd := rtkSetCompressionLevel("go-test", "low", cfg)
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if !result.IsError || !strings.Contains(result.Text, "RTK not configured") {
		t.Errorf("expected RTK not configured error, got %q", result.Text)
	}
}

func TestRtkSetCompressionLevel_NilCompressionLevels(t *testing.T) {
	cfg := newTestRTKConfig(t)
	cfg.RTK.CompressionLevels = nil

	teaCmd := rtkSetCompressionLevel("go-test", "high", cfg)
	msg := teaCmd()

	_, ok := msg.(rtkUpdatedMsg)
	if !ok {
		t.Fatalf("expected rtkUpdatedMsg, got %T", msg)
	}
	if cfg.RTK.CompressionLevels["go-test"] != "high" {
		t.Errorf("expected go-test=high, got %q", cfg.RTK.CompressionLevels["go-test"])
	}
}

// ---------------------------------------------------------------------------
// rtkToggle
// ---------------------------------------------------------------------------

func TestRtkToggle_EnableToDisable(t *testing.T) {
	cfg := newTestRTKConfig(t)
	cfg.RTK.Enabled = true

	teaCmd := rtkToggle(cfg)
	msg := teaCmd()

	updated, ok := msg.(rtkUpdatedMsg)
	if !ok {
		t.Fatalf("expected rtkUpdatedMsg, got %T", msg)
	}
	if updated.setting != "enabled" || updated.value != "disabled" {
		t.Errorf("got setting=%q value=%q, want enabled/disabled", updated.setting, updated.value)
	}
	if cfg.RTK.Enabled {
		t.Error("expected RTK to be disabled after toggle")
	}
}

func TestRtkToggle_DisableToEnable(t *testing.T) {
	cfg := newTestRTKConfig(t)
	cfg.RTK.Enabled = false

	teaCmd := rtkToggle(cfg)
	msg := teaCmd()

	updated := msg.(rtkUpdatedMsg)
	if updated.value != "enabled" {
		t.Errorf("expected value 'enabled', got %q", updated.value)
	}
	if !cfg.RTK.Enabled {
		t.Error("expected RTK to be enabled after toggle")
	}
}

func TestRtkToggle_NilRTK(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}

	teaCmd := rtkToggle(cfg)
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if !result.IsError || !strings.Contains(result.Text, "RTK not configured") {
		t.Errorf("expected RTK not configured error, got %q", result.Text)
	}
}

// ---------------------------------------------------------------------------
// rtkError
// ---------------------------------------------------------------------------

func TestRtkError(t *testing.T) {
	teaCmd := rtkError("something went wrong")
	msg := teaCmd()

	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
	if result.Text != "something went wrong" {
		t.Errorf("expected error text 'something went wrong', got %q", result.Text)
	}
}

// ---------------------------------------------------------------------------
// rtkExport
// ---------------------------------------------------------------------------

func TestRtkExport_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "export.json")
	m := metricsWithData()

	teaCmd := rtkExport(path, m)
	msg := teaCmd()

	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}
	if !strings.Contains(result.Text, "Metrics exported to") {
		t.Errorf("expected success message, got %q", result.Text)
	}

	// Verify file contents
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read exported file: %v", err)
	}

	var export map[string]interface{}
	if err := json.Unmarshal(data, &export); err != nil {
		t.Fatalf("exported file is not valid JSON: %v", err)
	}
	if _, ok := export["total_tokens"]; !ok {
		t.Error("exported JSON missing 'total_tokens' key")
	}
	if _, ok := export["saved_tokens"]; !ok {
		t.Error("exported JSON missing 'saved_tokens' key")
	}
	if _, ok := export["commands"]; !ok {
		t.Error("exported JSON missing 'commands' key")
	}
}

func TestRtkExport_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "metrics.json")
	m := tools.NewMetrics()

	teaCmd := rtkExport(path, m)
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("expected export file to be created")
	}
}

func TestRtkExport_InvalidPath(t *testing.T) {
	// /dev/null/impossible is not writable on any platform
	m := tools.NewMetrics()

	teaCmd := rtkExport("/dev/null/impossible/file.json", m)
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected error for invalid path")
	}
}

// ---------------------------------------------------------------------------
// NewRTKCommand subcommand routing
// ---------------------------------------------------------------------------

func TestNewRTKCommand_SubcommandRouting(t *testing.T) {
	cfg := newTestRTKConfig(t)
	m := metricsWithData()
	_ = NewRTKCommand(cfg, m) // verify construction doesn't panic

	tests := []struct {
		args    string
		msgType string // "display", "updated", "result"
	}{
		{"status", "display"},
		{"metrics", "display"},
		{"config", "display"},
		{"toggle", "updated"},
	}

	for _, tt := range tests {
		t.Run(tt.args, func(t *testing.T) {
			// Need a fresh config for toggle to avoid state leakage
			freshCfg := newTestRTKConfig(t)
			freshCmd := NewRTKCommand(freshCfg, m)

			teaCmd := freshCmd.Execute(tt.args)
			msg := teaCmd()

			switch tt.msgType {
			case "display":
				if _, ok := msg.(rtkDisplayMsg); !ok {
					t.Errorf("expected rtkDisplayMsg for %q, got %T", tt.args, msg)
				}
			case "updated":
				if _, ok := msg.(rtkUpdatedMsg); !ok {
					t.Errorf("expected rtkUpdatedMsg for %q, got %T", tt.args, msg)
				}
			}
		})
	}
}

func TestNewRTKCommand_EnableMissingArgs(t *testing.T) {
	cfg := newTestRTKConfig(t)
	m := tools.NewMetrics()
	cmd := NewRTKCommand(cfg, m)

	teaCmd := cmd.Execute("enable")
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected error for enable without category")
	}
	if !strings.Contains(result.Text, "missing category") {
		t.Errorf("expected 'missing category' error, got %q", result.Text)
	}
}

func TestNewRTKCommand_DisableMissingArgs(t *testing.T) {
	cfg := newTestRTKConfig(t)
	m := tools.NewMetrics()
	cmd := NewRTKCommand(cfg, m)

	teaCmd := cmd.Execute("disable")
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected error for disable without category")
	}
	if !strings.Contains(result.Text, "missing category") {
		t.Errorf("expected 'missing category' error, got %q", result.Text)
	}
}

func TestNewRTKCommand_LevelMissingArgs(t *testing.T) {
	cfg := newTestRTKConfig(t)
	m := tools.NewMetrics()
	cmd := NewRTKCommand(cfg, m)

	// No args at all
	teaCmd := cmd.Execute("level")
	msg := teaCmd()
	result := msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected error for level without args")
	}

	// Only compressor, no level
	teaCmd = cmd.Execute("level go-test")
	msg = teaCmd()
	result = msg.(CommandResultMsg)
	if !result.IsError {
		t.Error("expected error for level with only compressor")
	}
}

func TestNewRTKCommand_LevelWithArgs(t *testing.T) {
	cfg := newTestRTKConfig(t)
	m := tools.NewMetrics()
	cmd := NewRTKCommand(cfg, m)

	teaCmd := cmd.Execute("level go-test high")
	msg := teaCmd()

	updated, ok := msg.(rtkUpdatedMsg)
	if !ok {
		t.Fatalf("expected rtkUpdatedMsg, got %T", msg)
	}
	if updated.setting != "go-test" || updated.value != "high" {
		t.Errorf("got setting=%q value=%q, want go-test/high", updated.setting, updated.value)
	}
}

func TestNewRTKCommand_EnableWithCategory(t *testing.T) {
	cfg := newTestRTKConfig(t)
	cfg.RTK.EnabledCategories["docker"] = false
	m := tools.NewMetrics()
	cmd := NewRTKCommand(cfg, m)

	teaCmd := cmd.Execute("enable docker")
	msg := teaCmd()

	_, ok := msg.(rtkUpdatedMsg)
	if !ok {
		t.Fatalf("expected rtkUpdatedMsg, got %T", msg)
	}
	if !cfg.RTK.EnabledCategories["docker"] {
		t.Error("docker should be enabled")
	}
}

func TestNewRTKCommand_DisableWithCategory(t *testing.T) {
	cfg := newTestRTKConfig(t)
	m := tools.NewMetrics()
	cmd := NewRTKCommand(cfg, m)

	teaCmd := cmd.Execute("disable git")
	msg := teaCmd()

	_, ok := msg.(rtkUpdatedMsg)
	if !ok {
		t.Fatalf("expected rtkUpdatedMsg, got %T", msg)
	}
	if cfg.RTK.EnabledCategories["git"] {
		t.Error("git should be disabled")
	}
}

func TestNewRTKCommand_ExportDefault(t *testing.T) {
	cfg := newTestRTKConfig(t)
	m := tools.NewMetrics()
	cmd := NewRTKCommand(cfg, m)

	teaCmd := cmd.Execute("export")
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}

	// Should have written to cfg.RTK.ExportPath
	if _, err := os.Stat(cfg.RTK.ExportPath); os.IsNotExist(err) {
		t.Error("expected file at default export path")
	}
}

func TestNewRTKCommand_ExportCustomPath(t *testing.T) {
	cfg := newTestRTKConfig(t)
	customPath := filepath.Join(t.TempDir(), "custom.json")
	m := tools.NewMetrics()
	cmd := NewRTKCommand(cfg, m)

	teaCmd := cmd.Execute("export " + customPath)
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if result.IsError {
		t.Errorf("unexpected error: %s", result.Text)
	}

	if _, err := os.Stat(customPath); os.IsNotExist(err) {
		t.Error("expected file at custom export path")
	}
}

func TestNewRTKCommand_ExportNilRTK(t *testing.T) {
	cfg := &config.Config{ConfigDir: t.TempDir()}
	m := tools.NewMetrics()
	rtk := NewRTKCommand(cfg, m)

	teaCmd := rtk.Execute("export")
	msg := teaCmd()

	result := msg.(CommandResultMsg)
	if !result.IsError || !strings.Contains(result.Text, "RTK not configured") {
		t.Errorf("expected RTK not configured error, got %q", result.Text)
	}
}
