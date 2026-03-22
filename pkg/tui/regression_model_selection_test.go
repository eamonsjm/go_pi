package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ---------------------------------------------------------------------------
// Model selection regression tests
//
// All tests use dynamic model names from defaultModels to avoid brittleness
// when the model list changes (see gp-lq0u notes about the reverted
// TestAppModelChangeNotification that hardcoded 'claude-opus').
// ---------------------------------------------------------------------------

func TestModelSelectorFlow(t *testing.T) {
	ms := NewModelSelector(nil)
	ms.SetSize(100, 30)

	if ms.Visible() {
		t.Error("model selector should not be visible initially")
	}

	ms.Show()
	if !ms.Visible() {
		t.Error("model selector should be visible after Show()")
	}
	if ms.cursor != 0 {
		t.Errorf("cursor should be reset to 0 when shown, got %d", ms.cursor)
	}
	if ms.filter != "" {
		t.Errorf("filter should be cleared when shown, got %q", ms.filter)
	}
}

func TestModelSelectorNavigation(t *testing.T) {
	ms := NewModelSelector(nil)
	ms.SetSize(100, 30)
	ms.Show()

	if len(ms.filtered) == 0 {
		t.Fatal("no models available for testing")
	}

	ms.Update(tea.KeyMsg{Type: tea.KeyDown})
	if ms.cursor != 1 {
		t.Errorf("expected cursor at 1 after down arrow, got %d", ms.cursor)
	}

	ms.Update(tea.KeyMsg{Type: tea.KeyDown})
	if ms.cursor != 2 {
		t.Errorf("expected cursor at 2 after second down arrow, got %d", ms.cursor)
	}

	ms.Update(tea.KeyMsg{Type: tea.KeyUp})
	if ms.cursor != 1 {
		t.Errorf("expected cursor at 1 after up arrow, got %d", ms.cursor)
	}

	// Cursor should not go below 0.
	ms.cursor = 0
	ms.Update(tea.KeyMsg{Type: tea.KeyUp})
	if ms.cursor < 0 {
		t.Errorf("cursor should not go below 0, got %d", ms.cursor)
	}

	// Cursor should not go past end.
	ms.cursor = len(ms.filtered) - 1
	ms.Update(tea.KeyMsg{Type: tea.KeyDown})
	if ms.cursor >= len(ms.filtered) {
		t.Errorf("cursor should not go past models count, got %d", ms.cursor)
	}
}

func TestModelSelectorFiltering(t *testing.T) {
	ms := NewModelSelector(nil)
	ms.SetSize(100, 30)
	ms.Show()

	initialCount := len(ms.filtered)
	if initialCount == 0 {
		t.Fatal("no models to filter")
	}

	// Use a filter term that matches at least one model dynamically.
	// Find a model label that's at least 4 chars to use as filter.
	if len(ms.models) == 0 {
		t.Fatal("no models defined")
	}
	filterTerm := strings.ToLower(ms.models[0].Label[:4])

	for _, r := range filterTerm {
		ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	if ms.filter != filterTerm {
		t.Errorf("expected filter %q, got %q", filterTerm, ms.filter)
	}

	// All filtered models should match the filter term.
	for _, idx := range ms.filtered {
		opt := ms.models[idx]
		haystack := strings.ToLower(opt.Label + " " + opt.Model)
		if !strings.Contains(haystack, filterTerm) {
			t.Errorf("filtered model %q should match %q filter", opt.Label, filterTerm)
		}
	}
}

func TestModelSelectorBackspaceClear(t *testing.T) {
	ms := NewModelSelector(nil)
	ms.SetSize(100, 30)
	ms.Show()

	// Type "test".
	for _, r := range "test" {
		ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if ms.filter != "test" {
		t.Errorf("expected filter 'test', got %q", ms.filter)
	}

	// Backspace 4 times.
	for i := 0; i < 4; i++ {
		ms.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}

	if ms.filter != "" {
		t.Errorf("filter should be empty after backspacing all chars, got %q", ms.filter)
	}
}

func TestModelSelectorSelection(t *testing.T) {
	ms := NewModelSelector(nil)
	ms.SetSize(100, 30)
	ms.Show()

	if len(ms.filtered) == 0 {
		t.Fatal("no models to select")
	}

	// Get expected model from dynamic list.
	expectedModel := ms.models[ms.filtered[0]]

	cmd := ms.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should return a command")
	}

	msg := cmd()
	sel, ok := msg.(modelSelectedMsg)
	if !ok {
		t.Fatalf("expected modelSelectedMsg, got %T", msg)
	}

	if sel.model != expectedModel.Model {
		t.Errorf("expected model %q, got %q", expectedModel.Model, sel.model)
	}
	if sel.provider != expectedModel.Provider {
		t.Errorf("expected provider %q, got %q", expectedModel.Provider, sel.provider)
	}

	if ms.Visible() {
		t.Error("model selector should be hidden after selection")
	}
}

func TestModelSelectorEscape(t *testing.T) {
	ms := NewModelSelector(nil)
	ms.SetSize(100, 30)
	ms.Show()

	if !ms.Visible() {
		t.Fatal("model selector should be visible")
	}

	cmd := ms.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("Escape should return a command")
	}

	msg := cmd()
	if _, ok := msg.(modelCancelledMsg); !ok {
		t.Errorf("expected modelCancelledMsg, got %T", msg)
	}

	if ms.Visible() {
		t.Error("model selector should be hidden after Escape")
	}
}

