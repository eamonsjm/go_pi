package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestParseSlashCommand(t *testing.T) {
	tests := []struct {
		input    string
		wantName string
		wantArgs string
	}{
		{"/model", "model", ""},
		{"/model gpt-4o", "model", "gpt-4o"},
		{"/compact", "compact", ""},
		{"/name  my session ", "name", "my session"},
		{"/resume abc-123", "resume", "abc-123"},
		{"/settings temperature 0.7", "settings", "temperature 0.7"},
	}
	for _, tt := range tests {
		name, args := parseSlashCommand(tt.input)
		if name != tt.wantName {
			t.Errorf("parseSlashCommand(%q) name = %q, want %q", tt.input, name, tt.wantName)
		}
		if args != tt.wantArgs {
			t.Errorf("parseSlashCommand(%q) args = %q, want %q", tt.input, args, tt.wantArgs)
		}
	}
}

func TestEditor_NewEditor(t *testing.T) {
	e := NewEditor()
	if e.state != editorIdle {
		t.Errorf("expected initial state editorIdle, got %d", e.state)
	}
	if e.Value() != "" {
		t.Errorf("expected empty value, got %q", e.Value())
	}
}

func TestEditor_SetState(t *testing.T) {
	e := NewEditor()
	e.ctrlCCount = 3

	e.SetState(editorRunning)
	if e.state != editorRunning {
		t.Errorf("expected editorRunning, got %d", e.state)
	}
	if e.ctrlCCount != 0 {
		t.Errorf("expected ctrlCCount reset to 0, got %d", e.ctrlCCount)
	}
}

func TestEditor_SetWidth(t *testing.T) {
	e := NewEditor()
	e.SetWidth(100)
	if e.width != 100 {
		t.Errorf("expected width 100, got %d", e.width)
	}
}

func TestEditor_SetWidthMinimum(t *testing.T) {
	e := NewEditor()
	e.SetWidth(5) // inner would be 3, should clamp to 10
	if e.width != 5 {
		t.Errorf("expected width 5, got %d", e.width)
	}
}

func TestEditor_Height(t *testing.T) {
	e := NewEditor()
	h := e.Height()
	// 3 lines + 2 for border
	if h != 5 {
		t.Errorf("expected height 5, got %d", h)
	}
}

func TestEditor_SetCommands(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	e.SetCommands(reg)
	if e.commands != reg {
		t.Error("expected commands registry to be set")
	}
}

func TestEditor_BorderStyle(t *testing.T) {
	e := NewEditor()

	e.state = editorIdle
	s := e.borderStyle()
	if s.GetBorderBottomForeground() != EditorStyle.GetBorderBottomForeground() {
		t.Error("idle state should use EditorStyle")
	}

	e.state = editorRunning
	s = e.borderStyle()
	if s.GetBorderBottomForeground() != EditorActiveStyle.GetBorderBottomForeground() {
		t.Error("running state should use EditorActiveStyle")
	}

	e.state = editorThinking
	s = e.borderStyle()
	if s.GetBorderBottomForeground() != EditorThinkingStyle.GetBorderBottomForeground() {
		t.Error("thinking state should use EditorThinkingStyle")
	}
}

func TestEditor_Update_CtrlC_Idle_SinglePress(t *testing.T) {
	e := NewEditor()
	e.state = editorIdle

	cmd := e.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Error("single Ctrl-C when idle should return nil")
	}
	if e.ctrlCCount != 1 {
		t.Errorf("expected ctrlCCount=1, got %d", e.ctrlCCount)
	}
}

func TestEditor_Update_CtrlC_Idle_DoublePress(t *testing.T) {
	e := NewEditor()
	e.state = editorIdle

	e.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	cmd := e.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("double Ctrl-C when idle should return quit command")
	}
	msg := cmd()
	if _, ok := msg.(editorQuitMsg); !ok {
		t.Errorf("expected editorQuitMsg, got %T", msg)
	}
}

func TestEditor_Update_CtrlC_Running(t *testing.T) {
	e := NewEditor()
	e.state = editorRunning

	cmd := e.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("Ctrl-C when running should return cancel command")
	}
	msg := cmd()
	if _, ok := msg.(editorCancelMsg); !ok {
		t.Errorf("expected editorCancelMsg, got %T", msg)
	}
}

