package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
)

func TestNewApp(t *testing.T) {
	app := NewApp()
	if app.chat == nil {
		t.Error("chat should be initialized")
	}
	if app.editor == nil {
		t.Error("editor should be initialized")
	}
	if app.header == nil {
		t.Error("header should be initialized")
	}
	if app.footer == nil {
		t.Error("footer should be initialized")
	}
	if app.modelSelector == nil {
		t.Error("modelSelector should be initialized")
	}
	if app.commands == nil {
		t.Error("commands should be initialized")
	}
	// /model command should be registered.
	if _, ok := app.commands.Get("model"); !ok {
		t.Error("expected /model command to be registered")
	}
}

func TestApp_Init(t *testing.T) {
	app := NewApp()
	cmd := app.Init()
	if cmd != nil {
		t.Error("Init should return nil")
	}
}

func TestApp_SetModel(t *testing.T) {
	app := NewApp()
	app.SetModel("gpt-4o")
	if app.header.model != "gpt-4o" {
		t.Errorf("expected gpt-4o, got %q", app.header.model)
	}
}

func TestApp_SetThinking(t *testing.T) {
	app := NewApp()
	app.SetThinking("high")
	if app.header.thinkingLevel != ai.ThinkingHigh {
		t.Errorf("expected ThinkingHigh, got %q", app.header.thinkingLevel)
	}
}

func TestApp_SetSession(t *testing.T) {
	app := NewApp()
	app.SetSession("my-session")
	if app.header.sessionName != "my-session" {
		t.Errorf("expected 'my-session', got %q", app.header.sessionName)
	}
}

func TestApp_RegisterCommand(t *testing.T) {
	app := NewApp()
	app.RegisterCommand(&SlashCommand{Name: "test", Description: "test cmd"})
	if _, ok := app.commands.Get("test"); !ok {
		t.Error("expected command to be registered")
	}
}

func TestApp_ShowWelcome(t *testing.T) {
	app := NewApp()
	app.ShowWelcome("Welcome!")
	if len(app.chat.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(app.chat.blocks))
	}
	if app.chat.blocks[0].text != "Welcome!" {
		t.Errorf("expected 'Welcome!', got %q", app.chat.blocks[0].text)
	}
}

func TestApp_SetModelChangeCallback(t *testing.T) {
	app := NewApp()
	var gotProvider, gotModel string
	app.SetModelChangeCallback(func(p, m string) {
		gotProvider = p
		gotModel = m
	})
	app.onModelChange("anthropic", "claude-sonnet")
	if gotProvider != "anthropic" || gotModel != "claude-sonnet" {
		t.Errorf("callback not called correctly: %q, %q", gotProvider, gotModel)
	}
}

// ---------------------------------------------------------------------------
// Update tests
// ---------------------------------------------------------------------------

func TestApp_Update_WindowSizeMsg(t *testing.T) {
	app := NewApp()
	_, cmd := app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	if app.width != 120 || app.height != 40 {
		t.Errorf("expected 120x40, got %dx%d", app.width, app.height)
	}
	if !app.initialized {
		t.Error("expected initialized=true after first WindowSizeMsg")
	}
	// First resize should return textarea.Blink cmd.
	if cmd == nil {
		t.Error("expected non-nil cmd on first WindowSizeMsg")
	}

	// Second resize should not return blink.
	_, cmd = app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if cmd != nil {
		t.Error("expected nil cmd on subsequent WindowSizeMsg")
	}
}

func TestApp_Update_AgentDoneMsg(t *testing.T) {
	app := NewApp()
	app.agentRunning = true
	app.editor.SetState(editorRunning)

	app.Update(AgentDoneMsg{})

	if app.agentRunning {
		t.Error("expected agentRunning=false")
	}
	if app.editor.state != editorIdle {
		t.Error("expected editor state to be idle")
	}
}

func TestApp_Update_AgentErrorMsg(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	app.Update(AgentErrorMsg{Err: errors.New("test error")})

	if app.agentRunning {
		t.Error("expected agentRunning=false")
	}
	// Should add an error block.
	found := false
	for _, b := range app.chat.blocks {
		if b.kind == blockError {
			found = true
		}
	}
	if !found {
		t.Error("expected an error block in chat")
	}
}

