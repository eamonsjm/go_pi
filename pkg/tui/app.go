package tui

import (
	"context"
	"fmt"
	"log"
	"time"

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

	// Keybinding configuration.
	keybindings *KeybindingConfig

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

	// Dependencies for keybinding actions.
	cfg       *config.Config
	agentLoop *agent.AgentLoop

	// initialPrompt, if set, is auto-submitted after the first window resize.
	initialPrompt string

	// mouseEnabled tracks whether mouse capture is active. When true, the
	// terminal reports mouse events to bubbletea (enabling scroll in the
	// viewport) but prevents native text selection. When false (default),
	// the terminal handles mouse natively so users can select and copy text.
	mouseEnabled bool

	// quitting tracks whether we are in the process of exiting.
	quitting bool

	// initialized is set after the first WindowSizeMsg to defer editor focus.
	initialized bool

	// deltaGen is incremented on each assistant text delta. The idle-render
	// tick carries the generation at scheduling time; if they still match
	// when the tick fires, no new deltas arrived and we trigger a glamour
	// re-render of the streaming block.
	deltaGen uint64

	// Session-level usage accumulator and provider info for /session command.
	sessionMgr     *session.Manager
	providerInfo   ProviderInfo
	sessionUsage   ai.Usage
	sessionUsageID string // session ID this usage belongs to

	// Plugin UI request handling.
	// uiRequestPending is non-nil while waiting for the user to respond to a UI dialog.
	// The onUIResponse callback should be set to send the response back to the plugin.
	uiRequestPending *PluginUIRequestMsg
	onUIResponse     func(*PluginUIResponseMsg)
	hasUI            bool // true when in interactive mode, false in print/RPC mode
}

