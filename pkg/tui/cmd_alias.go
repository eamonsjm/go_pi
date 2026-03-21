package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/config"
)

// NewAliasCommand creates the /alias command for defining command aliases.
func NewAliasCommand(cfg *config.Config, registry *CommandRegistry) *SlashCommand {
	return &SlashCommand{
		Name:        "alias",
		Description: "Define a command alias (/alias <alias> <target>) or show usage",
		Execute: func(args string) tea.Cmd {
			args = strings.TrimSpace(args)
			parts := strings.Fields(args)

			if len(parts) == 0 {
				return func() tea.Msg {
					return CommandResultMsg{
						Text: "Usage: /alias <alias> <target>\n  /alias --reset to clear all aliases\nExample: /alias clear new",
					}
				}
			}

			// Handle --reset flag
			if len(parts) == 1 && parts[0] == "--reset" {
				return func() tea.Msg {
					// Clear all aliases from registry
					for alias := range registry.AllAliases() {
						registry.RemoveAlias(alias)
					}

					// Clear all aliases from config
					if cfg.Aliases != nil {
						cfg.Aliases = make(map[string]string)
					}

					if err := cfg.Save(); err != nil {
						return CommandResultMsg{
							Text:    fmt.Sprintf("Warning: aliases cleared but failed to save config: %v", err),
							IsError: false,
						}
					}

					return CommandResultMsg{
						Text: "All aliases cleared",
					}
				}
			}

			if len(parts) == 1 {
				return func() tea.Msg {
					return CommandResultMsg{
						Text:    fmt.Sprintf("Error: missing target command for alias '%s'", parts[0]),
						IsError: true,
					}
				}
			}

			alias := parts[0]
			target := parts[1]

			// Check if target command exists
			if _, ok := registry.Get(target); !ok {
				return func() tea.Msg {
					return CommandResultMsg{
						Text:    fmt.Sprintf("Error: target command '/%s' not found", target),
						IsError: true,
					}
				}
			}

			// Set the alias
			if !registry.SetAlias(alias, target) {
				return func() tea.Msg {
					return CommandResultMsg{
						Text:    fmt.Sprintf("Error: failed to set alias '%s'", alias),
						IsError: true,
					}
				}
			}

			// Persist to config
			if cfg.Aliases == nil {
				cfg.Aliases = make(map[string]string)
			}
			cfg.Aliases[alias] = target

			if err := cfg.Save(); err != nil {
				return func() tea.Msg {
					return CommandResultMsg{
						Text:    fmt.Sprintf("Warning: alias created but failed to save config: %v", err),
						IsError: false,
					}
				}
			}

			return func() tea.Msg {
				return CommandResultMsg{
					Text: fmt.Sprintf("Alias '/%s' → '/%s' created", alias, target),
				}
			}
		},
	}
}

// NewAliasesCommand creates the /aliases command for listing all aliases.
func NewAliasesCommand(registry *CommandRegistry) *SlashCommand {
	return &SlashCommand{
		Name:        "aliases",
		Description: "List all defined command aliases",
		Execute: func(args string) tea.Cmd {
			return func() tea.Msg {
				aliases := registry.AllAliases()
				if len(aliases) == 0 {
					return CommandResultMsg{
						Text: "No aliases defined. Use `/alias <alias> <target>` to create one.",
					}
				}

				// Sort for consistent display
				keys := make([]string, 0, len(aliases))
				for k := range aliases {
					keys = append(keys, k)
				}
				sort.Strings(keys)

				var sb strings.Builder
				sb.WriteString("Aliases:\n")
				for _, alias := range keys {
					target := aliases[alias]
					fmt.Fprintf(&sb, "  /%s → /%s\n", alias, target)
				}

				return CommandResultMsg{Text: sb.String()}
			}
		},
	}
}

// NewUnaliasCommand creates the /unalias command for removing aliases.
func NewUnaliasCommand(cfg *config.Config, registry *CommandRegistry) *SlashCommand {
	return &SlashCommand{
		Name:        "unalias",
		Description: "Remove a command alias (/unalias <alias>)",
		Execute: func(args string) tea.Cmd {
			alias := strings.TrimSpace(args)
			if alias == "" {
				return func() tea.Msg {
					return CommandResultMsg{
						Text:    "Usage: /unalias <alias>",
						IsError: true,
					}
				}
			}

			// Check if alias exists
			if registry.GetAlias(alias) == "" {
				return func() tea.Msg {
					return CommandResultMsg{
						Text:    fmt.Sprintf("Error: alias '/%s' not found", alias),
						IsError: true,
					}
				}
			}

			// Remove the alias
			registry.RemoveAlias(alias)

			// Persist to config
			if cfg.Aliases != nil {
				delete(cfg.Aliases, alias)
				if err := cfg.Save(); err != nil {
					return func() tea.Msg {
						return CommandResultMsg{
							Text:    fmt.Sprintf("Warning: alias removed but failed to save config: %v", err),
							IsError: false,
						}
					}
				}
			}

			return func() tea.Msg {
				return CommandResultMsg{
					Text: fmt.Sprintf("Alias '/%s' removed", alias),
				}
			}
		},
	}
}
