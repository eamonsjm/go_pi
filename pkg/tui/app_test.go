package tui

import (
	"errors"
	"fmt"
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

// Test that setModelMsg/setThinkingMsg/setSessionMsg are handled by Update,
// which is the code path used when a tea.Program is set.
func TestApp_SetModelMsg(t *testing.T) {
	app := NewApp()
	app.width, app.height = 80, 24
	app.Update(setModelMsg{name: "gpt-4o"})
	if app.header.model != "gpt-4o" {
		t.Errorf("expected gpt-4o, got %q", app.header.model)
	}
}

func TestApp_SetThinkingMsg(t *testing.T) {
	app := NewApp()
	app.width, app.height = 80, 24
	app.Update(setThinkingMsg{level: "high"})
	if app.header.thinkingLevel != ai.ThinkingHigh {
		t.Errorf("expected ThinkingHigh, got %q", app.header.thinkingLevel)
	}
}

func TestApp_SetSessionMsg(t *testing.T) {
	app := NewApp()
	app.width, app.height = 80, 24
	app.Update(setSessionMsg{name: "my-session"})
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
	if !called {
		t.Error("expected Execute to be called for known command")
	}
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
// StreamEventMsg handling: deltas trigger immediate rebuild so each token
// appears in the viewport without waiting for the next render tick. The App must:
//   - Accumulate all text deltas into the block text (no data loss)
//   - Call rebuildContent() immediately when content changes
//   - Keep render ticks as a safety-net fallback
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
	// Each delta triggers an immediate rebuildContent, so dirty is false.
	if app.chat.dirty {
		t.Error("expected dirty=false — immediate rebuild clears it")
	}

	// Tick is a no-op since dirty is already false.
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

	// First delta triggers immediate rebuild, clearing dirty.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "a"},
	})
	if app.chat.dirty {
		t.Error("expected dirty=false — immediate rebuild clears it")
	}

	// More deltas also rebuild immediately.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "b"},
	})
	if app.chat.dirty {
		t.Error("expected dirty=false after second delta")
	}

	// Tick is a no-op since dirty is already false.
	app.Update(renderTickMsg{})
	if app.chat.dirty {
		t.Error("expected dirty=false after tick")
	}

	// No-change tick: dirty stays false.
	app.Update(renderTickMsg{})
	if app.chat.dirty {
		t.Error("expected dirty=false when no new deltas")
	}

	// New delta after tick: rebuilt immediately, dirty cleared.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "c"},
	})
	if app.chat.dirty {
		t.Error("expected dirty=false after new delta (immediate rebuild)")
	}
}