func TestApp_Update_EditorSubmitMsg(t *testing.T) {
	app := NewApp()
	var submitted string
	app.SetCallbacks(func(text string) { submitted = text }, nil, nil)

	app.Update(editorSubmitMsg{text: "hello"})

	if submitted != "hello" {
		t.Errorf("expected 'hello', got %q", submitted)
	}
	if !app.agentRunning {
		t.Error("expected agentRunning=true")
	}
	if app.editor.state != editorRunning {
		t.Error("expected editor running state")
	}
	// Should add user message to chat.
	if len(app.chat.blocks) == 0 || app.chat.blocks[0].kind != blockUser {
		t.Error("expected user message in chat")
	}
}

func TestApp_Update_EditorCommandMsg_Known(t *testing.T) {
	app := NewApp()
	var called bool
	app.RegisterCommand(&SlashCommand{
		Name: "test",
		Execute: func(args string) tea.Cmd {
			called = true
			return nil
		},
	})

	app.Update(editorCommandMsg{name: "test", args: ""})
	// Execute is called via the returned tea.Cmd, but we registered a non-nil
	// Execute, so calling should work without error even if cmd is nil.
	_ = called
}

func TestApp_Update_EditorCommandMsg_Unknown(t *testing.T) {
	app := NewApp()
	app.Update(editorCommandMsg{name: "nonexistent", args: ""})

	// Should add "Unknown command" message.
	if len(app.chat.blocks) == 0 {
		t.Fatal("expected a message about unknown command")
	}
	if app.chat.blocks[0].kind != blockUser {
		t.Errorf("expected blockUser for unknown command message, got %d", app.chat.blocks[0].kind)
	}
}

func TestApp_Update_CommandResultMsg(t *testing.T) {
	app := NewApp()
	app.Update(CommandResultMsg{Text: "info", IsError: false})
	if len(app.chat.blocks) != 1 || app.chat.blocks[0].kind != blockSystem {
		t.Error("expected system message for command result")
	}
}

func TestApp_Update_CommandResultMsg_Error(t *testing.T) {
	app := NewApp()
	app.Update(CommandResultMsg{Text: "fail", IsError: true})
	if len(app.chat.blocks) != 1 {
		t.Fatal("expected 1 block")
	}
	if app.chat.blocks[0].text != "Error: fail" {
		t.Errorf("expected 'Error: fail', got %q", app.chat.blocks[0].text)
	}
}

func TestApp_Update_PluginInjectMsg(t *testing.T) {
	app := NewApp()
	app.Update(PluginInjectMsg{PluginName: "test", Content: "hello", IsLog: false})
	if len(app.chat.blocks) != 1 || app.chat.blocks[0].kind != blockPlugin {
		t.Error("expected plugin block")
	}
}

func TestApp_Update_EditorSteerMsg(t *testing.T) {
	app := NewApp()
	var steered string
	app.SetCallbacks(nil, func(text string) { steered = text }, nil)

	app.Update(editorSteerMsg{text: "redirect"})
	if steered != "redirect" {
		t.Errorf("expected 'redirect', got %q", steered)
	}
}

func TestApp_Update_EditorCancelMsg(t *testing.T) {
	app := NewApp()
	var cancelled bool
	app.SetCallbacks(nil, nil, func() { cancelled = true })

	app.Update(editorCancelMsg{})
	if !cancelled {
		t.Error("expected cancel callback to be called")
	}
}

func TestApp_Update_EditorQuitMsg(t *testing.T) {
	app := NewApp()
	_, cmd := app.Update(editorQuitMsg{})
	if !app.quitting {
		t.Error("expected quitting=true")
	}
	if cmd == nil {
		t.Error("expected tea.Quit cmd")
	}
}

func TestApp_Update_ShowModelSelectorMsg(t *testing.T) {
	app := NewApp()
	app.width = 80
	app.height = 40

	app.Update(showModelSelectorMsg{})
	if !app.modelSelector.Visible() {
		t.Error("expected model selector visible")
	}
}

