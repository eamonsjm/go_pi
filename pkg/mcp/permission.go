package mcp

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/ejm/go_pi/pkg/config"
)

// ConfirmToolFunc asks the user for permission to execute an MCP tool.
// Returns true if approved.
type ConfirmToolFunc func(serverName, toolName, description string) (bool, error)

// MCPPermissionHook implements tools.Hook for MCP tool permission checks.
// It intercepts MCP tool execution (tools with "mcp__" prefix) and enforces
// deny lists, auto-approve lists, annotation-based read-only bypass, and
// interactive confirmation.
type MCPPermissionHook struct {
	configs map[string]*config.MCPPermissionConfig // server name -> config

	mu               sync.RWMutex       // protects confirm and annotationSource
	confirm          ConfirmToolFunc    // UI callback for interactive approval
	annotationSource AnnotationSource   // may be nil if no annotation source is available
}

// AnnotationSource provides access to tool annotations by server and tool name.
type AnnotationSource interface {
	GetAnnotations(serverName, toolName string) *ToolAnnotations
}

// NewMCPPermissionHook creates a new permission hook.
func NewMCPPermissionHook(configs map[string]*config.MCPPermissionConfig, confirm ConfirmToolFunc) *MCPPermissionHook {
	return &MCPPermissionHook{
		configs: configs,
		confirm: confirm,
	}
}

// SetConfirm sets (or replaces) the interactive confirmation callback.
// This allows wiring the callback after construction, e.g. once the TUI is ready.
func (h *MCPPermissionHook) SetConfirm(fn ConfirmToolFunc) {
	h.mu.Lock()
	h.confirm = fn
	h.mu.Unlock()
}

// SetAnnotationSource sets the annotation source for permission decisions.
func (h *MCPPermissionHook) SetAnnotationSource(src AnnotationSource) {
	h.mu.Lock()
	h.annotationSource = src
	h.mu.Unlock()
}

// BeforeExecute checks permissions before an MCP tool executes.
//
// Decision order:
//  1. Not an MCP tool (no "mcp__" prefix) → skip
//  2. Deny list → block
//  3. Auto-approve list → allow
//  4. Annotation readOnlyHint=true → allow
//  5. Interactive confirmation → allow/deny
func (h *MCPPermissionHook) BeforeExecute(_ context.Context, toolName string, _ map[string]any) error {
	server, tool := parseMCPToolName(toolName)
	if server == "" {
		return nil // not an MCP tool
	}

	h.mu.RLock()
	confirm := h.confirm
	annotationSource := h.annotationSource
	h.mu.RUnlock()

	cfg := h.configs[server]

	// Deny takes precedence over everything.
	if cfg != nil && slices.Contains(cfg.Deny, tool) {
		return fmt.Errorf("MCP tool %q on server %q is denied by configuration", tool, server)
	}

	// Auto-approve check.
	if cfg != nil && slices.Contains(cfg.AutoApprove, tool) {
		return nil
	}

	// Annotation-based bypass: explicitly read-only tools skip confirmation.
	if annotationSource != nil {
		annotations := annotationSource.GetAnnotations(server, tool)
		if AnnotationReadOnly(annotations) {
			return nil
		}
	}

	// Default: require user confirmation.
	if confirm == nil {
		// No confirmation callback available (non-interactive mode).
		// Conservative default: deny.
		return fmt.Errorf("MCP tool %q on server %q requires confirmation but no interactive session is available", tool, server)
	}

	approved, err := confirm(server, tool, "MCP tool execution")
	if err != nil {
		return fmt.Errorf("confirmation error for MCP tool %q on server %q: %w", tool, server, err)
	}
	if !approved {
		return fmt.Errorf("user denied MCP tool %q on server %q", tool, server)
	}
	return nil
}

// AfterExecute is a no-op for the permission hook.
func (h *MCPPermissionHook) AfterExecute(_ context.Context, _ string, _ map[string]any, result string, err error) (string, error) {
	return result, err
}

// MCPAnnotationSource implements AnnotationSource by looking up tools in the
// tools.Registry.
type MCPAnnotationSource struct {
	getAnnotations func(serverName, toolName string) *ToolAnnotations
}

// NewMCPAnnotationSource creates an annotation source backed by a lookup function.
func NewMCPAnnotationSource(fn func(serverName, toolName string) *ToolAnnotations) *MCPAnnotationSource {
	return &MCPAnnotationSource{getAnnotations: fn}
}

// GetAnnotations returns annotations for the given server/tool combination.
func (s *MCPAnnotationSource) GetAnnotations(serverName, toolName string) *ToolAnnotations {
	if s.getAnnotations == nil {
		return nil
	}
	return s.getAnnotations(serverName, toolName)
}
