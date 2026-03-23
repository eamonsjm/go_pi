package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

// Content size limits for resource reads.
const (
	maxResourceTextBytes   = 1 << 20   // 1 MB
	maxResourceBinaryBytes = 512 << 10 // 512 KB
)

// Subscription TTL for auto-unsubscribe.
const (
	subscriptionTTL          = 5 * time.Minute
	subscriptionReapInterval = 1 * time.Minute
)

// --- Wire format types ---

// MCPResourceInfo is the wire format of a resource from resources/list.
type MCPResourceInfo struct {
	URI         string           `json:"uri"`
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	MimeType    string           `json:"mimeType,omitempty"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// MCPResourceTemplate is the wire format of a resource template from
// resources/templates/list. URITemplate follows RFC 6570.
type MCPResourceTemplate struct {
	URITemplate string           `json:"uriTemplate"`
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	MimeType    string           `json:"mimeType,omitempty"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// ResourcesListPage is the response from resources/list.
type ResourcesListPage struct {
	Resources  []MCPResourceInfo `json:"resources"`
	NextCursor string            `json:"nextCursor,omitempty"`
}

// ResourceTemplatesListPage is the response from resources/templates/list.
type ResourceTemplatesListPage struct {
	ResourceTemplates []MCPResourceTemplate `json:"resourceTemplates"`
	NextCursor        string                `json:"nextCursor,omitempty"`
}

// MCPResourceContent is a content item in a resources/read response.
type MCPResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"` // base64-encoded binary
}

// MCPResourceReadResult is the response from resources/read.
type MCPResourceReadResult struct {
	Contents []MCPResourceContent `json:"contents"`
}

// --- ResourceReader interface ---

// ResourceReader is the interface for reading resources on an MCP server.
// MCPServer implements this; defined as interface for testability.
type ResourceReader interface {
	ServerName() string
	ReadResource(ctx context.Context, uri string) (*MCPResourceReadResult, error)
}

// --- MCPResourceTool ---

// MCPResourceTool implements tools.RichTool for reading MCP server resources.
// Each MCP server that advertises resources gets one instance named
// "mcp__<server>__read_resource".
type MCPResourceTool struct {
	server    ResourceReader
	name      string // namespaced: "mcp__servername__read_resource"
	desc      string
	resources []MCPResourceInfo
	templates []MCPResourceTemplate
}

// Compile-time interface checks.
var (
	_ tools.Tool     = (*MCPResourceTool)(nil)
	_ tools.RichTool = (*MCPResourceTool)(nil)
)

// Name returns the namespaced tool name.
func (t *MCPResourceTool) Name() string { return t.name }

// Description returns the tool description including available resources.
func (t *MCPResourceTool) Description() string { return t.desc }

// Schema returns the JSON Schema for the tool's input.
func (t *MCPResourceTool) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"uri": map[string]any{
				"type":        "string",
				"description": "Resource URI (e.g., file:///path or template URI with parameters filled in)",
			},
		},
		"required": []string{"uri"},
	}
}

// Execute implements tools.Tool by delegating to ExecuteRich and flattening.
func (t *MCPResourceTool) Execute(ctx context.Context, params map[string]any) (string, error) {
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
			if block.Text == "" {
				fmt.Fprintf(&b, "[%s content]", block.Type)
			} else {
				b.WriteString(block.Text)
			}
		}
	}
	return b.String(), nil
}

// ExecuteRich implements tools.RichTool -- reads a resource and returns content blocks.
func (t *MCPResourceTool) ExecuteRich(ctx context.Context, params map[string]any) ([]ai.ContentBlock, error) {
	uri, _ := params["uri"].(string)
	if uri == "" {
		return nil, fmt.Errorf("uri parameter is required")
	}

	result, err := t.server.ReadResource(ctx, uri)
	if err != nil {
		return nil, err
	}

	return convertResourceResult(result), nil
}

// convertResourceResult maps resource contents to ai.ContentBlock, enforcing size caps.
func convertResourceResult(result *MCPResourceReadResult) []ai.ContentBlock {
	blocks := make([]ai.ContentBlock, 0, len(result.Contents))
	for _, item := range result.Contents {
		if item.Blob != "" {
			blocks = append(blocks, convertBlobContent(item)...)
		} else {
			blocks = append(blocks, convertTextContent(item))
		}
	}
	return blocks
}

