package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModelSelector_NewModelSelector(t *testing.T) {
	ms := NewModelSelector()
	if ms.visible {
		t.Error("expected not visible initially")
	}
	if len(ms.models) != len(defaultModels) {
		t.Errorf("expected %d models, got %d", len(defaultModels), len(ms.models))
	}
	if len(ms.filtered) != len(defaultModels) {
		t.Errorf("expected all models in filtered, got %d", len(ms.filtered))
	}
}

func TestModelSelector_ShowHide(t *testing.T) {
	ms := NewModelSelector()

	ms.Show()
	if !ms.Visible() {
		t.Error("expected visible after Show()")
	}
	if ms.cursor != 0 {
		t.Error("expected cursor reset to 0 on Show()")
	}
	if ms.filter != "" {
		t.Error("expected filter cleared on Show()")
	}

	ms.Hide()
	if ms.Visible() {
		t.Error("expected not visible after Hide()")
	}
}

func TestModelSelector_SetSize(t *testing.T) {
	ms := NewModelSelector()
	ms.SetSize(100, 50)
	if ms.width != 100 || ms.height != 50 {
		t.Errorf("expected 100x50, got %dx%d", ms.width, ms.height)
	}
}

func TestModelSelector_ResetFilter(t *testing.T) {
	ms := NewModelSelector()
	ms.resetFilter()
	if len(ms.filtered) != len(ms.models) {
		t.Errorf("resetFilter should include all models")
	}
	for i, idx := range ms.filtered {
		if idx != i {
			t.Errorf("filtered[%d] = %d, want %d", i, idx, i)
		}
	}
}

func TestModelSelector_ApplyFilter(t *testing.T) {
	ms := NewModelSelector()

	ms.filter = "opus"
	ms.applyFilter()
	// Should match Opus entries (newer versions plus legacy ones).
	if len(ms.filtered) < 2 {
		t.Errorf("expected at least 2 opus matches, got %d", len(ms.filtered))
	}
	for _, idx := range ms.filtered {
		opt := ms.models[idx]
		if !strings.Contains(strings.ToLower(opt.Label), "opus") &&
			!strings.Contains(strings.ToLower(opt.Model), "opus") {
			t.Errorf("unexpected filtered model: %q", opt.Label)
		}
	}
}

func TestModelSelector_ApplyFilter_CaseInsensitive(t *testing.T) {
	ms := NewModelSelector()

	ms.filter = "SONNET"
	ms.applyFilter()
	if len(ms.filtered) == 0 {
		t.Error("expected case-insensitive filter to match sonnet models")
	}
}

func TestModelSelector_ApplyFilter_NoMatch(t *testing.T) {
	ms := NewModelSelector()

	ms.filter = "xyznonexistent"
	ms.applyFilter()
	if len(ms.filtered) != 0 {
		t.Errorf("expected 0 matches, got %d", len(ms.filtered))
	}
}

func TestModelSelector_ApplyFilter_EmptyResetsAll(t *testing.T) {
	ms := NewModelSelector()
	ms.filter = "opus"
	ms.applyFilter()

	ms.filter = ""
	ms.applyFilter()
	// Empty filter should show all models from current provider.
	provider := ms.providers[ms.providerIdx]
	expectedCount := len(ms.modelsByProv[provider])
	if len(ms.filtered) != expectedCount {
		t.Errorf("empty filter should show all models from current provider, got %d (expected %d)", len(ms.filtered), expectedCount)
	}
}

func TestModelSelector_ApplyFilter_CursorClamp(t *testing.T) {
	ms := NewModelSelector()
	ms.cursor = 7 // last default model

	ms.filter = "opus"
	ms.applyFilter()
	// cursor should be clamped since there are fewer matches.
	if ms.cursor >= len(ms.filtered) {
		t.Errorf("cursor %d should be < %d", ms.cursor, len(ms.filtered))
	}
}

func TestModelSelector_Update_NotVisible(t *testing.T) {
	ms := NewModelSelector()
	ms.visible = false

	cmd := ms.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("Update should return nil when not visible")
	}
}

func TestModelSelector_Update_Escape(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	cmd := ms.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("Escape should return a command")
	}
	msg := cmd()
	if _, ok := msg.(modelCancelledMsg); !ok {
		t.Errorf("expected modelCancelledMsg, got %T", msg)
	}
	if ms.visible {
		t.Error("should be hidden after Escape")
	}
}

