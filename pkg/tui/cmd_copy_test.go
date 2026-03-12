package tui

import (
	"testing"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/session"
)

func TestNewCopyCommand_Metadata(t *testing.T) {
	mgr := session.NewManager(t.TempDir())
	cmd := NewCopyCommand(mgr)

	if cmd.Name != "copy" {
		t.Errorf("Name = %q, want %q", cmd.Name, "copy")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestNewCopyCommand_NoMessages(t *testing.T) {
	mgr := session.NewManager(t.TempDir())
	mgr.NewSession()
	cmd := NewCopyCommand(mgr)

	teaCmd := cmd.Execute("")
	msg := teaCmd()
	result, ok := msg.(CommandResultMsg)
	if !ok {
		t.Fatalf("expected CommandResultMsg, got %T", msg)
	}
	if !result.IsError {
		t.Error("expected error when no messages")
	}
}

func TestLastAssistantText(t *testing.T) {
	tests := []struct {
		name string
		msgs []ai.Message
		want string
	}{
		{
			name: "empty",
			msgs: nil,
			want: "",
		},
		{
			name: "only user messages",
			msgs: []ai.Message{
				ai.NewTextMessage(ai.RoleUser, "hello"),
			},
			want: "",
		},
		{
			name: "single assistant message",
			msgs: []ai.Message{
				ai.NewTextMessage(ai.RoleUser, "hello"),
				ai.NewTextMessage(ai.RoleAssistant, "hi there"),
			},
			want: "hi there",
		},
		{
			name: "multiple assistant messages returns last",
			msgs: []ai.Message{
				ai.NewTextMessage(ai.RoleUser, "hello"),
				ai.NewTextMessage(ai.RoleAssistant, "first"),
				ai.NewTextMessage(ai.RoleUser, "more"),
				ai.NewTextMessage(ai.RoleAssistant, "second"),
			},
			want: "second",
		},
		{
			name: "skips assistant with no text",
			msgs: []ai.Message{
				ai.NewTextMessage(ai.RoleAssistant, "text response"),
				{Role: ai.RoleAssistant, Content: []ai.ContentBlock{
					{Type: ai.ContentTypeToolUse, ToolName: "bash"},
				}},
			},
			want: "text response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lastAssistantText(tt.msgs)
			if got != tt.want {
				t.Errorf("lastAssistantText() = %q, want %q", got, tt.want)
			}
		})
	}
}