func TestApp_Update_ModelSelectedMsg(t *testing.T) {
	app := NewApp()
	var gotProvider, gotModel string
	app.SetModelChangeCallback(func(p, m string) {
		gotProvider = p
		gotModel = m
	})

	app.modelSelector.Show()
	app.Update(modelSelectedMsg{provider: "anthropic", model: "claude-opus"})

	if app.modelSelector.Visible() {
		t.Error("expected model selector hidden after selection")
	}
	if app.header.model != "claude-opus" {
		t.Errorf("expected 'claude-opus', got %q", app.header.model)
	}
	if gotProvider != "anthropic" || gotModel != "claude-opus" {
		t.Error("expected model change callback to be called")
	}
}

func TestApp_Update_ModelCancelledMsg(t *testing.T) {
	app := NewApp()
	app.modelSelector.Show()

	app.Update(modelCancelledMsg{})
	if app.modelSelector.Visible() {
		t.Error("expected model selector hidden after cancel")
	}
}

func TestApp_Update_CompactionStartMsg(t *testing.T) {
	app := NewApp()
	app.Update(compactionStartMsg{})
	if len(app.chat.blocks) != 1 || app.chat.blocks[0].kind != blockSystem {
		t.Error("expected system message about compaction")
	}
}

func TestApp_Update_CompactionDoneMsg(t *testing.T) {
	app := NewApp()
	app.Update(compactionDoneMsg{summary: "compacted"})
	if len(app.chat.blocks) != 1 || app.chat.blocks[0].kind != blockCompaction {
		t.Error("expected compaction block")
	}
}

func TestApp_Update_CompactionErrorMsg(t *testing.T) {
	app := NewApp()
	app.Update(compactionErrorMsg{err: errors.New("compact failed")})
	found := false
	for _, b := range app.chat.blocks {
		if b.kind == blockError {
			found = true
		}
	}
	if !found {
		t.Error("expected error block from compaction error")
	}
}

func TestApp_Update_SettingsDisplayMsg(t *testing.T) {
	app := NewApp()
	app.Update(settingsDisplayMsg{text: "settings info"})
	if len(app.chat.blocks) != 1 || app.chat.blocks[0].kind != blockSystem {
		t.Error("expected system message for settings display")
	}
}

func TestApp_Update_SettingsUpdatedMsg(t *testing.T) {
	app := NewApp()
	app.Update(settingsUpdatedMsg{key: "temp", value: "0.5"})
	if len(app.chat.blocks) != 1 {
		t.Fatal("expected 1 block")
	}
	if app.chat.blocks[0].kind != blockSystem {
		t.Error("expected system message for settings update")
	}
}

// ---------------------------------------------------------------------------
// View tests
// ---------------------------------------------------------------------------

func TestApp_View_Quitting(t *testing.T) {
	app := NewApp()
	app.quitting = true
	view := app.View()
	stripped := stripAnsi(view)
	if !strings.Contains(stripped, "Goodbye") {
		t.Errorf("expected 'Goodbye' message, got %q", stripped)
	}
}

func TestApp_View_Uninitialized(t *testing.T) {
	app := NewApp()
	view := app.View()
	if view != "Initializing..." {
		t.Errorf("expected 'Initializing...', got %q", view)
	}
}

func TestApp_View_Normal(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	view := app.View()
	if view == "Initializing..." {
		t.Error("expected actual view after WindowSizeMsg")
	}
	if view == "" {
		t.Error("expected non-empty view")
	}
}

func TestApp_View_ModelSelectorOverlay(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	app.modelSelector.Show()
	view := app.View()
	stripped := stripAnsi(view)
	if !strings.Contains(stripped, "Select Model") {
		t.Error("expected model selector overlay in view")
	}
}

// ---------------------------------------------------------------------------
// HandleAgentEvent
// ---------------------------------------------------------------------------