func TestModelSelector_Update_Enter(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	cmd := ms.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should return a command")
	}
	msg := cmd()
	sel, ok := msg.(modelSelectedMsg)
	if !ok {
		t.Fatalf("expected modelSelectedMsg, got %T", msg)
	}
	// First model should be selected (cursor=0).
	if sel.model != defaultModels[0].Model {
		t.Errorf("expected %q, got %q", defaultModels[0].Model, sel.model)
	}
	if sel.provider != defaultModels[0].Provider {
		t.Errorf("expected provider %q, got %q", defaultModels[0].Provider, sel.provider)
	}
}

func TestModelSelector_Update_Enter_EmptyFilter(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.filter = "xyznonexistent"
	ms.applyFilter() // should produce 0 matches

	cmd := ms.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("Enter with no matches should return nil")
	}
}

func TestModelSelector_Update_Navigation(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	// Move down.
	ms.Update(tea.KeyMsg{Type: tea.KeyDown})
	if ms.cursor != 1 {
		t.Errorf("expected cursor=1 after down, got %d", ms.cursor)
	}

	// Move down again.
	ms.Update(tea.KeyMsg{Type: tea.KeyDown})
	if ms.cursor != 2 {
		t.Errorf("expected cursor=2, got %d", ms.cursor)
	}

	// Move up.
	ms.Update(tea.KeyMsg{Type: tea.KeyUp})
	if ms.cursor != 1 {
		t.Errorf("expected cursor=1 after up, got %d", ms.cursor)
	}

	// Move up past top - should clamp at 0.
	ms.Update(tea.KeyMsg{Type: tea.KeyUp})
	ms.Update(tea.KeyMsg{Type: tea.KeyUp})
	if ms.cursor != 0 {
		t.Errorf("expected cursor=0 at top, got %d", ms.cursor)
	}
}

func TestModelSelector_Update_DownClamp(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	// Move to last item.
	for i := 0; i < len(ms.filtered)+5; i++ {
		ms.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if ms.cursor != len(ms.filtered)-1 {
		t.Errorf("expected cursor clamped at %d, got %d", len(ms.filtered)-1, ms.cursor)
	}
}

func TestModelSelector_Update_TypeFilter(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	before := len(ms.filtered)

	// Type "gpt" to filter.
	ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})

	if ms.filter != "gpt" {
		t.Errorf("expected filter 'gpt', got %q", ms.filter)
	}
	if len(ms.filtered) >= before {
		t.Errorf("expected fewer matches after filtering, got %d (was %d)", len(ms.filtered), before)
	}
}

func TestModelSelector_Update_Backspace(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.filter = "gpt"
	ms.applyFilter()
	before := len(ms.filtered)

	ms.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if ms.filter != "gp" {
		t.Errorf("expected filter 'gp', got %q", ms.filter)
	}
	// Less restrictive filter should have at least as many matches.
	if len(ms.filtered) < before {
		t.Errorf("expected at least %d matches, got %d", before, len(ms.filtered))
	}
}

func TestModelSelector_Update_BackspaceEmpty(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.filter = ""

	ms.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if ms.filter != "" {
		t.Errorf("expected empty filter, got %q", ms.filter)
	}
}

func TestModelSelector_View_NotVisible(t *testing.T) {
	ms := NewModelSelector()
	ms.visible = false
	if v := ms.View(); v != "" {
		t.Errorf("expected empty View when not visible, got %q", v)
	}
}

func TestModelSelector_View_Visible(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.SetSize(80, 40)
	view := ms.View()
	stripped := stripAnsi(view)
	if !strings.Contains(stripped, "Select Model") {
		t.Error("expected 'Select Model' title in view")
	}
}

func TestRegisterModelCommand_NoArgs(t *testing.T) {
	cmd := RegisterModelCommand()
	if cmd.Name != "model" {
		t.Errorf("expected name 'model', got %q", cmd.Name)
	}

	result := cmd.Execute("")
	if result == nil {
		t.Fatal("expected command to return a tea.Cmd")
	}
	msg := result()
	if _, ok := msg.(showModelSelectorMsg); !ok {
		t.Errorf("expected showModelSelectorMsg, got %T", msg)
	}
}

