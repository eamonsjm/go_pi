package tui

import (
	"context"
	"fmt"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/auth"
	"github.com/ejm/go_pi/pkg/config"
	"github.com/ejm/go_pi/pkg/session"
)

// ---------------------------------------------------------------------------
// App — the root Bubble Tea model that composes all TUI components
// ---------------------------------------------------------------------------

// App is the top-level Bubble Tea model for the coding agent TUI.
type App struct {
	// Sub-components.
	chat          *ChatView
	editor        *Editor
	header        *Header
	footer        *Footer
	modelSelector *ModelSelector

	// Slash command registry.
	commands *CommandRegistry

	// Terminal dimensions.
	width  int
	height int

	// Agent state.
	agentRunning bool

	// authPendingCodeCh is non-nil while waiting for the user to paste an
	// OAuth authorization code. The next editorSubmitMsg sends the text to
	// this channel instead of the agent.
	authPendingCodeCh chan string

	// Callbacks wired by the caller that owns the agent loop.
	onSubmit       func(text string)
	onCancel       func()
	onSteer        func(text string)
	onModelChange  func(provider, model string)
	onLoginSuccess func(provider string)

	// quitting tracks whether we are in the process of exiting.
	quitting bool

	// initialized is set after the first WindowSizeMsg to defer editor focus.
	initialized bool
}

