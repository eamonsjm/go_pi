package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

// Pagination safety limits for tools/list.
const (
	maxPaginationPages = 100
	maxTotalItems      = 10000
)

// ToolResult is the result of calling a tool via tools/call.
type ToolResult struct {
	Content []ContentItem `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

// ContentItem is a single content item in an MCP tool result.
type ContentItem struct {
	Type     string              `json:"type"`
	Text     string              `json:"text,omitempty"`
	MimeType string              `json:"mimeType,omitempty"`
	Data     string              `json:"data,omitempty"`
	Encoding string              `json:"encoding,omitempty"`
	Resource *EmbeddedResource `json:"resource,omitempty"`
}

// EmbeddedResource is a resource embedded in a tool result.
type EmbeddedResource struct {
	URI  string `json:"uri"`
	Text string `json:"text,omitempty"`
}

// ToolExecution describes task support for a tool.
type ToolExecution struct {
	TaskSupport string `json:"taskSupport,omitempty"` // "required", "optional", "forbidden" (default)
}

// ToolInfo is the wire format of a tool from tools/list.
type ToolInfo struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	InputSchema map[string]any   `json:"inputSchema"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
	Execution   ToolExecution `json:"execution,omitempty"`
}

// ToolsListPage is the response from tools/list.
type ToolsListPage struct {
	Tools      []ToolInfo `json:"tools"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// ToolAnnotations describes behavior hints for an MCP tool.
type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// AnnotationReadOnly returns the effective readOnlyHint value.
// Spec default: false (assume tool may modify state).
func AnnotationReadOnly(a *ToolAnnotations) bool {
	if a == nil || a.ReadOnlyHint == nil {
		return false
	}
	return *a.ReadOnlyHint
}

// AnnotationDestructive returns the effective destructiveHint value.
// Spec default: true (assume tool may be destructive).
func AnnotationDestructive(a *ToolAnnotations) bool {
	if a == nil || a.DestructiveHint == nil {
		return true
	}
	return *a.DestructiveHint
}

// AnnotationIdempotent returns the effective idempotentHint value.
// Spec default: false (assume tool is not idempotent).
func AnnotationIdempotent(a *ToolAnnotations) bool {
	if a == nil || a.IdempotentHint == nil {
		return false
	}
	return *a.IdempotentHint
}

// AnnotationOpenWorld returns the effective openWorldHint value.
// Spec default: true (assume tool interacts with external systems).
func AnnotationOpenWorld(a *ToolAnnotations) bool {
	if a == nil || a.OpenWorldHint == nil {
		return true
	}
	return *a.OpenWorldHint
}

// ToolCaller is the interface for calling tools on an MCP server.
// Server implements this; defined as interface for testability.
type ToolCaller interface {
	CallTool(ctx context.Context, name string, params map[string]any) (*ToolResult, error)
	ServerName() string
}

// Compile-time interface checks.
var (
	_ tools.Tool     = (*Tool)(nil)
	_ tools.RichTool = (*Tool)(nil)
)

// Tool implements tools.RichTool for an MCP server tool.
type Tool struct {
	server       ToolCaller
	name         string         // namespaced: "mcp__servername__toolname"
	originalName string         // name as known by the MCP server
	title        string         // human-readable display name
	desc         string
	inputSchema  map[string]any // JSON Schema from server
	annotations  *ToolAnnotations
}

// Name returns the namespaced tool name.
func (t *Tool) Name() string { return t.name }

// Description returns the tool's description.
func (t *Tool) Description() string { return t.desc }

// Title returns the human-readable display name from MCP Tool.title.
func (t *Tool) Title() string { return t.title }

// Schema returns the JSON Schema for the tool's input parameters.
func (t *Tool) Schema() any { return t.inputSchema }

// Annotations returns the tool's behavior hint annotations. May be nil.
func (t *Tool) Annotations() *ToolAnnotations { return t.annotations }

// Execute implements tools.Tool by delegating to ExecuteRich and flattening.
func (t *Tool) Execute(ctx context.Context, params map[string]any) (string, error) {
	blocks, err := t.ExecuteRich(ctx, params)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, block := range blocks {
		switch block.Type {
		case ai.ContentTypeText:
			b.WriteString(block.Text)
		case ai.ContentTypeImage:
			fmt.Fprintf(&b, "[image: %s]", block.MediaType)
		default:
			// Non-text block with no Text field; emit placeholder.
			if block.Text == "" {
				fmt.Fprintf(&b, "[%s content]", block.Type)
			} else {
				b.WriteString(block.Text)
			}
		}
	}
	return b.String(), nil
}

// ExecuteRich implements tools.RichTool — returns []ai.ContentBlock for multi-content results.
func (t *Tool) ExecuteRich(ctx context.Context, params map[string]any) ([]ai.ContentBlock, error) {
	result, err := t.server.CallTool(ctx, t.originalName, params)
	if err != nil {
		return nil, err
	}

	blocks := t.convertResult(result)

	// When MCP isError=true, return a RichToolError so the agent loop
	// preserves the content blocks AND sets isError=true on the message.
	if result.IsError {
		return nil, &tools.RichToolError{Blocks: blocks}
	}

	return blocks, nil
}

// convertResult maps MCP content items to ai.ContentBlock.
func (t *Tool) convertResult(result *ToolResult) []ai.ContentBlock {
	blocks := make([]ai.ContentBlock, 0, len(result.Content))
	for _, item := range result.Content {
		switch item.Type {
		case "text":
			blocks = append(blocks, ai.ContentBlock{
				Type: ai.ContentTypeText,
				Text: item.Text,
			})
		case "image":
			blocks = append(blocks, ai.ContentBlock{
				Type:      ai.ContentTypeImage,
				MediaType: item.MimeType,
				ImageData: item.Data,
			})
		case "audio":
			blocks = append(blocks, ai.ContentBlock{
				Type: ai.ContentTypeText,
				Text: fmt.Sprintf("[audio: %s, %d bytes, encoding=%s]",
					item.MimeType, len(item.Data), item.Encoding),
			})
		case "resource":
			if item.Resource != nil {
				blocks = append(blocks, ai.ContentBlock{
					Type: ai.ContentTypeText,
					Text: fmt.Sprintf("[resource: %s]\n%s", item.Resource.URI, item.Resource.Text),
				})
			}
		default:
			log.Printf("mcp: unrecognized content type %q in tool result", item.Type)
			blocks = append(blocks, ai.ContentBlock{
				Type: ai.ContentTypeText,
				Text: fmt.Sprintf("[unsupported content type: %s]", item.Type),
			})
		}
	}
	return blocks
}

// parseToolName splits a namespaced tool name "mcp__server__tool" into
// server name and original tool name. Returns empty strings if the format
// doesn't match.
func parseToolName(name string) (serverName, toolName string) {
	if !strings.HasPrefix(name, "mcp__") {
		return "", ""
	}
	rest := name[len("mcp__"):]
	idx := strings.Index(rest, "__")
	if idx < 0 {
		return "", ""
	}
	return rest[:idx], rest[idx+2:]
}

// buildToolName creates a namespaced tool name from server and tool names.
func buildToolName(serverName, toolName string) string {
	return "mcp__" + serverName + "__" + toolName
}

// BridgeTool creates a Tool from a ToolInfo and ToolCaller.
func BridgeTool(server ToolCaller, info ToolInfo) *Tool {
	return &Tool{
		server:       server,
		name:         buildToolName(server.ServerName(), info.Name),
		originalName: info.Name,
		title:        info.Title,
		desc:         info.Description,
		inputSchema:  info.InputSchema,
		annotations:  info.Annotations,
	}
}

// DiscoverTools fetches all tools from an MCP server via paginated tools/list,
// filtering out tools that require task support. The caller provides a function
// to perform the actual tools/list RPC call.
func DiscoverTools(
	ctx context.Context,
	server ToolCaller,
	listTools func(ctx context.Context, cursor string) (*ToolsListPage, error),
) ([]tools.Tool, error) {
	var allTools []tools.Tool
	var cursor string
	for pages := 0; pages < maxPaginationPages; pages++ {
		page, err := listTools(ctx, cursor)
		if err != nil {
			return nil, fmt.Errorf("tools/list (page %d): %w", pages, err)
		}
		for _, info := range page.Tools {
			if info.Execution.TaskSupport == "required" {
				log.Printf("mcp: skipping tool %q from %q (requires task support)",
					info.Name, server.ServerName())
				continue
			}
			allTools = append(allTools, BridgeTool(server, info))
		}
		if page.NextCursor == "" || len(allTools) >= maxTotalItems {
			break
		}
		cursor = page.NextCursor
	}
	return allTools, nil
}

// ListTools sends a tools/list request via the MCP client with pagination.
func (c *Client) ListTools(ctx context.Context, cursor string) (*ToolsListPage, error) {
	params := map[string]any{}
	if cursor != "" {
		params["cursor"] = cursor
	}
	result, err := c.Request(ctx, "tools/list", params)
	if err != nil {
		return nil, err
	}
	var page ToolsListPage
	if err := json.Unmarshal(result, &page); err != nil {
		return nil, fmt.Errorf("parsing tools/list response: %w", err)
	}
	return &page, nil
}

// CallToolRaw sends a tools/call request for the given tool name and arguments.
func (c *Client) CallToolRaw(ctx context.Context, name string, arguments map[string]any) (*ToolResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": arguments,
	}
	result, err := c.Request(ctx, "tools/call", params)
	if err != nil {
		return nil, err
	}
	var toolResult ToolResult
	if err := json.Unmarshal(result, &toolResult); err != nil {
		return nil, fmt.Errorf("parsing tools/call response: %w", err)
	}
	return &toolResult, nil
}
