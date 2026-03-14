package tui

import (
	"testing"

	"github.com/ejm/go_pi/pkg/ai"
)

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{10000, "10.0k"},
		{999999, "1000.0k"},
		{1000000, "1.0M"},
		{2500000, "2.5M"},
	}
	for _, tt := range tests {
		got := formatTokens(tt.n)
		if got != tt.want {
			t.Errorf("formatTokens(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestEstimateCost(t *testing.T) {
	// Zero usage = zero cost.
	cost := estimateCost(ai.Usage{})
	if cost != 0 {
		t.Errorf("expected 0, got %f", cost)
	}

	// 1M input tokens (no cache) = $3.00
	cost = estimateCost(ai.Usage{InputTokens: 1_000_000})
	if cost < 2.99 || cost > 3.01 {
		t.Errorf("expected ~3.00, got %f", cost)
	}

	// 1M output tokens = $15.00
	cost = estimateCost(ai.Usage{OutputTokens: 1_000_000})
	if cost < 14.99 || cost > 15.01 {
		t.Errorf("expected ~15.00, got %f", cost)
	}

	// Cache read discount: 1M input with 500k cache-read.
	// Non-cached: 500k * $3/M = $1.50
	// Cached: 500k * $0.30/M = $0.15
	// Total: $1.65
	cost = estimateCost(ai.Usage{InputTokens: 1_000_000, CacheRead: 500_000})
	if cost < 1.64 || cost > 1.66 {
		t.Errorf("expected ~1.65, got %f", cost)
	}
}

func TestEstimateCost_NeverNegative(t *testing.T) {
	// CacheRead > InputTokens would produce a negative unclamped input cost.
	cost := estimateCost(ai.Usage{InputTokens: 100, CacheRead: 1000})
	if cost < 0 {
		t.Errorf("cost should never be negative, got %f", cost)
	}
}

func TestStripAnsi(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"", ""},
		{"\x1b[31mred\x1b[0m", "red"},
		{"\x1b[1m\x1b[34mbold blue\x1b[0m", "bold blue"},
		{"no escapes here", "no escapes here"},
		{"\x1b[38;5;196mextended\x1b[0m", "extended"},
		// 24-bit color with semicolons (the old hand-rolled stripper handled this,
		// but the library approach is more robust for edge cases).
		{"\x1b[38;2;255;0;0mtrue color\x1b[0m", "true color"},
		// OSC hyperlink sequence (old code would fail here).
		{"\x1b]8;;https://example.com\x07link\x1b]8;;\x07", "link"},
	}
	for _, tt := range tests {
		got := stripAnsi(tt.input)
		if got != tt.want {
			t.Errorf("stripAnsi(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLipglossWidth(t *testing.T) {
	// Plain text should return len.
	if w := lipglossWidth("hello"); w != 5 {
		t.Errorf("expected 5, got %d", w)
	}

	// Styled text should strip ANSI.
	styled := "\x1b[31mhi\x1b[0m"
	if w := lipglossWidth(styled); w != 2 {
		t.Errorf("expected 2, got %d", w)
	}
}

func TestFooter_SetUsage(t *testing.T) {
	f := &Footer{maxContext: 200_000}

	f.SetUsage(ai.Usage{InputTokens: 100_000})
	if f.contextPct != 50 {
		t.Errorf("expected contextPct=50, got %f", f.contextPct)
	}

	// Overflow is capped at 100%.
	f.SetUsage(ai.Usage{InputTokens: 300_000})
	if f.contextPct != 100 {
		t.Errorf("expected contextPct=100, got %f", f.contextPct)
	}
}

func TestFooter_SetMaxContext(t *testing.T) {
	f := &Footer{maxContext: 200_000}
	f.SetMaxContext(100_000)
	if f.maxContext != 100_000 {
		t.Errorf("expected 100000, got %d", f.maxContext)
	}
}

func TestFooter_Height(t *testing.T) {
	f := &Footer{}
	if h := f.Height(); h != 1 {
		t.Errorf("expected height 1, got %d", h)
	}
}

func TestFooter_AbbreviateCwd(t *testing.T) {
	f := &Footer{cwd: "/tmp/some/path"}
	// With a cwd that doesn't start with $HOME, abbreviation returns as-is.
	got := f.abbreviateCwd()
	if got != "/tmp/some/path" {
		t.Errorf("expected /tmp/some/path, got %q", got)
	}
}
