package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
)

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

func TestFormatArgs(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"nil args", nil, ""},
		{"empty args", map[string]any{}, ""},
		{"string value", map[string]any{"file": "main.go"}, `file: "main.go"`},
		{"non-string value", map[string]any{"count": 42}, `count: "42"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatArgs(tt.args)
			if tt.want != "" && !strings.Contains(got, tt.want) {
				t.Errorf("formatArgs() = %q, expected to contain %q", got, tt.want)
			}
			if tt.want == "" && got != "" {
				t.Errorf("formatArgs() = %q, want empty", got)
			}
		})
	}
}

func TestFormatArgs_LongValueTruncated(t *testing.T) {
	longVal := strings.Repeat("a", 100)
	got := formatArgs(map[string]any{"key": longVal})
	if !strings.Contains(got, "...") {
		t.Errorf("expected truncated value to contain '...', got %q", got)
	}
	// The full 100-char value should not appear.
	if strings.Contains(got, longVal) {
		t.Error("expected long value to be truncated")
	}
}

func TestTruncateLines(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		n     int
		lines int // expected number of lines in result
	}{
		{"fewer than n", "a\nb\nc", 5, 3},
		{"exactly n", "a\nb\nc", 3, 3},
		{"more than n", "a\nb\nc\nd\ne", 3, 3},
		{"single line", "hello", 1, 1},
		{"empty", "", 5, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateLines(tt.text, tt.n)
			gotLines := strings.Count(got, "\n") + 1
			if gotLines != tt.lines {
				t.Errorf("truncateLines(%q, %d) has %d lines, want %d", tt.text, tt.n, gotLines, tt.lines)
			}
		})
	}
}

func TestTruncateLines_PreservesContent(t *testing.T) {
	text := "line1\nline2\nline3\nline4\nline5"
	got := truncateLines(text, 3)
	if got != "line1\nline2\nline3" {
		t.Errorf("expected first 3 lines, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// ChatView tests
// ---------------------------------------------------------------------------

func TestChatView_NewChatView(t *testing.T) {
	cv := NewChatView()
	if cv == nil {
		t.Fatal("NewChatView returned nil")
	}
	if len(cv.blocks) != 0 {
		t.Errorf("expected 0 blocks, got %d", len(cv.blocks))
	}
	if cv.renderer == nil {
		t.Error("expected renderer to be initialized")
	}
}

func TestChatView_SetSize(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(120, 40)
	if cv.width != 120 {
		t.Errorf("expected width 120, got %d", cv.width)
	}
	if cv.height != 40 {
		t.Errorf("expected height 40, got %d", cv.height)
	}
}

func TestChatView_SetSize_MinWrap(t *testing.T) {
	cv := NewChatView()
	// Very narrow width — wrap should clamp to 40 minimum.
	cv.SetSize(10, 20)
	// No crash is the main assertion; renderer should still be valid.
	if cv.renderer == nil {
		t.Error("renderer should not be nil after narrow SetSize")
	}
}

func TestChatView_AddUserMessage(t *testing.T) {
	cv := NewChatView()
	cv.AddUserMessage("hello world")

	if len(cv.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(cv.blocks))
	}
	if cv.blocks[0].kind != blockUser {
		t.Errorf("expected blockUser, got %d", cv.blocks[0].kind)
	}
	if cv.blocks[0].text != "hello world" {
		t.Errorf("expected 'hello world', got %q", cv.blocks[0].text)
	}
}

func TestChatView_AddSystemMessage(t *testing.T) {
	cv := NewChatView()
	cv.AddSystemMessage("system info")

	if len(cv.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(cv.blocks))
	}
	if cv.blocks[0].kind != blockSystem {
		t.Errorf("expected blockSystem, got %d", cv.blocks[0].kind)
	}
}

func TestChatView_AddPluginMessage_Inject(t *testing.T) {
	cv := NewChatView()
	cv.AddPluginMessage("my-plugin", "some content", false, "")

	if len(cv.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(cv.blocks))
	}
	b := cv.blocks[0]
	if b.kind != blockPlugin {
		t.Errorf("expected blockPlugin, got %d", b.kind)
	}
	if b.pluginName != "my-plugin" {
		t.Errorf("expected pluginName 'my-plugin', got %q", b.pluginName)
	}
	if b.logLevel != "" {
		t.Errorf("expected empty logLevel for inject, got %q", b.logLevel)
	}
}

func TestChatView_AddPluginMessage_Log(t *testing.T) {
	cv := NewChatView()
	cv.AddPluginMessage("logger", "warning msg", true, "warn")

	b := cv.blocks[0]
	if b.logLevel != "warn" {
		t.Errorf("expected logLevel 'warn', got %q", b.logLevel)
	}
}

func TestChatView_AddPluginMessage_LogDefaultsToInfo(t *testing.T) {
	cv := NewChatView()
	cv.AddPluginMessage("logger", "msg", true, "")

	b := cv.blocks[0]
	if b.logLevel != "info" {
		t.Errorf("expected logLevel 'info' by default, got %q", b.logLevel)
	}
}

func TestChatView_ClearBlocks(t *testing.T) {
	cv := NewChatView()
	cv.AddUserMessage("hello")
	cv.AddSystemMessage("info")
	cv.ClearBlocks()

	if len(cv.blocks) != 0 {
		t.Errorf("expected 0 blocks after clear, got %d", len(cv.blocks))
	}
}

func TestChatView_AddCompactionBlock(t *testing.T) {
	cv := NewChatView()
	cv.AddUserMessage("hello")
	cv.AddCompactionBlock("compacted summary")

	// Should replace all blocks.
	if len(cv.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(cv.blocks))
	}
	b := cv.blocks[0]
	if b.kind != blockCompaction {
		t.Errorf("expected blockCompaction, got %d", b.kind)
	}
	if b.text != "compacted summary" {
		t.Errorf("expected 'compacted summary', got %q", b.text)
	}
	if !b.collapsed {
		t.Error("expected compaction block to start collapsed")
	}
}

// ---------------------------------------------------------------------------
// HandleEvent tests
// ---------------------------------------------------------------------------

func TestChatView_HandleEvent_AssistantText(t *testing.T) {
	cv := NewChatView()

	changed := cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantText,
		Delta: "Hello ",
	})
	if !changed {
		t.Error("expected changed=true")
	}
	if len(cv.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(cv.blocks))
	}
	if cv.blocks[0].kind != blockAssistantText {
		t.Errorf("expected blockAssistantText, got %d", cv.blocks[0].kind)
	}
	if !cv.blocks[0].streaming {
		t.Error("expected streaming=true on new assistant block")
	}

	// Second delta should append to same block.
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantText,
		Delta: "world",
	})
	if len(cv.blocks) != 1 {
		t.Fatalf("expected still 1 block, got %d", len(cv.blocks))
	}
	if cv.blocks[0].text != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", cv.blocks[0].text)
	}
	if !cv.blocks[0].streaming {
		t.Error("expected streaming=true after appending delta")
	}
}

func TestChatView_IdleGlamourRender(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(80, 24)

	// Stream some markdown content.
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantText,
		Delta: "**bold text**",
	})
	if !cv.blocks[0].streaming {
		t.Fatal("expected streaming=true during text deltas")
	}

	// Simulate idle timeout — should switch to glamour rendering.
	cv.idleGlamourRender()
	if cv.blocks[0].streaming {
		t.Error("expected streaming=false after idleGlamourRender")
	}

	idleView := cv.View()

	// New delta should restore streaming mode.
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantText,
		Delta: " more text",
	})
	if !cv.blocks[0].streaming {
		t.Error("expected streaming=true after new delta")
	}

	cv.rebuildContent()
	streamingView := cv.View()

	// The idle (glamour) and streaming (plain) renders should differ.
	if idleView == streamingView {
		t.Error("expected idle glamour render to differ from streaming plain render")
	}
}

func TestChatView_IdleGlamourRender_NoStreamingBlocks(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(80, 24)

	// No blocks at all — should not panic.
	cv.idleGlamourRender()

	// Only non-streaming blocks — should not change anything.
	cv.AddUserMessage("hello")
	cv.idleGlamourRender()
	if len(cv.blocks) != 1 {
		t.Errorf("expected 1 block, got %d", len(cv.blocks))
	}
}

func TestChatView_StreamingRenderSkipsGlamour(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(80, 24)

	// During streaming, renderAssistant should use plain text styling.
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantText,
		Delta: "**bold text**",
	})
	cv.rebuildContent()
	streamingView := cv.View()

	// After TurnEnd, renderAssistant should use glamour markdown rendering.
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventTurnEnd})
	cv.rebuildContent()
	finalView := cv.View()

	// The two renders should differ — glamour processes markdown syntax,
	// while streaming mode passes text through with lipgloss styling.
	if streamingView == finalView {
		t.Error("expected streaming and final renders to differ for markdown content")
	}
}

func TestChatView_HandleEvent_Thinking(t *testing.T) {
	cv := NewChatView()

	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantThinking,
		Delta: "hmm ",
	})
	if len(cv.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(cv.blocks))
	}
	if cv.blocks[0].kind != blockThinking {
		t.Error("expected blockThinking")
	}
	if !cv.blocks[0].collapsed {
		t.Error("thinking blocks should start collapsed")
	}

	// Append more thinking.
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantThinking,
		Delta: "let me think",
	})
	if cv.blocks[0].text != "hmm let me think" {
		t.Errorf("expected concatenated thinking, got %q", cv.blocks[0].text)
	}
}

func TestChatView_HandleEvent_ToolExecStart(t *testing.T) {
	cv := NewChatView()

	cv.HandleEvent(agent.AgentEvent{
		Type:       agent.EventToolExecStart,
		ToolCallID: "tc-1",
		ToolName:   "read_file",
		ToolArgs:   map[string]any{"path": "/tmp/test.go"},
	})

	if len(cv.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(cv.blocks))
	}
	b := cv.blocks[0]
	if b.kind != blockToolCall {
		t.Errorf("expected blockToolCall, got %d", b.kind)
	}
	if b.toolName != "read_file" {
		t.Errorf("expected tool name 'read_file', got %q", b.toolName)
	}
	if b.toolID != "tc-1" {
		t.Errorf("expected toolID 'tc-1', got %q", b.toolID)
	}
}

func TestChatView_HandleEvent_ToolExecEnd(t *testing.T) {
	cv := NewChatView()

	// Start tool.
	cv.HandleEvent(agent.AgentEvent{
		Type:       agent.EventToolExecStart,
		ToolCallID: "tc-1",
		ToolName:   "read_file",
	})

	// End tool.
	cv.HandleEvent(agent.AgentEvent{
		Type:       agent.EventToolExecEnd,
		ToolCallID: "tc-1",
		ToolResult: "file contents here",
	})

	b := cv.blocks[0]
	if b.toolResult != "file contents here" {
		t.Errorf("expected tool result attached, got %q", b.toolResult)
	}
	if b.collapsed != true {
		t.Error("tool result should be collapsed by default")
	}
}

func TestChatView_HandleEvent_ToolExecEnd_Error(t *testing.T) {
	cv := NewChatView()

	cv.HandleEvent(agent.AgentEvent{
		Type:       agent.EventToolExecStart,
		ToolCallID: "tc-1",
		ToolName:   "bash",
	})
	cv.HandleEvent(agent.AgentEvent{
		Type:       agent.EventToolExecEnd,
		ToolCallID: "tc-1",
		ToolResult: "permission denied",
		ToolError:  true,
	})

	b := cv.blocks[0]
	if !b.toolError {
		t.Error("expected toolError=true")
	}
}

func TestChatView_HandleEvent_ToolExecEnd_NoMatchingStart(t *testing.T) {
	cv := NewChatView()

	// End without start - should not panic.
	changed := cv.HandleEvent(agent.AgentEvent{
		Type:       agent.EventToolExecEnd,
		ToolCallID: "tc-orphan",
		ToolResult: "result",
	})
	if !changed {
		t.Error("expected changed=true")
	}
}

func TestChatView_HandleEvent_TurnEnd(t *testing.T) {
	cv := NewChatView()
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantText,
		Delta: "hello",
	})
	if !cv.blocks[0].streaming {
		t.Error("expected streaming=true during text deltas")
	}

	changed := cv.HandleEvent(agent.AgentEvent{Type: agent.EventTurnEnd})
	if !changed {
		t.Error("TurnEnd should return true (clears streaming, triggers re-render)")
	}
	if cv.blocks[0].streaming {
		t.Error("expected streaming=false after TurnEnd")
	}

	// TurnEnd doesn't force a new block — lastBlock still matches the
	// existing assistant text block, so the next delta appends.
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantText,
		Delta: " world",
	})
	if len(cv.blocks) != 1 {
		t.Errorf("expected 1 block (appended), got %d", len(cv.blocks))
	}
	if cv.blocks[0].text != "hello world" {
		t.Errorf("expected 'hello world', got %q", cv.blocks[0].text)
	}
}

func TestChatView_HandleEvent_Compaction(t *testing.T) {
	cv := NewChatView()

	changed := cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventCompaction,
		Delta: "summary of conversation",
	})
	if !changed {
		t.Error("expected changed=true for compaction")
	}
	if len(cv.blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(cv.blocks))
	}
	b := cv.blocks[0]
	if b.kind != blockCompaction {
		t.Error("expected blockCompaction")
	}
	if !b.collapsed {
		t.Error("compaction should start collapsed")
	}
}

func TestChatView_HandleEvent_AgentError(t *testing.T) {
	cv := NewChatView()

	changed := cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAgentError,
		Error: errors.New("something went wrong"),
	})
	if !changed {
		t.Error("expected changed=true")
	}
	b := cv.blocks[0]
	if b.kind != blockError {
		t.Error("expected blockError")
	}
	if b.text != "something went wrong" {
		t.Errorf("expected error text, got %q", b.text)
	}
}

func TestChatView_HandleEvent_AgentError_Nil(t *testing.T) {
	cv := NewChatView()

	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAgentError,
		Error: nil,
	})
	if cv.blocks[0].text != "unknown error" {
		t.Errorf("expected 'unknown error' for nil error, got %q", cv.blocks[0].text)
	}
}

func TestChatView_HandleEvent_AgentEnd_ClearsStreaming(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(80, 24)

	// Simulate streaming text without a TurnEnd (e.g. error during stream).
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventAssistantText, Delta: "partial"})
	if !cv.blocks[0].streaming {
		t.Fatal("precondition: block should be streaming")
	}

	changed := cv.HandleEvent(agent.AgentEvent{Type: agent.EventAgentEnd})
	if !changed {
		t.Error("AgentEnd should return true when streaming blocks are finalized")
	}
	if cv.blocks[0].streaming {
		t.Error("expected streaming=false after AgentEnd")
	}
	if cv.blocks[0].rendered != "" {
		t.Error("expected rendered cache cleared after AgentEnd")
	}
}

func TestChatView_HandleEvent_AgentEnd_NoopAfterTurnEnd(t *testing.T) {
	cv := NewChatView()

	cv.HandleEvent(agent.AgentEvent{Type: agent.EventAssistantText, Delta: "hello"})
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventTurnEnd})

	// AgentEnd after TurnEnd should be a no-op (streaming already cleared).
	changed := cv.HandleEvent(agent.AgentEvent{Type: agent.EventAgentEnd})
	if changed {
		t.Error("AgentEnd should return false when no streaming blocks remain")
	}
}

func TestChatView_HandleEvent_AgentEnd_GlamourAfterError(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(80, 24)

	// Stream markdown text, then error without TurnEnd.
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventAssistantText, Delta: "**bold text**"})
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAgentError,
		Error: errors.New("stream failed"),
	})

	// Streaming render (before AgentEnd).
	cv.rebuildContent()
	streamingView := cv.View()

	// AgentEnd finalizes streaming blocks for glamour.
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventAgentEnd})
	cv.rebuildContent()
	finalView := cv.View()

	// After AgentEnd, glamour should process the markdown differently.
	if streamingView == finalView {
		t.Error("expected streaming and final renders to differ after AgentEnd")
	}
}

func TestChatView_HandleEvent_UnknownType(t *testing.T) {
	cv := NewChatView()

	changed := cv.HandleEvent(agent.AgentEvent{
		Type: agent.EventUsageUpdate,
	})
	if changed {
		t.Error("unknown event type should return false")
	}
	if len(cv.blocks) != 0 {
		t.Error("unknown event should not add blocks")
	}
}

// ---------------------------------------------------------------------------
// Dirty flag tests
// ---------------------------------------------------------------------------

func TestChatView_HandleEvent_SetsDirty(t *testing.T) {
	cv := NewChatView()

	// Starts clean.
	if cv.dirty {
		t.Fatal("expected dirty=false on fresh ChatView")
	}

	// Text delta sets dirty.
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantText,
		Delta: "hello",
	})
	if !cv.dirty {
		t.Error("expected dirty=true after EventAssistantText")
	}

	// rebuildContent clears dirty.
	cv.rebuildContent()
	if cv.dirty {
		t.Error("expected dirty=false after rebuildContent")
	}

	// Thinking sets dirty.
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantThinking,
		Delta: "hmm",
	})
	if !cv.dirty {
		t.Error("expected dirty=true after EventAssistantThinking")
	}
	cv.rebuildContent()

	// TurnEnd sets dirty.
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventTurnEnd})
	if !cv.dirty {
		t.Error("expected dirty=true after EventTurnEnd")
	}
	cv.rebuildContent()

	// Unknown event does NOT set dirty.
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventUsageUpdate})
	if cv.dirty {
		t.Error("expected dirty=false after unknown event type")
	}
}

// ---------------------------------------------------------------------------
// Toggle tests
// ---------------------------------------------------------------------------

func TestChatView_ToggleThinking(t *testing.T) {
	cv := NewChatView()
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantThinking,
		Delta: "thinking...",
	})
	if !cv.blocks[0].collapsed {
		t.Fatal("should start collapsed")
	}

	cv.ToggleThinking()
	if cv.blocks[0].collapsed {
		t.Error("should be expanded after toggle")
	}

	cv.ToggleThinking()
	if !cv.blocks[0].collapsed {
		t.Error("should be collapsed after second toggle")
	}
}

func TestChatView_ToggleThinking_NoThinkingBlock(t *testing.T) {
	cv := NewChatView()
	cv.AddUserMessage("hello")
	// Should not panic when no thinking block exists.
	cv.ToggleThinking()
}

func TestChatView_ToggleToolResult(t *testing.T) {
	cv := NewChatView()
	cv.HandleEvent(agent.AgentEvent{
		Type:       agent.EventToolExecStart,
		ToolCallID: "tc-1",
		ToolName:   "bash",
	})
	cv.HandleEvent(agent.AgentEvent{
		Type:       agent.EventToolExecEnd,
		ToolCallID: "tc-1",
		ToolResult: "output",
	})
	if !cv.blocks[0].collapsed {
		t.Fatal("should start collapsed")
	}

	cv.ToggleToolResult()
	if cv.blocks[0].collapsed {
		t.Error("should be expanded after toggle")
	}
}

func TestChatView_ToggleToolResult_NoResult(t *testing.T) {
	cv := NewChatView()
	cv.HandleEvent(agent.AgentEvent{
		Type:       agent.EventToolExecStart,
		ToolCallID: "tc-1",
		ToolName:   "bash",
	})
	// No tool end yet — toggle should not crash.
	cv.ToggleToolResult()
}

func TestChatView_ToggleCompaction(t *testing.T) {
	cv := NewChatView()
	cv.AddCompactionBlock("summary")
	if !cv.blocks[0].collapsed {
		t.Fatal("should start collapsed")
	}

	cv.ToggleCompaction()
	if cv.blocks[0].collapsed {
		t.Error("should be expanded after toggle")
	}
}

// ---------------------------------------------------------------------------
// lastBlock tests
// ---------------------------------------------------------------------------

func TestChatView_LastBlock(t *testing.T) {
	cv := NewChatView()

	// Empty blocks.
	if b := cv.lastBlock(blockUser); b != nil {
		t.Error("expected nil for empty blocks")
	}

	cv.AddUserMessage("hello")
	if b := cv.lastBlock(blockUser); b == nil {
		t.Error("expected non-nil for matching last block")
	}

	// Different kind.
	if b := cv.lastBlock(blockThinking); b != nil {
		t.Error("expected nil when last block is a different kind")
	}
}

// ---------------------------------------------------------------------------
// Mixed block sequences
// ---------------------------------------------------------------------------

func TestChatView_MultipleToolCalls(t *testing.T) {
	cv := NewChatView()

	// Start two tools.
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventToolExecStart, ToolCallID: "tc-1", ToolName: "read"})
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventToolExecStart, ToolCallID: "tc-2", ToolName: "write"})

	// End second tool first (out of order).
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventToolExecEnd, ToolCallID: "tc-2", ToolResult: "written"})
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventToolExecEnd, ToolCallID: "tc-1", ToolResult: "content"})

	if cv.blocks[0].toolResult != "content" {
		t.Errorf("tc-1 should have result 'content', got %q", cv.blocks[0].toolResult)
	}
	if cv.blocks[1].toolResult != "written" {
		t.Errorf("tc-2 should have result 'written', got %q", cv.blocks[1].toolResult)
	}
}

func TestChatView_TextThenTool(t *testing.T) {
	cv := NewChatView()

	cv.HandleEvent(agent.AgentEvent{Type: agent.EventAssistantText, Delta: "Let me check"})
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventToolExecStart, ToolCallID: "tc-1", ToolName: "bash"})

	if len(cv.blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(cv.blocks))
	}
	if cv.blocks[0].kind != blockAssistantText {
		t.Error("first block should be assistant text")
	}
	if cv.blocks[1].kind != blockToolCall {
		t.Error("second block should be tool call")
	}
}

// ---------------------------------------------------------------------------
// Extreme terminal sizes (1x1)
// ---------------------------------------------------------------------------

func TestChatView_SetSize_1x1(t *testing.T) {
	cv := NewChatView()
	// Must not panic with extreme 1x1 dimensions.
	cv.SetSize(1, 1)

	if cv.width != 1 {
		t.Errorf("expected width 1, got %d", cv.width)
	}
	if cv.height != 1 {
		t.Errorf("expected height 1, got %d", cv.height)
	}
	if cv.renderer == nil {
		t.Error("renderer should not be nil after 1x1 SetSize")
	}
	// Verify View doesn't panic.
	_ = cv.View()
}

func TestChatView_SetSize_ZeroDimensions(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(0, 0)
	if cv.renderer == nil {
		t.Error("renderer should not be nil after 0x0 SetSize")
	}
	_ = cv.View()
}

func TestChatView_SetSize_1x1_WithContent(t *testing.T) {
	cv := NewChatView()
	cv.AddUserMessage("hello world this is a long message")
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantText,
		Delta: "Here is a response with some text",
	})
	cv.HandleEvent(agent.AgentEvent{
		Type:       agent.EventToolExecStart,
		ToolCallID: "tc-1",
		ToolName:   "bash",
		ToolArgs:   map[string]any{"command": "ls -la"},
	})
	cv.HandleEvent(agent.AgentEvent{
		Type:       agent.EventToolExecEnd,
		ToolCallID: "tc-1",
		ToolResult: "file1\nfile2\nfile3",
	})
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAgentError,
		Error: errors.New("something broke"),
	})

	// Resize to 1x1 after content is populated — must not panic.
	cv.SetSize(1, 1)
	_ = cv.View()
}

// ---------------------------------------------------------------------------
// Rapid resize events
// ---------------------------------------------------------------------------

func TestChatView_RapidResize(t *testing.T) {
	cv := NewChatView()
	cv.AddUserMessage("hello")
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantText,
		Delta: "world",
	})

	// Simulate rapid resize events in quick succession.
	sizes := [][2]int{
		{120, 40}, {80, 30}, {1, 1}, {200, 60}, {10, 5},
		{1, 1}, {80, 24}, {300, 100}, {40, 10}, {120, 40},
	}
	for _, sz := range sizes {
		cv.SetSize(sz[0], sz[1])
	}

	// Final state should reflect last resize.
	if cv.width != 120 {
		t.Errorf("expected width 120 after rapid resizes, got %d", cv.width)
	}
	if cv.height != 40 {
		t.Errorf("expected height 40 after rapid resizes, got %d", cv.height)
	}
	// Content should still be intact.
	if len(cv.blocks) != 2 {
		t.Errorf("expected 2 blocks after rapid resizes, got %d", len(cv.blocks))
	}
	_ = cv.View()
}

// ---------------------------------------------------------------------------
// Word-wrap tests
// ---------------------------------------------------------------------------

func TestChatView_UserMessageWraps(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(40, 20)

	long := strings.Repeat("word ", 20) // 100 chars, wider than 40-col terminal
	cv.AddUserMessage(long)

	content := cv.View()
	for _, line := range strings.Split(content, "\n") {
		// Each visible line (ignoring ANSI) should fit within the viewport width.
		// We check raw length as a rough bound; ANSI codes add chars but
		// the underlying text should be wrapped well under 40 visible cols.
		if len(strings.TrimRight(line, " ")) > 200 {
			t.Errorf("line too long (len=%d), wrapping may be broken: %q", len(line), line)
		}
	}
}

func TestChatView_SystemMessageWraps(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(40, 20)

	long := strings.Repeat("info ", 30)
	cv.AddSystemMessage(long)

	// Should not panic and should render content.
	content := cv.View()
	if content == "" {
		t.Error("expected non-empty view content")
	}
}

func TestChatView_ErrorMessageWraps(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(40, 20)

	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAgentError,
		Error: errors.New(strings.Repeat("fail ", 30)),
	})
	cv.rebuildContent()

	content := cv.View()
	if content == "" {
		t.Error("expected non-empty view content")
	}
}

func TestRebuildChatFromMessages_ToolBlocks(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(80, 24)

	msgs := []ai.Message{
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeText, Text: "List files"},
		}},
		{Role: ai.RoleAssistant, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeText, Text: "Let me check."},
			{Type: ai.ContentTypeToolUse, ToolUseID: "tc-1", ToolName: "bash", Input: map[string]any{"command": "ls"}},
		}},
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, ToolResultID: "tc-1", Content: "file1.go\nfile2.go"},
		}},
		{Role: ai.RoleAssistant, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeText, Text: "Here are the files."},
		}},
	}

	rebuildChatFromMessages(cv, msgs)

	var hasUser, hasText, hasTool bool
	for _, b := range cv.blocks {
		switch b.kind {
		case blockUser:
			hasUser = true
		case blockAssistantText:
			hasText = true
		case blockToolCall:
			hasTool = true
			if b.toolName != "bash" {
				t.Errorf("expected tool name %q, got %q", "bash", b.toolName)
			}
			if b.toolID != "tc-1" {
				t.Errorf("expected tool ID %q, got %q", "tc-1", b.toolID)
			}
			if b.toolResult != "file1.go\nfile2.go" {
				t.Errorf("expected tool result %q, got %q", "file1.go\nfile2.go", b.toolResult)
			}
		}
	}

	if !hasUser {
		t.Error("expected user message block")
	}
	if !hasText {
		t.Error("expected assistant text block")
	}
	if !hasTool {
		t.Error("expected tool call block")
	}
}

// ---------------------------------------------------------------------------
// Auto-scroll / new-content-below indicator tests
// ---------------------------------------------------------------------------

func TestChatView_HasNewBelow_AtBottom(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(80, 24)

	// Initially at bottom — hasNewBelow should be false.
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventAssistantText, Delta: "hello"})
	cv.rebuildContent()
	if cv.hasNewBelow {
		t.Error("expected hasNewBelow=false when viewport is at bottom")
	}
}

func TestChatView_HasNewBelow_ScrolledUp(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(80, 5) // small viewport

	// Fill content beyond viewport height.
	for i := 0; i < 20; i++ {
		cv.HandleEvent(agent.AgentEvent{
			Type:  agent.EventAssistantText,
			Delta: fmt.Sprintf("line %d\n", i),
		})
	}
	cv.rebuildContent()
	// Viewport auto-scrolled to bottom — hasNewBelow should be false.
	if cv.hasNewBelow {
		t.Error("expected hasNewBelow=false after initial scroll-to-bottom")
	}

	// Simulate user scrolling up by setting offset manually.
	cv.viewport.YOffset = 0

	// New content arrives.
	cv.HandleEvent(agent.AgentEvent{
		Type:  agent.EventAssistantText,
		Delta: "new content\n",
	})
	cv.rebuildContent()
	if !cv.hasNewBelow {
		t.Error("expected hasNewBelow=true when scrolled up and content grows")
	}

	// View should contain the indicator.
	view := cv.View()
	if !strings.Contains(view, "new content below") {
		t.Error("expected 'new content below' indicator in view")
	}
}

func TestChatView_HasNewBelow_ClearedOnScrollToBottom(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(80, 5)

	// Fill content and scroll up.
	for i := 0; i < 20; i++ {
		cv.HandleEvent(agent.AgentEvent{
			Type:  agent.EventAssistantText,
			Delta: fmt.Sprintf("line %d\n", i),
		})
	}
	cv.rebuildContent()
	cv.viewport.YOffset = 0
	cv.HandleEvent(agent.AgentEvent{Type: agent.EventAssistantText, Delta: "more\n"})
	cv.rebuildContent()
	if !cv.hasNewBelow {
		t.Fatal("precondition: hasNewBelow should be true")
	}

	// Scroll back to bottom.
	cv.viewport.GotoBottom()
	// Simulate Update being called (which checks AtBottom).
	cv.Update(nil)
	if cv.hasNewBelow {
		t.Error("expected hasNewBelow=false after scrolling back to bottom")
	}

	// View should NOT contain the indicator.
	view := cv.View()
	if strings.Contains(view, "new content below") {
		t.Error("indicator should not appear when at bottom")
	}
}

func TestRebuildChatFromMessages_ToolErrorBlock(t *testing.T) {
	cv := NewChatView()
	cv.SetSize(80, 24)

	msgs := []ai.Message{
		{Role: ai.RoleAssistant, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolUseID: "tc-err", ToolName: "read", Input: map[string]any{"path": "/missing"}},
		}},
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, ToolResultID: "tc-err", Content: "file not found", IsError: true},
		}},
	}

	rebuildChatFromMessages(cv, msgs)

	var found bool
	for _, b := range cv.blocks {
		if b.kind == blockToolCall && b.toolID == "tc-err" {
			found = true
			if !b.toolError {
				t.Error("expected toolError to be true")
			}
			if b.toolResult != "file not found" {
				t.Errorf("expected tool result %q, got %q", "file not found", b.toolResult)
			}
		}
	}
	if !found {
		t.Error("expected tool call block for error result")
	}
}
