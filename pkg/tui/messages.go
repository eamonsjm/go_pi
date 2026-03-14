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

// PluginInjectMsg carries an inject_message or log message from a plugin
// process into the TUI for display or agent injection.
type PluginInjectMsg struct {
	PluginName string
	Content    string
	Role       string // "user" to feed back to the agent, otherwise display only
	IsLog      bool
	LogLevel   string
}

