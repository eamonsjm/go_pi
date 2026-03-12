package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/session"
)

func TestNewExportCommand_Metadata(t *testing.T) {
	mgr := session.NewManager(t.TempDir())
	cmd := NewExportCommand(mgr)

	if cmd.Name != "export" {
		t.Errorf("Name = %q, want %q", cmd.Name, "export")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestNewExportCommand_NoSession(t *testing.T) {
	mgr := session.NewManager(t.TempDir())
	cmd := NewExportCommand(mgr)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if !result.IsError {
		t.Error("expected error when no session")
	}
}

func TestNewExportCommand_ExplicitPath(t *testing.T) {
	dir := t.TempDir()
	mgr := session.NewManager(dir)
	mgr.NewSession()
	mgr.SaveMessage(ai.NewTextMessage(ai.RoleUser, "hello"))
	mgr.SaveMessage(ai.NewTextMessage(ai.RoleAssistant, "hi there"))

	outPath := filepath.Join(dir, "test-export.html")
	cmd := NewExportCommand(mgr)
	teaCmd := cmd.Execute(outPath)
	msg := teaCmd()

	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Text)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed to read exported file: %v", err)
	}
	html := string(data)

	if !strings.Contains(html, "<!DOCTYPE html>") {
		t.Error("export should be valid HTML")
	}
	if !strings.Contains(html, "hello") {
		t.Error("export should contain user message")
	}
	if !strings.Contains(html, "hi there") {
		t.Error("export should contain assistant message")
	}
}

func TestRenderCodeBlocks(t *testing.T) {
	input := "before\n```go\nfmt.Println(&quot;hi&quot;)\n```\nafter"
	result := renderCodeBlocks(input)

	if !strings.Contains(result, `class="language-go"`) {
		t.Error("expected language class in code block")
	}
	if !strings.Contains(result, "</code></pre>") {
		t.Error("expected closing code/pre tags")
	}
}

func TestRenderSessionHTML_ToolUseAndThinking(t *testing.T) {
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "do something"),
		{Role: ai.RoleAssistant, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeThinking, Thinking: "let me think..."},
			{Type: ai.ContentTypeText, Text: "done"},
			{Type: ai.ContentTypeToolUse, ToolName: "bash", ToolUseID: "t1", Input: map[string]any{"cmd": "ls"}},
		}},
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, ToolResultID: "t1", Content: "file.go"},
		}},
	}

	html := renderSessionHTML("test-id", msgs)

	if !strings.Contains(html, "thinking") {
		t.Error("HTML should contain thinking section")
	}
	if !strings.Contains(html, "bash") {
		t.Error("HTML should contain tool name")
	}
	if !strings.Contains(html, "file.go") {
		t.Error("HTML should contain tool result")
	}
}
