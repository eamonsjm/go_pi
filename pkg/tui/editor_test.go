package tui

import (
	"fmt"
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

	st := Styles()

	e.state = editorIdle
	s := e.borderStyle()
	if s.GetBorderBottomForeground() != st.EditorStyle.GetBorderBottomForeground() {
		t.Error("idle state should use EditorStyle")
	}

	e.state = editorRunning
	s = e.borderStyle()
	if s.GetBorderBottomForeground() != st.EditorActiveStyle.GetBorderBottomForeground() {
		t.Error("running state should use EditorActiveStyle")
	}

	e.state = editorThinking
	s = e.borderStyle()
	if s.GetBorderBottomForeground() != st.EditorThinkingStyle.GetBorderBottomForeground() {
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

func TestEditor_CommandHint_MultipleMatches_OnePerLine(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	reg.Register(&SlashCommand{Name: "module", Description: "Load module"})
	e.SetCommands(reg)
	e.textarea.SetValue("/mod")

	hint := e.commandHint()
	stripped := stripAnsi(hint)
	lines := strings.Split(stripped, "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 hint lines (one per command), got %d: %q", len(lines), stripped)
	}
}

func TestEditor_Height_IncludesHintLines(t *testing.T) {
	e := NewEditor()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	reg.Register(&SlashCommand{Name: "module", Description: "Load module"})
	e.SetCommands(reg)

	baseH := e.Height() // no hint active
	if baseH != 5 {
		t.Fatalf("expected base height 5, got %d", baseH)
	}

	e.textarea.SetValue("/mod") // triggers 2-line hint
	hintH := e.Height()
	// 5 (base) + 2 hint lines + 0 (newline counted in View, not Height)
	// Actually: 2 hint lines = strings.Count("\n")+1 = 2, so 5+2=7
	if hintH != 7 {
		t.Errorf("expected height 7 with 2 hint lines, got %d", hintH)
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

// ---------------------------------------------------------------------------
// Kill ring tests
// ---------------------------------------------------------------------------

func TestKillRing_PushAndCurrent(t *testing.T) {
	var kr killRing
	kr.push("hello", killForward)
	if got := kr.current(); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestKillRing_PushEmpty(t *testing.T) {
	var kr killRing
	kr.push("", killForward)
	if got := kr.current(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestKillRing_AccumulateForward(t *testing.T) {
	var kr killRing
	kr.push("hello", killForward)
	kr.push(" world", killForward)
	if got := kr.current(); got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}

func TestKillRing_AccumulateBackward(t *testing.T) {
	var kr killRing
	kr.push("world", killBackward)
	kr.push("hello ", killBackward)
	if got := kr.current(); got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}

func TestKillRing_DirectionChangeBreaksAccumulation(t *testing.T) {
	var kr killRing
	kr.push("hello", killForward)
	kr.push("world", killBackward)
	if got := kr.current(); got != "world" {
		t.Errorf("expected 'world', got %q", got)
	}
}

func TestKillRing_Prev(t *testing.T) {
	var kr killRing
	kr.push("first", killForward)
	kr.resetDirection()
	kr.push("second", killForward)
	kr.resetDirection()
	kr.push("third", killForward)

	if got := kr.current(); got != "third" {
		t.Errorf("expected 'third', got %q", got)
	}
	if got := kr.prev(); got != "second" {
		t.Errorf("expected 'second', got %q", got)
	}
	if got := kr.prev(); got != "first" {
		t.Errorf("expected 'first', got %q", got)
	}
	// Wraps around.
	if got := kr.prev(); got != "third" {
		t.Errorf("expected 'third' (wrap), got %q", got)
	}
}

func TestKillRing_MaxSize(t *testing.T) {
	var kr killRing
	for i := 0; i < maxKillRingSize+10; i++ {
		kr.resetDirection()
		kr.push(fmt.Sprintf("entry-%d", i), killForward)
	}
	if len(kr.entries) != maxKillRingSize {
		t.Errorf("expected %d entries, got %d", maxKillRingSize, len(kr.entries))
	}
}

// ---------------------------------------------------------------------------
// Undo stack tests
// ---------------------------------------------------------------------------

func TestUndoStack_PushPop(t *testing.T) {
	var u undoStack
	u.push("hello", 0, 5)
	entry, ok := u.pop()
	if !ok {
		t.Fatal("expected pop to succeed")
	}
	if entry.value != "hello" || entry.row != 0 || entry.col != 5 {
		t.Errorf("unexpected entry: %+v", entry)
	}
}

func TestUndoStack_DeduplicatesConsecutive(t *testing.T) {
	var u undoStack
	u.push("hello", 0, 5)
	u.push("hello", 0, 5)
	u.push("hello", 0, 6) // same value, different pos — still deduped
	if len(u.entries) != 1 {
		t.Errorf("expected 1 entry (deduped), got %d", len(u.entries))
	}
}

func TestUndoStack_PopEmpty(t *testing.T) {
	var u undoStack
	_, ok := u.pop()
	if ok {
		t.Error("expected pop to fail on empty stack")
	}
}

func TestUndoStack_MaxDepth(t *testing.T) {
	var u undoStack
	for i := 0; i < maxUndoDepth+10; i++ {
		u.push(fmt.Sprintf("state-%d", i), 0, i)
	}
	if len(u.entries) != maxUndoDepth {
		t.Errorf("expected %d entries, got %d", maxUndoDepth, len(u.entries))
	}
}

// ---------------------------------------------------------------------------
// diffKilled tests
// ---------------------------------------------------------------------------

func TestDiffKilled_EndOfLine(t *testing.T) {
	got := diffKilled("hello world", "hello")
	if got != " world" {
		t.Errorf("expected ' world', got %q", got)
	}
}

func TestDiffKilled_StartOfLine(t *testing.T) {
	got := diffKilled("hello world", "world")
	if got != "hello " {
		t.Errorf("expected 'hello ', got %q", got)
	}
}

func TestDiffKilled_Middle(t *testing.T) {
	got := diffKilled("hello beautiful world", "hello world")
	if got != "beautiful " {
		t.Errorf("expected 'beautiful ', got %q", got)
	}
}

func TestDiffKilled_NoChange(t *testing.T) {
	got := diffKilled("hello", "hello")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestDiffKilled_Newline(t *testing.T) {
	got := diffKilled("line1\nline2", "line1line2")
	if got != "\n" {
		t.Errorf("expected newline, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Editor yank/undo integration
// ---------------------------------------------------------------------------

func TestEditor_KillAndYank(t *testing.T) {
	e := NewEditor()
	e.textarea.Focus()
	e.textarea.SetValue("hello world")
	e.textarea.CursorEnd()

	// Ctrl+U kills to start of line → "hello world" killed, editor empty.
	e.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	if got := e.Value(); got != "" {
		t.Errorf("after Ctrl+U expected empty, got %q", got)
	}
	if got := e.kills.current(); got != "hello world" {
		t.Errorf("kill ring should contain 'hello world', got %q", got)
	}

	// Ctrl+Y yanks it back.
	e.Update(tea.KeyMsg{Type: tea.KeyCtrlY})
	if got := e.Value(); got != "hello world" {
		t.Errorf("after yank expected 'hello world', got %q", got)
	}
}

func TestEditor_Undo(t *testing.T) {
	e := NewEditor()
	e.textarea.Focus()
	e.textarea.SetValue("initial")

	// Type a character — undo should snapshot "initial" first.
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'X'}})
	if !strings.Contains(e.Value(), "X") {
		t.Fatal("expected X to be inserted")
	}

	// Undo should restore to "initial".
	e.Update(tea.KeyMsg{Type: tea.KeyCtrlUnderscore})
	if got := e.Value(); got != "initial" {
		t.Errorf("after undo expected 'initial', got %q", got)
	}
}

func TestEditor_CtrlD_ForwardDelete(t *testing.T) {
	e := NewEditor()
	e.textarea.Focus()
	e.state = editorIdle
	e.textarea.SetValue("abc")
	e.textarea.CursorStart()

	// Ctrl+D should delete 'a' (forward delete), not quit.
	cmd := e.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	got := e.Value()
	if got == "abc" {
		t.Error("Ctrl+D should have deleted a character, value unchanged")
	}
	// Should not return a quit message.
	if cmd != nil {
		msg := cmd()
		if _, isQuit := msg.(editorQuitMsg); isQuit {
			t.Error("Ctrl+D with text should not quit")
		}
	}
}

// ---------------------------------------------------------------------------
// classifyKillKey / isAltY / isEditingKey
// ---------------------------------------------------------------------------

func TestClassifyKillKey(t *testing.T) {
	tests := []struct {
		msg  tea.KeyMsg
		want killDirection
	}{
		{tea.KeyMsg{Type: tea.KeyCtrlK}, killForward},
		{tea.KeyMsg{Type: tea.KeyCtrlU}, killBackward},
		{tea.KeyMsg{Type: tea.KeyCtrlW}, killBackward},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}, Alt: true}, killForward},
		{tea.KeyMsg{Type: tea.KeyDelete, Alt: true}, killForward},
		{tea.KeyMsg{Type: tea.KeyBackspace, Alt: true}, killBackward},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}, killNone},
		{tea.KeyMsg{Type: tea.KeyEnter}, killNone},
	}
	for _, tt := range tests {
		if got := classifyKillKey(tt.msg); got != tt.want {
			t.Errorf("classifyKillKey(%v) = %d, want %d", tt.msg, got, tt.want)
		}
	}
}

func TestIsAltY(t *testing.T) {
	if !isAltY(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}, Alt: true}) {
		t.Error("Alt+y should be detected")
	}
	if isAltY(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}) {
		t.Error("plain y should not be Alt+Y")
	}
	if isAltY(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}, Alt: true}) {
		t.Error("Alt+n should not be Alt+Y")
	}
}

