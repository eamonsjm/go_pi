package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSetTheme_UpdatesActiveTheme(t *testing.T) {
	// Save and restore original theme.
	original := ActiveTheme()
	defer SetTheme(original)

	lt := lightTheme()
	SetTheme(lt)
	got := ActiveTheme()
	if got.Name != "light" {
		t.Errorf("expected theme name 'light', got %q", got.Name)
	}
	if got.Primary != lt.Primary {
		t.Errorf("expected Primary %q, got %q", lt.Primary, got.Primary)
	}
}

func TestSetTheme_StyleSnapshotConsistency(t *testing.T) {
	original := ActiveTheme()
	defer SetTheme(original)

	SetTheme(lightTheme())
	s := Styles()
	if s.theme.Name != "light" {
		t.Errorf("expected styles to reflect light theme, got %q", s.theme.Name)
	}

	SetTheme(darkTheme())
	s2 := Styles()
	if s2.theme.Name != "dark" {
		t.Errorf("expected styles to reflect dark theme, got %q", s2.theme.Name)
	}

	// The first snapshot should still be valid (immutable).
	if s.theme.Name != "light" {
		t.Error("previous snapshot was mutated")
	}
}

func TestResolveTheme_Dark(t *testing.T) {
	theme, err := ResolveTheme("dark")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if theme.Name != "dark" {
		t.Errorf("expected 'dark', got %q", theme.Name)
	}
}

func TestResolveTheme_Light(t *testing.T) {
	theme, err := ResolveTheme("light")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if theme.Name != "light" {
		t.Errorf("expected 'light', got %q", theme.Name)
	}
}

func TestResolveTheme_Auto(t *testing.T) {
	// "auto" and "" should both resolve without error.
	for _, name := range []string{"auto", ""} {
		theme, err := ResolveTheme(name)
		if err != nil {
			t.Fatalf("ResolveTheme(%q) unexpected error: %v", name, err)
		}
		if theme.Name != "dark" && theme.Name != "light" {
			t.Errorf("expected 'dark' or 'light', got %q", theme.Name)
		}
	}
}

func TestResolveTheme_CustomNotFound(t *testing.T) {
	_, err := ResolveTheme("nonexistent-theme-xyz")
	if err == nil {
		t.Error("expected error for nonexistent custom theme")
	}
}

func TestLightTheme_Fields(t *testing.T) {
	lt := lightTheme()
	if lt.Name != "light" {
		t.Errorf("expected name 'light', got %q", lt.Name)
	}
	if lt.GlamourStyle != "light" {
		t.Errorf("expected glamour_style 'light', got %q", lt.GlamourStyle)
	}
	// Verify a few key fields differ from dark.
	dt := darkTheme()
	if lt.Text == dt.Text {
		t.Error("light and dark themes should have different Text colors")
	}
	if lt.Background == dt.Background {
		t.Error("light and dark themes should have different Background colors")
	}
}

func TestDarkTheme_Fields(t *testing.T) {
	dt := darkTheme()
	if dt.Name != "dark" {
		t.Errorf("expected name 'dark', got %q", dt.Name)
	}
	if dt.GlamourStyle != "dark" {
		t.Errorf("expected glamour_style 'dark', got %q", dt.GlamourStyle)
	}
	// Verify all required fields are non-empty.
	if dt.Primary == "" || dt.Error == "" || dt.Muted == "" {
		t.Error("dark theme has empty required fields")
	}
}

func TestLoadCustomTheme_FromProjectLocal(t *testing.T) {
	// Create a temporary project-local theme file.
	dir := t.TempDir()

	themeDir := filepath.Join(dir, ".gi", "themes")
	if err := os.MkdirAll(themeDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Only set specific fields — unset fields should inherit from dark theme.
	customJSON := map[string]string{
		"primary": "#FF0000",
		"error":   "#00FF00",
	}
	data, _ := json.Marshal(customJSON)
	if err := os.WriteFile(filepath.Join(themeDir, "test-custom.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Change to the temp dir so project-local path resolves.
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	loaded, err := ResolveTheme("test-custom")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded.Primary != "#FF0000" {
		t.Errorf("expected Primary '#FF0000', got %q", loaded.Primary)
	}
	// Name is overridden by the theme loader.
	if loaded.Name != "test-custom" {
		t.Errorf("expected name 'test-custom', got %q", loaded.Name)
	}
	// Partial theme inherits defaults from dark theme.
	dt := darkTheme()
	if loaded.Secondary != dt.Secondary {
		t.Errorf("expected Secondary to inherit from dark theme %q, got %q", dt.Secondary, loaded.Secondary)
	}
}

func TestLoadCustomTheme_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	themeDir := filepath.Join(dir, ".gi", "themes")
	os.MkdirAll(themeDir, 0o755)
	os.WriteFile(filepath.Join(themeDir, "bad.json"), []byte("{invalid json"), 0o644)

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	_, err := ResolveTheme("bad")
	if err == nil {
		t.Error("expected error for invalid JSON theme")
	}
}
