package agent

import "github.com/ejm/go_pi/pkg/ai"

// AgentEventType identifies the kind of event emitted by the agent loop.
type AgentEventType string

const (
	EventAgentStart        AgentEventType = "agent_start"
	EventAgentEnd          AgentEventType = "agent_end"
	EventTurnStart         AgentEventType = "turn_start"
	EventTurnEnd           AgentEventType = "turn_end"
	EventAssistantText     AgentEventType = "assistant_text"
	EventAssistantThinking AgentEventType = "assistant_thinking"
	EventToolExecStart     AgentEventType = "tool_exec_start"
	EventToolExecEnd       AgentEventType = "tool_exec_end"
	EventToolResult        AgentEventType = "tool_result"
	EventUsageUpdate       AgentEventType = "usage_update"
	EventAgentError        AgentEventType = "agent_error"
	EventCompaction        AgentEventType = "compaction"
	EventAutoCompaction    AgentEventType = "auto_compaction"
)

// AgentEvent represents an event emitted during agent execution.
// Consumers read these from the Events() channel to track progress,
// render UI, or record session history.
type AgentEvent struct {
	Type AgentEventType

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
