package plugin

import (
	"context"
	"crypto/rand"
	"fmt"
)

// Error represents an error reported by a plugin process. It preserves
// the plugin's error content so callers can use errors.As to distinguish plugin
// errors from system errors.
type Error struct {
	Content string
}

func (e *Error) Error() string {
	return e.Content
}

// Tool wraps a plugin-provided tool definition and its owning process
// to implement the tools.Tool interface. This allows plugin tools to be
// registered in the standard tools.Registry alongside built-in tools.
type Tool struct {
	def     ToolDef
	process *Process
}

// Name returns the tool name as declared by the plugin.
func (t *Tool) Name() string {
	return t.def.Name
}

// Description returns the tool's human-readable description.
func (t *Tool) Description() string {
	return t.def.Description
}

// Schema returns the JSON Schema for the tool's input parameters.
func (t *Tool) Schema() any {
	return t.def.InputSchema
}

// Execute sends a tool_call message to the plugin process and waits for the
// result. If the plugin reports an error, it is returned as a Go error.
func (t *Tool) Execute(ctx context.Context, params map[string]any) (string, error) {
	if !t.process.Alive() {
		if t.process.Restarting() {
			return "", fmt.Errorf("plugin %s is restarting after a crash", t.process.name)
		}
		return "", fmt.Errorf("plugin %s is not running", t.process.name)
	}

	id, err := randomID()
	if err != nil {
		return "", fmt.Errorf("plugin tool %s: %w", t.def.Name, err)
	}
	content, isError, err := t.process.ExecuteTool(ctx, id, t.def.Name, params)
	if err != nil {
		return "", fmt.Errorf("plugin tool %s: %w", t.def.Name, err)
	}

	if isError {
		return "", &Error{Content: content}
	}

	return content, nil
}

// Command holds the metadata for a plugin-provided slash command and a
// reference to the owning plugin process. The TUI layer can use ExecuteCommand
// on the process to invoke the command.
type Command struct {
	Def     CommandDef
	Process *Process
}

// randomID generates a random hex string suitable for correlating tool call
// requests and responses.
func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("crypto/rand.Read failed: %w", err)
	}
	return fmt.Sprintf("plugin_%x", b), nil
}
