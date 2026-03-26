package agent

import "github.com/ejm/go_pi/pkg/ai"

// EventType identifies the kind of event emitted by the agent loop.
type EventType string

const (
	EventAgentStart        EventType = "agent_start"
	EventAgentEnd          EventType = "agent_end"
	EventTurnStart         EventType = "turn_start"
	EventTurnEnd           EventType = "turn_end"
	EventAssistantText     EventType = "assistant_text"
	EventAssistantThinking EventType = "assistant_thinking"
	EventToolExecStart     EventType = "tool_exec_start"
	EventToolExecEnd       EventType = "tool_exec_end"
	EventToolResult        EventType = "tool_result"
	EventUsageUpdate       EventType = "usage_update"
	EventAgentError        EventType = "agent_error"
	EventCompaction        EventType = "compaction"
	EventAutoCompaction    EventType = "auto_compaction"
)

// Event represents an event emitted during agent execution.
// Consumers read these from the Events() channel to track progress,
// render UI, or record session history.
type Event struct {
	Type EventType

	// Message is set for turn_start (the full assistant message so far)
	// and turn_end (the completed assistant message).
	Message *ai.Message

	// Delta carries incremental text for assistant_text and assistant_thinking events.
	Delta string

	// Tool execution fields — set for tool_exec_start and tool_exec_end.
	ToolCallID string
	ToolName   string
	ToolArgs   map[string]any
	ToolResult string
	ToolError  bool

	// Usage is set for usage_update events.
	Usage *ai.Usage

	// Error is set for agent_error events.
	Error error
}
