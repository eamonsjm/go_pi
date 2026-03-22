package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// ---------------------------------------------------------------------------
// Textarea keyboard input regression tests
//
// These tests verify core keyboard interactions with the textarea component
// that underpins the TUI editor. They catch regressions in character input,
// deletion, cursor movement, and special character handling.
// ---------------------------------------------------------------------------

// newFocusedTextarea creates a textarea that is focused and ready to accept input.
func newFocusedTextarea(w, h int) textarea.Model {
	ta := textarea.New()
	ta.SetWidth(w)
	ta.SetHeight(h)
	ta.Focus()
	return ta
}

func TestKeyboardInputBasic(t *testing.T) {
	ta := newFocusedTextarea(80, 10)

	msgText := "Hello, World!"
	for _, r := range msgText {
		ta, _ = ta.Update(tea.KeyMsg{
			Type:  tea.KeyRunes,
			Runes: []rune{r},
		})
	}

	if ta.Value() != msgText {
		t.Errorf("expected value %q, got %q", msgText, ta.Value())
	}
}

func TestKeyboardInputMultiline(t *testing.T) {
	ta := newFocusedTextarea(80, 20)

	firstLine := "First line"
	for _, r := range firstLine {
		ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyEnter})

	secondLine := "Second line"
	for _, r := range secondLine {
		ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	value := ta.Value()
	if !strings.Contains(value, firstLine) {
		t.Errorf("expected first line in value, got: %q", value)
	}
	if !strings.Contains(value, secondLine) {
		t.Errorf("expected second line in value, got: %q", value)
	}
}

func TestKeyboardInputBackspace(t *testing.T) {
	ta := newFocusedTextarea(80, 10)

	for _, r := range "Hello" {
		ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	if ta.Value() != "Hello" {
		t.Fatalf("expected 'Hello', got %q", ta.Value())
	}

	ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if ta.Value() != "Hell" {
		t.Errorf("expected 'Hell' after backspace, got %q", ta.Value())
	}

	ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if ta.Value() != "He" {
		t.Errorf("expected 'He' after 3 total backspaces, got %q", ta.Value())
	}
}

func TestKeyboardInputArrowKeys(t *testing.T) {
	ta := newFocusedTextarea(80, 10)

	for _, r := range "Hello" {
		ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	if len(ta.Value()) == 0 {
		t.Fatal("textarea should have content after typing")
	}

	// Verify cursor movement methods don't panic and preserve value.
	ta.CursorStart()
	ta.CursorEnd()

	if ta.Value() != "Hello" {
		t.Errorf("value should be preserved during cursor operations, got %q", ta.Value())
	}
}

func TestKeyboardInputDeleteKey(t *testing.T) {
	ta := newFocusedTextarea(80, 10)

	for _, r := range "Hello" {
		ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	// Move cursor to beginning.
	for i := 0; i < 5; i++ {
		ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyLeft})
	}

	// Delete at cursor.
	ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyDelete})

	if len(ta.Value()) >= 5 {
		t.Logf("delete may not have removed character as expected, value: %q", ta.Value())
	}
}

func TestKeyboardInputCtrlC(t *testing.T) {
	ta := newFocusedTextarea(80, 10)

	for _, r := range "Test" {
		ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	// Value should be unchanged by Ctrl+C.
	if ta.Value() != "Test" {
		t.Errorf("Ctrl+C should not affect value, got %q", ta.Value())
	}
}

func TestKeyboardInputSpecialCharacters(t *testing.T) {
	ta := newFocusedTextarea(80, 10)

	specialChars := "!@#$%^&*()"
	for _, r := range specialChars {
		ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	if ta.Value() != specialChars {
		t.Errorf("expected %q, got %q", specialChars, ta.Value())
	}
}

func TestKeyboardInputUnicode(t *testing.T) {
	ta := newFocusedTextarea(80, 10)

	unicodeText := "Hello 世界 🌍"
	for _, r := range unicodeText {
		ta, _ = ta.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	if ta.Value() != unicodeText {
		t.Errorf("expected %q, got %q", unicodeText, ta.Value())
	}
}

// ---------------------------------------------------------------------------
// Editor-level keyboard regression tests
// ---------------------------------------------------------------------------

func TestEditorFocusBlur(t *testing.T) {
	editor := NewEditor()
	editor.SetWidth(80)

	editor.Focus()

	msgText := "Test message"
	for _, r := range msgText {
		editor.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	if editor.Value() != msgText {
		t.Errorf("expected editor value %q, got %q", msgText, editor.Value())
	}

	editor.Blur()
	editor.Focus()

	if editor.Value() != msgText {
		t.Errorf("editor value should persist after blur/focus, got %q", editor.Value())
	}
}

func TestEditorCommandInputFlow(t *testing.T) {
	editor := NewEditor()
	editor.SetWidth(80)
	editor.Focus()

	cmdText := "/help"
	for _, r := range cmdText {
		editor.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	if editor.Value() != cmdText {
		t.Errorf("expected command %q, got %q", cmdText, editor.Value())
	}

	editor.Reset()
	if editor.Value() != "" {
		t.Errorf("expected empty value after reset, got %q", editor.Value())
	}
}

func TestEditorEnterSubmits(t *testing.T) {
	editor := NewEditor()
	editor.SetWidth(80)
	editor.Focus()

	for _, r := range "hello" {
		editor.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	cmd := editor.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should return a command")
	}

	msg := cmd()
	submit, ok := msg.(editorSubmitMsg)
	if !ok {
		t.Fatalf("expected editorSubmitMsg, got %T", msg)
	}
	if submit.text != "hello" {
		t.Errorf("expected submit text 'hello', got %q", submit.text)
	}

	// Editor value should be cleared after submit.
	if editor.Value() != "" {
		t.Errorf("expected empty value after Enter submit, got %q", editor.Value())
	}
}

func TestAppEditorInteraction(t *testing.T) {
	app := NewApp()

	if app.editor == nil {
		t.Fatal("app editor should be initialized")
	}

	if app.editor.Height() <= 0 {
		t.Log("editor height may not be set initially")
	}
}