func TestIsEditingKey(t *testing.T) {
	if isEditingKey(tea.KeyMsg{Type: tea.KeyUp}) {
		t.Error("Up arrow should not be an editing key")
	}
	if isEditingKey(tea.KeyMsg{Type: tea.KeyLeft}) {
		t.Error("Left arrow should not be an editing key")
	}
	if !isEditingKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}) {
		t.Error("character 'a' should be an editing key")
	}
	if !isEditingKey(tea.KeyMsg{Type: tea.KeyBackspace}) {
		t.Error("backspace should be an editing key")
	}
	if !isEditingKey(tea.KeyMsg{Type: tea.KeyCtrlK}) {
		t.Error("Ctrl+K should be an editing key")
	}
}

// ---------------------------------------------------------------------------
// Shell command execution
// ---------------------------------------------------------------------------

func TestExecuteShellCommand_BangCommand(t *testing.T) {
	cmd := executeShellCommand("!echo hello")
	if cmd == nil {
		t.Fatal("executeShellCommand should return a command")
	}
	msg := cmd()
	shellMsg, ok := msg.(editorShellResultMsg)
	if !ok {
		t.Fatalf("expected editorShellResultMsg, got %T", msg)
	}
	if shellMsg.sendToAI != true {
		t.Errorf("expected sendToAI=true for ! command, got %v", shellMsg.sendToAI)
	}
	if shellMsg.command != "echo hello" {
		t.Errorf("expected command 'echo hello', got %q", shellMsg.command)
	}
	if shellMsg.errorMsg != "" {
		t.Errorf("expected no error, got %q", shellMsg.errorMsg)
	}
	if !strings.Contains(shellMsg.output, "hello") {
		t.Errorf("expected output containing 'hello', got %q", shellMsg.output)
	}
}

