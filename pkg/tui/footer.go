package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/ejm/go_pi/pkg/ai"
)

// Footer renders a single-line status bar at the bottom of the terminal.
type Footer struct {
	width      int
	cwd        string
	usage      ai.Usage
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

	// Cost estimate (rough: $3/M input, $15/M output for Sonnet).
	cost := estimateCost(f.usage)
	costStr := fmt.Sprintf("$%.2f", cost)

	// Context usage.
	ctxStr := fmt.Sprintf("%.0f%%", f.contextPct)

	// Build the line.
	left := MutedStyle.Render(dir)
	right := MutedStyle.Render(strings.Join([]string{tokens, costStr, ctxStr}, "  "))

	// Pad so left is left-aligned and right is right-aligned.
	gap := f.width - lipglossWidth(left) - lipglossWidth(right)
	if gap < 1 {
		gap = 1
	}
	padding := strings.Repeat(" ", gap)

	line := FooterStyle.Width(f.width).Render(left + padding + right)
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

// estimateCost gives a rough USD estimate based on Claude Sonnet pricing.
// Input: $3/M tokens, Output: $15/M tokens, Cache-read: $0.30/M.
func estimateCost(u ai.Usage) float64 {
	input := float64(u.InputTokens-u.CacheRead) * 3.0 / 1_000_000
	cacheRead := float64(u.CacheRead) * 0.30 / 1_000_000
	output := float64(u.OutputTokens) * 15.0 / 1_000_000
	total := input + cacheRead + output
	if total < 0 {
		total = 0
	}
	return total
}

// lipglossWidth returns the visible (printed) width of a styled string.
func lipglossWidth(s string) int {
	return ansi.StringWidth(s)
}

// stripAnsi removes all ANSI escape sequences from s, returning only the
// printable content. Delegates to charmbracelet/x/ansi which correctly
// handles CSI parameter bytes, OSC sequences, and other escape types.
func stripAnsi(s string) string {
	return ansi.Strip(s)
}
