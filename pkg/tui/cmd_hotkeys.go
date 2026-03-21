package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// NewHotkeysCommand returns a SlashCommand for /hotkeys that displays all
// current keybindings.
func NewHotkeysCommand(kb *KeybindingConfig) *SlashCommand {
	return &SlashCommand{
		Name:        "hotkeys",
		Description: "Show current keybindings",
		Execute: func(args string) tea.Cmd {
			var sb strings.Builder
			sb.WriteString("Keybindings:\n\n")

			// Fixed bindings (editor-level, not customizable).
			sb.WriteString("  Editor (fixed):\n")
			fixed := [][2]string{
				{"Enter", "Send message / steer agent"},
				{"Shift+Enter", "Insert newline"},
				{"Up", "Recall previous message (when empty)"},
				{"Ctrl+C", "Cancel agent / double-press to quit"},
				{"Ctrl+D", "Quit (when idle and empty)"},
				{"Escape", "Cancel running agent"},
			}
			for _, f := range fixed {
				fmt.Fprintf(&sb, "    %-20s  %s\n", f[0], f[1])
			}

			// Viewport navigation.
			sb.WriteString("\n  Viewport navigation:\n")
			viewport := [][2]string{
				{"Up/Down", "Scroll one line"},
				{"PgUp/PgDn", "Scroll one page"},
				{"Home/End", "Jump to top/bottom"},
				{"Mouse wheel", "Scroll viewport"},
			}
			for _, v := range viewport {
				fmt.Fprintf(&sb, "    %-20s  %s\n", v[0], v[1])
			}

			// Customizable bindings.
			sb.WriteString("\n  Customizable:\n")
			for _, b := range kb.AllBindings() {
				desc := actionDescriptions[b.Action]
				if desc == "" {
					desc = string(b.Action)
				}
				fmt.Fprintf(&sb, "    %-20s  %s\n", b.Key, desc)
			}

			sb.WriteString("\n  Override in ~/.gi/keybindings.json")

			text := sb.String()
			return func() tea.Msg {
				return CommandResultMsg{Text: text}
			}
		},
	}
}