func TestApp_DeltaCoalescing_AgentDoneFlushesDirty(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Send deltas — immediate rebuild clears dirty.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "final"},
	})
	if app.chat.dirty {
		t.Fatal("expected dirty=false — immediate rebuild already flushed")
	}

	// AgentDoneMsg should still work cleanly.
	app.Update(AgentDoneMsg{})
	if app.chat.dirty {
		t.Error("expected dirty=false after AgentDoneMsg")
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

	// All should accumulate correctly.
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

	// Each event triggers immediate rebuild, so dirty is already false.
	if app.chat.dirty {
		t.Error("expected dirty=false — immediate rebuild clears it")
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

func TestApp_DeltaCoalescing_StreamEventImmediateRebuild(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Text delta triggers immediate rebuildContent so the viewport is
	// updated before the next View() call — eliminating chunky rendering.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "test"},
	})
	// The block should have a populated rendered cache from the immediate rebuild.
	if app.chat.blocks[0].rendered == "" {
		t.Error("expected non-empty rendered cache — immediate rebuildContent")
	}
	if app.chat.dirty {
		t.Error("expected dirty=false — immediate rebuild clears it")
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

	// TurnEnd should mark streaming=false and trigger immediate rebuild.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventTurnEnd},
	})
	if app.chat.blocks[0].streaming {
		t.Error("expected streaming=false after TurnEnd")
	}
	// Immediate rebuild clears dirty.
	if app.chat.dirty {
		t.Error("expected dirty=false — immediate rebuild after TurnEnd")
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

	// toggle_tool_result is bound to alt+r (moved from ctrl+r to make room for history_search).
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}, Alt: true})
	if app.chat.blocks[0].collapsed {
		t.Error("should be expanded after Alt+R while running")
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

// ---------------------------------------------------------------------------
// Frame rate and responsiveness verification (gp-6nw.4)
//
// These tests verify that:
//   - The render tick targets ~30fps (33ms interval)
//   - User input (cancel, steer, scroll) is processed during streaming
//   - The tick-based loop does not starve keyboard handling
// ---------------------------------------------------------------------------

func TestApp_TickRender_Produces_RenderTickMsg(t *testing.T) {
	// tickRender() must return a cmd that eventually produces a renderTickMsg.
	cmd := tickRender()
	if cmd == nil {
		t.Fatal("tickRender() returned nil cmd")
	}
	// Execute the cmd — it wraps tea.Tick which returns the msg immediately
	// in test context (no real timer). We can't control wall time, but we
	// can verify the msg type by calling the returned func.
	msg := cmd()
	if _, ok := msg.(renderTickMsg); !ok {
		t.Errorf("expected renderTickMsg, got %T", msg)
	}
}

func TestApp_TickIdleRender_Produces_IdleRenderMsg(t *testing.T) {
	cmd := tickIdleRender(42)
	if cmd == nil {
		t.Fatal("tickIdleRender() returned nil cmd")
	}
	msg := cmd()
	idle, ok := msg.(idleRenderMsg)
	if !ok {
		t.Fatalf("expected idleRenderMsg, got %T", msg)
	}
	if idle.gen != 42 {
		t.Errorf("expected gen=42, got %d", idle.gen)
	}
}

func TestApp_TickSelfPerpetuation_WhileStreaming(t *testing.T) {
	app := NewApp()
	app.agentRunning = true

	// Submit triggers the first tick.
	_, cmd := app.Update(editorSubmitMsg{text: "go"})
	if cmd == nil {
		t.Fatal("expected tick cmd after submit")
	}

	// Each tick while running should schedule the next tick.
	for i := 0; i < 10; i++ {
		_, cmd = app.Update(renderTickMsg{})
		if cmd == nil {
			t.Fatalf("tick %d did not schedule next tick while agent running", i)
		}
	}

	// Stop the agent — next tick should NOT schedule another.
	app.agentRunning = false
	_, cmd = app.Update(renderTickMsg{})
	if cmd != nil {
		t.Error("tick should not schedule another tick when agent is not running")
	}
}

func TestApp_CancelDuringStreaming(t *testing.T) {
	app := NewApp()
	var cancelled bool
	app.SetCallbacks(func(string) {}, nil, func() { cancelled = true })

	// Start agent run.
	app.agentRunning = true
	app.editor.SetState(editorRunning)

	// Stream some deltas.
	for _, d := range []string{"Hello", " world"} {
		app.Update(StreamEventMsg{
			Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: d},
		})
	}

	// Ctrl-C during streaming should trigger cancel.
	app.Update(editorCancelMsg{})
	if !cancelled {
		t.Error("expected cancel callback to fire during streaming")
	}
}

func TestApp_SteerDuringStreaming(t *testing.T) {
	app := NewApp()
	var steered string
	app.SetCallbacks(func(string) {}, func(text string) { steered = text }, nil)

	// Start agent run with content.
	app.agentRunning = true
	app.editor.SetState(editorRunning)
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "Working..."},
	})

	// Steer message during streaming should be processed.
	app.Update(editorSteerMsg{text: "focus on tests"})
	if steered != "focus on tests" {
		t.Errorf("expected steer text 'focus on tests', got %q", steered)
	}
}

func TestApp_KeyboardInterleavedWithTicks(t *testing.T) {
	app := NewApp()
	app.agentRunning = true
	app.editor.SetState(editorRunning)
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	// Add a thinking block for toggle testing.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantThinking, Delta: "deep thought"},
	})

	// Interleave ticks with keyboard events.
	app.Update(renderTickMsg{})

	// Toggle thinking expand/collapse via Ctrl+T during streaming.
	if !app.chat.blocks[0].collapsed {
		t.Fatal("thinking should start collapsed")
	}
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if app.chat.blocks[0].collapsed {
		t.Error("Ctrl+T should expand thinking even between ticks")
	}

	// Another tick should still process fine.
	_, cmd := app.Update(renderTickMsg{})
	if cmd == nil {
		t.Error("tick should continue after keyboard event")
	}

	// Toggle back.
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if !app.chat.blocks[0].collapsed {
		t.Error("second Ctrl+T should collapse thinking again")
	}
}