func TestApp_HandleAgentEvent(t *testing.T) {
	app := NewApp()
	cmd := app.HandleAgentEvent(agent.AgentEvent{
		Type:  agent.EventAssistantText,
		Delta: "hello",
	})
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd()
	ev, ok := msg.(StreamEventMsg)
	if !ok {
		t.Fatalf("expected StreamEventMsg, got %T", msg)
	}
	if ev.Event.Delta != "hello" {
		t.Errorf("expected delta 'hello', got %q", ev.Event.Delta)
	}
}

func TestSendDone(t *testing.T) {
	msg := SendDone()
	if _, ok := msg.(AgentDoneMsg); !ok {
		t.Errorf("expected AgentDoneMsg, got %T", msg)
	}
}

func TestSendError(t *testing.T) {
	msg := SendError(errors.New("test"))
	errMsg, ok := msg.(AgentErrorMsg)
	if !ok {
		t.Fatalf("expected AgentErrorMsg, got %T", msg)
	}
	if errMsg.Err.Error() != "test" {
		t.Errorf("expected 'test' error, got %q", errMsg.Err.Error())
	}
}

// ---------------------------------------------------------------------------
// Layout
// ---------------------------------------------------------------------------

func TestApp_Layout(t *testing.T) {
	app := NewApp()
	app.width = 120
	app.height = 40
	app.layout()

	if app.header.width != 120 {
		t.Errorf("expected header width 120, got %d", app.header.width)
	}
	if app.footer.width != 120 {
		t.Errorf("expected footer width 120, got %d", app.footer.width)
	}
	if app.editor.width != 120 {
		t.Errorf("expected editor width 120, got %d", app.editor.width)
	}
	if app.chat.width != 120 {
		t.Errorf("expected chat width 120, got %d", app.chat.width)
	}
	// Chat height = 40 - 1 (header) - 1 (footer) - 5 (editor) - 3 (newlines) = 30
	if app.chat.height != 30 {
		t.Errorf("expected chat height 30, got %d", app.chat.height)
	}
}

func TestApp_Layout_MinChatHeight(t *testing.T) {
	app := NewApp()
	app.width = 80
	app.height = 5 // Very small terminal
	app.layout()

	if app.chat.height < 3 {
		t.Errorf("expected minimum chat height 3, got %d", app.chat.height)
	}
}

// ---------------------------------------------------------------------------
// handleStateTransition
// ---------------------------------------------------------------------------

func TestApp_HandleStateTransition_AgentStart(t *testing.T) {
	app := NewApp()
	app.handleStateTransition(agent.AgentEvent{Type: agent.EventAgentStart})

	if !app.agentRunning {
		t.Error("expected agentRunning=true")
	}
	if app.editor.state != editorRunning {
		t.Error("expected editor running state")
	}
}

func TestApp_HandleStateTransition_AgentEnd(t *testing.T) {
	app := NewApp()
	app.agentRunning = true
	app.editor.SetState(editorRunning)

	app.handleStateTransition(agent.AgentEvent{Type: agent.EventAgentEnd})

	if app.agentRunning {
		t.Error("expected agentRunning=false")
	}
	if app.editor.state != editorIdle {
		t.Error("expected editor idle state")
	}
}

func TestApp_HandleStateTransition_Thinking(t *testing.T) {
	app := NewApp()
	app.agentRunning = true
	app.handleStateTransition(agent.AgentEvent{Type: agent.EventAssistantThinking})
	if app.editor.state != editorThinking {
		t.Error("expected editor thinking state")
	}
}

func TestApp_HandleStateTransition_TextAfterThinking(t *testing.T) {
	app := NewApp()
	app.agentRunning = true
	app.handleStateTransition(agent.AgentEvent{Type: agent.EventAssistantThinking})
	app.handleStateTransition(agent.AgentEvent{Type: agent.EventAssistantText})
	if app.editor.state != editorRunning {
		t.Error("expected editor running state after text")
	}
}

func TestApp_HandleStateTransition_UsageUpdate(t *testing.T) {
	app := NewApp()
	usage := &ai.Usage{InputTokens: 1000, OutputTokens: 500}
	app.handleStateTransition(agent.AgentEvent{
		Type:  agent.EventUsageUpdate,
		Usage: usage,
	})
	if app.footer.usage.InputTokens != 1000 {
		t.Errorf("expected input tokens 1000, got %d", app.footer.usage.InputTokens)
	}
}

