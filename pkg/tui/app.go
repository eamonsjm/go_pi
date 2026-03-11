package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
)

// ---------------------------------------------------------------------------
// App — the root Bubble Tea model that composes all TUI components
// ---------------------------------------------------------------------------

// App is the top-level Bubble Tea model for the coding agent TUI.
type App struct {
	// Sub-components.
	chat   *ChatView
	editor *Editor
	header *Header
	footer *Footer

	// Terminal dimensions.
	width  int
	height int

	// Agent state.
	agentRunning bool

	// Callbacks wired by the caller that owns the agent loop.
	onSubmit func(text string)
	onCancel func()
	onSteer  func(text string)

	// quitting tracks whether we are in the process of exiting.
	quitting bool
}

// NewApp creates a fully initialised App ready to be passed to tea.NewProgram.
func NewApp() *App {
	return &App{
		chat:   NewChatView(),
		editor: NewEditor(),
		header: NewHeader(),
		footer: NewFooter(),
	}
}

// SetCallbacks wires up the functions that bridge the TUI to the agent loop.
//
//   - onSubmit is called when the user presses Enter while the agent is idle.
//   - onSteer is called when the user presses Enter while the agent is running
//     (steering / interrupt injection).
//   - onCancel is called when the user presses Ctrl-C or Escape during a run.
func (a *App) SetCallbacks(onSubmit, onSteer func(string), onCancel func()) {
	a.onSubmit = onSubmit
	a.onSteer = onSteer
	a.onCancel = onCancel
}

// SetModel updates the model name shown in the header.
func (a *App) SetModel(name string) {
	a.header.SetModel(name)
}

// SetThinking updates the thinking level indicator.
func (a *App) SetThinking(level string) {
	a.header.SetThinking(thinkingFromString(level))
}

// SetSession updates the session name in the header.
func (a *App) SetSession(name string) {
	a.header.SetSession(name)
}

// ---------------------------------------------------------------------------
// Bubble Tea interface
// ---------------------------------------------------------------------------

// Init returns the initial command to start the cursor blink in the editor.
func (a *App) Init() tea.Cmd {
	return textarea.Blink
}

// Update processes messages and delegates to sub-components.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	// ---- Window resize ----
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.layout()
		return a, nil

	// ---- Agent events flowing in ----
	case StreamEventMsg:
		changed := a.chat.HandleEvent(msg.Event)
		a.handleStateTransition(msg.Event)
		if changed {
			a.chat.rebuildContent()
		}
		return a, nil

	case AgentDoneMsg:
		a.agentRunning = false
		a.editor.SetState(editorIdle)
		a.editor.Focus()
		return a, nil

	case AgentErrorMsg:
		a.agentRunning = false
		a.editor.SetState(editorIdle)
		a.editor.Focus()
		a.chat.HandleEvent(agent.AgentEvent{
			Type:  agent.EventAgentError,
			Error: msg.Err,
		})
		a.chat.rebuildContent()
		return a, nil

	// ---- Editor actions ----
	case editorSubmitMsg:
		a.chat.AddUserMessage(msg.text)
		a.agentRunning = true
		a.editor.SetState(editorRunning)
		if a.onSubmit != nil {
			a.onSubmit(msg.text)
		}
		return a, nil

	case editorSteerMsg:
		if a.onSteer != nil {
			a.onSteer(msg.text)
		}
		return a, nil

	case editorCancelMsg:
		if a.onCancel != nil {
			a.onCancel()
		}
		return a, nil

	case editorQuitMsg:
		a.quitting = true
		return a, tea.Quit

	// ---- Keyboard shortcuts handled at app level ----
	case tea.KeyMsg:
		switch msg.String() {
		case "t":
			// Toggle thinking expand/collapse (only when not typing).
			if a.agentRunning {
				a.chat.ToggleThinking()
				return a, nil
			}
		case "r":
			if a.agentRunning {
				a.chat.ToggleToolResult()
				return a, nil
			}
		}
	}

	// Delegate to sub-components.
	editorCmd := a.editor.Update(msg)
	if editorCmd != nil {
		cmds = append(cmds, editorCmd)
	}

	chatCmd := a.chat.Update(msg)
	if chatCmd != nil {
		cmds = append(cmds, chatCmd)
	}

	return a, tea.Batch(cmds...)
}

