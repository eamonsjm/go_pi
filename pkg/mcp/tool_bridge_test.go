package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

// mockToolCaller implements ToolCaller for testing.
type mockToolCaller struct {
	name   string
	result *ToolResult
	err    error
}

func (m *mockToolCaller) CallTool(_ context.Context, _ string, _ map[string]any) (*ToolResult, error) {
	return m.result, m.err
}

func (m *mockToolCaller) ServerName() string { return m.name }

func TestToolImplementsRichTool(t *testing.T) {
	tool := &Tool{
		server:       &mockToolCaller{name: "test"},
		name:         "mcp__test__hello",
		originalName: "hello",
		title:        "Hello Tool",
		desc:         "says hello",
		inputSchema:  map[string]any{"type": "object"},
	}

	// Verify it satisfies both interfaces.
	var _ tools.Tool = tool
	var _ tools.RichTool = tool

	if tool.Name() != "mcp__test__hello" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "mcp__test__hello")
	}
	if tool.Description() != "says hello" {
		t.Errorf("Description() = %q, want %q", tool.Description(), "says hello")
	}
	if tool.Title() != "Hello Tool" {
		t.Errorf("Title() = %q, want %q", tool.Title(), "Hello Tool")
	}
}

func TestToolExecuteRichTextResult(t *testing.T) {
	server := &mockToolCaller{
		name: "test",
		result: &ToolResult{
			Content: []ContentItem{
				{Type: "text", Text: "hello world"},
			},
		},
	}
	tool := &Tool{server: server, originalName: "greet"}

	blocks, err := tool.ExecuteRich(context.Background(), nil)
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

func TestToolExecuteRichImageResult(t *testing.T) {
	server := &mockToolCaller{
		name: "test",
		result: &ToolResult{
			Content: []ContentItem{
				{Type: "image", MimeType: "image/png", Data: "iVBOR..."},
			},
		},
	}
	tool := &Tool{server: server, originalName: "screenshot"}

	blocks, err := tool.ExecuteRich(context.Background(), nil)
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
}

func TestToolExecuteRichIsError(t *testing.T) {
	server := &mockToolCaller{
		name: "test",
		result: &ToolResult{
			Content: []ContentItem{
				{Type: "text", Text: "file not found"},
			},
			IsError: true,
		},
	}
	tool := &Tool{server: server, originalName: "read"}

	blocks, err := tool.ExecuteRich(context.Background(), nil)
	if blocks != nil {
		t.Errorf("expected nil blocks on isError, got %v", blocks)
	}

	var richErr *tools.RichToolError
	if !errors.As(err, &richErr) {
		t.Fatalf("expected *tools.RichToolError, got %T: %v", err, err)
	}
	if len(richErr.Blocks) != 1 {
		t.Fatalf("expected 1 block in RichToolError, got %d", len(richErr.Blocks))
	}
	if richErr.Blocks[0].Text != "file not found" {
		t.Errorf("expected 'file not found', got %q", richErr.Blocks[0].Text)
	}
	if richErr.Error() != "file not found" {
		t.Errorf("Error() = %q, want %q", richErr.Error(), "file not found")
	}
}

func TestToolExecuteRichTransportError(t *testing.T) {
	server := &mockToolCaller{
		name: "test",
		err:  errors.New("connection refused"),
	}
	tool := &Tool{server: server, originalName: "anything"}

	blocks, err := tool.ExecuteRich(context.Background(), nil)
	if blocks != nil {
		t.Errorf("expected nil blocks on transport error, got %v", blocks)
	}
	if err == nil || err.Error() != "connection refused" {
		t.Errorf("expected 'connection refused', got %v", err)
	}
	var richErr *tools.RichToolError
	if errors.As(err, &richErr) {
		t.Error("transport errors should NOT be RichToolError")
	}
}

func TestToolExecuteFlatten(t *testing.T) {
	server := &mockToolCaller{
		name: "test",
		result: &ToolResult{
			Content: []ContentItem{
				{Type: "text", Text: "line 1"},
				{Type: "text", Text: "line 2"},
			},
		},
	}
	tool := &Tool{server: server, originalName: "multi"}

	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "line 1line 2" {
		t.Errorf("Execute() = %q, want %q", result, "line 1line 2")
	}
}

func TestToolExecuteFlattenMixedContent(t *testing.T) {
	server := &mockToolCaller{
		name: "test",
		result: &ToolResult{
			Content: []ContentItem{
				{Type: "text", Text: "Here is the screenshot: "},
				{Type: "image", MimeType: "image/png", Data: "iVBOR..."},
				{Type: "text", Text: " and done"},
			},
		},
	}
	tool := &Tool{server: server, originalName: "mixed"}

	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "Here is the screenshot: [image: image/png] and done"
	if result != want {
		t.Errorf("Execute() = %q, want %q", result, want)
	}
}

func TestConvertResultAudio(t *testing.T) {
	tool := &Tool{}
	blocks := tool.convertResult(&ToolResult{
		Content: []ContentItem{
			{Type: "audio", MimeType: "audio/wav", Data: "RIFF", Encoding: "base64"},
		},
	})
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Type != ai.ContentTypeText {
		t.Errorf("expected text type, got %q", blocks[0].Type)
	}
	if blocks[0].Text != "[audio: audio/wav, 4 bytes, encoding=base64]" {
		t.Errorf("unexpected text: %q", blocks[0].Text)
	}
}

func TestConvertResultResource(t *testing.T) {
	tool := &Tool{}
	blocks := tool.convertResult(&ToolResult{
		Content: []ContentItem{
			{Type: "resource", Resource: &EmbeddedResource{
				URI:  "file:///tmp/test.txt",
				Text: "file contents",
			}},
		},
	})
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	expected := "[resource: file:///tmp/test.txt]\nfile contents"
	if blocks[0].Text != expected {
		t.Errorf("unexpected text: %q, want %q", blocks[0].Text, expected)
	}
}

func TestConvertResultUnknownType(t *testing.T) {
	tool := &Tool{}
	blocks := tool.convertResult(&ToolResult{
		Content: []ContentItem{
			{Type: "video"},
		},
	})
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Text != "[unsupported content type: video]" {
		t.Errorf("unexpected text: %q", blocks[0].Text)
	}
}

func TestParseToolName(t *testing.T) {
	tests := []struct {
		input      string
		wantServer string
		wantTool   string
	}{
		{"mcp__filesystem__read_file", "filesystem", "read_file"},
		{"mcp__db__query", "db", "query"},
		{"mcp__my-server__my-tool", "my-server", "my-tool"},
		{"read", "", ""},           // not MCP
		{"mcp__nodelimiter", "", ""}, // missing second __
		{"mcp____empty", "", "empty"},
	}
	for _, tt := range tests {
		server, tool := parseToolName(tt.input)
		if server != tt.wantServer || tool != tt.wantTool {
			t.Errorf("parseToolName(%q) = (%q, %q), want (%q, %q)",
				tt.input, server, tool, tt.wantServer, tt.wantTool)
		}
	}
}

func TestBuildToolName(t *testing.T) {
	got := buildToolName("filesystem", "read_file")
	want := "mcp__filesystem__read_file"
	if got != want {
		t.Errorf("buildToolName = %q, want %q", got, want)
	}
}

func TestBridgeTool(t *testing.T) {
	server := &mockToolCaller{name: "myserver"}
	readOnly := true
	info := ToolInfo{
		Name:        "search",
		Title:       "Search Files",
		Description: "searches files",
		InputSchema: map[string]any{"type": "object"},
		Annotations: &ToolAnnotations{ReadOnlyHint: &readOnly},
	}
	tool := BridgeTool(server, info)
	if tool.Name() != "mcp__myserver__search" {
		t.Errorf("Name() = %q", tool.Name())
	}
	if tool.Title() != "Search Files" {
		t.Errorf("Title() = %q", tool.Title())
	}
	if tool.Annotations() == nil || !AnnotationReadOnly(tool.Annotations()) {
		t.Error("expected readOnly annotation")
	}
}

func TestAnnotationDefaults(t *testing.T) {
	// nil annotations -> conservative defaults
	if AnnotationReadOnly(nil) {
		t.Error("nil readOnly should be false")
	}
	if !AnnotationDestructive(nil) {
		t.Error("nil destructive should be true")
	}
	if AnnotationIdempotent(nil) {
		t.Error("nil idempotent should be false")
	}
	if !AnnotationOpenWorld(nil) {
		t.Error("nil openWorld should be true")
	}

	// Empty annotations (all nil hints) -> same defaults
	a := &ToolAnnotations{}
	if AnnotationReadOnly(a) {
		t.Error("empty readOnly should be false")
	}
	if !AnnotationDestructive(a) {
		t.Error("empty destructive should be true")
	}
}

func TestAnnotationExplicitValues(t *testing.T) {
	tr := true
	fa := false
	a := &ToolAnnotations{
		ReadOnlyHint:    &tr,
		DestructiveHint: &fa,
		IdempotentHint:  &tr,
		OpenWorldHint:   &fa,
	}
	if !AnnotationReadOnly(a) {
		t.Error("expected readOnly true")
	}
	if AnnotationDestructive(a) {
		t.Error("expected destructive false")
	}
	if !AnnotationIdempotent(a) {
		t.Error("expected idempotent true")
	}
	if AnnotationOpenWorld(a) {
		t.Error("expected openWorld false")
	}
}

func TestDiscoverToolsFilterTaskRequired(t *testing.T) {
	server := &mockToolCaller{name: "test"}
	listFunc := func(_ context.Context, cursor string) (*ToolsListPage, error) {
		if cursor != "" {
			t.Fatalf("unexpected cursor %q", cursor)
		}
		return &ToolsListPage{
			Tools: []ToolInfo{
				{Name: "normal", Description: "ok"},
				{Name: "task-only", Description: "needs tasks", Execution: ToolExecution{TaskSupport: "required"}},
				{Name: "optional-task", Description: "optional", Execution: ToolExecution{TaskSupport: "optional"}},
			},
		}, nil
	}

	result, err := DiscoverTools(context.Background(), server, listFunc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result))
	}
	if result[0].Name() != "mcp__test__normal" {
		t.Errorf("tool[0] = %q", result[0].Name())
	}
	if result[1].Name() != "mcp__test__optional-task" {
		t.Errorf("tool[1] = %q", result[1].Name())
	}
}

func TestDiscoverToolsPagination(t *testing.T) {
	server := &mockToolCaller{name: "paginated"}
	calls := 0
	listFunc := func(_ context.Context, cursor string) (*ToolsListPage, error) {
		calls++
		switch cursor {
		case "":
			return &ToolsListPage{
				Tools:      []ToolInfo{{Name: "tool1"}},
				NextCursor: "page2",
			}, nil
		case "page2":
			return &ToolsListPage{
				Tools: []ToolInfo{{Name: "tool2"}},
			}, nil
		default:
			t.Fatalf("unexpected cursor %q", cursor)
			return nil, nil
		}
	}

	result, err := DiscoverTools(context.Background(), server, listFunc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 list calls, got %d", calls)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(result))
	}
}

func TestDiscoverToolsErrorPropagation(t *testing.T) {
	server := &mockToolCaller{name: "fail"}
	listFunc := func(_ context.Context, _ string) (*ToolsListPage, error) {
		return nil, errors.New("server gone")
	}

	_, err := DiscoverTools(context.Background(), server, listFunc)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, err) {
		t.Errorf("unexpected error: %v", err)
	}
}
