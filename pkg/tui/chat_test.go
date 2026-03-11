package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/ejm/go_pi/pkg/agent"
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

	changed := cv.HandleEvent(agent.AgentEvent{Type: agent.EventTurnEnd})
	if changed {
		t.Error("TurnEnd should return false (no content change)")
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
