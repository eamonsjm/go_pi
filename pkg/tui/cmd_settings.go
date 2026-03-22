package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/config"
)

// settingsDisplayMsg carries formatted settings text to show in chat.
type settingsDisplayMsg struct {
	text string
}

// settingsUpdatedMsg signals that a setting was changed.
type settingsUpdatedMsg struct {
	key   string
	value string
}

// parseThinkingLevel converts a string to the corresponding ThinkingLevel.
func parseThinkingLevel(s string) (ai.ThinkingLevel, bool) {
	switch s {
	case "off":
		return ai.ThinkingOff, true
	case "low":
		return ai.ThinkingLow, true
	case "medium":
		return ai.ThinkingMedium, true
	case "high":
		return ai.ThinkingHigh, true
	default:
		return "", false
	}
}

// NewSettingsCommand returns a SlashCommand for /settings that displays and
// modifies runtime configuration.
func NewSettingsCommand(cfg *config.Config, agentLoop *agent.AgentLoop, header *Header) *SlashCommand {
	return &SlashCommand{
		Name:        "settings",
		Description: "Show or change settings. Usage: /settings [key] [value]",
		Execute: func(args string) tea.Cmd {
			args = strings.TrimSpace(args)

			// No args — display current settings.
			if args == "" {
				return settingsDisplay(cfg)
			}

			// Parse key and value.
			parts := strings.SplitN(args, " ", 2)
			key := strings.TrimSpace(parts[0])
			value := ""
			if len(parts) > 1 {
				value = strings.TrimSpace(parts[1])
			}

			if value == "" {
				return settingsError(fmt.Sprintf("missing value for %q. Usage: /settings <key> <value>", key))
			}

			return settingsUpdate(key, value, cfg, agentLoop, header)
		},
	}
}

// settingsDisplay returns a Cmd that sends a settingsDisplayMsg with the
// current configuration formatted for display.
func settingsDisplay(cfg *config.Config) tea.Cmd {
	text := fmt.Sprintf(
		"Settings:\n"+
			"  provider:    %s\n"+
			"  model:       %s\n"+
			"  thinking:    %s\n"+
			"  max_tokens:  %d\n"+
			"  theme:       %s\n"+
			"  session_dir: %s\n"+
			"\n"+
			"Type /settings <key> <value> to change. Example:\n"+
			"  /settings thinking medium\n"+
			"  /settings max_tokens 16384\n"+
			"  /settings theme dark",
		cfg.DefaultProvider,
		cfg.DefaultModel,
		cfg.ThinkingLevel,
		cfg.MaxTokens,
		cfg.Theme,
		cfg.SessionDir,
	)
	return func() tea.Msg {
		return settingsDisplayMsg{text: text}
	}
}

// settingsError returns a Cmd that sends a CommandResultMsg with an error.
func settingsError(msg string) tea.Cmd {
	return func() tea.Msg {
		return CommandResultMsg{Text: msg, IsError: true}
	}
}

// settingsUpdate validates and applies a settings change, returning an
// appropriate Cmd.
func settingsUpdate(key, value string, cfg *config.Config, agentLoop *agent.AgentLoop, header *Header) tea.Cmd {
	switch key {

	case "thinking":
		level, ok := parseThinkingLevel(value)
		if !ok {
			return settingsError(fmt.Sprintf("invalid thinking level %q — valid: off, low, medium, high", value))
		}
		cfg.ThinkingLevel = value
		agentLoop.SetThinking(level)
		header.SetThinking(level)
		if err := cfg.Save(); err != nil {
			return settingsError(fmt.Sprintf("updated thinking to %s but failed to save: %v", value, err))
		}
		return func() tea.Msg {
			return settingsUpdatedMsg{key: key, value: value}
		}

	case "model":
		cfg.DefaultModel = value
		agentLoop.SetModel(value)
		header.SetModel(value)
		if err := cfg.Save(); err != nil {
			return settingsError(fmt.Sprintf("updated model to %s but failed to save: %v", value, err))
		}
		return func() tea.Msg {
			return settingsUpdatedMsg{key: key, value: value}
		}

	case "max_tokens":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return settingsError(fmt.Sprintf("invalid max_tokens %q — must be a positive integer", value))
		}
		cfg.MaxTokens = n
		agentLoop.SetMaxTokens(n)
		if err := cfg.Save(); err != nil {
			return settingsError(fmt.Sprintf("updated max_tokens to %d but failed to save: %v", n, err))
		}
		return func() tea.Msg {
			return settingsUpdatedMsg{key: key, value: value}
		}

	case "theme":
		return settingsError(fmt.Sprintf("use /theme %s to change the theme", value))

	case "provider":
		valid := config.ValidProviderNames()
		found := false
		for _, n := range valid {
			if n == value {
				found = true
				break
			}
		}
		if !found {
			return settingsError(fmt.Sprintf("unknown provider %q — valid: %s", value, strings.Join(valid, ", ")))
		}
		return settingsError(fmt.Sprintf("provider change to %q requires restart. Edit ~/.gi/settings.json and relaunch.", value))

	default:
		return settingsError(fmt.Sprintf("unknown setting %q — valid keys: thinking, model, max_tokens, provider, theme", key))
	}
}
