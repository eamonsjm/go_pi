package plugin

import (
	"context"
	"crypto/rand"
	"fmt"
)

// PluginTool wraps a plugin-provided tool definition and its owning process
// to implement the tools.Tool interface. This allows plugin tools to be
// registered in the standard tools.Registry alongside built-in tools.
type PluginTool struct {
	def     ToolDef
	process *PluginProcess
}

// Name returns the tool name as declared by the plugin.
func (t *PluginTool) Name() string {
	return t.def.Name
}

// Description returns the tool's human-readable description.
func (t *PluginTool) Description() string {
	return t.def.Description
}

// Schema returns the JSON Schema for the tool's input parameters.
func (t *PluginTool) Schema() any {
	return t.def.InputSchema
}

// Execute sends a tool_call message to the plugin process and waits for the
// result. If the plugin reports an error, it is returned as a Go error.
func (t *PluginTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	if !t.process.Alive() {
		if t.process.Restarting() {
			return "", fmt.Errorf("plugin %s is restarting after a crash", t.process.name)
		}
		return "", fmt.Errorf("plugin %s is not running", t.process.name)
	}

	id := randomID()
	content, isError, err := t.process.ExecuteTool(id, t.def.Name, params)
	if err != nil {
		return "", fmt.Errorf("plugin tool %s: %w", t.def.Name, err)
	}

	if isError {
		return "", fmt.Errorf("%s", content)
	}

	return content, nil
}

// PluginCommand holds the metadata for a plugin-provided slash command and a
// reference to the owning plugin process. The TUI layer can use ExecuteCommand
// on the process to invoke the command.
type PluginCommand struct {
	Def     CommandDef
	Process *PluginProcess
}

// randomID generates a random hex string suitable for correlating tool call
// requests and responses.
func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("plugin_%x", b)
}