// NewApp creates a fully initialised App ready to be passed to tea.NewProgram.
func NewApp() *App {
	reg := NewCommandRegistry()
	editor := NewEditor()
	editor.SetCommands(reg)
	kb := LoadKeybindings()

	app := &App{
		chat:          NewChatView(),
		editor:        editor,
		header:        NewHeader(),
		footer:        NewFooter(),
		modelSelector: NewModelSelector(),
		commands:      reg,
		keybindings:   kb,
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

// SetUIResponseCallback sets the callback for sending UI responses back to plugins.
func (a *App) SetUIResponseCallback(onUIResponse func(*PluginUIResponseMsg)) {
	a.onUIResponse = onUIResponse
}

// SetHasUI sets whether the TUI is running in interactive mode.
func (a *App) SetHasUI(hasUI bool) {
	a.hasUI = hasUI
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

// SetInitialPrompt sets a prompt that will be auto-submitted after the TUI
// initialises. Use this for CLI-provided initial messages (e.g. @filepath args).
func (a *App) SetInitialPrompt(prompt string) {
	a.initialPrompt = prompt
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
	// Store dependencies needed for keybinding actions.
	a.cfg = cfg
	a.agentLoop = agentLoop
	a.sessionMgr = sessionMgr

	// Build provider info for /session display.
	provName := agentLoop.ProviderName()
	a.providerInfo = ProviderInfo{
		Name:     provName,
		Model:    cfg.DefaultModel,
		API:      providerAPIType(provName),
		Endpoint: providerEndpoint(provName),
	}
	if authResolver != nil && authResolver.IsOAuthToken(provName) {
		a.providerInfo.Auth = "oauth"
	} else if authStore != nil && authStore.Get(provName) != nil {
		a.providerInfo.Auth = "api_key"
	} else if provName != "" {
		a.providerInfo.Auth = "env"
	}

	a.RegisterCommand(NewCompactCommand(ctx, agentLoop))
	a.RegisterCommand(NewSettingsCommand(cfg, agentLoop, a.header))
	a.RegisterCommand(NewRTKCommand(cfg))
	a.RegisterCommand(NewNewSessionCommand(agentLoop, sessionMgr, a.chat, a.header))
	a.RegisterCommand(NewResumeCommand(agentLoop, sessionMgr, a.chat, a.header))
	a.RegisterCommand(NewSessionInfoCommand(sessionMgr, a.chat,
		func() ProviderInfo {
			info := a.providerInfo
			info.Model = cfg.DefaultModel // pick up runtime model changes
			return info
		},
		func() ai.Usage { return a.sessionUsage },
	))
	a.RegisterCommand(NewNameCommand(sessionMgr, a.header, a.chat))
	a.RegisterCommand(NewForkCommand(ctx, agentLoop, sessionMgr, a.chat, a.header))
	a.RegisterCommand(NewTreeCommand(agentLoop, sessionMgr, a.chat, a.header))
	a.RegisterCommand(NewCopyCommand(sessionMgr))
	a.RegisterCommand(NewExportCommand(sessionMgr))
	a.RegisterCommand(NewShareCommand(sessionMgr))
	a.RegisterCommand(NewHotkeysCommand(a.keybindings))

	// Theme command.
	a.RegisterCommand(NewThemeCommand(cfg, a.chat))

	// Auth commands.
	if authStore != nil && authResolver != nil {
		a.RegisterCommand(NewLoginCommand(authStore, authResolver))
		a.RegisterCommand(NewLogoutCommand(authStore))
		a.RegisterCommand(NewAuthStatusCommand(authStore, authResolver))
	}

	// Alias commands.
	a.RegisterCommand(NewAliasCommand(cfg, a.commands))
	a.RegisterCommand(NewAliasesCommand(a.commands))
	a.RegisterCommand(NewUnaliasCommand(cfg, a.commands))

	// Load aliases from config.
	if cfg.Aliases != nil {
		for alias, target := range cfg.Aliases {
			a.commands.SetAlias(alias, target)
		}
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
			if a.initialPrompt != "" {
				prompt := a.initialPrompt
				a.initialPrompt = ""
				return a, func() tea.Msg { return editorSubmitMsg{text: prompt} }
			}
			return a, textarea.Blink
		}
		return a, nil

	// ---- Agent events flowing in ----
	case StreamEventMsg:
		changed := a.chat.HandleEvent(msg.Event)
		a.handleStateTransition(msg.Event)
		// Rebuild immediately when content changes so each delta is
		// visible in the next View() call. Without this, deltas
		// accumulate until the next renderTickMsg and appear as chunks.
		if changed {
			a.chat.rebuildContent()
		}
		// On each text delta, schedule an idle-render tick. If no further
		// deltas arrive within 100ms the tick triggers a glamour re-render
		// of the streaming block for polished output during pauses.
		if msg.Event.Type == agent.EventAssistantText {
			a.deltaGen++
			return a, tickIdleRender(a.deltaGen)
		}
		return a, nil

	case renderTickMsg:
		if a.chat.dirty {
			a.chat.rebuildContent()
		}
		if a.agentRunning {
			return a, tickRender()
		}
		return a, nil

	case idleRenderMsg:
		// If deltaGen still matches, no new deltas arrived in 100ms.
		// Switch the streaming block to glamour rendering.
		if msg.gen == a.deltaGen {
			a.chat.idleGlamourRender()
		}
		return a, nil

	case AgentDoneMsg:
		a.agentRunning = false
		a.editor.SetState(editorIdle)
		a.editor.Focus()
		if a.chat.dirty {
			a.chat.rebuildContent()
		}
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

		// If we're waiting for a UI response, handle it.
		if a.uiRequestPending != nil {
			req := a.uiRequestPending
			a.uiRequestPending = nil
			a.chat.AddUserMessage(msg.text)
			if a.onUIResponse != nil {
				a.onUIResponse(&PluginUIResponseMsg{
					PluginName: req.PluginName,
					ID:         req.ID,
					Value:      msg.text,
					Closed:     false,
					Error:      "",
				})
			}
			return a, nil
		}

		a.chat.AddUserMessage(msg.text)
		a.agentRunning = true
		a.editor.SetState(editorRunning)
		if a.onSubmit != nil {
			a.onSubmit(msg.text)
		}
		return a, tickRender()

	case SkillInvokeMsg:
		a.chat.AddUserMessage(msg.Display)
		a.agentRunning = true
		a.editor.SetState(editorRunning)
		if a.onSubmit != nil {
			a.onSubmit(msg.Prompt)
		}
		return a, tickRender()

	case editorCommandMsg:
		cmd, ok := a.commands.Get(msg.name)
		if !ok {
			a.chat.AddUserMessage(fmt.Sprintf("Unknown command: /%s", msg.name))
			return a, nil
		}
		return a, cmd.Execute(msg.args)

	case editorShellResultMsg:
		if msg.errorMsg != "" {
			a.chat.AddSystemMessage(fmt.Sprintf("Error executing command: %s", msg.errorMsg))
			return a, nil
		}

		// Display the command output as a system message.
		outputDisplay := msg.output
		if outputDisplay == "" {
			outputDisplay = "(no output)"
		}
		a.chat.AddSystemMessage(fmt.Sprintf("$ %s\n%s", msg.command, outputDisplay))

		// If sendToAI is true, submit the command result to the agent.
		if msg.sendToAI {
			fullMessage := fmt.Sprintf("!%s\n\nOutput:\n%s", msg.command, msg.output)
			a.chat.AddUserMessage(fullMessage)
			a.agentRunning = true
			a.editor.SetState(editorRunning)
			if a.onSubmit != nil {
				a.onSubmit(fullMessage)
			}
			return a, tickRender()
		}

		return a, nil

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

	case PluginUIRequestMsg:
		if !a.hasUI {
			// In headless mode, respond with sensible defaults instead of waiting for user input.
			response := &PluginUIResponseMsg{
				PluginName: msg.PluginName,
				ID:         msg.ID,
				Closed:     true,
				Error:      "UI not available in headless mode",
			}
			// Set a default value based on the dialog type.
			switch msg.UIType {
			case "confirm":
				response.Value = "false"
			case "select":
				if len(msg.UIOptions) > 0 {
					response.Value = msg.UIOptions[0]
				}
			case "input", "editor":
				response.Value = msg.UIDefault
			case "notify":
				response.Value = ""
			}
			if a.onUIResponse != nil {
				a.onUIResponse(response)
			}
			return a, nil
		}

		// Store the pending UI request. The next editor submission will be handled
		// as a response to this request rather than a prompt to the agent.
		a.uiRequestPending = &msg
		return a, nil

	case authOAuthMsg:
		a.authPendingCodeCh = msg.codeCh
		wrappedURL := wrapLongString(msg.url, a.width-4)
		if msg.codeCh != nil {
			// Code-paste flow (e.g. Anthropic): prompt user to paste code.
			if err := openBrowser(msg.url); err == nil {
				a.chat.AddSystemMessage(fmt.Sprintf(
					"Login to %s\n\nOpened authorization URL in your browser.\n\nIf it didn't open, copy this URL:\n%s\n\nAfter authorizing, paste the code below and press Enter.",
					msg.providerName, wrappedURL))
			} else {
				a.chat.AddSystemMessage(fmt.Sprintf(
					"Login to %s\n\nOpen this URL in your browser:\n%s\n\nAfter authorizing, paste the code below and press Enter.",
					msg.providerName, wrappedURL))
			}
		} else {
			// Callback-based flow (e.g. OpenAI): browser redirects automatically.
			if err := openBrowser(msg.url); err == nil {
				a.chat.AddSystemMessage(fmt.Sprintf(
					"Login to %s\n\nOpened authorization URL in your browser.\n\nIf it didn't open, copy this URL:\n%s\n\nWaiting for authorization...",
					msg.providerName, wrappedURL))
			} else {
				a.chat.AddSystemMessage(fmt.Sprintf(
					"Login to %s\n\nOpen this URL in your browser:\n%s\n\nWaiting for authorization...",
					msg.providerName, wrappedURL))
			}
		}
		return a, msg.waitCmd

	case themeChangedMsg:
		a.chat.AddSystemMessage(fmt.Sprintf("Switched to theme: %s", msg.name))
		return a, nil

	case settingsDisplayMsg:
		a.chat.AddSystemMessage(msg.text)
		return a, nil

	case settingsUpdatedMsg:
		a.chat.AddSystemMessage(fmt.Sprintf("Updated %s to %s", msg.key, msg.value))
		return a, nil

	case rtkDisplayMsg:
		a.chat.AddSystemMessage(msg.text)
		return a, nil

	case rtkUpdatedMsg:
		a.chat.AddSystemMessage(fmt.Sprintf("RTK %s set to %s", msg.setting, msg.value))
		return a, nil

	// ---- Model selector events ----
	case showModelSelectorMsg:
		a.modelSelector.SetSize(a.width, a.height)
		if msg.filter != "" {
			a.modelSelector.ShowWithFilter(msg.filter)
		} else {
			a.modelSelector.Show()
		}
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

		// When editor is in search mode, route all keys to it directly.
		if a.editor.IsSearching() {
			editorCmd := a.editor.Update(msg)
			a.layout() // search hint may change editor height
			return a, editorCmd
		}

		if action, ok := a.keybindings.ActionFor(msg.String()); ok {
			if cmd := a.handleAction(action); cmd != nil {
				return a, cmd
			}
		}

		// Route up/down arrows exclusively: either to the viewport
		// (when scrolled up) or to the editor (when at bottom).
		// Without this, both components receive the event and the
		// user sees history recall AND viewport scroll simultaneously.
		if msg.Type == tea.KeyUp || msg.Type == tea.KeyDown {
			if !a.chat.AtBottom() {
				chatCmd := a.chat.Update(msg)
				if chatCmd != nil {
					cmds = append(cmds, chatCmd)
				}
			} else {
				editorCmd := a.editor.Update(msg)
				if editorCmd != nil {
					cmds = append(cmds, editorCmd)
				}
			}
			return a, tea.Batch(cmds...)
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

// ---------------------------------------------------------------------------
// Keybinding action dispatch
// ---------------------------------------------------------------------------

// handleAction executes the given keybinding action and returns a tea.Cmd if
// the action was handled, or nil to let the event propagate.
func (a *App) handleAction(action Action) tea.Cmd {
	switch action {

	case ActionToggleThinking:
		if a.agentRunning {
			a.chat.ToggleThinking()
			return nil
		}

	case ActionToggleToolResult:
		if a.agentRunning {
			a.chat.ToggleToolResult()
			return nil
		}

	case ActionCycleThinking:
		return a.cycleThinking()

	case ActionCycleModelForward:
		return a.cycleModel(1)

	case ActionCycleModelBackward:
		return a.cycleModel(-1)

	case ActionSuspend:
		return tea.Suspend

	case ActionToggleMouse:
		a.mouseEnabled = !a.mouseEnabled
		if a.mouseEnabled {
			a.chat.AddSystemMessage("Mouse capture ON — scroll with mouse, Alt+M to toggle back for text selection")
			return func() tea.Msg { return tea.EnableMouseCellMotion() }
		}
		a.chat.AddSystemMessage("Mouse capture OFF — select and copy text freely, Alt+M to toggle back for scrolling")
		return func() tea.Msg { return tea.DisableMouse() }

	case ActionHistorySearch:
		if a.agentRunning {
			return nil
		}
		var prompts []string
		if a.sessionMgr != nil {
			prompts = a.sessionMgr.CollectUserPrompts(1000)
		}
		// Merge in-memory editor history (most recent first), deduplicating.
		seen := make(map[string]bool, len(prompts))
		for _, p := range prompts {
			seen[p] = true
		}
		for i := len(a.editor.history) - 1; i >= 0; i-- {
			h := a.editor.history[i]
			if !seen[h] {
				seen[h] = true
				prompts = append([]string{h}, prompts...)
			}
		}
		a.editor.EnterSearchMode(prompts)
		a.layout()
		return nil
	}

	return nil
}

// thinkingOrder defines the cycle order for thinking levels.
var thinkingOrder = []string{"off", "low", "medium", "high"}

// cycleThinking advances the thinking level to the next value in the cycle.
func (a *App) cycleThinking() tea.Cmd {
	if a.cfg == nil || a.agentLoop == nil {
		return nil
	}

	current := a.cfg.ThinkingLevel
	nextIdx := 0
	for i, level := range thinkingOrder {
		if level == current {
			nextIdx = (i + 1) % len(thinkingOrder)
			break
		}
	}

	next := thinkingOrder[nextIdx]
	level := validThinkingLevels[next]
	a.cfg.ThinkingLevel = next
	a.agentLoop.SetThinking(level)
	a.header.SetThinking(level)
	if err := a.cfg.Save(); err != nil {
		log.Printf("config save: %v", err)
	}

	text := fmt.Sprintf("Thinking: %s", next)
	a.chat.AddSystemMessage(text)
	return nil
}

// cycleModel switches to the next (or previous) model in the default list.
func (a *App) cycleModel(direction int) tea.Cmd {
	if a.cfg == nil {
		return nil
	}

	currentModel := a.cfg.DefaultModel
	currentIdx := -1
	for i, opt := range defaultModels {
		if opt.Model == currentModel {
			currentIdx = i
			break
		}
	}

	var nextIdx int
	if currentIdx < 0 {
		nextIdx = 0
	} else {
		nextIdx = (currentIdx + direction + len(defaultModels)) % len(defaultModels)
	}

	opt := defaultModels[nextIdx]
	return func() tea.Msg {
		return modelSelectedMsg{provider: opt.Provider, model: opt.Model}
	}
}

// ---------------------------------------------------------------------------
// Render tick
// ---------------------------------------------------------------------------

// tickRender returns a tea.Cmd that fires a renderTickMsg after ~33ms (~30fps).
func tickRender() tea.Cmd {
	return tea.Tick(33*time.Millisecond, func(time.Time) tea.Msg {
		return renderTickMsg{}
	})
}

// tickIdleRender returns a tea.Cmd that fires an idleRenderMsg after 100ms.
// The gen parameter is compared against App.deltaGen when the tick fires;
// if they match, no new text deltas arrived and a glamour re-render is safe.
func tickIdleRender(gen uint64) tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg {
		return idleRenderMsg{gen: gen}
	})
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
			// Accumulate session-level usage. Reset if session changed.
			if a.sessionMgr != nil {
				if id := a.sessionMgr.CurrentID(); id != a.sessionUsageID {
					a.sessionUsage = ai.Usage{}
					a.sessionUsageID = id
				}
			}
			a.sessionUsage.InputTokens += ev.Usage.InputTokens
			a.sessionUsage.OutputTokens += ev.Usage.OutputTokens
			a.sessionUsage.CacheRead += ev.Usage.CacheRead
			a.sessionUsage.CacheWrite += ev.Usage.CacheWrite
		}
	}
}

// thinkingFromString converts a string to the ai.ThinkingLevel type.
func thinkingFromString(s string) ai.ThinkingLevel {
	switch s {
	case "low":
		return ai.ThinkingLow
	case "medium":
		return ai.ThinkingMedium
	case "high":
		return ai.ThinkingHigh
	default:
		return ai.ThinkingOff
	}
}

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
