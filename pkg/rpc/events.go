package rpc

import "github.com/ejm/go_pi/pkg/agent"

// Event is the JSON-serializable representation of an agent event.
type Event struct {
	Type string `json:"type"`

	// Text/thinking deltas.
	Delta string `json:"delta,omitempty"`

	// Tool execution fields.
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	ToolArgs   map[string]any `json:"tool_args,omitempty"`
	ToolResult string         `json:"tool_result,omitempty"`
	ToolError  bool           `json:"tool_error,omitempty"`

	// Usage stats.
	Usage *UsageInfo `json:"usage,omitempty"`

	// Error message (for agent_error events).
	Error string `json:"error,omitempty"`
}

// UsageInfo is a JSON-friendly subset of ai.Usage.
type UsageInfo struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheRead    int `json:"cache_read_input_tokens,omitempty"`
	CacheWrite   int `json:"cache_creation_input_tokens,omitempty"`
}

// EventFromAgent converts an agent.Event to a serializable Event.
func EventFromAgent(e agent.Event) Event {
	ev := Event{Type: string(e.Type)}

	switch e.Type {
	case agent.EventAssistantText, agent.EventAssistantThinking:
		ev.Delta = e.Delta

	case agent.EventToolExecStart:
		ev.ToolCallID = e.ToolCallID
		ev.ToolName = e.ToolName
		ev.ToolArgs = e.ToolArgs

	case agent.EventToolExecEnd:
		ev.ToolCallID = e.ToolCallID
		ev.ToolName = e.ToolName
		ev.ToolResult = e.ToolResult
		ev.ToolError = e.ToolError

	case agent.EventUsageUpdate:
		if e.Usage != nil {
			ev.Usage = &UsageInfo{
				InputTokens:  e.Usage.InputTokens,
				OutputTokens: e.Usage.OutputTokens,
				CacheRead:    e.Usage.CacheRead,
				CacheWrite:   e.Usage.CacheWrite,
			}
		}

	case agent.EventAgentError:
		if e.Error != nil {
			ev.Error = e.Error.Error()
		}
	}

	return ev
}