func TestRegisterModelCommand_WithKnownModel(t *testing.T) {
	cmd := RegisterModelCommand()
	result := cmd.Execute("claude-sonnet-4-20250514")
	if result == nil {
		t.Fatal("expected command to return a tea.Cmd")
	}
	msg := result()
	// Auth check may return CommandResultMsg if no credentials are configured.
	switch m := msg.(type) {
	case modelSelectedMsg:
		if m.model != "claude-sonnet-4-20250514" {
			t.Errorf("expected model claude-sonnet-4-20250514, got %q", m.model)
		}
		if m.provider != "anthropic" {
			t.Errorf("expected provider 'anthropic', got %q", m.provider)
		}
	case CommandResultMsg:
		if !m.IsError {
			t.Error("expected auth error if not modelSelectedMsg")
		}
		if !strings.Contains(m.Text, "anthropic") {
			t.Errorf("expected auth error to mention provider, got %q", m.Text)
		}
	default:
		t.Fatalf("expected modelSelectedMsg or CommandResultMsg, got %T", msg)
	}
}

func TestRegisterModelCommand_WithUnknownModel(t *testing.T) {
	cmd := RegisterModelCommand()
	result := cmd.Execute("xyznonexistent999")
	if result == nil {
		t.Fatal("expected command to return a tea.Cmd")
	}
	msg := result()
	cmdResult, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if !cmdResult.IsError {
		t.Errorf("expected IsError to be true for unknown model, got false")
	}
	if !strings.Contains(cmdResult.Text, "No model matching") {
		t.Errorf("expected error message to contain 'No model matching', got %q", cmdResult.Text)
	}
	if !strings.Contains(cmdResult.Text, "Available models") {
		t.Errorf("expected error message to contain 'Available models', got %q", cmdResult.Text)
	}
}

func TestRegisterModelCommand_FuzzyMatch_SingleResult(t *testing.T) {
	cmd := RegisterModelCommand()
	// "haiku" should fuzzy-match exactly one model: claude-haiku-4-5-20251001
	result := cmd.Execute("haiku")
	if result == nil {
		t.Fatal("expected command to return a tea.Cmd")
	}
	msg := result()
	// Without auth, this may be a modelSelectedMsg or a CommandResultMsg (auth error).
	// We check that it's NOT a showModelSelectorMsg (since there's only one match).
	switch m := msg.(type) {
	case modelSelectedMsg:
		if m.model != "claude-haiku-4-5-20251001" {
			t.Errorf("expected model claude-haiku-4-5-20251001, got %q", m.model)
		}
		if m.provider != "anthropic" {
			t.Errorf("expected provider anthropic, got %q", m.provider)
		}
	case CommandResultMsg:
		// Auth error is acceptable — the important thing is it resolved to one model.
		if !m.IsError {
			t.Error("expected auth error if not modelSelectedMsg")
		}
		if !strings.Contains(m.Text, "anthropic") {
			t.Errorf("expected auth error to mention provider, got %q", m.Text)
		}
	default:
		t.Fatalf("expected modelSelectedMsg or CommandResultMsg, got %T", msg)
	}
}

func TestRegisterModelCommand_FuzzyMatch_MultipleResults(t *testing.T) {
	cmd := RegisterModelCommand()
	// "opus" matches multiple models (Opus 4.6, Opus 4, OpenRouter Opus) → show selector
	result := cmd.Execute("opus")
	if result == nil {
		t.Fatal("expected command to return a tea.Cmd")
	}
	msg := result()
	selectorMsg, ok := msg.(showModelSelectorMsg)
	if !ok {
		t.Fatalf("expected showModelSelectorMsg for ambiguous query, got %T", msg)
	}
	if selectorMsg.filter != "opus" {
		t.Errorf("expected filter 'opus', got %q", selectorMsg.filter)
	}
}

func TestRegisterModelCommand_FuzzyMatch_GPT(t *testing.T) {
	cmd := RegisterModelCommand()
	// "gpt" should match multiple GPT variants → show selector
	result := cmd.Execute("gpt")
	if result == nil {
		t.Fatal("expected command to return a tea.Cmd")
	}
	msg := result()
	if _, ok := msg.(showModelSelectorMsg); !ok {
		t.Fatalf("expected showModelSelectorMsg for 'gpt', got %T", msg)
	}
}

func TestRegisterModelCommand_FuzzyMatch_ByProvider(t *testing.T) {
	cmd := RegisterModelCommand()
	// "gemini" matches provider and model names → multiple matches → selector
	result := cmd.Execute("gemini")
	if result == nil {
		t.Fatal("expected command to return a tea.Cmd")
	}
	msg := result()
	if _, ok := msg.(showModelSelectorMsg); !ok {
		t.Fatalf("expected showModelSelectorMsg for 'gemini', got %T", msg)
	}
}