func TestApp_ViewportScrollDuringStreaming(t *testing.T) {
	app := NewApp()
	app.agentRunning = true
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 10})

	// Add enough content to make the viewport scrollable.
	for i := 0; i < 50; i++ {
		app.Update(StreamEventMsg{
			Event: agent.AgentEvent{
				Type:  agent.EventAssistantText,
				Delta: fmt.Sprintf("Line %d of output\n", i),
			},
		})
	}
	// Flush to viewport.
	app.Update(renderTickMsg{})

	// Scroll up via key event — should not panic or be blocked.
	app.Update(tea.KeyMsg{Type: tea.KeyUp})
	app.Update(tea.KeyMsg{Type: tea.KeyUp})
	app.Update(tea.KeyMsg{Type: tea.KeyUp})

	// More deltas arrive while scrolled up.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "new content"},
	})
	app.Update(renderTickMsg{})

	// Content should include all text — scroll position is separate.
	if !strings.Contains(app.chat.blocks[0].text, "new content") {
		t.Error("new deltas should accumulate even while scrolled up")
	}
}

func TestApp_UpArrowAtBottomDoesNotScrollViewport(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 10})

	// Populate editor history so Up has something to recall.
	app.editor.history = []string{"previous prompt"}
	app.editor.historyIdx = 1

	// Add enough content to make the viewport scrollable.
	for i := 0; i < 50; i++ {
		app.Update(StreamEventMsg{
			Event: agent.AgentEvent{
				Type:  agent.EventAssistantText,
				Delta: fmt.Sprintf("Line %d\n", i),
			},
		})
	}
	app.Update(renderTickMsg{})

	// Viewport should be at bottom before we press Up.
	if !app.chat.AtBottom() {
		t.Fatal("viewport should start at bottom")
	}

	// Press Up — should recall history, NOT scroll viewport.
	app.Update(tea.KeyMsg{Type: tea.KeyUp})

	if !app.chat.AtBottom() {
		t.Error("viewport should remain at bottom when Up arrow recalls history")
	}
	if got := app.editor.Value(); got != "previous prompt" {
		t.Errorf("editor should show recalled history, got %q", got)
	}
}

func TestApp_UpArrowScrolledUpDoesNotRecallHistory(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 10})

	// Populate editor history.
	app.editor.history = []string{"should not appear"}
	app.editor.historyIdx = 1

	// Add scrollable content.
	for i := 0; i < 50; i++ {
		app.Update(StreamEventMsg{
			Event: agent.AgentEvent{
				Type:  agent.EventAssistantText,
				Delta: fmt.Sprintf("Line %d\n", i),
			},
		})
	}
	app.Update(renderTickMsg{})

	// Scroll up via PageUp so viewport is no longer at bottom.
	app.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	if app.chat.AtBottom() {
		t.Fatal("viewport should not be at bottom after PageUp")
	}

	// Press Up — should scroll viewport, NOT recall history.
	app.Update(tea.KeyMsg{Type: tea.KeyUp})

	if got := app.editor.Value(); got != "" {
		t.Errorf("editor should remain empty when scrolled up, got %q", got)
	}
}

func TestApp_RenderOnlyOnDirty(t *testing.T) {
	app := NewApp()
	app.agentRunning = true
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	// Send a delta and flush it.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "hello"},
	})
	app.Update(renderTickMsg{})
	if app.chat.dirty {
		t.Fatal("dirty should be false after tick flush")
	}

	// Capture the rendered content.
	rendered := app.chat.blocks[0].rendered

	// Tick without new deltas should NOT clear the render cache.
	app.Update(renderTickMsg{})
	if app.chat.blocks[0].rendered != rendered {
		t.Error("tick without new deltas should preserve render cache (no wasted work)")
	}
}

