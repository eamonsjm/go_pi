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
