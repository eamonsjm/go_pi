package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/config"
)

// themeChangedMsg signals that the theme was switched.
type themeChangedMsg struct {
	name string
}

// NewThemeCommand returns a SlashCommand for /theme that displays or switches
// the active color theme.
func NewThemeCommand(cfg *config.Config, chat *ChatView) *SlashCommand {
	return &SlashCommand{
		Name:        "theme",
		Description: "Show or change theme. Usage: /theme [dark|light|auto|<name>]",
		Execute: func(args string) tea.Cmd {
			name := strings.TrimSpace(args)

			// No args — display current theme and usage.
			if name == "" {
				current := ActiveTheme()
				text := fmt.Sprintf(
					"Theme: %s\n\n"+
						"Built-in themes: dark, light, auto\n"+
						"Custom themes: place JSON files in ~/.gi/themes/ or .gi/themes/\n\n"+
						"Usage: /theme <name>",
					current.Name,
				)
				return func() tea.Msg {
					return CommandResultMsg{Text: text}
				}
			}

			// Resolve and apply the theme.
			t, err := ResolveTheme(name)
			if err != nil {
				return func() tea.Msg {
					return CommandResultMsg{
						Text:    fmt.Sprintf("Theme %q not found. Place a JSON file at ~/.gi/themes/%s.json", name, name),
						IsError: true,
					}
				}
			}

			SetTheme(t)
			chat.RefreshTheme()

			// Persist to config.
			cfg.Theme = name
			_ = cfg.Save()

			return func() tea.Msg {
				return themeChangedMsg{name: t.Name}
			}
		},
	}
}