func TestEditor_Update_Escape_Running(t *testing.T) {
	e := NewEditor()
	e.state = editorRunning

	cmd := e.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("Escape when running should return cancel command")
	}
	msg := cmd()
	if _, ok := msg.(editorCancelMsg); !ok {
		t.Errorf("expected editorCancelMsg, got %T", msg)
	}
}

func TestEditor_Update_Escape_Idle(t *testing.T) {
	e := NewEditor()
	e.state = editorIdle

	cmd := e.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd != nil {
		t.Error("Escape when idle should return nil")
	}
}

func TestEditor_Update_CtrlC_CountResetOnOtherKey(t *testing.T) {
	e := NewEditor()
	e.state = editorIdle

	e.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if e.ctrlCCount != 1 {
		t.Fatalf("expected ctrlCCount=1, got %d", e.ctrlCCount)
	}

	// Any other key resets the counter.
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if e.ctrlCCount != 0 {
		t.Errorf("expected ctrlCCount reset to 0 after other key, got %d", e.ctrlCCount)
	}
}

func TestEditor_Update_CtrlD_Idle_Empty(t *testing.T) {
	e := NewEditor()
	e.state = editorIdle

	cmd := e.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd == nil {
		t.Fatal("Ctrl-D when idle with empty editor should return quit command")
	}
	msg := cmd()
	if _, ok := msg.(editorQuitMsg); !ok {
		t.Errorf("expected editorQuitMsg, got %T", msg)
	}
}

func TestEditor_Update_CtrlD_Idle_WithText(t *testing.T) {
	e := NewEditor()
	e.state = editorIdle
	e.textarea.SetValue("some text")

	cmd := e.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd != nil {
		t.Error("Ctrl-D when idle with text should return nil (no quit)")
	}
}

func TestEditor_Update_CtrlD_Running(t *testing.T) {
	e := NewEditor()
	e.state = editorRunning

	cmd := e.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if cmd != nil {
		t.Error("Ctrl-D when agent is running should return nil")
	}
}

func TestEditor_CommandHint_NoCommands(t *testing.T) {
	e := NewEditor()
	// commands is nil
	hint := e.commandHint()
	if hint != "" {
		t.Errorf("expected empty hint with nil commands, got %q", hint)
	}
}

func TestEditor_CommandHint_NoSlash(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	e.SetCommands(reg)
	e.textarea.SetValue("hello")

	hint := e.commandHint()
	if hint != "" {
		t.Errorf("expected empty hint for non-slash input, got %q", hint)
	}
}

func TestEditor_CommandHint_WithMatch(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	e.SetCommands(reg)
	e.textarea.SetValue("/mod")

	hint := e.commandHint()
	stripped := stripAnsi(hint)
	if !strings.Contains(stripped, "/model") {
		t.Errorf("expected hint to contain '/model', got %q", stripped)
	}
	if !strings.Contains(stripped, "Switch model") {
		t.Errorf("expected hint to contain description, got %q", stripped)
	}
}

func TestEditor_CommandHint_NoMatch(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	e.SetCommands(reg)
	e.textarea.SetValue("/xyz")

	hint := e.commandHint()
	if hint != "" {
		t.Errorf("expected empty hint for non-matching command, got %q", hint)
	}
}

func TestEditor_CommandHint_HasSpace(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	e.SetCommands(reg)
	e.textarea.SetValue("/model gpt")

	hint := e.commandHint()
	if hint != "" {
		t.Errorf("expected empty hint when args already present, got %q", hint)
	}
}

func TestEditor_CommandHint_Multiline(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	e.SetCommands(reg)
	e.textarea.SetValue("/mod\nmore")

	hint := e.commandHint()
	if hint != "" {
		t.Errorf("expected empty hint for multiline input, got %q", hint)
	}
}