// View renders the full TUI layout.
func (a *App) View() string {
	if a.quitting {
		return MutedStyle.Render("Goodbye.") + "\n"
	}

	if a.width == 0 || a.height == 0 {
		return "Initializing..."
	}

	headerView := a.header.View()
	chatView := a.chat.View()
	editorView := a.editor.View()
	footerView := a.footer.View()

	return fmt.Sprintf("%s\n%s\n%s\n%s",
		headerView,
		chatView,
		editorView,
		footerView,
	)
}

// ---------------------------------------------------------------------------
// Public API for pushing events from the agent goroutine
// ---------------------------------------------------------------------------

// HandleAgentEvent converts an agent.AgentEvent into a Bubble Tea Cmd that
// can be sent via Program.Send or returned from a Cmd function.
func (a *App) HandleAgentEvent(event agent.AgentEvent) tea.Cmd {
	return func() tea.Msg {
		return StreamEventMsg{Event: event}
	}
}

// SendDone returns a Cmd that signals the agent loop is complete.
func SendDone() tea.Msg {
	return AgentDoneMsg{}
}

// SendError returns a Cmd that signals an agent error.
func SendError(err error) tea.Msg {
	return AgentErrorMsg{Err: err}
}

// ---------------------------------------------------------------------------
// Layout & state helpers
// ---------------------------------------------------------------------------

// layout recalculates component sizes after a terminal resize.
func (a *App) layout() {
	a.header.SetWidth(a.width)
	a.footer.SetWidth(a.width)
	a.editor.SetWidth(a.width)

	// Height allocation:
	//   Header: 1 line
	//   Footer: 1 line
	//   Editor: its own height (3 + 2 for border = 5)
	//   Chat:   remaining
	headerH := a.header.Height()
	footerH := a.footer.Height()
	editorH := a.editor.Height()

	chatH := a.height - headerH - footerH - editorH - 3 // 3 for newlines between sections
	if chatH < 3 {
		chatH = 3
	}

	a.chat.SetSize(a.width, chatH)
}

// handleStateTransition updates agentRunning and editor state based on events.
func (a *App) handleStateTransition(ev agent.AgentEvent) {
	switch ev.Type {
	case agent.EventAgentStart:
		a.agentRunning = true
		a.editor.SetState(editorRunning)

	case agent.EventAgentEnd:
		a.agentRunning = false
		a.editor.SetState(editorIdle)
		a.editor.Focus()

	case agent.EventAssistantThinking:
		if a.agentRunning {
			a.editor.SetState(editorThinking)
		}

	case agent.EventAssistantText:
		if a.agentRunning {
			a.editor.SetState(editorRunning)
		}

	case agent.EventUsageUpdate:
		if ev.Usage != nil {
			a.footer.SetUsage(*ev.Usage)
		}
	}
}

// thinkingFromString converts a string to the ai.ThinkingLevel type.
func thinkingFromString(s string) thinkingLevel {
	switch s {
	case "low":
		return thinkingLevelLow
	case "medium":
		return thinkingLevelMedium
	case "high":
		return thinkingLevelHigh
	default:
		return thinkingLevelOff
	}
}

// Re-declare local constants that map to ai.ThinkingLevel values so we don't
// have a circular import if ai imports tui in the future.  The Header actually
// uses the ai package directly, so these are only used by thinkingFromString.
type thinkingLevel = ai.ThinkingLevel

const (
	thinkingLevelOff    thinkingLevel = ai.ThinkingOff
	thinkingLevelLow    thinkingLevel = ai.ThinkingLow
	thinkingLevelMedium thinkingLevel = ai.ThinkingMedium
	thinkingLevelHigh   thinkingLevel = ai.ThinkingHigh
)
