package tui

import (
	"sync/atomic"

	"github.com/charmbracelet/lipgloss"
)

// styleSnapshot holds all color tokens and component styles as an immutable
// snapshot. Readers obtain the current snapshot via Styles(); SetTheme builds
// a new snapshot and swaps it in atomically, so View() goroutines never see a
// partially-updated set of styles.
type styleSnapshot struct {
	theme Theme

	// Color tokens.
	ColorPrimary    lipgloss.Color
	ColorSecondary  lipgloss.Color
	ColorSuccess    lipgloss.Color
	ColorWarning    lipgloss.Color
	ColorError      lipgloss.Color
	ColorMuted      lipgloss.Color
	ColorText       lipgloss.Color
	ColorBg         lipgloss.Color
	ColorBorder     lipgloss.Color
	ColorThinking   lipgloss.Color
	ColorToolName   lipgloss.Color
	ColorToolResult lipgloss.Color

	// Header / footer bars.
	HeaderStyle lipgloss.Style
	FooterStyle lipgloss.Style

	// Editor (textarea wrapper).
	EditorStyle         lipgloss.Style
	EditorActiveStyle   lipgloss.Style
	EditorThinkingStyle lipgloss.Style
	EditorSearchStyle   lipgloss.Style

	// Role labels rendered before each message block.
	UserRoleStyle      lipgloss.Style
	AssistantRoleStyle lipgloss.Style

	// Message content.
	UserMsgStyle       lipgloss.Style
	AssistantMsgStyle  lipgloss.Style
	ThinkingStyle      lipgloss.Style
	ThinkingLabelStyle lipgloss.Style

	// Tool call / result styles.
	ToolCallStyle   lipgloss.Style
	ToolArgsStyle   lipgloss.Style
	ToolResultStyle lipgloss.Style
	ToolErrorStyle  lipgloss.Style

	// Plugin message styles.
	PluginNameStyle    lipgloss.Style
	PluginLogWarnStyle lipgloss.Style

	// Misc.
	SpinnerStyle         lipgloss.Style
	MutedStyle           lipgloss.Style
	ErrorMsgStyle        lipgloss.Style
	SystemMsgStyle       lipgloss.Style
	NewContentBelowStyle lipgloss.Style
}

var currentStyles atomic.Pointer[styleSnapshot]

func init() {
	s := buildStyles(DarkTheme)
	currentStyles.Store(&s)
}

// Styles returns the current immutable style snapshot.
func Styles() *styleSnapshot {
	return currentStyles.Load()
}

// buildStyles constructs a complete styleSnapshot from a Theme.
func buildStyles(t Theme) styleSnapshot {
	primary := lipgloss.Color(t.Primary)
	secondary := lipgloss.Color(t.Secondary)
	success := lipgloss.Color(t.Success)
	warning := lipgloss.Color(t.Warning)
	errColor := lipgloss.Color(t.Error)
	muted := lipgloss.Color(t.Muted)
	text := lipgloss.Color(t.Text)
	bg := lipgloss.Color(t.Background)
	border := lipgloss.Color(t.Border)
	thinking := lipgloss.Color(t.Thinking)
	toolName := lipgloss.Color(t.ToolName)
	toolResult := lipgloss.Color(t.ToolResult)
	headerBg := lipgloss.Color(t.HeaderBg)

	return styleSnapshot{
		theme: t,

		ColorPrimary:    primary,
		ColorSecondary:  secondary,
		ColorSuccess:    success,
		ColorWarning:    warning,
		ColorError:      errColor,
		ColorMuted:      muted,
		ColorText:       text,
		ColorBg:         bg,
		ColorBorder:     border,
		ColorThinking:   thinking,
		ColorToolName:   toolName,
		ColorToolResult: toolResult,

		HeaderStyle: lipgloss.NewStyle().
			Bold(true).
			Foreground(text).
			Background(headerBg).
			Padding(0, 1),

		FooterStyle: lipgloss.NewStyle().
			Foreground(muted).
			Background(headerBg).
			Padding(0, 1),

		EditorStyle: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(border).
			Padding(0, 0),

		EditorActiveStyle: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(primary).
			Padding(0, 0),

		EditorThinkingStyle: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(thinking).
			Padding(0, 0),

		EditorSearchStyle: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(warning).
			Padding(0, 0),

		UserRoleStyle: lipgloss.NewStyle().
			Bold(true).
			Foreground(secondary),

		AssistantRoleStyle: lipgloss.NewStyle().
			Bold(true).
			Foreground(primary),

		UserMsgStyle: lipgloss.NewStyle().
			Foreground(text),

		AssistantMsgStyle: lipgloss.NewStyle().
			Foreground(text),

		ThinkingStyle: lipgloss.NewStyle().
			Foreground(thinking).
			Italic(true),

		ThinkingLabelStyle: lipgloss.NewStyle().
			Foreground(thinking).
			Bold(true),

		ToolCallStyle: lipgloss.NewStyle().
			Foreground(toolName).
			Bold(true),

		ToolArgsStyle: lipgloss.NewStyle().
			Foreground(muted),

		ToolResultStyle: lipgloss.NewStyle().
			Foreground(toolResult).
			Padding(0, 2),

		ToolErrorStyle: lipgloss.NewStyle().
			Foreground(errColor).
			Padding(0, 2),

		PluginNameStyle: lipgloss.NewStyle().
			Foreground(success).
			Bold(true),

		PluginLogWarnStyle: lipgloss.NewStyle().
			Foreground(warning),

		SpinnerStyle: lipgloss.NewStyle().
			Foreground(primary),

		MutedStyle: lipgloss.NewStyle().
			Foreground(muted),

		ErrorMsgStyle: lipgloss.NewStyle().
			Foreground(errColor).
			Bold(true),

		SystemMsgStyle: lipgloss.NewStyle().
			Foreground(muted).
			Italic(true),

		NewContentBelowStyle: lipgloss.NewStyle().
			Foreground(text).
			Background(lipgloss.Color("#374151")).
			Bold(true).
			Padding(0, 1),
	}
}
