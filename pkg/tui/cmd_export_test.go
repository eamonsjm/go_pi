package tui

import (
	"context"
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

func TestNewExportCommand_ExplicitFilename(t *testing.T) {
	dir := t.TempDir()
	mgr := session.NewManager(dir)
	mgr.NewSession()
	mgr.SaveMessage(context.Background(), ai.NewTextMessage(ai.RoleUser, "hello"))
	mgr.SaveMessage(context.Background(), ai.NewTextMessage(ai.RoleAssistant, "hi there"))

	// Passing a filename writes to ~/.gi/exports/<filename>.
	cmd := NewExportCommand(mgr)
	teaCmd := cmd.Execute("test-export.html")
	msg := teaCmd()

	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Text)
	}

	// The file should be written inside the exports directory.
	home, _ := os.UserHomeDir()
	outPath := filepath.Join(home, ".gi", "exports", "test-export.html")
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("failed to read exported file: %v", err)
	}
	t.Cleanup(func() { os.Remove(outPath) })

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

func TestNewExportCommand_PathTraversalBlocked(t *testing.T) {
	dir := t.TempDir()
	mgr := session.NewManager(dir)
	mgr.NewSession()
	mgr.SaveMessage(context.Background(), ai.NewTextMessage(ai.RoleUser, "hello"))

	tests := []struct {
		name string
		arg  string
	}{
		{"dot-dot slash", "../../etc/evil.html"},
		{"absolute path", "/tmp/evil.html"},
		{"nested traversal", "foo/../../bar.html"},
	}

	cmd := NewExportCommand(mgr)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			teaCmd := cmd.Execute(tt.arg)
			msg := teaCmd()
			result, ok := msg.(CommandResultMsg)
			if !ok {
				t.Fatalf("expected CommandResultMsg, got %T", msg)
			}
			// filepath.Base strips traversal, so these should succeed
			// but write to the exports dir, not the traversal target.
			if result.IsError {
				return // also acceptable — blocked outright
			}
			// If it succeeded, verify the file was NOT written outside exports dir.
			home, _ := os.UserHomeDir()
			exportDir := filepath.Join(home, ".gi", "exports")
			if !strings.HasPrefix(result.Text, "Exported session to "+exportDir) {
				t.Errorf("file written outside exports dir: %s", result.Text)
			}
		})
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

	// Tool input should be rendered as JSON, not Go map syntax.
	if strings.Contains(html, "map[cmd:ls]") {
		t.Error("tool input should be JSON, not Go map syntax")
	}
	// JSON keys are HTML-escaped in the output.
	if !strings.Contains(html, `&#34;cmd&#34;`) {
		t.Error("tool input should contain JSON key (HTML-escaped)")
	}
}
