package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/ejm/go_pi/pkg/ai"
)

// Footer renders a single-line status bar at the bottom of the terminal.
//
// A zero-value Footer is not usable. Use [NewFooter] to construct one.
type Footer struct {
	width      int
	cwd        string
	usage      ai.Usage
	model      string  // current model name for cost calculation
	contextPct float64 // 0..100
	maxContext int     // max context tokens for the model
}

// NewFooter creates a Footer, automatically capturing the current working
// directory.
func NewFooter() *Footer {
	cwd, _ := os.Getwd()
	return &Footer{
		cwd:        cwd,
		maxContext: 200_000, // sensible default
	}
}

// SetModel updates the model name used for cost calculation.
func (f *Footer) SetModel(model string) {
	f.model = model
}

// SetWidth adjusts the footer to the given terminal width.
func (f *Footer) SetWidth(w int) {
	f.width = w
}

// SetUsage updates the displayed token usage.
func (f *Footer) SetUsage(u ai.Usage) {
	f.usage = u
	if f.maxContext > 0 {
		f.contextPct = float64(u.InputTokens) / float64(f.maxContext) * 100
		if f.contextPct > 100 {
			f.contextPct = 100
		}
	}
}

// Height returns 1 (always a single line).
func (f *Footer) Height() int { return 1 }

// View renders the footer bar.
func (f *Footer) View() string {
	// Abbreviated working directory: replace $HOME with ~.
	dir := f.abbreviateCwd()

	// Token usage.
	tokens := fmt.Sprintf("%s↑ %s↓",
		formatTokens(f.usage.InputTokens),
		formatTokens(f.usage.OutputTokens),
	)

	// Cost estimate using model-aware pricing.
	cost := calculateCost(f.usage, f.model)
	costStr := fmt.Sprintf("$%.2f", cost)

	// Context usage.
	ctxStr := fmt.Sprintf("%.0f%%", f.contextPct)

	s := Styles()

	// Build the line.
	left := s.MutedStyle.Render(dir)
	right := s.MutedStyle.Render(strings.Join([]string{tokens, costStr, ctxStr}, "  "))

	// Pad so left is left-aligned and right is right-aligned.
	gap := f.width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		gap = 1
	}
	padding := strings.Repeat(" ", gap)

	line := s.FooterStyle.Width(f.width).Render(left + padding + right)
	return line
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (f *Footer) abbreviateCwd() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return f.cwd
	}
	if strings.HasPrefix(f.cwd, home) {
		return "~" + f.cwd[len(home):]
	}
	return f.cwd
}

// formatTokens renders a token count in a human-friendly form.
func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}