// ---------------------------------------------------------------------------
// Malformed / unexpected input to model selector
// ---------------------------------------------------------------------------

func TestModelSelector_Enter_EmptyModelsList(t *testing.T) {
	ms := &ModelSelector{
		models:  nil,
		visible: true,
	}
	ms.resetFilter()

	// Enter with no models at all should return nil (no selection possible).
	cmd := ms.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("Enter with zero models should return nil")
	}
}

func TestModelSelector_Navigation_EmptyFiltered(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.filter = "xyznonexistent"
	ms.applyFilter()

	// Up/down with empty filtered list should not panic.
	ms.Update(tea.KeyMsg{Type: tea.KeyUp})
	ms.Update(tea.KeyMsg{Type: tea.KeyDown})
	if ms.cursor != 0 {
		t.Errorf("expected cursor 0 with empty list, got %d", ms.cursor)
	}
}

func TestModelSelector_VeryLongFilter(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	// Type a very long filter string.
	long := make([]rune, 500)
	for i := range long {
		long[i] = 'a'
	}
	for _, r := range long {
		ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(ms.filter) != 500 {
		t.Errorf("expected filter length 500, got %d", len(ms.filter))
	}
	if len(ms.filtered) != 0 {
		t.Errorf("expected 0 matches for long gibberish filter, got %d", len(ms.filtered))
	}

	// View should not panic even with absurd filter.
	ms.SetSize(80, 40)
	_ = ms.View()
}

func TestModelSelector_SpecialCharsInFilter(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	// Type special characters that might cause issues in string matching.
	for _, r := range "/*+?.[](){}^$\\" {
		ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	// Should not panic; just produce no matches.
	if len(ms.filtered) != 0 {
		t.Errorf("expected 0 matches for special char filter, got %d", len(ms.filtered))
	}
}

func TestModelSelector_View_1x1(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.SetSize(1, 1)
	// View with extreme size should not panic.
	v := ms.View()
	if v == "" {
		t.Error("expected non-empty view when visible")
	}
}

func TestModelSelector_View_ZeroSize(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.SetSize(0, 0)
	// View with zero dimensions should not panic.
	_ = ms.View()
}

func TestModelSelector_BackspaceAfterFullClear(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	// Type one char then delete it, then backspace again on empty.
	ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	ms.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	ms.Update(tea.KeyMsg{Type: tea.KeyBackspace})

	if ms.filter != "" {
		t.Errorf("expected empty filter, got %q", ms.filter)
	}
	// All models from current provider should be shown after clearing filter.
	provider := ms.providers[ms.providerIdx]
	expectedCount := len(ms.modelsByProv[provider])
	if len(ms.filtered) != expectedCount {
		t.Errorf("expected all models from current provider after clearing filter, got %d (expected %d)", len(ms.filtered), expectedCount)
	}
}

func TestModelSelector_RapidShowHide(t *testing.T) {
	ms := NewModelSelector()
	for i := 0; i < 20; i++ {
		ms.Show()
		ms.Hide()
	}
	if ms.Visible() {
		t.Error("expected not visible after final Hide()")
	}
}

func TestRegisterModelCommand_WhitespaceOnly(t *testing.T) {
	cmd := RegisterModelCommand()
	result := cmd.Execute("   ")
	if result == nil {
		t.Fatal("expected command to return a tea.Cmd")
	}
	msg := result()
	// Whitespace-only args should be treated as "show selector" after trimming.
	if _, ok := msg.(showModelSelectorMsg); !ok {
		t.Errorf("expected showModelSelectorMsg for whitespace args, got %T", msg)
	}
}

func TestFuzzyMatchModels(t *testing.T) {
	tests := []struct {
		query    string
		minMatch int
		mustHave string // at least one match must contain this in its Model field
	}{
		{"haiku", 1, "haiku"},
		{"opus", 3, "opus"},     // Opus 4.6, Opus 4, OpenRouter Opus
		{"sonnet", 3, "sonnet"}, // Sonnet 4.6, Sonnet 4, OpenRouter Sonnet
		{"gpt", 7, "gpt"},
		{"gemini", 7, "gemini"},
		{"flash", 4, "flash"},
		{"openrouter", 6, ""},
		{"xyznothing", 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			matches := fuzzyMatchModels(tt.query)
			if len(matches) < tt.minMatch {
				t.Errorf("fuzzyMatchModels(%q): got %d matches, want at least %d", tt.query, len(matches), tt.minMatch)
			}
			if tt.mustHave != "" && len(matches) > 0 {
				found := false
				for _, m := range matches {
					if strings.Contains(strings.ToLower(m.Model), tt.mustHave) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("fuzzyMatchModels(%q): no match contains %q in Model", tt.query, tt.mustHave)
				}
			}
		})
	}
}

func TestFuzzyMatchModels_CaseInsensitive(t *testing.T) {
	lower := fuzzyMatchModels("haiku")
	upper := fuzzyMatchModels("HAIKU")
	mixed := fuzzyMatchModels("Haiku")

	if len(lower) != len(upper) || len(lower) != len(mixed) {
		t.Errorf("case sensitivity issue: lower=%d upper=%d mixed=%d", len(lower), len(upper), len(mixed))
	}
}

func TestModelSelector_ShowWithFilter(t *testing.T) {
	ms := NewModelSelector()
	ms.ShowWithFilter("opus")

	if !ms.Visible() {
		t.Error("expected visible after ShowWithFilter")
	}
	if ms.filter != "opus" {
		t.Errorf("expected filter 'opus', got %q", ms.filter)
	}
	// Should have matches across all providers (not just the first provider)
	if len(ms.filtered) < 2 {
		t.Errorf("expected at least 2 opus matches across all providers, got %d", len(ms.filtered))
	}
	// Verify matches actually contain "opus"
	for _, idx := range ms.filtered {
		opt := ms.models[idx]
		haystack := strings.ToLower(opt.Label + " " + opt.Model + " " + opt.Provider)
		if !strings.Contains(haystack, "opus") {
			t.Errorf("unexpected match: %q (provider: %s)", opt.Label, opt.Provider)
		}
	}
}

func TestModelSelector_ShowWithFilter_Empty(t *testing.T) {
	ms := NewModelSelector()
	ms.ShowWithFilter("")

	if !ms.Visible() {
		t.Error("expected visible")
	}
	// Empty filter should show models from current provider
	provider := ms.providers[ms.providerIdx]
	expected := len(ms.modelsByProv[provider])
	if len(ms.filtered) != expected {
		t.Errorf("empty filter: expected %d models from provider %s, got %d", expected, provider, len(ms.filtered))
	}
}

func TestResolveModelArg_ExactMatch(t *testing.T) {
	opt, ok := ResolveModelArg("claude-haiku-4-5-20251001")
	if !ok {
		t.Fatal("expected exact match for claude-haiku-4-5-20251001")
	}
	if opt.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("expected model claude-haiku-4-5-20251001, got %q", opt.Model)
	}
	if opt.Provider != "anthropic" {
		t.Errorf("expected provider anthropic, got %q", opt.Provider)
	}
}

