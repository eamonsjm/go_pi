package tui

import (
	tea "github.com/charmbracelet/bubbletea"
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

// WindowSizeMsg re-exports the Bubble Tea window-size message for convenience.
type WindowSizeMsg = tea.WindowSizeMsg
