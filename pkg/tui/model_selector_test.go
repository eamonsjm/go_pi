package tui

import (
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
	// Should match both Opus entries.
	for _, idx := range ms.filtered {
		opt := ms.models[idx]
		if opt.Label != "Claude Opus 4" && opt.Label != "Claude Opus 4 (OpenRouter)" {
			t.Errorf("unexpected filtered model: %q", opt.Label)
		}
	}
	if len(ms.filtered) < 2 {
		t.Errorf("expected at least 2 opus matches, got %d", len(ms.filtered))
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
	before := len(ms.filtered)

	ms.filter = ""
	ms.applyFilter()
	if len(ms.filtered) != len(ms.models) {
		t.Errorf("empty filter should show all models, got %d (was %d)", len(ms.filtered), before)
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
	if !containsSubstring(stripped, "Select Model") {
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
	sel, ok := msg.(modelSelectedMsg)
	if !ok {
		t.Fatalf("expected modelSelectedMsg, got %T", msg)
	}
	if sel.model != "claude-sonnet-4-20250514" {
		t.Errorf("expected model claude-sonnet-4-20250514, got %q", sel.model)
	}
	if sel.provider != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %q", sel.provider)
	}
}

func TestRegisterModelCommand_WithUnknownModel(t *testing.T) {
	cmd := RegisterModelCommand()
	result := cmd.Execute("custom-model-v1")
	if result == nil {
		t.Fatal("expected command to return a tea.Cmd")
	}
	msg := result()
	sel, ok := msg.(modelSelectedMsg)
	if !ok {
		t.Fatalf("expected modelSelectedMsg, got %T", msg)
	}
	if sel.model != "custom-model-v1" {
		t.Errorf("expected model 'custom-model-v1', got %q", sel.model)
	}
	if sel.provider != "" {
		t.Errorf("expected empty provider for unknown model, got %q", sel.provider)
	}
}
