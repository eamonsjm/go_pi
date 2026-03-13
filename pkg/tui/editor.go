package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// editorState tracks whether the agent is idle, running, or thinking so the
// border color can reflect that.
type editorState int

const (
	editorIdle     editorState = iota
	editorRunning              // agent executing
	editorThinking             // model is thinking
)

// editorCommandMsg is sent when the user submits a slash command.
type editorCommandMsg struct {
	name string
	args string
}

// Editor wraps a bubbles/textarea and manages submission, steering, and
// keyboard shortcuts.
type Editor struct {
	textarea textarea.Model
	state    editorState
	width    int

	// commands is the registry used for slash command dispatch and autocomplete.
	commands *CommandRegistry

	// history stores previously submitted messages for up/down-arrow recall.
	// Index 0 is the oldest entry; len-1 is the most recent.
	history []string

	// historyIdx is the current position in the history ring.
	// When equal to len(history), the user is at the "empty" position
	// (i.e. composing a new message, not recalling an old one).
	historyIdx int

	// draft holds the in-progress text the user was typing before they
	// started scrolling through history, so it can be restored on down-arrow.
	draft string

	// ctrlCCount tracks consecutive Ctrl-C presses while idle so the user
	// must press it twice to quit.
	ctrlCCount int
}

// NewEditor creates an Editor ready for use.
func NewEditor() *Editor {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (Enter to send, Shift+Enter for newline)"
	ta.CharLimit = 0 // unlimited
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	// Don't focus immediately — terminal color query responses can leak into
	// the textarea before the TUI is fully initialized. Focus is deferred
	// until the first WindowSizeMsg arrives (see App.Update).

	// Style the textarea itself.
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle().Foreground(ColorText)
	ta.BlurredStyle.Base = lipgloss.NewStyle().Foreground(ColorMuted)

	return &Editor{
		textarea: ta,
		state:    editorIdle,
	}
}

// SetWidth adjusts the editor to the given terminal width.
func (e *Editor) SetWidth(w int) {
	e.width = w
	// Account for border (1 left + 1 right).
	inner := w - 2
	if inner < 10 {
		inner = 10
	}
	e.textarea.SetWidth(inner)
}

// SetState updates the editor state (affects border color).
func (e *Editor) SetState(s editorState) {
	e.state = s
	// Reset Ctrl-C counter whenever state changes.
	e.ctrlCCount = 0
}

// Focus gives the textarea keyboard focus.
func (e *Editor) Focus() {
	e.textarea.Focus()
}

// Blur removes keyboard focus from the textarea.
func (e *Editor) Blur() {
	e.textarea.Blur()
}

// Value returns the current textarea content.
func (e *Editor) Value() string {
	return e.textarea.Value()
}

// Reset clears the textarea content.
func (e *Editor) Reset() {
	e.textarea.Reset()
}

// SetCommands sets the command registry used for slash command parsing and
// autocomplete.
func (e *Editor) SetCommands(reg *CommandRegistry) {
	e.commands = reg
}

// Height returns the outer height (including border).
func (e *Editor) Height() int {
	// textarea lines + 2 for top/bottom border
	return e.textarea.Height() + 2
}

// ---------------------------------------------------------------------------
// Bubble Tea interface
// ---------------------------------------------------------------------------

// editorSubmitMsg is sent when the user presses Enter to submit text.
type editorSubmitMsg struct {
	text string
}

// editorSteerMsg is sent when the user submits text while the agent is running.
type editorSteerMsg struct {
	text string
}

// editorCancelMsg is sent when the user presses Ctrl-C or Escape to cancel.
type editorCancelMsg struct{}

// editorQuitMsg is sent when the user double-presses Ctrl-C while idle.
type editorQuitMsg struct{}

