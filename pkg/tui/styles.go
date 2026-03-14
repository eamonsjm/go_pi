package tui

import "github.com/charmbracelet/lipgloss"

// ---------------------------------------------------------------------------
// Color palette — dark theme matching modern coding-agent aesthetics
// ---------------------------------------------------------------------------

var (
	ColorPrimary    = lipgloss.Color("#7C3AED") // Purple
	ColorSecondary  = lipgloss.Color("#06B6D4") // Cyan
	ColorSuccess    = lipgloss.Color("#10B981") // Green
	ColorWarning    = lipgloss.Color("#F59E0B") // Amber
	ColorError      = lipgloss.Color("#EF4444") // Red
	ColorMuted      = lipgloss.Color("#6B7280") // Gray
	ColorText       = lipgloss.Color("#E5E7EB") // Light gray
	ColorBg         = lipgloss.Color("#1F2937") // Dark background
	ColorBorder     = lipgloss.Color("#374151") // Border
	ColorThinking   = lipgloss.Color("#3B82F6") // Blue for thinking
	ColorToolName   = lipgloss.Color("#F59E0B") // Amber for tool names
	ColorToolResult = lipgloss.Color("#9CA3AF") // Gray for tool results
)

// ---------------------------------------------------------------------------
// Component styles
// ---------------------------------------------------------------------------

// Header / footer bars.
var (
	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorText).
			Background(lipgloss.Color("#111827")).
			Padding(0, 1)

	FooterStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Background(lipgloss.Color("#111827")).
			Padding(0, 1)
)

// Editor (textarea wrapper).
var (
	EditorStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 0)

	EditorActiveStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary).
				Padding(0, 0)

	EditorThinkingStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorThinking).
				Padding(0, 0)
)

// ---------------------------------------------------------------------------
// Message styles
// ---------------------------------------------------------------------------

// Role labels rendered before each message block.
var (
	UserRoleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorSecondary)

	AssistantRoleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorPrimary)
)

// Message content.
var (
	UserMsgStyle = lipgloss.NewStyle().
			Foreground(ColorText)

	AssistantMsgStyle = lipgloss.NewStyle().
				Foreground(ColorText)

	ThinkingStyle = lipgloss.NewStyle().
			Foreground(ColorThinking).
			Italic(true)

	ThinkingLabelStyle = lipgloss.NewStyle().
				Foreground(ColorThinking).
				Bold(true)
)

// Tool call / result styles.
var (
	ToolCallStyle = lipgloss.NewStyle().
			Foreground(ColorToolName).
			Bold(true)

	ToolArgsStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	ToolResultStyle = lipgloss.NewStyle().
			Foreground(ColorToolResult).
			Padding(0, 2)

	ToolErrorStyle = lipgloss.NewStyle().
			Foreground(ColorError).
			Padding(0, 2)
)

// Plugin message styles.
var (
	PluginNameStyle = lipgloss.NewStyle().
			Foreground(ColorSuccess).
			Bold(true)

	PluginLogWarnStyle = lipgloss.NewStyle().
				Foreground(ColorWarning)
)

// Misc.
var (
	SpinnerStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary)

	MutedStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	ErrorMsgStyle = lipgloss.NewStyle().
			Foreground(ColorError).
			Bold(true)

	SystemMsgStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true)

	NewContentBelowStyle = lipgloss.NewStyle().
				Foreground(ColorText).
				Background(lipgloss.Color("#374151")).
				Bold(true).
				Padding(0, 1)
)
