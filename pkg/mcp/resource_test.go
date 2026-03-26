package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

// mockResourceReader implements ResourceReader for testing.
type mockResourceReader struct {
	name   string
	result *ResourceReadResult
	err    error
}

func (m *mockResourceReader) ServerName() string { return m.name }
func (m *mockResourceReader) ReadResource(_ context.Context, _ string) (*ResourceReadResult, error) {
	return m.result, m.err
}

func TestResourceToolImplementsRichTool(t *testing.T) {
	tool := &ResourceTool{
		server: &mockResourceReader{name: "fs"},
		name:   "mcp__fs__read_resource",
		desc:   "Read a resource",
	}

	var _ tools.Tool = tool
	var _ tools.RichTool = tool

	if tool.Name() != "mcp__fs__read_resource" {
		t.Errorf("Name() = %q", tool.Name())
	}
}

func TestResourceToolExecuteRichText(t *testing.T) {
	server := &mockResourceReader{
		name: "fs",
		result: &ResourceReadResult{
			Contents: []ResourceContent{
				{URI: "file:///test.txt", Text: "hello world"},
			},
		},
	}
	tool := &ResourceTool{server: server, name: "mcp__fs__read_resource"}

	blocks, err := tool.ExecuteRich(context.Background(), map[string]any{"uri": "file:///test.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Type != ai.ContentTypeText || blocks[0].Text != "hello world" {
		t.Errorf("unexpected block: %+v", blocks[0])
	}
}

func TestResourceToolExecuteRichImage(t *testing.T) {
	imgData := base64.StdEncoding.EncodeToString([]byte("fakepng"))
	server := &mockResourceReader{
		name: "fs",
		result: &ResourceReadResult{
			Contents: []ResourceContent{
				{URI: "file:///img.png", MimeType: "image/png", Blob: imgData},
			},
		},
	}
	tool := &ResourceTool{server: server, name: "mcp__fs__read_resource"}

	blocks, err := tool.ExecuteRich(context.Background(), map[string]any{"uri": "file:///img.png"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Type != ai.ContentTypeImage {
		t.Errorf("expected image type, got %q", blocks[0].Type)
	}
	if blocks[0].MediaType != "image/png" {
		t.Errorf("expected image/png, got %q", blocks[0].MediaType)
	}
	if blocks[0].ImageData != imgData {
		t.Errorf("image data mismatch")
	}
}

func TestResourceToolExecuteRichMissingURI(t *testing.T) {
	tool := &ResourceTool{
		server: &mockResourceReader{name: "fs"},
		name:   "mcp__fs__read_resource",
	}

	_, err := tool.ExecuteRich(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing URI")
	}
	if !strings.Contains(err.Error(), "uri parameter is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResourceToolExecuteRichTransportError(t *testing.T) {
	server := &mockResourceReader{
		name: "fs",
		err:  errors.New("connection refused"),
	}
	tool := &ResourceTool{server: server, name: "mcp__fs__read_resource"}

	_, err := tool.ExecuteRich(context.Background(), map[string]any{"uri": "file:///x"})
	if err == nil || err.Error() != "connection refused" {
		t.Errorf("expected 'connection refused', got %v", err)
	}
}

func TestResourceToolExecuteFlatten(t *testing.T) {
	server := &mockResourceReader{
		name: "fs",
		result: &ResourceReadResult{
			Contents: []ResourceContent{
				{URI: "file:///a.txt", Text: "part 1"},
				{URI: "file:///b.txt", Text: "part 2"},
			},
		},
	}
	tool := &ResourceTool{server: server, name: "mcp__fs__read_resource"}

	result, err := tool.Execute(context.Background(), map[string]any{"uri": "file:///a.txt"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "part 1part 2" {
		t.Errorf("Execute() = %q, want %q", result, "part 1part 2")
	}
}

func TestConvertResourceResultTextTruncation(t *testing.T) {
	// Create text larger than maxResourceTextBytes.
	bigText := strings.Repeat("x", maxResourceTextBytes+100)
	result := &ResourceReadResult{
		Contents: []ResourceContent{
			{URI: "file:///big.txt", Text: bigText},
		},
	}

	blocks := convertResourceResult(result)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !strings.Contains(blocks[0].Text, "[truncated at") {
		t.Error("expected truncation marker")
	}
	// Text before truncation marker should be exactly maxResourceTextBytes.
	idx := strings.Index(blocks[0].Text, "\n[truncated at")
	if idx != maxResourceTextBytes {
		t.Errorf("truncation at %d, expected %d", idx, maxResourceTextBytes)
	}
}

func TestConvertResourceResultBinarySizeCap(t *testing.T) {
	// Create binary content larger than maxResourceBinaryBytes.
	bigBlob := base64.StdEncoding.EncodeToString(make([]byte, maxResourceBinaryBytes+100))
	result := &ResourceReadResult{
		Contents: []ResourceContent{
			{URI: "file:///big.bin", MimeType: "application/octet-stream", Blob: bigBlob},
		},
	}

	blocks := convertResourceResult(result)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Type != ai.ContentTypeText {
		t.Errorf("expected text type for oversized binary, got %q", blocks[0].Type)
	}
	if !strings.Contains(blocks[0].Text, "too large") {
		t.Errorf("expected 'too large' message, got %q", blocks[0].Text)
	}
}

func TestConvertResourceResultInvalidBase64(t *testing.T) {
	result := &ResourceReadResult{
		Contents: []ResourceContent{
			{URI: "file:///bad.bin", Blob: "not-valid-base64!!!"},
		},
	}

	blocks := convertResourceResult(result)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !strings.Contains(blocks[0].Text, "invalid base64") {
		t.Errorf("expected base64 error, got %q", blocks[0].Text)
	}
}

func TestConvertResourceResultBinaryNonImage(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("PDF content"))
	result := &ResourceReadResult{
		Contents: []ResourceContent{
			{URI: "file:///doc.pdf", MimeType: "application/pdf", Blob: data},
		},
	}

	blocks := convertResourceResult(result)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Type != ai.ContentTypeText {
		t.Errorf("expected text type for non-image binary, got %q", blocks[0].Type)
	}
	if !strings.Contains(blocks[0].Text, "application/pdf") {
		t.Errorf("expected MIME type in output, got %q", blocks[0].Text)
	}
}

func TestBuildResourceDescription(t *testing.T) {
	resources := []ResourceInfo{
		{URI: "file:///config.json", Name: "config", Description: "App configuration"},
		{URI: "file:///data.csv", Name: "data"},
	}
	templates := []ResourceTemplate{
		{URITemplate: "file:///{path}", Name: "file", Description: "Read any file"},
	}

	desc := buildResourceDescription("myserver", resources, templates)

	if !strings.Contains(desc, `MCP server "myserver"`) {
		t.Error("expected server name in description")
	}
	if !strings.Contains(desc, "file:///config.json") {
		t.Error("expected resource URI")
	}
	if !strings.Contains(desc, "App configuration") {
		t.Error("expected resource description")
	}
	if !strings.Contains(desc, "file:///{path}") {
		t.Error("expected template URI")
	}
	if !strings.Contains(desc, "Read any file") {
		t.Error("expected template description")
	}
}

func TestBuildResourceDescriptionEmpty(t *testing.T) {
	desc := buildResourceDescription("empty", nil, nil)
	if !strings.Contains(desc, `MCP server "empty"`) {
		t.Error("expected server name")
	}
	if strings.Contains(desc, "Available resources") {
		t.Error("should not list resources section when empty")
	}
}

func TestBuildResourceDescriptionTruncation(t *testing.T) {
	// More than 50 resources should be truncated.
	resources := make([]ResourceInfo, 55)
	for i := range resources {
		resources[i] = ResourceInfo{URI: "file:///r" + strings.Repeat("x", i)}
	}

	desc := buildResourceDescription("big", resources, nil)
	if !strings.Contains(desc, "and 5 more") {
		t.Error("expected truncation message for resources")
	}
}

func TestBridgeResource(t *testing.T) {
	server := &mockResourceReader{name: "myserver"}
	resources := []ResourceInfo{{URI: "file:///test.txt", Name: "test"}}
	templates := []ResourceTemplate{{URITemplate: "file:///{path}", Name: "file"}}

	tool := BridgeResource(server, resources, templates)

	if tool.Name() != "mcp__myserver__read_resource" {
		t.Errorf("Name() = %q", tool.Name())
	}
	if !strings.Contains(tool.Description(), "file:///test.txt") {
		t.Error("expected resource URI in description")
	}
	if len(tool.resources) != 1 {
		t.Errorf("expected 1 resource, got %d", len(tool.resources))
	}
	if len(tool.templates) != 1 {
		t.Errorf("expected 1 template, got %d", len(tool.templates))
	}
}

func TestDiscoverResourcesPagination(t *testing.T) {
	calls := 0
	listFunc := func(_ context.Context, cursor string) (*ResourcesListPage, error) {
		calls++
		switch cursor {
		case "":
			return &ResourcesListPage{
				Resources:  []ResourceInfo{{URI: "file:///a.txt", Name: "a"}},
				NextCursor: "page2",
			}, nil
		case "page2":
			return &ResourcesListPage{
				Resources: []ResourceInfo{{URI: "file:///b.txt", Name: "b"}},
			}, nil
		default:
			t.Fatalf("unexpected cursor %q", cursor)
			return nil, nil
		}
	}

	result, err := DiscoverResources(context.Background(), listFunc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(result))
	}
	if result[0].URI != "file:///a.txt" || result[1].URI != "file:///b.txt" {
		t.Errorf("unexpected resources: %v", result)
	}
}

func TestDiscoverResourcesError(t *testing.T) {
	listFunc := func(_ context.Context, _ string) (*ResourcesListPage, error) {
		return nil, errors.New("server gone")
	}

	_, err := DiscoverResources(context.Background(), listFunc)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "server gone") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDiscoverResourceTemplatesPagination(t *testing.T) {
	calls := 0
	listFunc := func(_ context.Context, cursor string) (*ResourceTemplatesListPage, error) {
		calls++
		switch cursor {
		case "":
			return &ResourceTemplatesListPage{
				ResourceTemplates: []ResourceTemplate{{URITemplate: "file:///{path}", Name: "file"}},
				NextCursor:        "page2",
			}, nil
		case "page2":
			return &ResourceTemplatesListPage{
				ResourceTemplates: []ResourceTemplate{{URITemplate: "db:///{table}", Name: "table"}},
			}, nil
		default:
			t.Fatalf("unexpected cursor %q", cursor)
			return nil, nil
		}
	}

	result, err := DiscoverResourceTemplates(context.Background(), listFunc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 templates, got %d", len(result))
	}
}

func TestDiscoverResourceTemplatesError(t *testing.T) {
	listFunc := func(_ context.Context, _ string) (*ResourceTemplatesListPage, error) {
		return nil, errors.New("timeout")
	}

	_, err := DiscoverResourceTemplates(context.Background(), listFunc)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSubscriptionManagerTouchAfterClose(t *testing.T) {
	// Regression test for gp-5puq: touch() must not panic on a nil subs map
	// after close() has been called.
	sm := newSubscriptionManager(nil) // nil client is fine -- touch returns before using it
	sm.close()

	// Before the fix this panicked with "assignment to entry in nil map".
	sm.touch(context.Background(), "file:///test.txt")
}

func TestSubscriptionManagerTouchCloseRace(t *testing.T) {
	// Exercise the close/touch race. We manually construct the manager and
	// simulate close() by nilling subs under the lock. The URI is
	// pre-populated so touch() hits the refresh path (no subscribe RPC
	// needed, avoiding the need for a real Client).
	sm := &subscriptionManager{
		subs: map[string]*subscription{
			"file:///test.txt": {lastAccess: time.Now()},
		},
	}

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		// Simulate the critical part of close(): nil out the subs map.
		sm.mu.Lock()
		sm.subs = nil
		sm.mu.Unlock()
	}()

	// Hammer touch() concurrently. Some calls see the populated map and
	// refresh; others see nil and return early. Without the nil-guard fix,
	// the nil-map read in the refresh path would return (nil, false) then
	// fall through to a nil-map write panic.
	for i := 0; i < 100; i++ {
		sm.touch(context.Background(), "file:///test.txt")
	}
	<-closeDone
}

func TestResourceToolSchema(t *testing.T) {
	tool := &ResourceTool{
		server: &mockResourceReader{name: "fs"},
		name:   "mcp__fs__read_resource",
	}

	schema, ok := tool.Schema().(map[string]any)
	if !ok {
		t.Fatal("Schema() should return map[string]any")
	}
	if schema["type"] != "object" {
		t.Errorf("schema type = %v", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties")
	}
	if _, ok := props["uri"]; !ok {
		t.Error("expected uri property")
	}
	required, ok := schema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "uri" {
		t.Errorf("expected required=[uri], got %v", schema["required"])
	}
}