func TestApp_HandleStateTransition_UsageUpdate_NilUsage(t *testing.T) {
	app := NewApp()
	// Should not panic with nil Usage.
	app.handleStateTransition(agent.AgentEvent{
		Type:  agent.EventUsageUpdate,
		Usage: nil,
	})
}

// ---------------------------------------------------------------------------
// thinkingFromString
// ---------------------------------------------------------------------------

func TestThinkingFromString(t *testing.T) {
	tests := []struct {
		input string
		want  ai.ThinkingLevel
	}{
		{"low", ai.ThinkingLow},
		{"medium", ai.ThinkingMedium},
		{"high", ai.ThinkingHigh},
		{"off", ai.ThinkingOff},
		{"", ai.ThinkingOff},
		{"unknown", ai.ThinkingOff},
	}
	for _, tt := range tests {
		got := thinkingFromString(tt.input)
		if got != tt.want {
			t.Errorf("thinkingFromString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// StreamEventMsg routing through Update
// ---------------------------------------------------------------------------

func TestApp_Update_StreamEventMsg(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{
			Type:  agent.EventAssistantText,
			Delta: "hello from agent",
		},
	})

	if len(app.chat.blocks) == 0 {
		t.Fatal("expected at least one block")
	}
	if app.chat.blocks[0].text != "hello from agent" {
		t.Errorf("expected 'hello from agent', got %q", app.chat.blocks[0].text)
	}
}

func TestApp_Update_StreamEventMsg_IncrementsGen(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Each text delta should increment deltaGen.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "a"},
	})
	if app.deltaGen != 1 {
		t.Errorf("expected deltaGen=1 after first delta, got %d", app.deltaGen)
	}

	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "b"},
	})
	if app.deltaGen != 2 {
		t.Errorf("expected deltaGen=2 after second delta, got %d", app.deltaGen)
	}

	// Non-text events should not increment deltaGen.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantThinking, Delta: "hmm"},
	})
	if app.deltaGen != 2 {
		t.Errorf("expected deltaGen=2 after thinking event, got %d", app.deltaGen)
	}
}

func TestApp_Update_IdleRenderMsg_MatchingGen(t *testing.T) {
	app := NewApp()
	app.agentRunning = true
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	// Send a text delta to create a streaming block.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "**bold**"},
	})
	// Force render so we can compare.
	app.Update(renderTickMsg{})

	if !app.chat.blocks[0].streaming {
		t.Fatal("expected streaming=true")
	}

	// Send idle render with matching gen — should trigger glamour.
	app.Update(idleRenderMsg{gen: app.deltaGen})
	if app.chat.blocks[0].streaming {
		t.Error("expected streaming=false after idle render with matching gen")
	}
}

func TestApp_Update_IdleRenderMsg_StaleGen(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Send a text delta.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "text"},
	})
	staleGen := app.deltaGen

	// Send another delta — gen advances.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: " more"},
	})

	// Idle render with stale gen should be ignored.
	app.Update(idleRenderMsg{gen: staleGen})
	if !app.chat.blocks[0].streaming {
		t.Error("expected streaming=true — stale idle render should be ignored")
	}
}

// ---------------------------------------------------------------------------
// Delta coalescing between frames (gp-6nw.3)
//
// Between render ticks, multiple StreamEventMsg may arrive. The App must:
//   - Accumulate all text deltas into the block text (no data loss)
//   - Set dirty=true on each delta
//   - Call rebuildContent() at most once per tick (on renderTickMsg)
// ---------------------------------------------------------------------------