func TestApp_StreamCompletionTriggersGlamour(t *testing.T) {
	app := NewApp()
	app.agentRunning = true
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	// Stream deltas — should be in streaming mode.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "**bold text**"},
	})
	app.Update(renderTickMsg{})
	if !app.chat.blocks[0].streaming {
		t.Fatal("expected streaming=true during active deltas")
	}

	// TurnEnd signals the stream finished — clears streaming flags.
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventTurnEnd},
	})
	if app.chat.blocks[0].streaming {
		t.Error("expected streaming=false after TurnEnd — glamour should render")
	}

	// AgentDoneMsg flushes any remaining dirty state.
	app.Update(AgentDoneMsg{})
	if app.chat.dirty {
		t.Error("expected dirty=false after AgentDoneMsg flush")
	}
}

func TestApp_MultipleAgentCycles_TickLifecycle(t *testing.T) {
	app := NewApp()
	app.SetCallbacks(func(string) {}, nil, nil)

	// First agent run.
	app.Update(editorSubmitMsg{text: "first"})
	app.Update(StreamEventMsg{
		Event: agent.AgentEvent{Type: agent.EventAssistantText, Delta: "response 1"},
	})
	_, cmd := app.Update(renderTickMsg{})
	if cmd == nil {
		t.Error("tick should continue during first run")
	}
	app.Update(AgentDoneMsg{})

	// Between runs, ticks should stop.
	_, cmd = app.Update(renderTickMsg{})
	if cmd != nil {
		t.Error("tick should stop between agent runs")
	}

	// Second agent run — ticks should resume.
	app.Update(editorSubmitMsg{text: "second"})
	_, cmd = app.Update(renderTickMsg{})
	if cmd == nil {
		t.Error("tick should resume for second agent run")
	}
	app.Update(AgentDoneMsg{})
}

func TestApp_Update_PluginUIRequestMsg_InteractiveMode(t *testing.T) {
	app := NewApp()
	app.SetHasUI(true) // Interactive mode

	// Store a UI request
	app.Update(PluginUIRequestMsg{
		PluginName: "test-plugin",
		ID:         "req123",
		UIType:     "input",
		UITitle:    "Enter your name:",
		UIDefault:  "John",
	})

	// Verify the request is stored
	if app.uiRequestPending == nil {
		t.Error("UI request should be stored")
	}
	if app.uiRequestPending.PluginName != "test-plugin" {
		t.Errorf("expected plugin name test-plugin, got %s", app.uiRequestPending.PluginName)
	}

	// Submit a response via the editor
	responseChan := make(chan *PluginUIResponseMsg, 1)
	app.SetUIResponseCallback(func(resp *PluginUIResponseMsg) {
		responseChan <- resp
	})

	app.Update(editorSubmitMsg{text: "Alice"})

	// Verify the response was sent
	resp := <-responseChan
	if resp.ID != "req123" {
		t.Errorf("expected request ID req123, got %s", resp.ID)
	}
	if resp.Value != "Alice" {
		t.Errorf("expected value Alice, got %s", resp.Value)
	}
	if resp.Closed {
		t.Error("response should not be marked as closed")
	}

	// Verify the request is cleared
	if app.uiRequestPending != nil {
		t.Error("UI request should be cleared after response")
	}
}

// ---------------------------------------------------------------------------
// Model selection and provider switching end-to-end flow (gp-u2ch)
// ---------------------------------------------------------------------------

func TestApp_ModelCommandFlow_NoArgs_ShowsSelector(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	// Simulate /model command with no args via editorCommandMsg.
	_, cmd := app.Update(editorCommandMsg{name: "model", args: ""})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from /model command")
	}
	// Execute the returned cmd to get the message.
	msg := cmd()
	// Feed the message back into Update to trigger the selector.
	app.Update(msg)

	if !app.modelSelector.Visible() {
		t.Error("expected model selector visible after /model with no args")
	}
	// Editor should be blurred when selector is shown.
	view := app.View()
	stripped := stripAnsi(view)
	if !strings.Contains(stripped, "Select Model") {
		t.Error("expected 'Select Model' in view when selector is shown")
	}
}