func TestResolveModelArg_CaseInsensitive(t *testing.T) {
	opt, ok := ResolveModelArg("CLAUDE-OPUS-4-6")
	if !ok {
		t.Fatal("expected case-insensitive match for CLAUDE-OPUS-4-6")
	}
	if opt.Model != "claude-opus-4-6" {
		t.Errorf("expected model claude-opus-4-6, got %q", opt.Model)
	}
}

func TestResolveModelArg_NoMatch(t *testing.T) {
	_, ok := ResolveModelArg("nonexistent-model")
	if ok {
		t.Error("expected no match for nonexistent-model")
	}
}

func TestResolveModelArg_FuzzyDoesNotMatch(t *testing.T) {
	// "haiku" alone is a fuzzy match but NOT an exact model ID
	_, ok := ResolveModelArg("haiku")
	if ok {
		t.Error("expected no match for partial name 'haiku' — ResolveModelArg is exact only")
	}
}

func TestResolveModelArg_EmptyString(t *testing.T) {
	_, ok := ResolveModelArg("")
	if ok {
		t.Error("expected no match for empty string")
	}
}

func TestRegisterModelCommand_CaseInsensitiveMatch(t *testing.T) {
	cmd := RegisterModelCommand()
	// EqualFold should match regardless of case.
	result := cmd.Execute("CLAUDE-SONNET-4-20250514")
	msg := result()
	// Auth check may return CommandResultMsg if no credentials are configured.
	switch m := msg.(type) {
	case modelSelectedMsg:
		if m.provider != "anthropic" {
			t.Errorf("expected provider 'anthropic', got %q", m.provider)
		}
	case CommandResultMsg:
		if !m.IsError {
			t.Error("expected auth error if not modelSelectedMsg")
		}
		if !strings.Contains(m.Text, "anthropic") {
			t.Errorf("expected auth error to mention provider, got %q", m.Text)
		}
	default:
		t.Fatalf("expected modelSelectedMsg or CommandResultMsg, got %T", msg)
	}
}

