package ai

import "testing"

func TestNewTextMessage(t *testing.T) {
	msg := NewTextMessage(RoleUser, "hello world")

	if msg.Role != RoleUser {
		t.Errorf("expected role %q, got %q", RoleUser, msg.Role)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	cb := msg.Content[0]
	if cb.Type != ContentTypeText {
		t.Errorf("expected type %q, got %q", ContentTypeText, cb.Type)
	}
	if cb.Text != "hello world" {
		t.Errorf("expected text %q, got %q", "hello world", cb.Text)
	}
}

func TestNewTextMessage_AssistantRole(t *testing.T) {
	msg := NewTextMessage(RoleAssistant, "response")
	if msg.Role != RoleAssistant {
		t.Errorf("expected role %q, got %q", RoleAssistant, msg.Role)
	}
}

func TestNewToolResultMessage(t *testing.T) {
	msg := NewToolResultMessage("tool_123", "result data", false)

	if msg.Role != RoleUser {
		t.Errorf("expected role %q, got %q", RoleUser, msg.Role)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	cb := msg.Content[0]
	if cb.Type != ContentTypeToolResult {
		t.Errorf("expected type %q, got %q", ContentTypeToolResult, cb.Type)
	}
	if cb.ToolResultID != "tool_123" {
		t.Errorf("expected tool_use_id %q, got %q", "tool_123", cb.ToolResultID)
	}
	if cb.Content != "result data" {
		t.Errorf("expected content %q, got %q", "result data", cb.Content)
	}
	if cb.IsError {
		t.Error("expected IsError to be false")
	}
}

func TestNewToolResultMessage_WithError(t *testing.T) {
	msg := NewToolResultMessage("tool_456", "error message", true)

	cb := msg.Content[0]
	if !cb.IsError {
		t.Error("expected IsError to be true")
	}
	if cb.Content != "error message" {
		t.Errorf("expected content %q, got %q", "error message", cb.Content)
	}
}

func TestMessage_GetText(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: "Hello "},
			{Type: ContentTypeToolUse, ToolName: "search"},
			{Type: ContentTypeText, Text: "World"},
		},
	}

	got := msg.GetText()
	want := "Hello World"
	if got != want {
		t.Errorf("GetText() = %q, want %q", got, want)
	}
}

func TestMessage_GetText_Empty(t *testing.T) {
	msg := Message{
		Role:    RoleAssistant,
		Content: []ContentBlock{{Type: ContentTypeToolUse, ToolName: "x"}},
	}
	if got := msg.GetText(); got != "" {
		t.Errorf("GetText() = %q, want empty string", got)
	}
}

func TestMessage_GetToolCalls(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: "Let me search"},
			{Type: ContentTypeToolUse, ToolUseID: "tc1", ToolName: "search", Input: map[string]any{"q": "foo"}},
			{Type: ContentTypeToolUse, ToolUseID: "tc2", ToolName: "read", Input: map[string]any{"path": "/x"}},
			{Type: ContentTypeThinking, Thinking: "hmm"},
		},
	}

	calls := msg.GetToolCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}
	if calls[0].ToolName != "search" {
		t.Errorf("expected first tool name %q, got %q", "search", calls[0].ToolName)
	}
	if calls[1].ToolName != "read" {
		t.Errorf("expected second tool name %q, got %q", "read", calls[1].ToolName)
	}
}

func TestMessage_GetToolCalls_None(t *testing.T) {
	msg := NewTextMessage(RoleAssistant, "no tools here")
	calls := msg.GetToolCalls()
	if calls != nil {
		t.Errorf("expected nil, got %v", calls)
	}
}

func TestMessage_GetThinking(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{Type: ContentTypeThinking, Thinking: "First "},
			{Type: ContentTypeText, Text: "visible text"},
			{Type: ContentTypeThinking, Thinking: "Second"},
		},
	}

	got := msg.GetThinking()
	want := "First Second"
	if got != want {
		t.Errorf("GetThinking() = %q, want %q", got, want)
	}
}

