// Package plugin implements the Gi plugin system. Plugins are external
// executables that communicate with the host via JSONL over stdin/stdout,
// enabling language-agnostic extensibility.
package plugin

import (
	"time"

	"github.com/ejm/go_pi/pkg/ai"
)

// --- Host -> Plugin message types ---

// HostMessage is a message sent from the host to a plugin over stdin.
type HostMessage struct {
	Type   string         `json:"type"`
	ID     string         `json:"id,omitempty"`
	Name   string         `json:"name,omitempty"`
	Params map[string]any `json:"params,omitempty"`
	Args   string         `json:"args,omitempty"`
	Config *PluginConfig  `json:"config,omitempty"`
	Event  *EventPayload  `json:"event,omitempty"`
}

// PluginConfig is the configuration sent to a plugin during initialization.
type PluginConfig struct {
	Cwd       string `json:"cwd"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	GiVersion string `json:"gi_version"`
}

// EventPayload carries agent lifecycle event data forwarded to plugins.
type EventPayload struct {
	Type       string         `json:"type"`
	ToolName   string         `json:"tool_name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolArgs   map[string]any `json:"tool_args,omitempty"`
	ToolResult string         `json:"tool_result,omitempty"`
	ToolError  bool           `json:"tool_error,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// --- Plugin -> Host message types ---

// PluginMessage is a message sent from a plugin to the host over stdout.
type PluginMessage struct {
	Type     string       `json:"type"`
	ID       string       `json:"id,omitempty"`
	Content  string       `json:"content,omitempty"`
	Text     string       `json:"text,omitempty"`
	IsError  bool         `json:"is_error,omitempty"`
	Tools    []ToolDef    `json:"tools,omitempty"`
	Commands []CommandDef `json:"commands,omitempty"`
	Role     string       `json:"role,omitempty"`
	Level    string       `json:"level,omitempty"`
	Message  string       `json:"message,omitempty"`
	Status   *HeartbeatStatus `json:"status,omitempty"`
}

// HeartbeatStatus carries plugin health metrics returned in a heartbeat_ack.
type HeartbeatStatus struct {
	MemoryBytes int64 `json:"memory_bytes,omitempty"`
	Goroutines  int   `json:"goroutines,omitempty"`
	UptimeSecs  int64 `json:"uptime_seconds,omitempty"`
}

// HeartbeatConfig controls periodic health checking of plugins.
type HeartbeatConfig struct {
	Interval time.Duration // How often to send heartbeats (default 10s).
	Timeout  time.Duration // How long to wait for an ack (default 5s).
}

// ToolDef describes a tool provided by a plugin.
type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// CommandDef describes a slash command provided by a plugin.
type CommandDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Manifest represents the plugin.json manifest file.
type Manifest struct {
	Name         string   `json:"name"`
	Version      string   `json:"version,omitempty"`
	Description  string   `json:"description,omitempty"`
	Executable   string   `json:"executable"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// ToAIToolDef converts a plugin ToolDef to the ai.ToolDef used by the
// provider and tool registry.
func (d ToolDef) ToAIToolDef() ai.ToolDef {
	return ai.ToolDef{
		Name:        d.Name,
		Description: d.Description,
		InputSchema: d.InputSchema,
	}
}