// NewApp creates a fully initialised App ready to be passed to tea.NewProgram.
func NewApp() *App {
	reg := NewCommandRegistry()
	editor := NewEditor()
	editor.SetCommands(reg)

	app := &App{
		chat:          NewChatView(),
		editor:        editor,
		header:        NewHeader(),
		footer:        NewFooter(),
		modelSelector: NewModelSelector(),
		commands:      reg,
	}

	// Register the /model command.
	modelCmd := RegisterModelCommand()
	reg.Register(&modelCmd)

	return app
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

// SetModelChangeCallback sets the function called when the user switches the
// AI model via the /model command. The callback receives the provider name
// (may be empty if unknown) and the model identifier.
func (a *App) SetModelChangeCallback(fn func(provider, model string)) {
	a.onModelChange = fn
}

// SetLoginSuccessCallback sets the function called after a successful /login.
// The callback receives the provider name and should re-resolve credentials
// and wire the new provider into the agent loop.
func (a *App) SetLoginSuccessCallback(fn func(provider string)) {
	a.onLoginSuccess = fn
}

// RegisterCommand adds a slash command to the app's command registry. Commands
// are available to the user by typing /name in the editor.
func (a *App) RegisterCommand(cmd *SlashCommand) {
	a.commands.Register(cmd)
}

// ShowWelcome adds an initial system message to the chat view. Use this to
// display setup instructions or welcome text before the user interacts.
func (a *App) ShowWelcome(text string) {
	a.chat.AddSystemMessage(text)
}

// RestoreSession replays saved messages into the chat view and updates the
// header to show the session ID. Call this before tea.NewProgram.Run() to
// display conversation history from a resumed session.
func (a *App) RestoreSession(sessionID string, msgs []ai.Message) {
	rebuildChatFromMessages(a.chat, msgs)
	a.header.SetSession(shortID(sessionID))
}

// RegisterBuiltinCommands registers all built-in slash commands that need
// access to external dependencies (agent loop, session manager, config).
// The ctx should be the application lifecycle context so that long-running
// commands like /compact are cancelled when the application exits.
func (a *App) RegisterBuiltinCommands(ctx context.Context, agentLoop *agent.AgentLoop, sessionMgr *session.Manager, cfg *config.Config, authStore *auth.Store, authResolver *auth.Resolver) {
	a.RegisterCommand(NewCompactCommand(ctx, agentLoop))
	a.RegisterCommand(NewSettingsCommand(cfg, agentLoop, a.header))
	a.RegisterCommand(NewNewSessionCommand(agentLoop, sessionMgr, a.chat, a.header))
	a.RegisterCommand(NewResumeCommand(agentLoop, sessionMgr, a.chat, a.header))
	a.RegisterCommand(NewSessionInfoCommand(sessionMgr, a.chat))
	a.RegisterCommand(NewNameCommand(sessionMgr, a.header, a.chat))

	// Auth commands.
	if authStore != nil && authResolver != nil {
		a.RegisterCommand(NewLoginCommand(authStore, authResolver))
		a.RegisterCommand(NewLogoutCommand(authStore))
		a.RegisterCommand(NewAuthStatusCommand(authStore, authResolver))
	}
}

// ---------------------------------------------------------------------------
// Bubble Tea interface
// ---------------------------------------------------------------------------

// Init returns the initial command to start the cursor blink in the editor.
func (a *App) Init() tea.Cmd {
	return nil
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
		if !a.initialized {
			a.initialized = true
			a.editor.Focus()
			return a, textarea.Blink
		}
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
		// If we're waiting for an OAuth code, route the input there.
		if a.authPendingCodeCh != nil {
			ch := a.authPendingCodeCh
			a.authPendingCodeCh = nil
			a.chat.AddUserMessage(msg.text)
			ch <- msg.text
			return a, nil
		}

		a.chat.AddUserMessage(msg.text)
		a.agentRunning = true
		a.editor.SetState(editorRunning)
		if a.onSubmit != nil {
			a.onSubmit(msg.text)
		}
		return a, nil

	case editorCommandMsg:
		cmd, ok := a.commands.Get(msg.name)
		if !ok {
			a.chat.AddUserMessage(fmt.Sprintf("Unknown command: /%s", msg.name))
			return a, nil
		}
		return a, cmd.Execute(msg.args)

	case CommandResultMsg:
		if msg.IsError {
			a.chat.AddSystemMessage("Error: " + msg.Text)
		} else {
			a.chat.AddSystemMessage(msg.Text)
		}
		return a, nil

	case authLoginSuccessMsg:
		a.chat.AddSystemMessage(msg.text)
		if a.onLoginSuccess != nil {
			a.onLoginSuccess(msg.providerName)
		}
		return a, nil

	case PluginInjectMsg:
		a.chat.AddPluginMessage(msg.PluginName, msg.Content, msg.IsLog, msg.LogLevel)
		return a, nil

	case authOAuthMsg:
		a.authPendingCodeCh = msg.codeCh
		wrappedURL := wrapLongString(msg.url, a.width-4)
		if err := openBrowser(msg.url); err == nil {
			a.chat.AddSystemMessage(fmt.Sprintf(
				"Login to %s\n\nOpened authorization URL in your browser.\n\nIf it didn't open, copy this URL:\n%s\n\nAfter authorizing, paste the code below and press Enter.",
				msg.providerName, wrappedURL))
		} else {
			a.chat.AddSystemMessage(fmt.Sprintf(
				"Login to %s\n\nOpen this URL in your browser:\n%s\n\nAfter authorizing, paste the code below and press Enter.",
				msg.providerName, wrappedURL))
		}
		return a, msg.waitCmd

	case settingsDisplayMsg:
		a.chat.AddSystemMessage(msg.text)
		return a, nil

	case settingsUpdatedMsg:
		a.chat.AddSystemMessage(fmt.Sprintf("Updated %s to %s", msg.key, msg.value))
		return a, nil

	// ---- Model selector events ----
	case showModelSelectorMsg:
		a.modelSelector.SetSize(a.width, a.height)
		a.modelSelector.Show()
		a.editor.Blur()
		return a, nil

	case modelSelectedMsg:
		a.modelSelector.Hide()
		a.header.SetModel(msg.model)
		a.chat.AddSystemMessage(fmt.Sprintf("Switched to model: %s", msg.model))
		a.editor.Focus()
		if a.onModelChange != nil {
			a.onModelChange(msg.provider, msg.model)
		}
		return a, nil

	case modelCancelledMsg:
		a.modelSelector.Hide()
		a.editor.Focus()
		return a, nil

	// ---- Compaction events ----
	case compactionStartMsg:
		a.chat.AddSystemMessage("Compacting conversation...")
		return a, nil

	case compactionDoneMsg:
		a.chat.AddCompactionBlock(msg.summary)
		return a, nil

	case compactionErrorMsg:
		a.chat.HandleEvent(agent.AgentEvent{
			Type:  agent.EventAgentError,
			Error: msg.err,
		})
		a.chat.rebuildContent()
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

	// ---- Mouse events (silently discard) ----
	case tea.MouseMsg:
		return a, nil

	// ---- Keyboard shortcuts handled at app level ----
	case tea.KeyMsg:
		// When the model selector overlay is visible, route all keys to it.
		if a.modelSelector.Visible() {
			cmd := a.modelSelector.Update(msg)
			if cmd != nil {
				return a, cmd
			}
			return a, nil
		}

		switch msg.String() {
		case "ctrl+t":
			// Toggle thinking expand/collapse.
			if a.agentRunning {
				a.chat.ToggleThinking()
				return a, nil
			}
		case "ctrl+r":
			// Toggle tool result expand/collapse.
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

	base := fmt.Sprintf("%s\n%s\n%s\n%s",
		headerView,
		chatView,
		editorView,
		footerView,
	)

	// If the model selector is visible, render it as an overlay.
	if a.modelSelector.Visible() {
		return a.modelSelector.View()
	}

	return base
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
	a.modelSelector.SetSize(a.width, a.height)
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

// wrapLongString inserts newlines into a string that has no natural break
// points (like URLs) so it fits within the given width.
func wrapLongString(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	var result []byte
	for i, b := range []byte(s) {
		if i > 0 && i%width == 0 {
			result = append(result, '\n')
		}
		result = append(result, b)
	}
	return string(result)
}