func TestMessage_GetThinking_Empty(t *testing.T) {
	msg := NewTextMessage(RoleAssistant, "no thinking")
	if got := msg.GetThinking(); got != "" {
		t.Errorf("GetThinking() = %q, want empty string", got)
	}
}

func TestNewRichToolResultMessage(t *testing.T) {
	blocks := []ContentBlock{
		{Type: ContentTypeText, Text: "File contents:"},
		{Type: ContentTypeImage, MediaType: "image/png", ImageData: "base64data"},
	}
	msg := NewRichToolResultMessage("tool_789", blocks, false)

	if msg.Role != RoleUser {
		t.Errorf("expected role %q, got %q", RoleUser, msg.Role)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	cb := msg.Content[0]
	if cb.Type != ContentTypeToolResult {
		t.Errorf("expected type %q, got %q", ContentTypeToolResult, cb.Type)
	}
	if cb.ToolResultID != "tool_789" {
		t.Errorf("expected tool_use_id %q, got %q", "tool_789", cb.ToolResultID)
	}
	if cb.IsError {
		t.Error("expected IsError to be false")
	}
	if len(cb.ContentBlocks) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(cb.ContentBlocks))
	}
	if cb.ContentBlocks[0].Type != ContentTypeText || cb.ContentBlocks[0].Text != "File contents:" {
		t.Errorf("unexpected first content block: %+v", cb.ContentBlocks[0])
	}
	if cb.ContentBlocks[1].Type != ContentTypeImage || cb.ContentBlocks[1].MediaType != "image/png" {
		t.Errorf("unexpected second content block: %+v", cb.ContentBlocks[1])
	}
}

func TestEnsureContent_NilBecomesEmpty(t *testing.T) {
	msg := Message{Role: RoleAssistant}
	if msg.Content != nil {
		t.Fatal("precondition: Content should be nil")
	}
	msg.EnsureContent()
	if msg.Content == nil {
		t.Fatal("EnsureContent should make Content non-nil")
	}
	if len(msg.Content) != 0 {
		t.Errorf("expected empty slice, got %d elements", len(msg.Content))
	}
}

func TestEnsureContent_NonNilUnchanged(t *testing.T) {
	msg := NewTextMessage(RoleUser, "hello")
	msg.EnsureContent()
	if len(msg.Content) != 1 {
		t.Errorf("expected 1 content block preserved, got %d", len(msg.Content))
	}
	if msg.Content[0].Text != "hello" {
		t.Errorf("expected text preserved, got %q", msg.Content[0].Text)
	}
}

func TestMessage_GetText_NilContent(t *testing.T) {
	msg := Message{Role: RoleAssistant} // Content is nil
	if got := msg.GetText(); got != "" {
		t.Errorf("GetText() on nil Content = %q, want empty string", got)
	}
}

func TestMessage_GetToolCalls_NilContent(t *testing.T) {
	msg := Message{Role: RoleAssistant} // Content is nil
	calls := msg.GetToolCalls()
	if calls != nil {
		t.Errorf("GetToolCalls() on nil Content = %v, want nil", calls)
	}
}

func TestMessage_GetThinking_NilContent(t *testing.T) {
	msg := Message{Role: RoleAssistant} // Content is nil
	if got := msg.GetThinking(); got != "" {
		t.Errorf("GetThinking() on nil Content = %q, want empty string", got)
	}
}

func TestNewRichToolResultMessage_WithError(t *testing.T) {
	blocks := []ContentBlock{
		{Type: ContentTypeText, Text: "error occurred"},
	}
	msg := NewRichToolResultMessage("tool_err", blocks, true)

	cb := msg.Content[0]
	if !cb.IsError {
		t.Error("expected IsError to be true")
	}
	if len(cb.ContentBlocks) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(cb.ContentBlocks))
	}
}
