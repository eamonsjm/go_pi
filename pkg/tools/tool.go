package tools

import (
	"context"
	"sort"
	"strings"
	"sync"

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

// RichTool extends Tool with the ability to return multi-block results.
// Tools that implement RichTool can return content blocks directly
// (e.g., text + image). The agent loop checks for this interface and
// falls back to Tool.Execute if not implemented.
type RichTool interface {
	Tool
	ExecuteRich(ctx context.Context, params map[string]any) ([]ai.ContentBlock, error)
}

// RichToolError is returned by RichTool.ExecuteRich when the tool executed
// successfully at transport level but the result represents an error the
// LLM should reason about (e.g., MCP isError=true). The Blocks contain
// the error content that should be presented to the LLM as a tool result
// with isError=true, NOT flattened to a Go error string.
type RichToolError struct {
	Blocks []ai.ContentBlock
}

func (e *RichToolError) Error() string {
	var b strings.Builder
	for _, block := range e.Blocks {
		if block.Type == ai.ContentTypeText {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

// Registry holds a collection of tools indexed by name.
// All methods are safe for concurrent use.
type Registry struct {
	mu    sync.RWMutex
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
	r.mu.Lock()
	r.tools[t.Name()] = t
	r.mu.Unlock()
}

// Unregister removes a tool from the registry by name.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	delete(r.tools, name)
	r.mu.Unlock()
}

// ReplaceByPrefix atomically removes all tools with the given prefix
// and registers the new set. Used by MCP when re-discovering tools
// after notifications/tools/list_changed.
func (r *Registry) ReplaceByPrefix(prefix string, newTools []Tool) {
	r.mu.Lock()
	for name := range r.tools {
		if strings.HasPrefix(name, prefix) {
			delete(r.tools, name)
		}
	}
	for _, t := range newTools {
		r.tools[t.Name()] = t
	}
	r.mu.Unlock()
}

// AllWithPrefix returns all tools whose name starts with the given prefix.
func (r *Registry) AllWithPrefix(prefix string) []Tool {
	r.mu.RLock()
	var result []Tool
	for name, t := range r.tools {
		if strings.HasPrefix(name, prefix) {
			result = append(result, t)
		}
	}
	r.mu.RUnlock()
	return result
}

// Get returns the tool with the given name, or false if not found.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	t, ok := r.tools[name]
	r.mu.RUnlock()
	return t, ok
}

// All returns all registered tools sorted by name.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	result := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	r.mu.RUnlock()
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