// ---------------------------------------------------------------------------
// Provider switching
// ---------------------------------------------------------------------------

func TestModelSelector_ProviderSwitching_Tab(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	if ms.providerIdx != 0 {
		t.Fatalf("expected initial providerIdx=0, got %d", ms.providerIdx)
	}
	initialProvider := ms.providers[0]

	// Tab should advance to the next provider.
	ms.Update(tea.KeyMsg{Type: tea.KeyTab})
	if ms.providerIdx != 1 {
		t.Errorf("expected providerIdx=1 after Tab, got %d", ms.providerIdx)
	}

	// Filtered list should now show models from the second provider.
	secondProvider := ms.providers[1]
	for _, idx := range ms.filtered {
		if ms.models[idx].Provider != secondProvider {
			t.Errorf("expected models from %q, got model with provider %q", secondProvider, ms.models[idx].Provider)
		}
	}

	// Tab should wrap around to the first provider.
	for i := 1; i < len(ms.providers); i++ {
		ms.Update(tea.KeyMsg{Type: tea.KeyTab})
	}
	if ms.providerIdx != 0 {
		t.Errorf("expected providerIdx=0 after wrapping, got %d", ms.providerIdx)
	}
	if ms.providers[ms.providerIdx] != initialProvider {
		t.Errorf("expected provider %q after wrap, got %q", initialProvider, ms.providers[ms.providerIdx])
	}
}

func TestModelSelector_ProviderSwitching_ShiftTab(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	// Shift+Tab from first provider should wrap to last.
	ms.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	lastIdx := len(ms.providers) - 1
	if ms.providerIdx != lastIdx {
		t.Errorf("expected providerIdx=%d after Shift+Tab from 0, got %d", lastIdx, ms.providerIdx)
	}
	lastProvider := ms.providers[lastIdx]
	for _, idx := range ms.filtered {
		if ms.models[idx].Provider != lastProvider {
			t.Errorf("expected models from %q, got %q", lastProvider, ms.models[idx].Provider)
		}
	}

	// Shift+Tab again should go to second-to-last.
	ms.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	if ms.providerIdx != lastIdx-1 {
		t.Errorf("expected providerIdx=%d, got %d", lastIdx-1, ms.providerIdx)
	}
}

func TestModelSelector_ProviderSwitching_LeftRight(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	// Right arrow advances provider.
	ms.Update(tea.KeyMsg{Type: tea.KeyRight})
	if ms.providerIdx != 1 {
		t.Errorf("expected providerIdx=1 after Right, got %d", ms.providerIdx)
	}

	// Left arrow goes back.
	ms.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if ms.providerIdx != 0 {
		t.Errorf("expected providerIdx=0 after Left, got %d", ms.providerIdx)
	}

	// Left at 0 should clamp (not wrap).
	ms.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if ms.providerIdx != 0 {
		t.Errorf("expected providerIdx=0 (clamped), got %d", ms.providerIdx)
	}

	// Right at last should clamp (not wrap).
	for i := 0; i < len(ms.providers)+5; i++ {
		ms.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	if ms.providerIdx != len(ms.providers)-1 {
		t.Errorf("expected providerIdx=%d (clamped), got %d", len(ms.providers)-1, ms.providerIdx)
	}
}

func TestModelSelector_ProviderSwitching_NumberKeys(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	// Press '2' to jump to second provider.
	ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if ms.providerIdx != 1 {
		t.Errorf("expected providerIdx=1 after pressing '2', got %d", ms.providerIdx)
	}
	secondProvider := ms.providers[1]
	for _, idx := range ms.filtered {
		if ms.models[idx].Provider != secondProvider {
			t.Errorf("expected models from %q, got %q", secondProvider, ms.models[idx].Provider)
		}
	}

	// Press '1' to jump back to first provider.
	ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if ms.providerIdx != 0 {
		t.Errorf("expected providerIdx=0 after pressing '1', got %d", ms.providerIdx)
	}

	// Press a number beyond provider count — should be ignored (treated as filter text).
	numProviders := len(ms.providers)
	if numProviders < 9 {
		beyondKey := rune('1' + numProviders) // e.g., '5' if there are 4 providers
		beforeFilter := ms.filter
		ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{beyondKey}})
		// The rune should be appended to filter instead.
		if ms.filter == beforeFilter {
			t.Error("expected out-of-range number key to be treated as filter text")
		}
	}
}