// convertBlobContent handles binary resource content with size cap enforcement.
func convertBlobContent(item MCPResourceContent) []ai.ContentBlock {
	decoded, err := base64.StdEncoding.DecodeString(item.Blob)
	if err != nil {
		return []ai.ContentBlock{{
			Type: ai.ContentTypeText,
			Text: fmt.Sprintf("[resource %s: invalid base64 encoding]", item.URI),
		}}
	}
	if len(decoded) > maxResourceBinaryBytes {
		return []ai.ContentBlock{{
			Type: ai.ContentTypeText,
			Text: fmt.Sprintf("[resource %s: binary content too large (%d bytes, max %d)]",
				item.URI, len(decoded), maxResourceBinaryBytes),
		}}
	}
	if strings.HasPrefix(item.MimeType, "image/") {
		return []ai.ContentBlock{{
			Type:      ai.ContentTypeImage,
			MediaType: item.MimeType,
			ImageData: item.Blob,
		}}
	}
	return []ai.ContentBlock{{
		Type: ai.ContentTypeText,
		Text: fmt.Sprintf("[resource %s: binary %s, %d bytes]",
			item.URI, item.MimeType, len(decoded)),
	}}
}

// convertTextContent handles text resource content with size cap enforcement.
func convertTextContent(item MCPResourceContent) ai.ContentBlock {
	text := item.Text
	if len(text) > maxResourceTextBytes {
		text = text[:maxResourceTextBytes] + fmt.Sprintf("\n[truncated at %d bytes]", maxResourceTextBytes)
	}
	return ai.ContentBlock{
		Type: ai.ContentTypeText,
		Text: text,
	}
}

// buildResourceDescription creates the tool description including available resources.
func buildResourceDescription(serverName string, resources []MCPResourceInfo, templates []MCPResourceTemplate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Read a resource from MCP server %q. Provide a resource URI.", serverName)

	if len(resources) > 0 {
		b.WriteString("\n\nAvailable resources:")
		limit := len(resources)
		if limit > 50 {
			limit = 50
		}
		for _, r := range resources[:limit] {
			fmt.Fprintf(&b, "\n- %s", r.URI)
			if r.Name != "" {
				fmt.Fprintf(&b, " (%s)", r.Name)
			}
			if r.Description != "" {
				fmt.Fprintf(&b, ": %s", r.Description)
			}
		}
		if len(resources) > 50 {
			fmt.Fprintf(&b, "\n... and %d more", len(resources)-50)
		}
	}

	if len(templates) > 0 {
		b.WriteString("\n\nURI templates (fill in parameters):")
		limit := len(templates)
		if limit > 20 {
			limit = 20
		}
		for _, tmpl := range templates[:limit] {
			fmt.Fprintf(&b, "\n- %s", tmpl.URITemplate)
			if tmpl.Name != "" {
				fmt.Fprintf(&b, " (%s)", tmpl.Name)
			}
			if tmpl.Description != "" {
				fmt.Fprintf(&b, ": %s", tmpl.Description)
			}
		}
		if len(templates) > 20 {
			fmt.Fprintf(&b, "\n... and %d more", len(templates)-20)
		}
	}

	return b.String()
}

// BridgeResource creates an MCPResourceTool from discovered resources and templates.
func BridgeResource(server ResourceReader, resources []MCPResourceInfo, templates []MCPResourceTemplate) *MCPResourceTool {
	name := buildMCPToolName(server.ServerName(), "read_resource")
	return &MCPResourceTool{
		server:    server,
		name:      name,
		desc:      buildResourceDescription(server.ServerName(), resources, templates),
		resources: resources,
		templates: templates,
	}
}

// --- Resource Discovery ---

// DiscoverResources fetches all resources from an MCP server via paginated
// resources/list. The caller provides a function to perform the RPC call.
func DiscoverResources(
	ctx context.Context,
	listResources func(ctx context.Context, cursor string) (*ResourcesListPage, error),
) ([]MCPResourceInfo, error) {
	var all []MCPResourceInfo
	var cursor string
	for pages := 0; pages < maxPaginationPages; pages++ {
		page, err := listResources(ctx, cursor)
		if err != nil {
			return nil, fmt.Errorf("resources/list (page %d): %w", pages, err)
		}
		all = append(all, page.Resources...)
		if page.NextCursor == "" || len(all) >= maxTotalItems {
			break
		}
		cursor = page.NextCursor
	}
	return all, nil
}