func TestExecuteShellCommand_DoubleBangCommand(t *testing.T) {
	cmd := executeShellCommand("!!echo world")
	if cmd == nil {
		t.Fatal("executeShellCommand should return a command")
	}
	msg := cmd()
	shellMsg, ok := msg.(editorShellResultMsg)
	if !ok {
		t.Fatalf("expected editorShellResultMsg, got %T", msg)
	}
	if shellMsg.sendToAI != false {
		t.Errorf("expected sendToAI=false for !! command, got %v", shellMsg.sendToAI)
	}
	if shellMsg.command != "echo world" {
		t.Errorf("expected command 'echo world', got %q", shellMsg.command)
	}
	if shellMsg.errorMsg != "" {
		t.Errorf("expected no error, got %q", shellMsg.errorMsg)
	}
	if !strings.Contains(shellMsg.output, "world") {
		t.Errorf("expected output containing 'world', got %q", shellMsg.output)
	}
}

func TestExecuteShellCommand_NoCommand(t *testing.T) {
	cmd := executeShellCommand("!   ")
	if cmd == nil {
		t.Fatal("executeShellCommand should return a command")
	}
	msg := cmd()
	shellMsg, ok := msg.(editorShellResultMsg)
	if !ok {
		t.Fatalf("expected editorShellResultMsg, got %T", msg)
	}
	if shellMsg.errorMsg == "" {
		t.Error("expected error for empty command")
	}
}