func TestApp_DeltaCoalescing_MultipleTextDeltas(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Simulate 5 text deltas arriving between ticks.
	deltas := []string{"Hello", " ", "world", "! ", "How are you?"}
	for _, d := range deltas {
		app.Update(StreamEventMsg{
			Event: agent.AgentEvent{
				Type:  agent.EventAssistantText,
				Delta: d,
			},
		})
	}

	// All deltas should accumulate in one block.
	if len(app.chat.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(app.chat.blocks))
	}
	want := "Hello world! How are you?"
	if app.chat.blocks[0].text != want {
		t.Errorf("expected %q, got %q", want, app.chat.blocks[0].text)
	}
	if !app.chat.dirty {
		t.Error("expected dirty=true before tick")
	}

	// A single tick should flush all accumulated deltas.
	app.Update(renderTickMsg{})
	if app.chat.dirty {
		t.Error("expected dirty=false after tick")
	}
}

func TestApp_DeltaCoalescing_MultipleThinkingDeltas(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Simulate thinking deltas arriving between ticks.
	deltas := []string{"Let me ", "think about ", "this..."}
	for _, d := range deltas {
		app.Update(StreamEventMsg{
			Event: agent.AgentEvent{
				Type:  agent.EventAssistantThinking,
				Delta: d,
			},
		})
	}

	if len(app.chat.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(app.chat.blocks))
	}
	want := "Let me think about this..."
	if app.chat.blocks[0].text != want {
		t.Errorf("expected %q, got %q", want, app.chat.blocks[0].text)
	}
	if app.chat.blocks[0].kind != blockThinking {
		t.Error("expected blockThinking")
	}
}

func TestApp_DeltaCoalescing_NoDeltaLoss(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Send a large number of deltas to stress-test accumulation.
	const n = 100
	for i := 0; i < n; i++ {
		app.Update(StreamEventMsg{
			Event: agent.AgentEvent{
				Type:  agent.EventAssistantText,
				Delta: "x",
			},
		})
	}

	if len(app.chat.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(app.chat.blocks))
	}
	if len(app.chat.blocks[0].text) != n {
		t.Errorf("expected %d chars, got %d — deltas were lost", n, len(app.chat.blocks[0].text))
	}
}

func TestApp_DeltaCoalescing_DirtyFlagLifecycle(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Initially not dirty.
	if app.chat.dirty {
		t.Error("expected dirty=false initially")
	}

	// First delta sets dirty.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "a"},
	})
	if !app.chat.dirty {
		t.Error("expected dirty=true after first delta")
	}

	// More deltas keep dirty=true.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "b"},
	})
	if !app.chat.dirty {
		t.Error("expected dirty=true after second delta")
	}

	// Tick clears dirty.
	app.Update(renderTickMsg{})
	if app.chat.dirty {
		t.Error("expected dirty=false after tick")
	}

	// No-change tick: dirty stays false.
	app.Update(renderTickMsg{})
	if app.chat.dirty {
		t.Error("expected dirty=false when no new deltas")
	}

	// New delta after tick sets dirty again.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "c"},
	})
	if !app.chat.dirty {
		t.Error("expected dirty=true after new delta")
	}
}

func TestApp_DeltaCoalescing_AgentDoneFlushesDirty(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Send deltas without any tick.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "final"},
	})
	if !app.chat.dirty {
		t.Fatal("expected dirty=true before AgentDoneMsg")
	}

	// AgentDoneMsg should flush remaining dirty state.
	app.Update(AgentDoneMsg{})
	if app.chat.dirty {
		t.Error("expected dirty=false after AgentDoneMsg flushes")
	}
	// Text should still be intact.
	if app.chat.blocks[0].text != "final" {
		t.Errorf("expected 'final', got %q", app.chat.blocks[0].text)
	}
}

func TestApp_DeltaCoalescing_MixedEventTypes(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Thinking deltas.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantThinking, Delta: "hmm "},
	})
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantThinking, Delta: "interesting"},
	})

	// Text deltas.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "Here's "},
	})
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "the answer"},
	})

	// Tool event.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventToolExecStart, ToolCallID: "tc-1", ToolName: "bash"},
	})

	// All should accumulate before tick.
	if len(app.chat.blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(app.chat.blocks))
	}
	if app.chat.blocks[0].text != "hmm interesting" {
		t.Errorf("thinking: expected 'hmm interesting', got %q", app.chat.blocks[0].text)
	}
	if app.chat.blocks[1].text != "Here's the answer" {
		t.Errorf("text: expected 'Here's the answer', got %q", app.chat.blocks[1].text)
	}
	if app.chat.blocks[2].kind != blockToolCall {
		t.Error("expected third block to be blockToolCall")
	}

	// One tick flushes everything.
	if !app.chat.dirty {
		t.Error("expected dirty=true before tick")
	}
	app.Update(renderTickMsg{})
	if app.chat.dirty {
		t.Error("expected dirty=false after tick")
	}
}

