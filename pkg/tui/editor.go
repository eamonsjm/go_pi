package tui

import (
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

// Editor wraps a bubbles/textarea and manages submission, steering, and
// keyboard shortcuts.
type Editor struct {
	textarea textarea.Model
	state    editorState
	width    int

	// lastMsg stores the previously submitted text so the user can recall it
	// with the up-arrow key when the textarea is empty.
	lastMsg string

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
	ta.Focus()

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
			e.lastMsg = text
			e.textarea.Reset()
			e.ctrlCCount = 0

			if e.state == editorIdle {
				return func() tea.Msg { return editorSubmitMsg{text: text} }
			}
			// Agent running — send as steering.
			return func() tea.Msg { return editorSteerMsg{text: text} }

		case tea.KeyUp:
			// Recall previous message if textarea is empty.
			if e.textarea.Value() == "" && e.lastMsg != "" {
				e.textarea.SetValue(e.lastMsg)
				// Move cursor to end.
				e.textarea.CursorEnd()
				return nil
			}
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

// View renders the editor with a styled border.
func (e *Editor) View() string {
	style := e.borderStyle()
	style = style.Width(e.width - 2) // account for border chars

	return style.Render(e.textarea.View())
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