func (e *Editor) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {

		case tea.KeyCtrlC:
			if e.state != editorIdle {
				// Cancel the running agent.
				e.ctrlCCount = 0
				return func() tea.Msg { return editorCancelMsg{} }
			}
			e.ctrlCCount++
			if e.ctrlCCount >= 2 {
				return func() tea.Msg { return editorQuitMsg{} }
			}
			return nil

		case tea.KeyCtrlD:
			// Ctrl+D (EOF) quits when idle and the editor is empty,
			// matching standard terminal behavior.
			if e.state == editorIdle && e.textarea.Value() == "" {
				return func() tea.Msg { return editorQuitMsg{} }
			}
			return nil

		case tea.KeyEscape:
			if e.state != editorIdle {
				return func() tea.Msg { return editorCancelMsg{} }
			}
			return nil

		case tea.KeyEnter:
			// Submit on Enter (no shift).
			text := e.textarea.Value()
			if text == "" {
				return nil
			}
			// Append to history (skip duplicates of the most recent entry).
			if len(e.history) == 0 || e.history[len(e.history)-1] != text {
				e.history = append(e.history, text)
			}
			e.historyIdx = len(e.history)
			e.draft = ""
			e.textarea.Reset()
			e.ctrlCCount = 0

			// Slash commands are always handled locally, regardless of
			// whether the agent is idle or running.
			if strings.HasPrefix(text, "/") {
				name, args := parseSlashCommand(text)
				return func() tea.Msg { return editorCommandMsg{name: name, args: args} }
			}

			if e.state == editorIdle {
				return func() tea.Msg { return editorSubmitMsg{text: text} }
			}
			// Agent running — send as steering.
			return func() tea.Msg { return editorSteerMsg{text: text} }

		case tea.KeyUp:
			if len(e.history) == 0 {
				break
			}
			// Only enter history mode when textarea is on a single line
			// (avoid hijacking multi-line cursor movement).
			if strings.Contains(e.textarea.Value(), "\n") {
				break
			}
			// Save the current text as draft when first entering history.
			if e.historyIdx == len(e.history) {
				e.draft = e.textarea.Value()
			}
			if e.historyIdx > 0 {
				e.historyIdx--
				e.textarea.SetValue(e.history[e.historyIdx])
				e.textarea.CursorEnd()
				return nil
			}
			return nil // already at oldest entry

		case tea.KeyDown:
			if len(e.history) == 0 {
				break
			}
			// Only handle history navigation on single-line content.
			if strings.Contains(e.textarea.Value(), "\n") {
				break
			}
			// Not in history mode — let the textarea handle it.
			if e.historyIdx >= len(e.history) {
				break
			}
			e.historyIdx++
			if e.historyIdx == len(e.history) {
				// Past the newest entry — restore draft (or empty).
				e.textarea.SetValue(e.draft)
			} else {
				e.textarea.SetValue(e.history[e.historyIdx])
			}
			e.textarea.CursorEnd()
			return nil
		}

		// Reset Ctrl-C counter on any non-Ctrl-C keypress.
		if msg.Type != tea.KeyCtrlC {
			e.ctrlCCount = 0
		}
	}

	var cmd tea.Cmd
	e.textarea, cmd = e.textarea.Update(msg)
	return cmd
}

// View renders the editor with a styled border and an optional autocomplete
// hint line above it when the user is typing a slash command.
func (e *Editor) View() string {
	style := e.borderStyle()
	style = style.Width(e.width - 2) // account for border chars

	hint := e.commandHint()
	editor := style.Render(e.textarea.View())
	if hint != "" {
		return hint + "\n" + editor
	}
	return editor
}

// commandHint returns an autocomplete hint string when the current input looks
// like an incomplete slash command, or empty string otherwise.
func (e *Editor) commandHint() string {
	if e.commands == nil {
		return ""
	}
	text := e.textarea.Value()
	if !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") {
		return ""
	}

	// Extract the partial command name (everything after "/" up to the first space).
	rest := text[1:]
	if idx := strings.IndexByte(rest, ' '); idx >= 0 {
		// Already has a space — user is typing args, no hint needed.
		return ""
	}

	matches := e.commands.Match(rest)
	if len(matches) == 0 {
		return ""
	}

	// Build a single hint line: "/name — description, /other — description"
	parts := make([]string, 0, len(matches))
	for _, cmd := range matches {
		parts = append(parts, fmt.Sprintf("/%s — %s", cmd.Name, cmd.Description))
	}
	return MutedStyle.Render(strings.Join(parts, "  "))
}

// borderStyle returns the appropriate border style based on the current state.
func (e *Editor) borderStyle() lipgloss.Style {
	switch e.state {
	case editorRunning:
		return EditorActiveStyle
	case editorThinking:
		return EditorThinkingStyle
	default:
		return EditorStyle
	}
}

// parseSlashCommand splits "/name some args" into ("name", "some args").
func parseSlashCommand(text string) (name, args string) {
	// Strip the leading "/".
	text = text[1:]
	if idx := strings.IndexByte(text, ' '); idx >= 0 {
		return text[:idx], strings.TrimSpace(text[idx+1:])
	}
	return text, ""
}
