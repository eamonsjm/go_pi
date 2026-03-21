package tui

import "github.com/charmbracelet/x/ansi"

// stripAnsi removes all ANSI escape sequences from s, returning only the
// printable content. Used by tests to compare rendered output.
func stripAnsi(s string) string {
	return ansi.Strip(s)
}
