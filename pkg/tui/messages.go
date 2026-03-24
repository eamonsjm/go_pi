package tui

import (
	"github.com/ejm/go_pi/pkg/agent"
)

// ---------------------------------------------------------------------------
// Bubble Tea messages used for inter-component communication
// ---------------------------------------------------------------------------

// StreamEventMsg wraps an AgentEvent flowing from the agent loop into the TUI.
type StreamEventMsg struct {
	Event agent.AgentEvent
}

// AgentDoneMsg signals that the agent loop has finished (no more events).
type AgentDoneMsg struct{}

// AgentErrorMsg signals a fatal error from the agent loop.
type AgentErrorMsg struct {
	Err error
}

// CommandResultMsg carries the result of a slash command execution back to the
// chat view (e.g. informational output, errors, etc.).
type CommandResultMsg struct {
	Text    string
	IsError bool
}

// renderTickMsg is sent by the tick-based render loop (~30fps) during
// streaming. On each tick the App checks ChatView.dirty and flushes a
// re-render only when content has actually changed.
type renderTickMsg struct{}

// idleRenderMsg fires 100ms after the last text delta. If no new deltas
// arrived (gen still matches App.deltaGen), the active streaming block is
// switched to glamour rendering for a polished view during natural pauses.
type idleRenderMsg struct {
	gen uint64
}

// PluginInjectMsg carries an inject_message or log message from a plugin
// process into the TUI for display or agent injection.
type PluginInjectMsg struct {
	PluginName string
	Content    string
	Role       string // "user" to feed back to the agent, otherwise display only
	IsLog      bool
	LogLevel   string
}

// PluginUIRequestMsg carries a ui_request message from a plugin process.
// The TUI should render the appropriate dialog and send back a PluginUIResponseMsg.
type PluginUIRequestMsg struct {
	PluginName string
	ID         string // Unique request ID to correlate with response
	UIType     string // "select" | "confirm" | "input" | "editor" | "notify"
	UITitle    string
	UIOptions  []string // For select dialog
	UIDefault  string   // For input/editor
	UILevel    string   // For notify: "info" | "warning" | "error"
}

// SkillInvokeMsg carries an expanded skill prompt to be submitted as a new
// agent turn. Sent by skill slash commands after parsing args and rendering.
type SkillInvokeMsg struct {
	Display string // what to show in chat (e.g. "/commit files")
	Prompt  string // rendered prompt to send to agent
}

// PluginUIResponseMsg is sent by the TUI after the user responds to a dialog.
type PluginUIResponseMsg struct {
	PluginName string
	ID         string // Matches the request ID
	Value      string // User's response
	Closed     bool   // True if user closed without responding
	Error      string // Error message if dialog failed
}

// MCPConfirmMsg is sent from the MCP permission hook (via App.ConfirmMCPTool)
// to request interactive user approval for an MCP tool invocation. The caller
// blocks on ResultCh until the TUI sends a boolean response.
type MCPConfirmMsg struct {
	ServerName string
	ToolName   string
	ResultCh   chan bool
}

// SamplingConfirmMsg is sent by the MCP sampling confirmation callback when a
// server requests sampling approval. The background goroutine blocks on
// ResponseCh until the user types "y" or "n".
type SamplingConfirmMsg struct {
	ServerName string
	ResponseCh chan<- bool
}

// setModelMsg routes a model name change through the Bubble Tea message loop
// so that App.SetModel is safe to call from any goroutine.
type setModelMsg struct {
	name string
}

// setThinkingMsg routes a thinking level change through the Bubble Tea message
// loop so that App.SetThinking is safe to call from any goroutine.
type setThinkingMsg struct {
	level string
}

// setSessionMsg routes a session name change through the Bubble Tea message
// loop so that App.SetSession is safe to call from any goroutine.
type setSessionMsg struct {
	name string
}