func TestEditor_SlashCommand_WhileRunning(t *testing.T) {
	e := NewEditor()
	e.state = editorRunning
	e.textarea.SetValue("/compact")

	cmd := e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("slash command while running should return a command")
	}
	msg := cmd()
	cmdMsg, ok := msg.(editorCommandMsg)
	if !ok {
		t.Fatalf("expected editorCommandMsg, got %T", msg)
	}
	if cmdMsg.name != "compact" {
		t.Errorf("expected command name 'compact', got %q", cmdMsg.name)
	}
}

func TestEditor_SlashCommand_WhileThinking(t *testing.T) {
	e := NewEditor()
	e.state = editorThinking
	e.textarea.SetValue("/clear")

	cmd := e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("slash command while thinking should return a command")
	}
	msg := cmd()
	cmdMsg, ok := msg.(editorCommandMsg)
	if !ok {
		t.Fatalf("expected editorCommandMsg, got %T", msg)
	}
	if cmdMsg.name != "clear" {
		t.Errorf("expected command name 'clear', got %q", cmdMsg.name)
	}
}

func TestEditor_Steering_NonSlash_WhileRunning(t *testing.T) {
	e := NewEditor()
	e.state = editorRunning
	e.textarea.SetValue("do something else")

	cmd := e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("non-slash text while running should return a command")
	}
	msg := cmd()
	steerMsg, ok := msg.(editorSteerMsg)
	if !ok {
		t.Fatalf("expected editorSteerMsg, got %T", msg)
	}
	if steerMsg.text != "do something else" {
		t.Errorf("expected steering text 'do something else', got %q", steerMsg.text)
	}
}

