package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// ---------------------------------------------------------------------------
// Test harness — wraps Editor in a tea.Model for use with teatest
// ---------------------------------------------------------------------------

// editorHarness wraps an Editor so teatest can drive it as a full tea.Program.
// It captures messages produced by the Editor's commands for test assertions.
type editorHarness struct {
	editor    *Editor
	submitted []string
	steered   []string
	commands  []editorCommandMsg
	cancelled int
	quitCount int
}

func newEditorHarness() *editorHarness {
	e := NewEditor()
	e.SetWidth(80)
	e.Focus()
	return &editorHarness{editor: e}
}

func newEditorHarnessWithCommands() *editorHarness {
	h := newEditorHarness()
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "compact", Description: "Compact context"})
	reg.Register(&SlashCommand{Name: "model", Description: "Switch model"})
	reg.Register(&SlashCommand{Name: "module", Description: "Load module"})
	reg.Register(&SlashCommand{Name: "clear", Description: "Clear chat"})
	reg.Register(&SlashCommand{Name: "login", Description: "Log in"})
	h.editor.SetCommands(reg)
	return h
}

// searchActivateMsg is a custom message to activate search mode within the
// test harness, since EnterSearchMode requires a prompts list that we can't
// easily pass via key events alone.
type searchActivateMsg struct {
	prompts []string
}

func (h *editorHarness) Init() tea.Cmd { return nil }

func (h *editorHarness) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if sa, ok := msg.(searchActivateMsg); ok {
		h.editor.EnterSearchMode(sa.prompts)
		return h, nil
	}

	cmd := h.editor.Update(msg)
	if cmd == nil {
		return h, nil
	}
	result := cmd()
	switch r := result.(type) {
	case editorSubmitMsg:
		h.submitted = append(h.submitted, r.text)
	case editorSteerMsg:
		h.steered = append(h.steered, r.text)
	case editorCommandMsg:
		h.commands = append(h.commands, r)
	case editorCancelMsg:
		h.cancelled++
	case editorQuitMsg:
		h.quitCount++
		return h, tea.Quit
	}
	return h, nil
}

func (h *editorHarness) View() string {
	return h.editor.View()
}

// finalEditorHarness retrieves the harness from the final model with a timeout.
func finalEditorHarness(t *testing.T, tm *teatest.TestModel) *editorHarness {
	t.Helper()
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second))
	h, ok := fm.(*editorHarness)
	if !ok {
		t.Fatalf("expected *editorHarness, got %T", fm)
	}
	return h
}

// settle gives the program a moment to process pending messages.
func settle() { time.Sleep(50 * time.Millisecond) }

// quitHarness sends double Ctrl+C to trigger quit in idle editor.
func quitHarness(tm *teatest.TestModel) {
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
}

// ---------------------------------------------------------------------------
// Tests: Text entry and submission
// ---------------------------------------------------------------------------

func TestTeatest_TypeAndSubmit(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Type("hello world")
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.submitted) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(got.submitted))
	}
	if got.submitted[0] != "hello world" {
		t.Errorf("expected submitted text %q, got %q", "hello world", got.submitted[0])
	}
}

func TestTeatest_EmptySubmitIgnored(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.submitted) != 0 {
		t.Errorf("expected 0 submissions for empty input, got %d", len(got.submitted))
	}
}

func TestTeatest_MultipleSubmissions(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	for _, text := range []string{"first", "second", "third"} {
		tm.Type(text)
		settle()
		tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
		settle()
	}
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.submitted) != 3 {
		t.Fatalf("expected 3 submissions, got %d", len(got.submitted))
	}
	want := []string{"first", "second", "third"}
	for i, w := range want {
		if got.submitted[i] != w {
			t.Errorf("submission[%d] = %q, want %q", i, got.submitted[i], w)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: Slash command entry
// ---------------------------------------------------------------------------

func TestTeatest_SlashCommandSubmission(t *testing.T) {
	t.Parallel()
	h := newEditorHarnessWithCommands()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Type("/compact")
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(got.commands))
	}
	if got.commands[0].name != "compact" {
		t.Errorf("expected command name %q, got %q", "compact", got.commands[0].name)
	}
	if got.commands[0].args != "" {
		t.Errorf("expected empty args, got %q", got.commands[0].args)
	}
}