func TestExecuteShellCommand_FailedCommand(t *testing.T) {
	cmd := executeShellCommand("!false")
	if cmd == nil {
		t.Fatal("executeShellCommand should return a command")
	}
	msg := cmd()
	shellMsg, ok := msg.(editorShellResultMsg)
	if !ok {
		t.Fatalf("expected editorShellResultMsg, got %T", msg)
	}
	if shellMsg.errorMsg == "" {
		t.Error("expected error for failed command")
	}
}

func TestEditor_Update_ShellCommand_Bang(t *testing.T) {
	e := NewEditor()
	e.state = editorIdle
	e.textarea.SetValue("!echo test")

	cmd := e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("shell command should return a command")
	}
	msg := cmd()
	shellMsg, ok := msg.(editorShellResultMsg)
	if !ok {
		t.Fatalf("expected editorShellResultMsg, got %T", msg)
	}
	if shellMsg.sendToAI != true {
		t.Errorf("expected sendToAI=true, got %v", shellMsg.sendToAI)
	}
	if !strings.Contains(shellMsg.output, "test") {
		t.Errorf("expected output containing 'test', got %q", shellMsg.output)
	}
}

func TestEditor_Update_ShellCommand_DoubleBang(t *testing.T) {
	e := NewEditor()
	e.state = editorIdle
	e.textarea.SetValue("!!echo local")

	cmd := e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("shell command should return a command")
	}
	msg := cmd()
	shellMsg, ok := msg.(editorShellResultMsg)
	if !ok {
		t.Fatalf("expected editorShellResultMsg, got %T", msg)
	}
	if shellMsg.sendToAI != false {
		t.Errorf("expected sendToAI=false, got %v", shellMsg.sendToAI)
	}
	if !strings.Contains(shellMsg.output, "local") {
		t.Errorf("expected output containing 'local', got %q", shellMsg.output)
	}
}

// ---------------------------------------------------------------------------
// Reverse history search (ctrl+r)
// ---------------------------------------------------------------------------

func TestEditor_EnterSearchMode(t *testing.T) {
	e := NewEditor()
	e.textarea.SetValue("draft text")

	prompts := []string{"hello world", "foo bar", "hello again"}
	e.EnterSearchMode(prompts)

	if !e.IsSearching() {
		t.Fatal("expected searching=true")
	}
	if e.searchDraft != "draft text" {
		t.Errorf("expected searchDraft=%q, got %q", "draft text", e.searchDraft)
	}
	// First prompt should be shown.
	if e.Value() != "hello world" {
		t.Errorf("expected textarea=%q, got %q", "hello world", e.Value())
	}
}

func TestEditor_SearchFilter(t *testing.T) {
	e := NewEditor()
	prompts := []string{"hello world", "foo bar", "hello again", "baz"}
	e.EnterSearchMode(prompts)

	// Type "hello" to filter.
	e.updateSearch(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})

	if len(e.searchResults) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(e.searchResults))
	}
	if e.Value() != "hello world" {
		t.Errorf("expected first match %q, got %q", "hello world", e.Value())
	}
}