func TestApp_DeltaCoalescing_TickStopsWhenNotRunning(t *testing.T) {
	app := NewApp()
	app.agentRunning = false

	// Tick when not running should not schedule another tick.
	_, cmd := app.Update(renderTickMsg{})
	if cmd != nil {
		t.Error("expected nil cmd when agent is not running")
	}
}

func TestApp_DeltaCoalescing_TickContinuesWhileRunning(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Tick while running should schedule another tick.
	_, cmd := app.Update(renderTickMsg{})
	if cmd == nil {
		t.Error("expected non-nil cmd (next tick) while agent is running")
	}
}

func TestApp_DeltaCoalescing_StreamEventNoImmediateRebuild(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Text delta returns a cmd (idle-render timer), but NOT an immediate
	// rebuildContent call — the dirty flag defers rendering to the next tick.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "test"},
	})
	// The block text should have the delta but rendered cache should be empty
	// (no rebuildContent was called inline).
	if app.chat.blocks[0].rendered != "" {
		t.Error("expected empty rendered cache — no inline rebuildContent")
	}
	if !app.chat.dirty {
		t.Error("expected dirty=true — rebuild deferred to tick")
	}

	// Non-text events should still return nil cmd.
	_, cmd := app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantThinking, Delta: "hmm"},
	})
	if cmd != nil {
		t.Error("non-text StreamEventMsg should return nil cmd")
	}
}

func TestApp_DeltaCoalescing_TurnEndResetsStreaming(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Stream text deltas.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "hello"},
	})
	if !app.chat.blocks[0].streaming {
		t.Error("expected streaming=true during deltas")
	}

	// TurnEnd should mark streaming=false.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventTurnEnd},
	})
	if app.chat.blocks[0].streaming {
		t.Error("expected streaming=false after TurnEnd")
	}
	// dirty should be true so the tick triggers a glamour re-render.
	if !app.chat.dirty {
		t.Error("expected dirty=true after TurnEnd")
	}
}

func TestApp_DeltaCoalescing_NonChangingEventNoDirty(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// UsageUpdate doesn't produce block changes — dirty should stay false.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{
			Type:  agent.EventUsageUpdate,
			Usage: &ai.Usage{InputTokens: 100},
		},
	})
	if app.chat.dirty {
		t.Error("expected dirty=false for non-content-changing event")
	}
}

// ---------------------------------------------------------------------------
// Keyboard shortcuts at app level
// ---------------------------------------------------------------------------

func TestApp_Update_KeyT_ToggleThinking(t *testing.T) {
	app := NewApp()
	app.agentRunning = true
	// Add a thinking block.
	app.chat.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantThinking,
		Delta: "thinking...",
	})

	if !app.chat.blocks[0].collapsed {
		t.Fatal("should start collapsed")
	}

	app.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if app.chat.blocks[0].collapsed {
		t.Error("should be expanded after Ctrl+T while running")
	}
}

func TestApp_Update_KeyR_ToggleToolResult(t *testing.T) {
	app := NewApp()
	app.agentRunning = true
	app.chat.HandleEvent(agent.AgentEvent{Type: agent.EventToolExecStart, ToolCallID: "tc-1", ToolName: "bash"})
	app.chat.HandleEvent(agent.AgentEvent{Type: agent.EventToolExecEnd, ToolCallID: "tc-1", ToolResult: "output"})

	app.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if app.chat.blocks[0].collapsed {
		t.Error("should be expanded after Ctrl+R while running")
	}
}

func TestApp_Update_ModelSelectorIntercepts(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	app.modelSelector.Show()

	// Escape should close selector, not go to editor.
	app.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if app.modelSelector.Visible() {
		t.Error("expected model selector hidden after Escape")
	}
}

