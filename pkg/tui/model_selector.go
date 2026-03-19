package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Messages emitted by the model selector
// ---------------------------------------------------------------------------

// modelSelectedMsg is sent when the user picks a model from the selector.
type modelSelectedMsg struct {
	provider string
	model    string
}

// modelCancelledMsg is sent when the user dismisses the selector.
type modelCancelledMsg struct{}

// showModelSelectorMsg tells the App to show the model selector overlay.
type showModelSelectorMsg struct{}

// ---------------------------------------------------------------------------
// ModelOption — a single entry in the model list
// ---------------------------------------------------------------------------

// ModelOption describes one selectable model.
type ModelOption struct {
	Provider string
	Model    string
	Label    string // display name
}

// ---------------------------------------------------------------------------
// Default model catalogue
// ---------------------------------------------------------------------------

var defaultModels = []ModelOption{
	{Provider: "anthropic", Model: "claude-sonnet-4-20250514", Label: "Claude Sonnet 4"},
	{Provider: "anthropic", Model: "claude-opus-4-20250514", Label: "Claude Opus 4"},
	{Provider: "anthropic", Model: "claude-haiku-3-5-20241022", Label: "Claude Haiku 3.5"},
	{Provider: "openrouter", Model: "anthropic/claude-sonnet-4-20250514", Label: "Claude Sonnet 4 (OpenRouter)"},
	{Provider: "openrouter", Model: "anthropic/claude-opus-4-20250514", Label: "Claude Opus 4 (OpenRouter)"},
	{Provider: "openrouter", Model: "google/gemini-2.5-pro", Label: "Gemini 2.5 Pro (OpenRouter)"},
	{Provider: "gemini", Model: "gemini-2.5-pro", Label: "Gemini 2.5 Pro"},
	{Provider: "gemini", Model: "gemini-2.0-flash", Label: "Gemini 2.0 Flash"},
	{Provider: "openai", Model: "gpt-4o", Label: "GPT-4o"},
	{Provider: "openai", Model: "o3", Label: "o3"},
}

// ---------------------------------------------------------------------------
// ModelSelector — Bubble Tea overlay component
// ---------------------------------------------------------------------------

// ModelSelector is an overlay that lets the user pick from a list of models.
type ModelSelector struct {
	models   []ModelOption
	filtered []int // indices into models that match the current filter
	cursor   int   // position within filtered
	visible  bool
	width    int
	height   int
	filter   string // current search text
}

// NewModelSelector creates a ModelSelector pre-populated with common models.
func NewModelSelector() *ModelSelector {
	ms := &ModelSelector{
		models: make([]ModelOption, len(defaultModels)),
	}
	copy(ms.models, defaultModels)
	ms.resetFilter()
	return ms
}

// SetSize updates the overlay dimensions (usually called after a window resize).
func (ms *ModelSelector) SetSize(w, h int) {
	ms.width = w
	ms.height = h
}

// Show makes the selector visible and resets state.
func (ms *ModelSelector) Show() {
	ms.visible = true
	ms.filter = ""
	ms.cursor = 0
	ms.resetFilter()
}

// Hide hides the selector.
func (ms *ModelSelector) Hide() {
	ms.visible = false
}

