package tui

import (
	"strings"
	"testing"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/session"
)

func TestNewShareCommand_Metadata(t *testing.T) {
	mgr := session.NewManager(t.TempDir())
	cmd := NewShareCommand(mgr)

	if cmd.Name != "share" {
		t.Errorf("Name = %q, want %q", cmd.Name, "share")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestNewShareCommand_NoSession(t *testing.T) {
	mgr := session.NewManager(t.TempDir())
	cmd := NewShareCommand(mgr)

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

func TestRenderSessionMarkdown(t *testing.T) {
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "hello world"),
		ai.NewTextMessage(ai.RoleAssistant, "hi there"),
		{Role: ai.RoleAssistant, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolName: "bash", Input: "ls"},
		}},
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, ToolResultID: "t1", Content: "file.go"},
		}},
		{Role: ai.RoleAssistant, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeThinking, Thinking: "pondering..."},
		}},
	}

	md := renderSessionMarkdown("abc123def456", msgs)

	if !strings.Contains(md, "# Session abc123def456") {
		t.Error("markdown should contain session header")
	}
	if !strings.Contains(md, "hello world") {
		t.Error("markdown should contain user message")
	}
	if !strings.Contains(md, "hi there") {
		t.Error("markdown should contain assistant message")
	}
	if !strings.Contains(md, "bash") {
		t.Error("markdown should contain tool name")
	}
	if !strings.Contains(md, "file.go") {
		t.Error("markdown should contain tool result")
	}
	if !strings.Contains(md, "pondering...") {
		t.Error("markdown should contain thinking")
	}
	if !strings.Contains(md, "Exported from go_pi") {
		t.Error("markdown should contain footer")
	}
}

func TestRenderSessionMarkdown_RedactedSecrets(t *testing.T) {
	msgs := []ai.Message{
		{Role: ai.RoleUser, Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolResult, Content: "API_KEY=AKIAIOSFODNN7EXAMPLE"},
		}},
	}

	// Redact first, then render — this is what the share command does.
	sanitized := redactSessionMessages(msgs)
	md := renderSessionMarkdown("test123", sanitized)

	if strings.Contains(md, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("rendered markdown should not contain the raw AWS key")
	}
	if !strings.Contains(md, "[REDACTED]") {
		t.Error("rendered markdown should contain [REDACTED] placeholder")
	}
}