func TestTeatest_SlashCommandWithArgs(t *testing.T) {
	t.Parallel()
	h := newEditorHarnessWithCommands()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Type("/model gpt-4o")
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(got.commands))
	}
	if got.commands[0].name != "model" {
		t.Errorf("expected command name %q, got %q", "model", got.commands[0].name)
	}
	if got.commands[0].args != "gpt-4o" {
		t.Errorf("expected args %q, got %q", "gpt-4o", got.commands[0].args)
	}
}

// ---------------------------------------------------------------------------
// Tests: Tab completion
// ---------------------------------------------------------------------------

func TestTeatest_TabCompletionUnique(t *testing.T) {
	t.Parallel()
	h := newEditorHarnessWithCommands()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Type("/com")
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.commands) != 1 {
		t.Fatalf("expected 1 command after tab completion, got %d", len(got.commands))
	}
	if got.commands[0].name != "compact" {
		t.Errorf("expected tab-completed command %q, got %q", "compact", got.commands[0].name)
	}
}

func TestTeatest_TabCompletionCycleMultiple(t *testing.T) {
	t.Parallel()
	h := newEditorHarnessWithCommands()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Type("/mod")
	settle()
	// First Tab -> /model, Second Tab -> /module
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(got.commands))
	}
	if got.commands[0].name != "module" {
		t.Errorf("expected cycled to %q, got %q", "module", got.commands[0].name)
	}
}

// ---------------------------------------------------------------------------
// Tests: History navigation (Up/Down arrows)
// ---------------------------------------------------------------------------

func TestTeatest_HistoryUpDown(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	for _, msg := range []string{"alpha", "beta", "gamma"} {
		tm.Type(msg)
		settle()
		tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
		settle()
	}

	// Up x2: gamma -> beta
	tm.Send(tea.KeyMsg{Type: tea.KeyUp})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyUp})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.submitted) != 4 {
		t.Fatalf("expected 4 submissions, got %d: %v", len(got.submitted), got.submitted)
	}
	if got.submitted[3] != "beta" {
		t.Errorf("expected recalled submission %q, got %q", "beta", got.submitted[3])
	}
}

func TestTeatest_HistoryDraftPreservation(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Type("old message")
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()

	tm.Type("my draft")
	settle()
	// Up recalls, Down restores draft.
	tm.Send(tea.KeyMsg{Type: tea.KeyUp})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.submitted) != 2 {
		t.Fatalf("expected 2 submissions, got %d: %v", len(got.submitted), got.submitted)
	}
	if got.submitted[1] != "my draft" {
		t.Errorf("expected draft %q, got %q", "my draft", got.submitted[1])
	}
}

// ---------------------------------------------------------------------------
// Tests: Ctrl+C behavior
// ---------------------------------------------------------------------------

func TestTeatest_CtrlC_DoubleQuit(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})

	got := finalEditorHarness(t, tm)
	if got.quitCount != 1 {
		t.Errorf("expected 1 quit, got %d", got.quitCount)
	}
}

func TestTeatest_CtrlC_ResetByOtherKey(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	// Single Ctrl+C, then another key resets counter.
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	settle()
	tm.Type("a")
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if got.quitCount != 1 {
		t.Errorf("expected exactly 1 quit (from double press), got %d", got.quitCount)
	}
}

func TestTeatest_CtrlC_CancelsWhileRunning(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	h.editor.SetState(editorRunning)
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	settle()

	h.editor.SetState(editorIdle)
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if got.cancelled != 1 {
		t.Errorf("expected 1 cancel, got %d", got.cancelled)
	}
}

// ---------------------------------------------------------------------------
// Tests: Escape behavior
// ---------------------------------------------------------------------------

func TestTeatest_Escape_CancelsWhileRunning(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	h.editor.SetState(editorRunning)
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Send(tea.KeyMsg{Type: tea.KeyEscape})
	settle()

	h.editor.SetState(editorIdle)
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if got.cancelled != 1 {
		t.Errorf("expected 1 cancel from Escape, got %d", got.cancelled)
	}
}

// ---------------------------------------------------------------------------
// Tests: Ctrl+D behavior
// ---------------------------------------------------------------------------

func TestTeatest_CtrlD_QuitsWhenEmpty(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlD})

	got := finalEditorHarness(t, tm)
	if got.quitCount != 1 {
		t.Errorf("expected quit from Ctrl+D on empty editor, got quitCount=%d", got.quitCount)
	}
}