// DiscoverResourceTemplates fetches all resource templates via paginated
// resources/templates/list.
func DiscoverResourceTemplates(
	ctx context.Context,
	listTemplates func(ctx context.Context, cursor string) (*ResourceTemplatesListPage, error),
) ([]MCPResourceTemplate, error) {
	var all []MCPResourceTemplate
	var cursor string
	for pages := 0; pages < maxPaginationPages; pages++ {
		page, err := listTemplates(ctx, cursor)
		if err != nil {
			return nil, fmt.Errorf("resources/templates/list (page %d): %w", pages, err)
		}
		all = append(all, page.ResourceTemplates...)
		if page.NextCursor == "" || len(all) >= maxTotalItems {
			break
		}
		cursor = page.NextCursor
	}
	return all, nil
}

// --- MCPClient resource methods ---

// ListResources sends a resources/list request with pagination.
func (c *MCPClient) ListResources(ctx context.Context, cursor string) (*ResourcesListPage, error) {
	params := map[string]any{}
	if cursor != "" {
		params["cursor"] = cursor
	}
	result, err := c.Request(ctx, "resources/list", params)
	if err != nil {
		return nil, err
	}
	var page ResourcesListPage
	if err := json.Unmarshal(result, &page); err != nil {
		return nil, fmt.Errorf("parsing resources/list response: %w", err)
	}
	return &page, nil
}

// ListResourceTemplates sends a resources/templates/list request with pagination.
func (c *MCPClient) ListResourceTemplates(ctx context.Context, cursor string) (*ResourceTemplatesListPage, error) {
	params := map[string]any{}
	if cursor != "" {
		params["cursor"] = cursor
	}
	result, err := c.Request(ctx, "resources/templates/list", params)
	if err != nil {
		return nil, err
	}
	var page ResourceTemplatesListPage
	if err := json.Unmarshal(result, &page); err != nil {
		return nil, fmt.Errorf("parsing resources/templates/list response: %w", err)
	}
	return &page, nil
}

// ReadResourceRaw sends a resources/read request for the given URI.
func (c *MCPClient) ReadResourceRaw(ctx context.Context, uri string) (*MCPResourceReadResult, error) {
	params := map[string]any{"uri": uri}
	result, err := c.Request(ctx, "resources/read", params)
	if err != nil {
		return nil, err
	}
	var readResult MCPResourceReadResult
	if err := json.Unmarshal(result, &readResult); err != nil {
		return nil, fmt.Errorf("parsing resources/read response: %w", err)
	}
	return &readResult, nil
}

// SubscribeResource sends a resources/subscribe request.
func (c *MCPClient) SubscribeResource(ctx context.Context, uri string) error {
	params := map[string]any{"uri": uri}
	_, err := c.Request(ctx, "resources/subscribe", params)
	return err
}

// UnsubscribeResource sends a resources/unsubscribe request.
func (c *MCPClient) UnsubscribeResource(ctx context.Context, uri string) error {
	params := map[string]any{"uri": uri}
	_, err := c.Request(ctx, "resources/unsubscribe", params)
	return err
}

// --- MCPServer resource methods ---

// ReadResource implements ResourceReader -- reads a resource via the MCP client
// and manages subscriptions (subscribes on first access, refreshes TTL).
func (s *MCPServer) ReadResource(ctx context.Context, uri string) (*MCPResourceReadResult, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrServerCrashed
	}
	s.mu.Unlock()

	result, err := s.client.ReadResourceRaw(ctx, uri)
	if err != nil {
		return nil, err
	}

	// Touch subscription (subscribe on first access, refresh TTL).
	if s.subscriptions != nil {
		s.subscriptions.touch(ctx, uri)
	}

	return result, nil
}

// discoverAndRegisterResources discovers resources/templates and registers the
// read_resource tool. Only called if the server advertises resource capability.
func (s *MCPServer) discoverAndRegisterResources(ctx context.Context) error {
	resources, err := DiscoverResources(ctx, func(ctx context.Context, cursor string) (*ResourcesListPage, error) {
		return s.client.ListResources(ctx, cursor)
	})
	if err != nil {
		return fmt.Errorf("discovering resources: %w", err)
	}

	templates, err := DiscoverResourceTemplates(ctx, func(ctx context.Context, cursor string) (*ResourceTemplatesListPage, error) {
		return s.client.ListResourceTemplates(ctx, cursor)
	})
	if err != nil {
		return fmt.Errorf("discovering resource templates: %w", err)
	}

	if len(resources) == 0 && len(templates) == 0 {
		log.Printf("mcp: server %q: no resources or templates found", s.name)
		return nil
	}

	tool := BridgeResource(s, resources, templates)

	s.mu.Lock()
	s.resourceTool = tool
	s.mu.Unlock()

	s.manager.toolRegistry.Register(tool)

	log.Printf("mcp: server %q: registered read_resource tool (%d resources, %d templates)",
		s.name, len(resources), len(templates))
	return nil
}