func TestApp_ModelCommandFlow_WithFilter_ShowsPreFiltered(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// /model opus → multiple matches → show selector pre-filtered.
	_, cmd := app.Update(editorCommandMsg{name: "model", args: "opus"})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from /model opus")
	}
	msg := cmd()
	selectorMsg, ok := msg.(showModelSelectorMsg)
	if !ok {
		t.Fatalf("expected showModelSelectorMsg, got %T", msg)
	}
	if selectorMsg.filter != "opus" {
		t.Errorf("expected filter 'opus', got %q", selectorMsg.filter)
	}

	// Feed it back to show the selector with filter.
	app.Update(msg)
	if !app.modelSelector.Visible() {
		t.Error("expected model selector visible")
	}
	if app.modelSelector.filter != "opus" {
		t.Errorf("expected selector filter 'opus', got %q", app.modelSelector.filter)
	}
}

func TestApp_ModelCommandFlow_SelectModel_UpdatesHeader(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	var gotProvider, gotModel string
	app.SetModelChangeCallback(func(p, m string) {
		gotProvider = p
		gotModel = m
	})

	// Show selector, navigate, select.
	app.Update(showModelSelectorMsg{})
	if !app.modelSelector.Visible() {
		t.Fatal("expected selector visible")
	}

	// Select current model (Enter on first item).
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from Enter in selector")
	}
	msg := cmd()
	app.Update(msg)

	// Selector should be hidden.
	if app.modelSelector.Visible() {
		t.Error("expected selector hidden after selection")
	}
	// Header should be updated.
	if app.header.model == "" {
		t.Error("expected header model to be set")
	}
	// Callback should have fired.
	if gotProvider == "" || gotModel == "" {
		t.Error("expected model change callback to be called")
	}
	// System message should confirm the switch.
	found := false
	for _, b := range app.chat.blocks {
		if b.kind == blockSystem && strings.Contains(b.text, "Switched to model") {
			found = true
		}
	}
	if !found {
		t.Error("expected 'Switched to model' system message")
	}
}

func TestApp_ModelCommandFlow_CancelSelector(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	var callbackCalled bool
	app.SetModelChangeCallback(func(p, m string) {
		callbackCalled = true
	})

	// Show selector, then cancel.
	app.Update(showModelSelectorMsg{})
	if !app.modelSelector.Visible() {
		t.Fatal("expected selector visible")
	}

	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from Escape")
	}
	msg := cmd()
	app.Update(msg)

	// Selector should be hidden.
	if app.modelSelector.Visible() {
		t.Error("expected selector hidden after cancel")
	}
	// No system message or callback for cancel.
	if callbackCalled {
		t.Error("model change callback should NOT be called on cancel")
	}
	for _, b := range app.chat.blocks {
		if b.kind == blockSystem && strings.Contains(b.text, "Switched to model") {
			t.Error("should NOT have 'Switched to model' message on cancel")
		}
	}
}

func TestApp_ModelCommandFlow_ProviderSwitch_ThenSelect(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	var gotProvider string
	app.SetModelChangeCallback(func(p, m string) {
		gotProvider = p
	})

	// Show selector.
	app.Update(showModelSelectorMsg{})

	// Tab to switch to the second provider.
	app.Update(tea.KeyMsg{Type: tea.KeyTab})
	secondProvider := app.modelSelector.providers[app.modelSelector.providerIdx]

	// Select model from second provider.
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd from Enter")
	}
	msg := cmd()
	app.Update(msg)

	if gotProvider != secondProvider {
		t.Errorf("expected provider %q, got %q", secondProvider, gotProvider)
	}
}

func TestApp_ModelCommandFlow_NavigateAndSelect(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	var gotModel string
	app.SetModelChangeCallback(func(p, m string) {
		gotModel = m
	})

	// Show selector.
	app.Update(showModelSelectorMsg{})
	firstModel := app.modelSelector.models[app.modelSelector.filtered[0]].Model

	// Navigate down 2 positions.
	app.Update(tea.KeyMsg{Type: tea.KeyDown})
	app.Update(tea.KeyMsg{Type: tea.KeyDown})
	thirdModel := app.modelSelector.models[app.modelSelector.filtered[2]].Model

	// Select the third model.
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd from Enter")
	}
	msg := cmd()
	app.Update(msg)

	if gotModel == firstModel {
		t.Error("should have selected the third model, not the first")
	}
	if gotModel != thirdModel {
		t.Errorf("expected model %q (position 2), got %q", thirdModel, gotModel)
	}
}