func TestModelSelector_ProviderSwitching_ResetsCursor(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	// Move cursor down in first provider.
	ms.Update(tea.KeyMsg{Type: tea.KeyDown})
	ms.Update(tea.KeyMsg{Type: tea.KeyDown})
	if ms.cursor < 2 {
		t.Fatalf("expected cursor >= 2, got %d", ms.cursor)
	}

	// Switch provider — cursor should reset to 0.
	ms.Update(tea.KeyMsg{Type: tea.KeyTab})
	if ms.cursor != 0 {
		t.Errorf("expected cursor=0 after provider switch, got %d", ms.cursor)
	}
}

func TestModelSelector_ProviderSwitching_ModelsMatchProvider(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	// Check each provider's filtered list.
	for i, provider := range ms.providers {
		ms.providerIdx = i
		ms.applyProviderFilter()

		if len(ms.filtered) == 0 {
			t.Errorf("provider %q should have at least 1 model", provider)
		}
		for _, idx := range ms.filtered {
			if ms.models[idx].Provider != provider {
				t.Errorf("provider %q: expected all models from same provider, got %q",
					provider, ms.models[idx].Provider)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Authentication status display
// ---------------------------------------------------------------------------

func TestModelSelector_AuthStatus_Initialized(t *testing.T) {
	ms := NewModelSelector()
	// authStatus map should have an entry for each provider.
	for _, provider := range ms.providers {
		if _, exists := ms.authStatus[provider]; !exists {
			t.Errorf("expected authStatus entry for provider %q", provider)
		}
	}
}

func TestModelSelector_View_ProviderTabsWithAuth(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.SetSize(120, 40)
	view := ms.View()
	stripped := stripAnsi(view)

	// Each provider should appear in the view.
	for _, provider := range ms.providers {
		if !strings.Contains(stripped, provider) {
			t.Errorf("expected provider %q in view", provider)
		}
	}

	// Auth indicators (✓ or ✗) should appear.
	if !strings.Contains(stripped, "✓") && !strings.Contains(stripped, "✗") {
		t.Error("expected auth status indicator (✓ or ✗) in view")
	}
}

func TestModelSelector_View_AuthIndicatorPerProvider(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.SetSize(120, 40)
	view := ms.View()
	stripped := stripAnsi(view)

	// Each provider tab should show "[provider ✓]" or "[provider ✗]".
	for _, provider := range ms.providers {
		authOK := "[" + provider + " ✓]"
		authFail := "[" + provider + " ✗]"
		if !strings.Contains(stripped, authOK) && !strings.Contains(stripped, authFail) {
			t.Errorf("expected %q or %q in view, neither found", authOK, authFail)
		}
	}
}

func TestModelSelector_View_CurrentProviderHighlighted(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.SetSize(120, 40)

	// Switch to second provider.
	ms.Update(tea.KeyMsg{Type: tea.KeyTab})
	view := ms.View()
	stripped := stripAnsi(view)

	// The second provider should appear in the view.
	secondProvider := ms.providers[1]
	if !strings.Contains(stripped, secondProvider) {
		t.Errorf("expected second provider %q in view", secondProvider)
	}
}

func TestModelSelector_View_ModelList_MatchesProvider(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.SetSize(120, 40)

	// View should show models from the first provider.
	view := ms.View()
	stripped := stripAnsi(view)

	firstProvider := ms.providers[0]
	for _, idx := range ms.modelsByProv[firstProvider] {
		label := ms.models[idx].Label
		if !strings.Contains(stripped, label) {
			t.Errorf("expected model label %q from provider %q in view", label, firstProvider)
		}
	}

	// Switch to second provider — view should now show second provider's models.
	ms.Update(tea.KeyMsg{Type: tea.KeyTab})
	view = ms.View()
	stripped = stripAnsi(view)

	secondProvider := ms.providers[1]
	for _, idx := range ms.modelsByProv[secondProvider] {
		label := ms.models[idx].Label
		if !strings.Contains(stripped, label) {
			t.Errorf("expected model label %q from provider %q in view", label, secondProvider)
		}
	}
}

func TestModelSelector_View_FilterPrompt(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.SetSize(120, 40)

	// Type a filter.
	for _, r := range "flash" {
		ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	view := ms.View()
	stripped := stripAnsi(view)
	if !strings.Contains(stripped, "/ flash") {
		t.Error("expected filter prompt '/ flash' in view")
	}
}

func TestModelSelector_View_NoMatchMessage(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.SetSize(120, 40)

	// Type a filter that won't match anything.
	for _, r := range "zzzznonexistent" {
		ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	view := ms.View()
	stripped := stripAnsi(view)
	if !strings.Contains(stripped, "No matching models") {
		t.Error("expected 'No matching models' message in view")
	}
}

func TestModelSelector_View_HelpText(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.SetSize(200, 40) // wide enough to avoid truncation

	view := ms.View()
	stripped := stripAnsi(view)

	// Should show key hints — the help line mentions providers and key bindings.
	if !strings.Contains(stripped, "providers") {
		t.Error("expected 'providers' in help text")
	}
	if !strings.Contains(stripped, "esc") {
		t.Error("expected 'esc' in help text")
	}
	if !strings.Contains(stripped, "enter") {
		t.Error("expected 'enter' in help text")
	}
}

func TestModelSelector_View_CursorIndicator(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.SetSize(120, 40)

	view := ms.View()
	stripped := stripAnsi(view)
	if !strings.Contains(stripped, ">") {
		t.Error("expected cursor indicator '>' in view")
	}
}

func TestModelSelector_View_SelectedModelShowsDetail(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()
	ms.SetSize(120, 40)

	// The cursor is at position 0 (first model). The selected model should
	// show its model ID detail.
	view := ms.View()
	stripped := stripAnsi(view)

	firstModel := ms.models[ms.filtered[0]]
	if !strings.Contains(stripped, firstModel.Model) {
		t.Errorf("expected model ID %q in view for selected model", firstModel.Model)
	}
}

// ---------------------------------------------------------------------------
// Provider selection with filter interaction
// ---------------------------------------------------------------------------

func TestModelSelector_FilterWithProviderSwitch(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	// Type a filter in the first provider.
	for _, r := range "opus" {
		ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	opusCount := len(ms.filtered)

	// Switch to a different provider — filter should still be applied.
	ms.Update(tea.KeyMsg{Type: tea.KeyTab})

	// The filtered list should change (different provider's models).
	// If the new provider has no "opus" models, the list should be empty.
	// The key assertion is that the filter text persists across provider switches.
	if ms.filter != "opus" {
		t.Errorf("expected filter 'opus' preserved after provider switch, got %q", ms.filter)
	}

	// The result count may differ (the new provider may or may not have opus models).
	_ = opusCount // We just need to verify no panic and filter is preserved.
}

func TestModelSelector_Enter_SelectsFromCurrentProvider(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	// Switch to "openai" provider.
	for i, p := range ms.providers {
		if p == "openai" {
			ms.providerIdx = i
			ms.applyProviderFilter()
			break
		}
	}

	// Select first model — should be an OpenAI model.
	cmd := ms.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command from Enter")
	}
	msg := cmd()
	sel, ok := msg.(modelSelectedMsg)
	if !ok {
		t.Fatalf("expected modelSelectedMsg, got %T", msg)
	}
	if sel.provider != "openai" {
		t.Errorf("expected provider 'openai', got %q", sel.provider)
	}
}

func TestModelSelector_Enter_SelectsFromGemini(t *testing.T) {
	ms := NewModelSelector()
	ms.Show()

	// Switch to "gemini" provider.
	for i, p := range ms.providers {
		if p == "gemini" {
			ms.providerIdx = i
			ms.applyProviderFilter()
			break
		}
	}

	cmd := ms.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected command from Enter")
	}
	msg := cmd()
	sel, ok := msg.(modelSelectedMsg)
	if !ok {
		t.Fatalf("expected modelSelectedMsg, got %T", msg)
	}
	if sel.provider != "gemini" {
		t.Errorf("expected provider 'gemini', got %q", sel.provider)
	}
	if !strings.Contains(sel.model, "gemini") {
		t.Errorf("expected a gemini model, got %q", sel.model)
	}
}