// submitText simulates typing text and pressing Enter.
func submitText(e *Editor, text string) {
	e.textarea.SetValue(text)
	e.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

func TestEditor_History_UpRecallsLast(t *testing.T) {
	e := NewEditor()
	submitText(e, "hello")

	// Up-arrow should recall "hello".
	e.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := e.Value(); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestEditor_History_DownReturnsToEmpty(t *testing.T) {
	e := NewEditor()
	submitText(e, "hello")

	e.Update(tea.KeyMsg{Type: tea.KeyUp})   // recall "hello"
	e.Update(tea.KeyMsg{Type: tea.KeyDown}) // back to empty
	if got := e.Value(); got != "" {
		t.Errorf("expected empty after down-arrow past newest, got %q", got)
	}
}

func TestEditor_History_MultipleEntries(t *testing.T) {
	e := NewEditor()
	submitText(e, "first")
	submitText(e, "second")
	submitText(e, "third")

	// Up x3 should walk back through history.
	e.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := e.Value(); got != "third" {
		t.Errorf("expected 'third', got %q", got)
	}
	e.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := e.Value(); got != "second" {
		t.Errorf("expected 'second', got %q", got)
	}
	e.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := e.Value(); got != "first" {
		t.Errorf("expected 'first', got %q", got)
	}

	// Can't go further back.
	e.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := e.Value(); got != "first" {
		t.Errorf("expected 'first' (oldest), got %q", got)
	}

	// Down walks forward.
	e.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := e.Value(); got != "second" {
		t.Errorf("expected 'second', got %q", got)
	}
	e.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := e.Value(); got != "third" {
		t.Errorf("expected 'third', got %q", got)
	}
	e.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := e.Value(); got != "" {
		t.Errorf("expected empty after down past newest, got %q", got)
	}
}

func TestEditor_History_SkipsDuplicates(t *testing.T) {
	e := NewEditor()
	submitText(e, "same")
	submitText(e, "same")
	submitText(e, "same")

	if len(e.history) != 1 {
		t.Errorf("expected 1 history entry (deduped), got %d", len(e.history))
	}
}

func TestEditor_History_PreservesDraft(t *testing.T) {
	e := NewEditor()
	submitText(e, "old message")

	// User starts typing a new message, then presses up.
	e.textarea.SetValue("work in prog")
	e.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := e.Value(); got != "old message" {
		t.Errorf("expected 'old message', got %q", got)
	}

	// Down should restore the draft.
	e.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := e.Value(); got != "work in prog" {
		t.Errorf("expected draft 'work in prog', got %q", got)
	}
}

func TestEditor_History_UpNoopWhenEmpty(t *testing.T) {
	e := NewEditor()
	// No history — up-arrow should be a no-op (falls through to textarea).
	cmd := e.Update(tea.KeyMsg{Type: tea.KeyUp})
	// Should not panic or change value.
	if got := e.Value(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	_ = cmd
}

// ---------------------------------------------------------------------------
// Tab completion
// ---------------------------------------------------------------------------

func TestEditor_Tab_UniqueMatch(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "compact", Description: "Compact context"})
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	e.SetCommands(reg)
	e.textarea.SetValue("/com")

	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	// Unique match should complete with trailing space.
	if got := e.Value(); got != "/compact " {
		t.Errorf("expected '/compact ' (with space), got %q", got)
	}
}

func TestEditor_Tab_MultipleMatches_Cycles(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	reg.Register(&SlashCommand{Name: "module", Description: "Load module"})
	reg.Register(&SlashCommand{Name: "compact", Description: "Compact context"})
	e.SetCommands(reg)
	e.textarea.SetValue("/mod")

	// First Tab — first match (alphabetically: model).
	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := e.Value(); got != "/model" {
		t.Errorf("expected '/model', got %q", got)
	}

	// Second Tab — second match (module).
	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := e.Value(); got != "/module" {
		t.Errorf("expected '/module', got %q", got)
	}

	// Third Tab — wraps back to first.
	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := e.Value(); got != "/model" {
		t.Errorf("expected '/model' (wrap), got %q", got)
	}
}

func TestEditor_Tab_NoMatch(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	e.SetCommands(reg)
	e.textarea.SetValue("/xyz")

	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	// No match — text unchanged.
	if got := e.Value(); got != "/xyz" {
		t.Errorf("expected '/xyz' unchanged, got %q", got)
	}
}

func TestEditor_Tab_NoCommands(t *testing.T) {
	e := NewEditor()
	e.textarea.SetValue("/foo")

	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := e.Value(); got != "/foo" {
		t.Errorf("expected '/foo' unchanged, got %q", got)
	}
}

func TestEditor_Tab_NotSlash(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	e.SetCommands(reg)
	e.textarea.SetValue("hello")

	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	// Non-slash text — Tab should not alter content.
	if got := e.Value(); got != "hello" {
		t.Errorf("expected 'hello' unchanged, got %q", got)
	}
}

func TestEditor_Tab_AlreadyHasArgs(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	e.SetCommands(reg)
	e.textarea.SetValue("/model gpt")

	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	// Space already present — no completion.
	if got := e.Value(); got != "/model gpt" {
		t.Errorf("expected '/model gpt' unchanged, got %q", got)
	}
}

func TestEditor_Tab_CycleResetOnOtherKey(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	reg.Register(&SlashCommand{Name: "module", Description: "Load module"})
	e.SetCommands(reg)
	e.textarea.SetValue("/mod")

	// Start cycling.
	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := e.Value(); got != "/model" {
		t.Fatalf("expected '/model', got %q", got)
	}

	// Type a character — resets tab state.
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})

	// Verify tab state was cleared.
	if e.tabMatches != nil {
		t.Error("expected tabMatches to be nil after non-Tab key")
	}
}

func TestEditor_Tab_SpaceAfterUniqueCompletion(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "login", Description: "Log in"})
	e.SetCommands(reg)
	e.textarea.SetValue("/lo")

	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	got := e.Value()
	if got != "/login " {
		t.Errorf("expected '/login ' with trailing space, got %q", got)
	}
}

func TestEditor_Tab_WorksWhileRunning(t *testing.T) {
	e := NewEditor()
	e.state = editorRunning
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "compact", Description: "Compact context"})
	e.SetCommands(reg)
	e.textarea.SetValue("/com")

	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := e.Value(); got != "/compact " {
		t.Errorf("expected '/compact ' while running, got %q", got)
	}
}

func TestEditor_History_DownNoopWhenNotInHistory(t *testing.T) {
	e := NewEditor()
	submitText(e, "hello")

	// Without pressing up first, down-arrow should not alter the value.
	e.textarea.SetValue("current")
	e.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := e.Value(); got != "current" {
		t.Errorf("expected 'current' unchanged, got %q", got)
	}
}
