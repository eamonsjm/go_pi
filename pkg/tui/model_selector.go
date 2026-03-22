package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ejm/go_pi/pkg/auth"
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
// If filter is non-empty, the selector opens pre-filtered to that query.
type showModelSelectorMsg struct {
	filter string
}

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
	// Anthropic Claude models (latest)
	{Provider: "anthropic", Model: "claude-opus-4-6", Label: "Claude Opus 4.6 (latest)"},
	{Provider: "anthropic", Model: "claude-sonnet-4-6", Label: "Claude Sonnet 4.6 (latest)"},
	{Provider: "anthropic", Model: "claude-haiku-4-5-20251001", Label: "Claude Haiku 4.5"},

	// Anthropic Claude models (previous generation)
	{Provider: "anthropic", Model: "claude-sonnet-4-20250514", Label: "Claude Sonnet 4"},
	{Provider: "anthropic", Model: "claude-opus-4-20250514", Label: "Claude Opus 4"},

	// OpenAI models
	{Provider: "openai", Model: "gpt-4.1", Label: "GPT-4.1"},
	{Provider: "openai", Model: "gpt-4.1-mini", Label: "GPT-4.1 Mini"},
	{Provider: "openai", Model: "gpt-4.1-nano", Label: "GPT-4.1 Nano"},
	{Provider: "openai", Model: "gpt-4o", Label: "GPT-4o"},
	{Provider: "openai", Model: "gpt-4o-mini", Label: "GPT-4o Mini"},
	{Provider: "openai", Model: "o3", Label: "o3"},
	{Provider: "openai", Model: "o4-mini", Label: "o4 Mini"},
	{Provider: "openai", Model: "o3-mini", Label: "o3 Mini"},

	// Google Gemini models
	{Provider: "gemini", Model: "gemini-2.5-pro", Label: "Gemini 2.5 Pro"},
	{Provider: "gemini", Model: "gemini-2.5-flash", Label: "Gemini 2.5 Flash"},
	{Provider: "gemini", Model: "gemini-2.0-flash", Label: "Gemini 2.0 Flash"},
	{Provider: "gemini", Model: "gemini-1.5-pro", Label: "Gemini 1.5 Pro"},
	{Provider: "gemini", Model: "gemini-1.5-flash", Label: "Gemini 1.5 Flash"},

	// OpenRouter (proxy for multiple providers)
	{Provider: "openrouter", Model: "anthropic/claude-opus-4-6", Label: "Claude Opus 4.6 (OpenRouter)"},
	{Provider: "openrouter", Model: "anthropic/claude-sonnet-4-6", Label: "Claude Sonnet 4.6 (OpenRouter)"},
	{Provider: "openrouter", Model: "openai/gpt-4.1", Label: "GPT-4.1 (OpenRouter)"},
	{Provider: "openrouter", Model: "openai/gpt-4o", Label: "GPT-4o (OpenRouter)"},
	{Provider: "openrouter", Model: "google/gemini-2.5-pro", Label: "Gemini 2.5 Pro (OpenRouter)"},
	{Provider: "openrouter", Model: "google/gemini-2.5-flash", Label: "Gemini 2.5 Flash (OpenRouter)"},
}

// ---------------------------------------------------------------------------
// ModelSelector — Bubble Tea overlay component
// ---------------------------------------------------------------------------

// ModelSelector is an overlay that lets the user pick from a list of models.
type ModelSelector struct {
	models       []ModelOption
	providers    []string           // unique providers in display order
	modelsByProv map[string][]int   // provider -> indices of models for that provider
	authStatus   map[string]bool    // provider -> authenticated
	filtered     []int              // indices into models that match the current filter
	cursor       int                // position within filtered (model index)
	providerIdx  int                // which provider is selected (for provider-centric view)
	visible      bool
	width        int
	height       int
	filter       string             // current search text
	authStore    *auth.Store        // for checking auth status
	providerMode bool               // if true, navigating providers; if false, navigating models
}

// NewModelSelector creates a ModelSelector pre-populated with common models.
func NewModelSelector() *ModelSelector {
	authStore, err := auth.NewStore("") // Use default path
	if err == nil {
		_ = authStore.Load() // Load credentials (ignore errors, just proceed)
	}

	ms := &ModelSelector{
		models:       make([]ModelOption, len(defaultModels)),
		modelsByProv: make(map[string][]int),
		authStatus:   make(map[string]bool),
		authStore:    authStore,
	}
	copy(ms.models, defaultModels)

	// Build provider list and model indices
	seenProviders := make(map[string]bool)
	for i, opt := range ms.models {
		if !seenProviders[opt.Provider] {
			ms.providers = append(ms.providers, opt.Provider)
			seenProviders[opt.Provider] = true
			// Check if authenticated
			ms.authStatus[opt.Provider] = ms.authStore != nil && ms.authStore.Get(opt.Provider) != nil
		}
		ms.modelsByProv[opt.Provider] = append(ms.modelsByProv[opt.Provider], i)
	}

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
	ms.providerIdx = 0
	ms.applyProviderFilter()
}

