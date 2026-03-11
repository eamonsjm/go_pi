package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/ejm/go_pi/pkg/ai"
)

// Header renders a single-line top bar with app name, model info, and session.
type Header struct {
	width         int
	model         string
	thinkingLevel ai.ThinkingLevel
	sessionName   string
}

// NewHeader creates a Header with default values.
func NewHeader() *Header {
	return &Header{
		model:         "claude-sonnet-4-20250514",
		thinkingLevel: ai.ThinkingOff,
	}
}

// SetWidth adjusts the header to the given terminal width.
func (h *Header) SetWidth(w int) {
	h.width = w
}

// SetModel updates the displayed model name.
func (h *Header) SetModel(name string) {
	h.model = name
}

// SetThinking updates the thinking level indicator.
func (h *Header) SetThinking(level ai.ThinkingLevel) {
	h.thinkingLevel = level
}

// SetSession updates the displayed session name.
func (h *Header) SetSession(name string) {
	h.sessionName = name
}

// Height returns 1 (always a single line).
func (h *Header) Height() int { return 1 }

// View renders the header bar.
func (h *Header) View() string {
	// App name.
	appName := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		Render("pi")

	// Model badge.
	modelBadge := lipgloss.NewStyle().
		Foreground(ColorSecondary).
		Render(h.model)

	// Thinking indicator (only when not off).
	var thinkBadge string
	if h.thinkingLevel != ai.ThinkingOff && h.thinkingLevel != "" {
		thinkBadge = lipgloss.NewStyle().
			Foreground(ColorThinking).
			Render("thinking: " + string(h.thinkingLevel))
	}

	// Session name (optional).
	var sessionBadge string
	if h.sessionName != "" {
		sessionBadge = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true).
			Render(h.sessionName)
	}

	// Assemble left-side parts.
	parts := []string{appName, modelBadge}
	if thinkBadge != "" {
		parts = append(parts, thinkBadge)
	}
	if sessionBadge != "" {
		parts = append(parts, sessionBadge)
	}
	left := strings.Join(parts, "  ")

	// Pad to full width.
	line := HeaderStyle.Width(h.width).Render(left)
	return line
}
