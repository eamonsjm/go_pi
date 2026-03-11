package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/session"
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

// ToolExecStartMsg is emitted when a tool begins executing.
type ToolExecStartMsg struct {
	ID   string
	Name string
	Args map[string]any
}

// ToolExecEndMsg is emitted when a tool finishes executing.
type ToolExecEndMsg struct {
	ID      string
	Name    string
	Result  string
	IsError bool
}

// CommandResultMsg carries the result of a slash command execution back to the
// chat view (e.g. informational output, errors, etc.).
type CommandResultMsg struct {
	Text    string
	IsError bool
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

// ---------------------------------------------------------------------------
// Session messages
// ---------------------------------------------------------------------------

// sessionCreatedMsg is sent when a new session is created.
type sessionCreatedMsg struct {
	id string
}

// sessionLoadedMsg is sent when a session is loaded from disk.
type sessionLoadedMsg struct {
	id           string
	messageCount int
}

// sessionPickerShowMsg carries the list of sessions to display for /resume.
type sessionPickerShowMsg struct {
	sessions []session.SessionInfo
}

// sessionPickerSelectMsg is sent when a session is selected from the picker.
type sessionPickerSelectMsg struct {
	id string
}

// WindowSizeMsg re-exports the Bubble Tea window-size message for convenience.
type WindowSizeMsg = tea.WindowSizeMsg