// ShowWithFilter makes the selector visible with a pre-populated filter,
// showing matches across all providers.
func (ms *ModelSelector) ShowWithFilter(query string) {
	ms.visible = true
	ms.cursor = 0
	ms.providerIdx = 0
	ms.filter = query
	ms.applyFilterAllProviders()
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

		case tea.KeyTab:
			// Tab switches to next provider
			ms.providerIdx = (ms.providerIdx + 1) % len(ms.providers)
			ms.applyProviderFilter()
			return nil

		case tea.KeyShiftTab:
			// Shift+Tab switches to previous provider
			ms.providerIdx--
			if ms.providerIdx < 0 {
				ms.providerIdx = len(ms.providers) - 1
			}
			ms.applyProviderFilter()
			return nil

		case tea.KeyLeft:
			// Left arrow switches to previous provider
			if ms.providerIdx > 0 {
				ms.providerIdx--
				ms.applyProviderFilter()
			}
			return nil

		case tea.KeyRight:
			// Right arrow switches to next provider
			if ms.providerIdx < len(ms.providers)-1 {
				ms.providerIdx++
				ms.applyProviderFilter()
			}
			return nil

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
				_, size := utf8.DecodeLastRuneInString(ms.filter)
				ms.filter = ms.filter[:len(ms.filter)-size]
				ms.applyFilter()
			}
			return nil

		default:
			// Number keys (1-9) jump to provider
			if msg.Type == tea.KeyRunes {
				rune := []rune(msg.String())[0]
				if rune >= '1' && rune <= '9' {
					idx := int(rune - '1')
					if idx < len(ms.providers) {
						ms.providerIdx = idx
						ms.applyProviderFilter()
						return nil
					}
				}
				// Regular character: accumulate for filter
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

	boxWidth := 60
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

	// Provider tabs with auth status
	providerTabs := ""
	for i, provider := range ms.providers {
		authStatus := "✗"
		if ms.authStatus[provider] {
			authStatus = "✓"
		}
		displayName := provider + " " + authStatus

		if i == ms.providerIdx {
			// Highlight current provider
			providerTabs += lipgloss.NewStyle().
				Foreground(ColorPrimary).
				Bold(true).
				Render("[" + displayName + "]")
		} else {
			providerTabs += lipgloss.NewStyle().
				Foreground(ColorMuted).
				Render("[" + displayName + "]")
		}

		if i < len(ms.providers)-1 {
			providerTabs += " "
		}
	}
	sb.WriteString(providerTabs + "\n\n")

	// Filter prompt if active.
	if ms.filter != "" {
		filterLine := lipgloss.NewStyle().
			Foreground(ColorSecondary).
			Render("/ " + ms.filter)
		sb.WriteString(filterLine + "\n\n")
	}

	// Model list (filtered).
	if len(ms.filtered) > 0 {
		for i, idx := range ms.filtered {
			opt := ms.models[idx]
			label := opt.Label
			detail := lipgloss.NewStyle().Foreground(ColorMuted).Render(
				fmt.Sprintf("  %s", opt.Model),
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
	} else {
		noMatch := lipgloss.NewStyle().Foreground(ColorMuted).Italic(true).Render("  No matching models")
		sb.WriteString(noMatch + "\n")
	}

	sb.WriteString("\n")
	help := lipgloss.NewStyle().Foreground(ColorMuted).Render(
		"↑/↓ models  ← → providers  tab next  1-9 jump  enter select  esc cancel  type filter",
	)
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
	ms.cursor = 0
}

// applyProviderFilter shows only models from the current provider.
func (ms *ModelSelector) applyProviderFilter() {
	if ms.providerIdx >= len(ms.providers) {
		ms.providerIdx = 0
	}
	provider := ms.providers[ms.providerIdx]
	ms.filtered = make([]int, len(ms.modelsByProv[provider]))
	copy(ms.filtered, ms.modelsByProv[provider])
	ms.cursor = 0
}

func (ms *ModelSelector) applyFilter() {
	if ms.filter == "" {
		ms.applyProviderFilter()
		return
	}

	// Filter models from the current provider only
	provider := ms.providers[ms.providerIdx]
	providerModels := ms.modelsByProv[provider]

	query := strings.ToLower(ms.filter)
	ms.filtered = ms.filtered[:0]
	for _, idx := range providerModels {
		opt := ms.models[idx]
		haystack := strings.ToLower(opt.Label + " " + opt.Model)
		if strings.Contains(haystack, query) {
			ms.filtered = append(ms.filtered, idx)
		}
	}
	if ms.cursor >= len(ms.filtered) {
		ms.cursor = 0
	}
}

// applyFilterAllProviders filters across ALL providers (used for pre-filtered show).
func (ms *ModelSelector) applyFilterAllProviders() {
	if ms.filter == "" {
		ms.applyProviderFilter()
		return
	}

	query := strings.ToLower(ms.filter)
	ms.filtered = ms.filtered[:0]
	for i, opt := range ms.models {
		haystack := strings.ToLower(opt.Label + " " + opt.Model + " " + opt.Provider)
		if strings.Contains(haystack, query) {
			ms.filtered = append(ms.filtered, i)
		}
	}
	if ms.cursor >= len(ms.filtered) {
		ms.cursor = 0
	}
}

// ---------------------------------------------------------------------------
// ResolveModelArg — exact model ID lookup for CLI positional args
// ---------------------------------------------------------------------------

// ResolveModelArg checks whether arg exactly matches a known model ID
// (case-insensitive). If it does, it returns the matching ModelOption and true.
// This is used to support `gi claude-haiku-4-5-20251001` as a positional arg.
func ResolveModelArg(arg string) (ModelOption, bool) {
	for _, opt := range defaultModels {
		if strings.EqualFold(opt.Model, arg) {
			return opt, true
		}
	}
	return ModelOption{}, false
}

// ---------------------------------------------------------------------------
// Fuzzy matching
// ---------------------------------------------------------------------------

// fuzzyMatchModels returns all models whose Label, Model ID, or Provider
// contain the query as a case-insensitive substring.
func fuzzyMatchModels(query string) []ModelOption {
	q := strings.ToLower(query)
	var matches []ModelOption
	for _, opt := range defaultModels {
		haystack := strings.ToLower(opt.Label + " " + opt.Model + " " + opt.Provider)
		if strings.Contains(haystack, q) {
			matches = append(matches, opt)
		}
	}
	return matches
}

// ---------------------------------------------------------------------------
// SlashCommand registration
// ---------------------------------------------------------------------------

// RegisterModelCommand returns a SlashCommand for /model that can be registered
// with the command infrastructure. It accepts a pre-loaded auth.Store so that
// model switches reuse the existing credentials instead of reading from disk
// on every invocation. If store is nil, auth checks are skipped gracefully.
func RegisterModelCommand(store *auth.Store) SlashCommand {
	return SlashCommand{
		Name:        "model",
		Description: "Switch the AI model. Usage: /model [model-name]",
		Execute: func(args string) tea.Cmd {
			args = strings.TrimSpace(args)
			if args == "" {
				// No args — show the interactive selector.
				return func() tea.Msg { return showModelSelectorMsg{} }
			}

			// 1. Exact match on model ID (case-insensitive).
			for _, opt := range defaultModels {
				if strings.EqualFold(opt.Model, args) {
					return modelSwitchWithAuthCheck(store, opt)
				}
			}

			// 2. Fuzzy match: substring across Label, Model, Provider.
			matches := fuzzyMatchModels(args)

			switch len(matches) {
			case 0:
				// No match — error with available models.
				var availableModels []string
				for _, opt := range defaultModels {
					availableModels = append(availableModels, fmt.Sprintf("  %s (%s/%s)", opt.Label, opt.Provider, opt.Model))
				}
				errorMsg := fmt.Sprintf("No model matching %q\n\nAvailable models:\n%s", args, strings.Join(availableModels, "\n"))
				return func() tea.Msg {
					return CommandResultMsg{Text: errorMsg, IsError: true}
				}

			case 1:
				// Single fuzzy match — switch directly (with auth check).
				return modelSwitchWithAuthCheck(store, matches[0])

			default:
				// Multiple matches — show selector pre-filtered.
				filter := args
				return func() tea.Msg { return showModelSelectorMsg{filter: filter} }
			}
		},
	}
}

// modelSwitchWithAuthCheck returns a tea.Cmd that verifies the user is
// authenticated to the model's provider before emitting modelSelectedMsg.
// If not authenticated, it returns an error prompting the user to log in.
// It reuses the provided auth.Store rather than creating a new one each time.
func modelSwitchWithAuthCheck(store *auth.Store, opt ModelOption) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			// Can't check auth — proceed anyway.
			return modelSelectedMsg{provider: opt.Provider, model: opt.Model}
		}

		if store.Get(opt.Provider) == nil {
			return CommandResultMsg{
				Text: fmt.Sprintf(
					"Not authenticated to %s. Run /login %s first, then retry /model %s",
					opt.Provider, opt.Provider, opt.Model,
				),
				IsError: true,
			}
		}

		return modelSelectedMsg{provider: opt.Provider, model: opt.Model}
	}
}