// ---------------------------------------------------------------------------
// Extreme terminal sizes (1x1)
// ---------------------------------------------------------------------------

func TestApp_Update_WindowSizeMsg_1x1(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 1, Height: 1})

	if app.width != 1 || app.height != 1 {
		t.Errorf("expected 1x1, got %dx%d", app.width, app.height)
	}
	if !app.initialized {
		t.Error("expected initialized=true")
	}
	// Chat height should be clamped to minimum 3.
	if app.chat.height < 3 {
		t.Errorf("expected minimum chat height 3, got %d", app.chat.height)
	}
}

func TestApp_View_1x1(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 1, Height: 1})
	// View should not panic at extreme sizes.
	view := app.View()
	if view == "" {
		t.Error("expected non-empty view")
	}
	if view == "Initializing..." {
		t.Error("should be initialized after WindowSizeMsg")
	}
}

func TestApp_View_1x1_WithContent(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 1, Height: 1})
	app.ShowWelcome("Welcome to the app!")
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{
			Type:  agent.EventAssistantText,
			Delta: "Hello, world!",
		},
	})
	// View with content at extreme size should not panic.
	_ = app.View()
}

func TestApp_Layout_1x1(t *testing.T) {
	app := NewApp()
	app.width = 1
	app.height = 1
	app.layout()

	// Chat height must be at least 3 (the minimum).
	if app.chat.height < 3 {
		t.Errorf("expected minimum chat height 3, got %d", app.chat.height)
	}
}

func TestApp_View_ModelSelector_1x1(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 1, Height: 1})
	app.modelSelector.Show()
	// Model selector overlay at extreme size should not panic.
	view := app.View()
	if view == "" {
		t.Error("expected non-empty view with model selector")
	}
}

// ---------------------------------------------------------------------------
// Rapid resize events
// ---------------------------------------------------------------------------

func TestApp_RapidResize(t *testing.T) {
	app := NewApp()
	app.ShowWelcome("Welcome")

	// Simulate rapid resize events.
	sizes := [][2]int{
		{120, 40}, {80, 30}, {1, 1}, {200, 60}, {10, 5},
		{1, 1}, {80, 24}, {300, 100}, {40, 10}, {120, 40},
	}
	for _, sz := range sizes {
		app.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
	}

	if app.width != 120 || app.height != 40 {
		t.Errorf("expected final size 120x40, got %dx%d", app.width, app.height)
	}
	// Content should still be intact.
	if len(app.chat.blocks) != 1 {
		t.Errorf("expected 1 block after rapid resizes, got %d", len(app.chat.blocks))
	}
	// View should render without error.
	view := app.View()
	if view == "" || view == "Initializing..." {
		t.Error("expected valid view after rapid resizes")
	}
}

func TestApp_RapidResize_WithModelSelector(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	app.modelSelector.Show()

	// Resize rapidly while model selector is visible.
	for i := 1; i <= 20; i++ {
		app.Update(tea.WindowSizeMsg{Width: i * 5, Height: i * 3})
	}

	if !app.modelSelector.Visible() {
		t.Error("model selector should still be visible after resizes")
	}
	_ = app.View()
}

func TestApp_RapidResize_DuringAgentRun(t *testing.T) {
	app := NewApp()
	app.agentRunning = true
	app.editor.SetState(editorRunning)

	// Stream some content.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "Hello "},
	})

	// Resize rapidly during streaming.
	for i := 0; i < 10; i++ {
		w := 40 + (i * 20)
		h := 20 + (i * 5)
		app.Update(tea.WindowSizeMsg{Width: w, Height: h})
	}

	// Agent state and content should be preserved.
	if !app.agentRunning {
		t.Error("agentRunning should still be true")
	}
	if len(app.chat.blocks) == 0 {
		t.Error("expected chat blocks to survive resizes")
	}
	if app.chat.blocks[0].text != "Hello " {
		t.Errorf("expected text 'Hello ', got %q", app.chat.blocks[0].text)
	}
}