// Visible reports whether the selector is currently shown.
func (ms *ModelSelector) Visible() bool {
	return ms.visible
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

// Update processes keyboard input when the selector is visible.
// Returns a tea.Cmd if a message should be emitted, or nil.
func (ms *ModelSelector) Update(msg tea.Msg) tea.Cmd {
	if !ms.visible {
		return nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEscape:
			ms.Hide()
			return func() tea.Msg { return modelCancelledMsg{} }

		case tea.KeyEnter:
			if len(ms.filtered) == 0 {
				return nil
			}
			opt := ms.models[ms.filtered[ms.cursor]]
			ms.Hide()
			return func() tea.Msg {
				return modelSelectedMsg{provider: opt.Provider, model: opt.Model}
			}

		case tea.KeyUp:
			if ms.cursor > 0 {
				ms.cursor--
			}
			return nil

		case tea.KeyDown:
			if ms.cursor < len(ms.filtered)-1 {
				ms.cursor++
			}
			return nil

		case tea.KeyBackspace:
			if len(ms.filter) > 0 {
				ms.filter = ms.filter[:len(ms.filter)-1]
				ms.applyFilter()
			}
			return nil

		default:
			// Accumulate printable runes for the filter.
			if msg.Type == tea.KeyRunes {
				ms.filter += msg.String()
				ms.applyFilter()
			}
			return nil
		}
	}

	return nil
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// View renders the model selector as an overlay box.
func (ms *ModelSelector) View() string {
	if !ms.visible {
		return ""
	}

	boxWidth := 52
	if ms.width > 0 && boxWidth > ms.width-4 {
		boxWidth = ms.width - 4
	}
	innerWidth := boxWidth - 4 // padding

	var sb strings.Builder

	// Title.
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		Render("Select Model")
	sb.WriteString(title + "\n\n")

	// Filter prompt.
	if ms.filter != "" {
		filterLine := lipgloss.NewStyle().
			Foreground(ColorSecondary).
			Render("/ " + ms.filter)
		sb.WriteString(filterLine + "\n\n")
	}

	// Model list.
	for i, idx := range ms.filtered {
		opt := ms.models[idx]
		label := opt.Label
		detail := lipgloss.NewStyle().Foreground(ColorMuted).Render(
			fmt.Sprintf("  %s / %s", opt.Provider, opt.Model),
		)

		if i == ms.cursor {
			pointer := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("> ")
			name := lipgloss.NewStyle().Foreground(ColorText).Bold(true).Render(label)
			sb.WriteString(pointer + name + "\n")
			sb.WriteString("  " + detail + "\n")
		} else {
			name := lipgloss.NewStyle().Foreground(ColorText).Render("  " + label)
			sb.WriteString(name + "\n")
		}
	}

	if len(ms.filtered) == 0 {
		noMatch := lipgloss.NewStyle().Foreground(ColorMuted).Italic(true).Render("  No matching models")
		sb.WriteString(noMatch + "\n")
	}

	sb.WriteString("\n")
	help := lipgloss.NewStyle().Foreground(ColorMuted).Render("↑/↓ navigate  enter select  esc cancel  type to filter")
	sb.WriteString(help)

	// Wrap in a styled box.
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		Width(innerWidth).
		Render(sb.String())

	// Center the box horizontally.
	if ms.width > 0 {
		box = lipgloss.PlaceHorizontal(ms.width, lipgloss.Center, box)
	}

	// Center vertically (roughly).
	if ms.height > 0 {
		box = lipgloss.PlaceVertical(ms.height, lipgloss.Center, box)
	}

	return box
}

// ---------------------------------------------------------------------------
// Filter helpers
// ---------------------------------------------------------------------------

func (ms *ModelSelector) resetFilter() {
	ms.filtered = make([]int, len(ms.models))
	for i := range ms.models {
		ms.filtered[i] = i
	}
}

func (ms *ModelSelector) applyFilter() {
	if ms.filter == "" {
		ms.resetFilter()
		ms.cursor = 0
		return
	}
	query := strings.ToLower(ms.filter)
	ms.filtered = ms.filtered[:0]
	for i, opt := range ms.models {
		haystack := strings.ToLower(opt.Label + " " + opt.Provider + " " + opt.Model)
		if strings.Contains(haystack, query) {
			ms.filtered = append(ms.filtered, i)
		}
	}
	if ms.cursor >= len(ms.filtered) {
		ms.cursor = 0
	}
}

// ---------------------------------------------------------------------------
// SlashCommand registration
// ---------------------------------------------------------------------------

// RegisterModelCommand returns a SlashCommand for /model that can be registered
// with the command infrastructure.
func RegisterModelCommand() SlashCommand {
	return SlashCommand{
		Name:        "model",
		Description: "Switch the AI model. Usage: /model [model-name]",
		Execute: func(args string) tea.Cmd {
			args = strings.TrimSpace(args)
			if args == "" {
				// No args — show the interactive selector.
				return func() tea.Msg { return showModelSelectorMsg{} }
			}
			// Direct model switch: try to match against known models first.
			for _, opt := range defaultModels {
				if strings.EqualFold(opt.Model, args) {
					provider := opt.Provider
					model := opt.Model
					return func() tea.Msg {
						return modelSelectedMsg{provider: provider, model: model}
					}
				}
			}
			// Unknown model — return error with list of available models.
			var availableModels []string
			for _, opt := range defaultModels {
				availableModels = append(availableModels, fmt.Sprintf("%s (%s/%s)", opt.Label, opt.Provider, opt.Model))
			}
			errorMsg := fmt.Sprintf("Unknown model: %s\n\nAvailable models:\n%s", args, strings.Join(availableModels, "\n"))
			return func() tea.Msg {
				return CommandResultMsg{Text: errorMsg, IsError: true}
			}
		},
	}
}