func TestModelSelectorView(t *testing.T) {
	ms := NewModelSelector(nil)
	ms.SetSize(80, 20)

	// Hidden view should be empty.
	hiddenView := ms.View()
	if hiddenView != "" {
		t.Errorf("hidden selector view should be empty, got %d chars", len(hiddenView))
	}

	ms.Show()
	visibleView := ms.View()
	if len(visibleView) == 0 {
		t.Error("visible selector should produce non-empty view")
	}

	stripped := stripAnsi(visibleView)
	if !strings.Contains(stripped, "Select Model") {
		t.Error("view should contain 'Select Model' title")
	}
}

// ---------------------------------------------------------------------------
// Model change callback regression tests (dynamic model names)
// ---------------------------------------------------------------------------

func TestAppModelChangeNotification(t *testing.T) {
	// Use dynamic model names from defaultModels to avoid brittleness.
	if len(defaultModels) == 0 {
		t.Skip("no default models defined")
	}
	testModel := defaultModels[0]

	app := NewApp()

	callbackCalled := false
	var callbackProvider, callbackModel string
	app.SetModelChangeCallback(func(provider, model string) {
		callbackCalled = true
		callbackProvider = provider
		callbackModel = model
	})

	// Send modelSelectedMsg through Update (the proper path that updates
	// both the header and fires the callback).
	app.modelSelector.Show()
	app.Update(modelSelectedMsg{
		provider: testModel.Provider,
		model:    testModel.Model,
	})

	if !callbackCalled {
		t.Error("model change callback not called")
	}
	if callbackProvider != testModel.Provider {
		t.Errorf("callback provider: want %q, got %q", testModel.Provider, callbackProvider)
	}
	if callbackModel != testModel.Model {
		t.Errorf("callback model: want %q, got %q", testModel.Model, callbackModel)
	}
	if app.header.model != testModel.Model {
		t.Errorf("header model: want %q, got %q", testModel.Model, app.header.model)
	}
	if app.modelSelector.Visible() {
		t.Error("model selector should be hidden after selection")
	}
}

func TestAppModelChangeNotification_MultipleModels(t *testing.T) {
	// Verify the callback works for every model in the catalogue.
	app := NewApp()

	for _, model := range defaultModels {
		var gotProvider, gotModel string
		app.SetModelChangeCallback(func(p, m string) {
			gotProvider = p
			gotModel = m
		})

		app.modelSelector.Show()
		app.Update(modelSelectedMsg{
			provider: model.Provider,
			model:    model.Model,
		})

		if gotProvider != model.Provider || gotModel != model.Model {
			t.Errorf("model %q: want callback(%q, %q), got callback(%q, %q)",
				model.Label, model.Provider, model.Model, gotProvider, gotModel)
		}
		if app.header.model != model.Model {
			t.Errorf("model %q: header not updated, want %q got %q",
				model.Label, model.Model, app.header.model)
		}
	}
}

// ---------------------------------------------------------------------------
// Provider switching regression tests
// ---------------------------------------------------------------------------

func TestModelSelectorProviderSwitchTab(t *testing.T) {
	ms := NewModelSelector(nil)
	ms.Show()

	if len(ms.providers) < 2 {
		t.Skip("need at least 2 providers for Tab test")
	}

	initialProvider := ms.providers[ms.providerIdx]

	ms.Update(tea.KeyMsg{Type: tea.KeyTab})

	if ms.providerIdx != 1 {
		t.Errorf("expected providerIdx=1 after Tab, got %d", ms.providerIdx)
	}
	newProvider := ms.providers[ms.providerIdx]
	if newProvider == initialProvider {
		t.Error("expected provider to change after Tab")
	}
	// Filtered models should belong to the new provider.
	for _, idx := range ms.filtered {
		if ms.models[idx].Provider != newProvider {
			t.Errorf("model %q has provider %q, expected %q",
				ms.models[idx].Model, ms.models[idx].Provider, newProvider)
		}
	}
}

func TestModelSelectorProviderSwitchTabWraps(t *testing.T) {
	ms := NewModelSelector(nil)
	ms.Show()

	providerCount := len(ms.providers)
	if providerCount < 2 {
		t.Skip("need at least 2 providers")
	}

	// Tab through all providers to wrap back to the first.
	for i := 0; i < providerCount; i++ {
		ms.Update(tea.KeyMsg{Type: tea.KeyTab})
	}

	if ms.providerIdx != 0 {
		t.Errorf("expected providerIdx=0 after full wrap, got %d", ms.providerIdx)
	}
}

