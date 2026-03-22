package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/muesli/termenv"
)

// Theme defines the color tokens used to render the TUI.
// Custom themes can be loaded from JSON files in ~/.gi/themes/ or .gi/themes/.
type Theme struct {
	Name         string `json:"name"`
	Primary      string `json:"primary"`
	Secondary    string `json:"secondary"`
	Success      string `json:"success"`
	Warning      string `json:"warning"`
	Error        string `json:"error"`
	Muted        string `json:"muted"`
	Text         string `json:"text"`
	Background   string `json:"background"`
	Border       string `json:"border"`
	Thinking     string `json:"thinking"`
	ToolName     string `json:"tool_name"`
	ToolResult   string `json:"tool_result"`
	HeaderBg     string `json:"header_bg"`
	GlamourStyle string `json:"glamour_style"` // "dark" or "light"
}

// Built-in themes.
var (
	DarkTheme = Theme{
		Name:         "dark",
		Primary:      "#7C3AED",
		Secondary:    "#06B6D4",
		Success:      "#10B981",
		Warning:      "#F59E0B",
		Error:        "#EF4444",
		Muted:        "#6B7280",
		Text:         "#E5E7EB",
		Background:   "#1F2937",
		Border:       "#374151",
		Thinking:     "#3B82F6",
		ToolName:     "#F59E0B",
		ToolResult:   "#9CA3AF",
		HeaderBg:     "#111827",
		GlamourStyle: "dark",
	}

	LightTheme = Theme{
		Name:         "light",
		Primary:      "#7C3AED",
		Secondary:    "#0891B2",
		Success:      "#059669",
		Warning:      "#D97706",
		Error:        "#DC2626",
		Muted:        "#6B7280",
		Text:         "#1F2937",
		Background:   "#F9FAFB",
		Border:       "#D1D5DB",
		Thinking:     "#2563EB",
		ToolName:     "#D97706",
		ToolResult:   "#6B7280",
		HeaderBg:     "#F3F4F6",
		GlamourStyle: "light",
	}
)

// ActiveTheme returns a copy of the current theme.
func ActiveTheme() Theme {
	return currentStyles.Load().theme
}

// SetTheme sets the active theme and rebuilds all component styles.
// The new snapshot is swapped in atomically so concurrent readers in
// View() goroutines always see a consistent set of styles.
func SetTheme(t Theme) {
	s := buildStyles(t)
	currentStyles.Store(&s)
}

// ResolveTheme determines the theme to use based on the given name.
// Supported names: "auto", "dark", "light", or a custom theme filename
// (without .json extension). Custom themes are searched in:
//  1. .gi/themes/<name>.json (project-local)
//  2. ~/.gi/themes/<name>.json (global)
func ResolveTheme(name string) (Theme, error) {
	switch name {
	case "", "auto":
		if termenv.NewOutput(os.Stderr).HasDarkBackground() {
			return DarkTheme, nil
		}
		return LightTheme, nil
	case "dark":
		return DarkTheme, nil
	case "light":
		return LightTheme, nil
	default:
		return loadCustomTheme(name)
	}
}

// loadCustomTheme searches for a JSON theme file and parses it.
func loadCustomTheme(name string) (Theme, error) {
	// Project-local first, then global.
	candidates := []string{
		filepath.Join(".gi", "themes", name+".json"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".gi", "themes", name+".json"))
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return Theme{}, fmt.Errorf("load theme %s: %w", name, err)
		}

		// Start from dark theme defaults so partial themes work.
		t := DarkTheme
		if err := json.Unmarshal(data, &t); err != nil {
			return Theme{}, fmt.Errorf("load theme %s: %w", name, err)
		}
		t.Name = name
		return t, nil
	}

	return Theme{}, os.ErrNotExist
}