func TestTeatest_CtrlD_DoesNotQuitWithText(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Type("some text")
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlD})
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if got.quitCount != 1 {
		t.Errorf("expected quit only from double Ctrl+C, got %d", got.quitCount)
	}
}

// ---------------------------------------------------------------------------
// Tests: Kill ring (Ctrl+U, Ctrl+Y)
// ---------------------------------------------------------------------------

func TestTeatest_KillAndYank(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Type("hello world")
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlU})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlY})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.submitted) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(got.submitted))
	}
	if got.submitted[0] != "hello world" {
		t.Errorf("expected yanked text %q, got %q", "hello world", got.submitted[0])
	}
}

// ---------------------------------------------------------------------------
// Tests: Undo (Ctrl+_)
// ---------------------------------------------------------------------------

func TestTeatest_Undo(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Type("hello")
	settle()
	tm.Type(" extra")
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlUnderscore})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.submitted) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(got.submitted))
	}
	if got.submitted[0] == "hello extra" {
		t.Error("undo did not revert the last edit")
	}
}

// ---------------------------------------------------------------------------
// Tests: Steering (text while agent is running)
// ---------------------------------------------------------------------------

func TestTeatest_SteeringWhileRunning(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	h.editor.SetState(editorRunning)
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Type("redirect focus")
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()

	h.editor.SetState(editorIdle)
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.steered) != 1 {
		t.Fatalf("expected 1 steer message, got %d", len(got.steered))
	}
	if got.steered[0] != "redirect focus" {
		t.Errorf("expected steer text %q, got %q", "redirect focus", got.steered[0])
	}
}

// ---------------------------------------------------------------------------
// Tests: Search mode (Ctrl+R)
// ---------------------------------------------------------------------------

func TestTeatest_SearchModeAcceptAndSubmit(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	h.editor.history = []string{"deploy prod", "run tests", "deploy staging"}
	h.editor.historyIdx = len(h.editor.history)
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	// Activate search with prompts (most recent first).
	prompts := []string{"deploy staging", "run tests", "deploy prod"}
	tm.Send(searchActivateMsg{prompts: prompts})
	settle()

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("deploy")})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.submitted) != 1 {
		t.Fatalf("expected 1 submission from search, got %d", len(got.submitted))
	}
	if got.submitted[0] != "deploy staging" {
		t.Errorf("expected %q, got %q", "deploy staging", got.submitted[0])
	}
}

func TestTeatest_SearchModeCancel(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Type("my draft text")
	settle()

	tm.Send(searchActivateMsg{prompts: []string{"found it", "another"}})
	settle()

	// Cancel with Escape restores draft.
	tm.Send(tea.KeyMsg{Type: tea.KeyEscape})
	settle()
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	settle()
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if len(got.submitted) != 1 {
		t.Fatalf("expected 1 submission, got %d", len(got.submitted))
	}
	if got.submitted[0] != "my draft text" {
		t.Errorf("expected draft restored %q, got %q", "my draft text", got.submitted[0])
	}
}

// ---------------------------------------------------------------------------
// Tests: Rendered output verification
// ---------------------------------------------------------------------------

func TestTeatest_OutputContainsTypedText(t *testing.T) {
	t.Parallel()
	h := newEditorHarness()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Type("visible text")
	settle()

	teatest.WaitFor(
		t, tm.Output(),
		func(bts []byte) bool { return len(bts) > 0 },
		teatest.WithDuration(2*time.Second),
		teatest.WithCheckInterval(50*time.Millisecond),
	)
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	if got.editor.Value() != "visible text" {
		t.Errorf("expected editor value %q, got %q", "visible text", got.editor.Value())
	}
}

func TestTeatest_SlashCommandHintVisible(t *testing.T) {
	t.Parallel()
	h := newEditorHarnessWithCommands()
	tm := teatest.NewTestModel(t, h, teatest.WithInitialTermSize(80, 24))

	tm.Type("/mod")
	settle()

	teatest.WaitFor(
		t, tm.Output(),
		func(bts []byte) bool { return len(bts) > 0 },
		teatest.WithDuration(2*time.Second),
		teatest.WithCheckInterval(50*time.Millisecond),
	)
	quitHarness(tm)

	got := finalEditorHarness(t, tm)
	hint := got.editor.commandHint()
	stripped := stripAnsi(hint)
	if stripped == "" {
		t.Error("expected non-empty command hint for '/mod'")
	}
}