// handleResourcesListChanged re-discovers resources and updates the tool.
func (s *MCPServer) handleResourcesListChanged() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.discoverAndRegisterResources(ctx); err != nil {
		log.Printf("mcp: failed to re-discover resources for %q: %v", s.name, err)
		return
	}

	s.manager.injectSystemMessage(fmt.Sprintf(
		"[MCP server %q resources updated -- resource list has changed]", s.name))
}

// handleResourcesUpdated handles a notification that a subscribed resource changed.
func (s *MCPServer) handleResourcesUpdated(params json.RawMessage) {
	var notification struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &notification); err != nil {
		log.Printf("mcp: server %q: malformed resources/updated notification", s.name)
		return
	}

	s.manager.injectSystemMessage(fmt.Sprintf(
		"[MCP server %q: resource %q has been updated -- re-read to get latest content]",
		s.name, notification.URI))
}

// --- Subscription Management ---

// subscription tracks a resource subscription with TTL.
type subscription struct {
	lastAccess time.Time
}

// subscriptionManager tracks resource subscriptions with TTL-based auto-unsubscribe.
// When a resource is read, it is subscribed (if not already) and the TTL is refreshed.
// After subscriptionTTL with no reads, the subscription is automatically removed.
type subscriptionManager struct {
	mu     sync.Mutex
	subs   map[string]*subscription // URI -> subscription
	client *MCPClient
	cancel context.CancelFunc
	done   chan struct{}
}

// newSubscriptionManager creates a subscription manager and starts the reaper goroutine.
func newSubscriptionManager(client *MCPClient) *subscriptionManager {
	ctx, cancel := context.WithCancel(context.Background())
	sm := &subscriptionManager{
		subs:   make(map[string]*subscription),
		client: client,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go sm.reapLoop(ctx)
	return sm
}

// touch subscribes to a resource (if not already) and refreshes the TTL.
func (sm *subscriptionManager) touch(ctx context.Context, uri string) {
	sm.mu.Lock()
	existing, ok := sm.subs[uri]
	if ok {
		existing.lastAccess = time.Now()
		sm.mu.Unlock()
		return
	}
	sm.subs[uri] = &subscription{lastAccess: time.Now()}
	sm.mu.Unlock()

	// Subscribe (best-effort -- don't fail the read on subscribe error).
	if err := sm.client.SubscribeResource(ctx, uri); err != nil {
		log.Printf("mcp: failed to subscribe to resource %q: %v", uri, err)
		sm.mu.Lock()
		delete(sm.subs, uri)
		sm.mu.Unlock()
	}
}

// reapLoop periodically unsubscribes stale subscriptions.
func (sm *subscriptionManager) reapLoop(ctx context.Context) {
	defer close(sm.done)
	ticker := time.NewTicker(subscriptionReapInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sm.reap()
		}
	}
}

// reap unsubscribes entries that have exceeded the TTL.
func (sm *subscriptionManager) reap() {
	sm.mu.Lock()
	now := time.Now()
	var stale []string
	for uri, sub := range sm.subs {
		if now.Sub(sub.lastAccess) > subscriptionTTL {
			stale = append(stale, uri)
			delete(sm.subs, uri)
		}
	}
	sm.mu.Unlock()

	for _, uri := range stale {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := sm.client.UnsubscribeResource(ctx, uri); err != nil {
			log.Printf("mcp: failed to unsubscribe from resource %q: %v", uri, err)
		}
		cancel()
	}
}

// close unsubscribes all tracked resources and stops the reaper.
func (sm *subscriptionManager) close() {
	sm.cancel()
	<-sm.done

	sm.mu.Lock()
	uris := make([]string, 0, len(sm.subs))
	for uri := range sm.subs {
		uris = append(uris, uri)
	}
	sm.subs = nil
	sm.mu.Unlock()

	for _, uri := range uris {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := sm.client.UnsubscribeResource(ctx, uri); err != nil {
			log.Printf("mcp: failed to unsubscribe from resource %q on close: %v", uri, err)
		}
		cancel()
	}
}