func TestApp_ModelCommandFlow_KeysRoutedToSelector(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})

	// Show selector.
	app.Update(showModelSelectorMsg{})
	if !app.modelSelector.Visible() {
		t.Fatal("selector should be visible")
	}

	// Type filter characters — they should go to the selector, not the editor.
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})

	if app.modelSelector.filter != "gpt" {
		t.Errorf("expected selector filter 'gpt', got %q", app.modelSelector.filter)
	}
	// Editor should NOT have received these characters.
	if app.editor.Value() != "" {
		t.Errorf("editor should be empty while selector is visible, got %q", app.editor.Value())
	}
}

func TestApp_ModelCommandFlow_UnknownModel_ShowsError(t *testing.T) {
	app := NewApp()

	// /model with an unknown model name.
	_, cmd := app.Update(editorCommandMsg{name: "model", args: "xyznonexistent999"})
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd()
	app.Update(msg)

	// Should show error message, not open selector.
	if app.modelSelector.Visible() {
		t.Error("selector should NOT be visible for unknown model")
	}
	found := false
	for _, b := range app.chat.blocks {
		if b.kind == blockSystem && strings.Contains(b.text, "No model matching") {
			found = true
		}
	}
	if !found {
		t.Error("expected error message about unknown model")
	}
}

func TestApp_ShowModelSelectorMsg_SetsSize(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 50})

	app.Update(showModelSelectorMsg{})

	if app.modelSelector.width != 120 {
		t.Errorf("expected selector width=120, got %d", app.modelSelector.width)
	}
	if app.modelSelector.height != 50 {
		t.Errorf("expected selector height=50, got %d", app.modelSelector.height)
	}
}

func TestApp_ModelSelection_MultipleSelections(t *testing.T) {
	app := NewApp()
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	var selections []string
	app.SetModelChangeCallback(func(p, m string) {
		selections = append(selections, m)
	})

	// First selection.
	app.Update(showModelSelectorMsg{})
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := cmd()
	app.Update(msg)

	// Second selection with different model.
	app.Update(showModelSelectorMsg{})
	app.Update(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg = cmd()
	app.Update(msg)

	if len(selections) != 2 {
		t.Fatalf("expected 2 selections, got %d", len(selections))
	}
	// Header should reflect the last selection.
	if app.header.model != selections[1] {
		t.Errorf("expected header model %q, got %q", selections[1], app.header.model)
	}
	// Should have 2 system messages about model switching.
	count := 0
	for _, b := range app.chat.blocks {
		if b.kind == blockSystem && strings.Contains(b.text, "Switched to model") {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 'Switched to model' messages, got %d", count)
	}
}

func TestApp_Update_PluginUIRequestMsg_HeadlessMode(t *testing.T) {
	app := NewApp()
	app.SetHasUI(false) // Headless mode

	responseChan := make(chan *PluginUIResponseMsg, 1)
	app.SetUIResponseCallback(func(resp *PluginUIResponseMsg) {
		responseChan <- resp
	})

	// Test select dialog in headless mode
	app.Update(PluginUIRequestMsg{
		PluginName: "test-plugin",
		ID:         "req-select",
		UIType:     "select",
		UIOptions:  []string{"red", "green", "blue"},
	})

	resp := <-responseChan
	if resp.Value != "red" {
		t.Errorf("expected first option red, got %s", resp.Value)
	}
	if !resp.Closed {
		t.Error("headless response should be marked as closed")
	}

	// Test confirm dialog in headless mode
	app.Update(PluginUIRequestMsg{
		PluginName: "test-plugin",
		ID:         "req-confirm",
		UIType:     "confirm",
	})

	resp = <-responseChan
	if resp.Value != "false" {
		t.Errorf("expected false for confirm, got %s", resp.Value)
	}

	// Test input dialog in headless mode
	app.Update(PluginUIRequestMsg{
		PluginName: "test-plugin",
		ID:         "req-input",
		UIType:     "input",
		UIDefault:  "default-value",
	})

	resp = <-responseChan
	if resp.Value != "default-value" {
		t.Errorf("expected default-value, got %s", resp.Value)
	}
}