func TestModelSelectorProviderSwitchShiftTab(t *testing.T) {
	ms := NewModelSelector(nil)
	ms.Show()

	if len(ms.providers) < 2 {
		t.Skip("need at least 2 providers")
	}

	// Shift+Tab from first provider should wrap to last.
	ms.Update(tea.KeyMsg{Type: tea.KeyShiftTab})

	expected := len(ms.providers) - 1
	if ms.providerIdx != expected {
		t.Errorf("expected providerIdx=%d after ShiftTab, got %d", expected, ms.providerIdx)
	}
}

func TestModelSelectorProviderSwitchLeftRight(t *testing.T) {
	ms := NewModelSelector(nil)
	ms.Show()

	if len(ms.providers) < 2 {
		t.Skip("need at least 2 providers")
	}

	// Right should advance.
	ms.Update(tea.KeyMsg{Type: tea.KeyRight})
	if ms.providerIdx != 1 {
		t.Errorf("expected providerIdx=1 after Right, got %d", ms.providerIdx)
	}

	// Left should go back.
	ms.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if ms.providerIdx != 0 {
		t.Errorf("expected providerIdx=0 after Left, got %d", ms.providerIdx)
	}

	// Left at 0 should clamp (not wrap).
	ms.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if ms.providerIdx != 0 {
		t.Errorf("expected providerIdx=0 clamped, got %d", ms.providerIdx)
	}

	// Right at max should clamp.
	ms.providerIdx = len(ms.providers) - 1
	ms.Update(tea.KeyMsg{Type: tea.KeyRight})
	if ms.providerIdx != len(ms.providers)-1 {
		t.Errorf("expected providerIdx=%d clamped, got %d", len(ms.providers)-1, ms.providerIdx)
	}
}

func TestModelSelectorProviderSwitchNumberKeys(t *testing.T) {
	ms := NewModelSelector(nil)
	ms.Show()

	if len(ms.providers) < 2 {
		t.Skip("need at least 2 providers")
	}

	// Press '2' to jump to second provider.
	ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if ms.providerIdx != 1 {
		t.Errorf("expected providerIdx=1 after '2', got %d", ms.providerIdx)
	}

	// Press '1' to jump back to first.
	ms.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if ms.providerIdx != 0 {
		t.Errorf("expected providerIdx=0 after '1', got %d", ms.providerIdx)
	}
}

func TestModelSelectorProviderSwitchResetsModelCursor(t *testing.T) {
	ms := NewModelSelector(nil)
	ms.Show()

	if len(ms.providers) < 2 {
		t.Skip("need at least 2 providers")
	}

	// Move cursor down.
	ms.Update(tea.KeyMsg{Type: tea.KeyDown})
	if ms.cursor == 0 {
		t.Skip("cursor didn't move, can't test reset")
	}

	// Switch provider.
	ms.Update(tea.KeyMsg{Type: tea.KeyTab})

	// Cursor should reset to 0 for the new provider.
	if ms.cursor != 0 {
		t.Errorf("expected cursor=0 after provider switch, got %d", ms.cursor)
	}
}

// ---------------------------------------------------------------------------
// App integration: model command
// ---------------------------------------------------------------------------

func TestAppModelCommandIntegration(t *testing.T) {
	app := NewApp()

	cmd, ok := app.commands.Get("model")
	if !ok {
		t.Fatal("/model command not registered")
	}
	if cmd.Name != "model" {
		t.Errorf("expected command name 'model', got %q", cmd.Name)
	}
	if cmd.Description == "" {
		t.Error("command description should not be empty")
	}
}

// ---------------------------------------------------------------------------
// End-to-end model selection flow with dynamic names
// ---------------------------------------------------------------------------

func TestModelSelectorEndToEndFlow(t *testing.T) {
	if len(defaultModels) == 0 {
		t.Skip("no default models")
	}

	app := NewApp()
	app.width = 80
	app.height = 40

	var gotProvider, gotModel string
	app.SetModelChangeCallback(func(p, m string) {
		gotProvider = p
		gotModel = m
	})

	// 1. Show model selector.
	app.Update(showModelSelectorMsg{})
	if !app.modelSelector.Visible() {
		t.Fatal("expected model selector visible")
	}

	// 2. Navigate down to the second model (if available).
	targetIdx := 0
	if len(app.modelSelector.filtered) > 1 {
		app.modelSelector.Update(tea.KeyMsg{Type: tea.KeyDown})
		targetIdx = 1
	}

	// 3. Get the expected model dynamically.
	expected := app.modelSelector.models[app.modelSelector.filtered[targetIdx]]

	// 4. Select it.
	cmd := app.modelSelector.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		msg := cmd()
		app.Update(msg)
	}

	// 5. Verify.
	if gotProvider != expected.Provider || gotModel != expected.Model {
		t.Errorf("want callback(%q, %q), got callback(%q, %q)",
			expected.Provider, expected.Model, gotProvider, gotModel)
	}
	if app.header.model != expected.Model {
		t.Errorf("header model: want %q, got %q", expected.Model, app.header.model)
	}
	if app.modelSelector.Visible() {
		t.Error("selector should be hidden after selection")
	}
}