func TestEditor_SearchNextMatch(t *testing.T) {
	e := NewEditor()
	prompts := []string{"hello world", "foo", "hello again"}
	e.EnterSearchMode(prompts)

	// Filter to "hello".
	e.updateSearch(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	if e.Value() != "hello world" {
		t.Fatalf("expected first match %q, got %q", "hello world", e.Value())
	}

	// Ctrl+R to go to next match.
	e.updateSearch(tea.KeyMsg{Type: tea.KeyCtrlR})
	if e.Value() != "hello again" {
		t.Errorf("expected second match %q, got %q", "hello again", e.Value())
	}

	// Wrap around.
	e.updateSearch(tea.KeyMsg{Type: tea.KeyCtrlR})
	if e.Value() != "hello world" {
		t.Errorf("expected wrap to first match %q, got %q", "hello world", e.Value())
	}
}

func TestEditor_SearchAccept(t *testing.T) {
	e := NewEditor()
	e.textarea.SetValue("draft")
	prompts := []string{"accepted prompt", "other"}
	e.EnterSearchMode(prompts)

	cmd := e.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	if e.IsSearching() {
		t.Error("expected searching=false after Enter")
	}
	if cmd == nil {
		t.Fatal("expected a command from Enter")
	}
	msg := cmd()
	submit, ok := msg.(editorSubmitMsg)
	if !ok {
		t.Fatalf("expected editorSubmitMsg, got %T", msg)
	}
	if submit.text != "accepted prompt" {
		t.Errorf("expected submitted %q, got %q", "accepted prompt", submit.text)
	}
}

func TestEditor_SearchCancel(t *testing.T) {
	e := NewEditor()
	e.textarea.SetValue("my draft")
	prompts := []string{"other"}
	e.EnterSearchMode(prompts)

	if e.Value() != "other" {
		t.Fatalf("expected search match, got %q", e.Value())
	}

	e.updateSearch(tea.KeyMsg{Type: tea.KeyEscape})
	if e.IsSearching() {
		t.Error("expected searching=false after Escape")
	}
	if e.Value() != "my draft" {
		t.Errorf("expected draft restored %q, got %q", "my draft", e.Value())
	}
}

func TestEditor_SearchBackspace(t *testing.T) {
	e := NewEditor()
	prompts := []string{"hello", "help", "world"}
	e.EnterSearchMode(prompts)

	e.updateSearch(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hel")})
	if len(e.searchResults) != 2 {
		t.Fatalf("expected 2 matches for 'hel', got %d", len(e.searchResults))
	}

	// Backspace widens the search.
	e.updateSearch(tea.KeyMsg{Type: tea.KeyBackspace})
	if e.searchQuery != "he" {
		t.Errorf("expected query %q after backspace, got %q", "he", e.searchQuery)
	}
	if len(e.searchResults) != 2 {
		t.Errorf("expected 2 matches for 'he', got %d", len(e.searchResults))
	}
}

func TestEditor_SearchNoMatches(t *testing.T) {
	e := NewEditor()
	prompts := []string{"hello", "world"}
	e.EnterSearchMode(prompts)

	e.updateSearch(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("xyz")})
	if len(e.searchResults) != 0 {
		t.Errorf("expected 0 matches, got %d", len(e.searchResults))
	}
	if e.Value() != "" {
		t.Errorf("expected empty textarea, got %q", e.Value())
	}
}

func TestEditor_SearchHint(t *testing.T) {
	e := NewEditor()
	if e.searchHint() != "" {
		t.Error("expected empty hint when not searching")
	}

	prompts := []string{"hello", "world"}
	e.EnterSearchMode(prompts)

	hint := e.searchHint()
	if !strings.Contains(hint, "reverse-search") {
		t.Errorf("expected hint to contain 'reverse-search', got %q", hint)
	}
	if !strings.Contains(hint, "1/2") {
		t.Errorf("expected hint to show match count, got %q", hint)
	}
}

func TestEditor_SearchEmptyPrompts(t *testing.T) {
	e := NewEditor()
	e.textarea.SetValue("draft")
	e.EnterSearchMode(nil)

	if !e.IsSearching() {
		t.Error("expected searching=true even with nil prompts")
	}
	// Enter on empty results should not panic.
	cmd := e.updateSearch(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("expected nil cmd when no match selected")
	}
}

func TestEditor_SearchCtrlRReenters(t *testing.T) {
	e := NewEditor()
	prompts := []string{"a", "b", "c"}
	e.EnterSearchMode(prompts)

	if e.Value() != "a" {
		t.Fatalf("expected first prompt, got %q", e.Value())
	}

	// Calling EnterSearchMode again (ctrl+r while searching) advances.
	e.EnterSearchMode(prompts)
	if e.Value() != "b" {
		t.Errorf("expected second prompt after re-enter, got %q", e.Value())
	}
}
