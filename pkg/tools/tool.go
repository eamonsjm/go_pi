package tools

import (
	"context"
	"sort"

	"github.com/ejm/go_pi/pkg/ai"
)

// Tool is the interface that all built-in tools must implement.
type Tool interface {
	// Name returns the tool name used in API calls.
	Name() string
	// Description returns a human-readable description of the tool.
	Description() string
	// Schema returns the JSON Schema for the tool's input parameters.
	Schema() any
	// Execute runs the tool with the given parameters and returns the result.
	Execute(ctx context.Context, params map[string]any) (string, error)
}

// Registry holds a collection of tools indexed by name.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates a new empty tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry. If a tool with the same name
// already exists, it is replaced.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get returns the tool with the given name, or false if not found.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// All returns all registered tools sorted by name.
func (r *Registry) All() []Tool {
	result := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name() < result[j].Name()
	})
	return result
}

// ToToolDefs converts all registered tools to ai.ToolDef slice
// suitable for passing to the LLM provider.
func (r *Registry) ToToolDefs() []ai.ToolDef {
	all := r.All()
	defs := make([]ai.ToolDef, len(all))
	for i, t := range all {
		defs[i] = ai.ToolDef{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
		}
	}
	return defs
}

// RegisterDefaults registers all built-in tools into the given registry.
func RegisterDefaults(r *Registry) {
	r.Register(&ReadTool{})
	r.Register(&WriteTool{})
	r.Register(&EditTool{})
	r.Register(&BashTool{})
	r.Register(&GlobTool{})
	r.Register(&GrepTool{})
}
