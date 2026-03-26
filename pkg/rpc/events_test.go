package rpc

import (
	"fmt"
	"testing"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
)

func TestEventFromAgent_TextDelta(t *testing.T) {
	e := agent.Event{
		Type:  agent.EventAssistantText,
		Delta: "hello",
	}
	ev := EventFromAgent(e)
	if ev.Type != "assistant_text" {
		t.Errorf("type = %q, want assistant_text", ev.Type)
	}
	if ev.Delta != "hello" {
		t.Errorf("delta = %q, want hello", ev.Delta)
	}
}

func TestEventFromAgent_ThinkingDelta(t *testing.T) {
	e := agent.Event{
		Type:  agent.EventAssistantThinking,
		Delta: "let me think...",
	}
	ev := EventFromAgent(e)
	if ev.Type != "assistant_thinking" {
		t.Errorf("type = %q, want assistant_thinking", ev.Type)
	}
	if ev.Delta != "let me think..." {
		t.Errorf("delta = %q, want %q", ev.Delta, "let me think...")
	}
}

func TestEventFromAgent_ToolExecStart(t *testing.T) {
	e := agent.Event{
		Type:       agent.EventToolExecStart,
		ToolCallID: "tc_1",
		ToolName:   "bash",
		ToolArgs:   map[string]any{"command": "ls"},
	}
	ev := EventFromAgent(e)
	if ev.ToolCallID != "tc_1" {
		t.Errorf("tool_call_id = %q, want tc_1", ev.ToolCallID)
	}
	if ev.ToolName != "bash" {
		t.Errorf("tool_name = %q, want bash", ev.ToolName)
	}
	if ev.ToolArgs["command"] != "ls" {
		t.Errorf("tool_args[command] = %v, want ls", ev.ToolArgs["command"])
	}
}

func TestEventFromAgent_ToolExecEnd(t *testing.T) {
	e := agent.Event{
		Type:       agent.EventToolExecEnd,
		ToolCallID: "tc_1",
		ToolName:   "bash",
		ToolResult: "file1.go\nfile2.go",
		ToolError:  false,
	}
	ev := EventFromAgent(e)
	if ev.ToolResult != "file1.go\nfile2.go" {
		t.Errorf("tool_result = %q, want file listing", ev.ToolResult)
	}
	if ev.ToolError {
		t.Error("tool_error should be false")
	}
}

func TestEventFromAgent_UsageUpdate(t *testing.T) {
	e := agent.Event{
		Type: agent.EventUsageUpdate,
		Usage: &ai.Usage{
			InputTokens:  1000,
			OutputTokens: 200,
			CacheRead:    500,
		},
	}
	ev := EventFromAgent(e)
	if ev.Usage == nil {
		t.Fatal("usage should not be nil")
	}
	if ev.Usage.InputTokens != 1000 {
		t.Errorf("input_tokens = %d, want 1000", ev.Usage.InputTokens)
	}
	if ev.Usage.OutputTokens != 200 {
		t.Errorf("output_tokens = %d, want 200", ev.Usage.OutputTokens)
	}
	if ev.Usage.CacheRead != 500 {
		t.Errorf("cache_read = %d, want 500", ev.Usage.CacheRead)
	}
}

func TestEventFromAgent_Error(t *testing.T) {
	e := agent.Event{
		Type:  agent.EventAgentError,
		Error: fmt.Errorf("connection failed"),
	}
	ev := EventFromAgent(e)
	if ev.Error != "connection failed" {
		t.Errorf("error = %q, want connection failed", ev.Error)
	}
}

func TestEventFromAgent_AgentStartEnd(t *testing.T) {
	for _, typ := range []agent.EventType{agent.EventAgentStart, agent.EventAgentEnd} {
		ev := EventFromAgent(agent.Event{Type: typ})
		if ev.Type != string(typ) {
			t.Errorf("type = %q, want %q", ev.Type, typ)
		}
	}
}
