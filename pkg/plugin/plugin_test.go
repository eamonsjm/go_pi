package plugin

import (
	"testing"

	"github.com/ejm/go_pi/pkg/ai"
)

func TestToAIToolDef(t *testing.T) {
	td := ToolDef{
		Name:        "my_tool",
		Description: "does things",
		InputSchema: map[string]any{"type": "object"},
	}

	got := td.ToAIToolDef()
	want := ai.ToolDef{
		Name:        "my_tool",
		Description: "does things",
		InputSchema: map[string]any{"type": "object"},
	}

	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
	if got.Description != want.Description {
		t.Errorf("Description = %q, want %q", got.Description, want.Description)
	}
	if got.InputSchema == nil {
		t.Error("InputSchema is nil, want non-nil")
	}
}

func TestToAIToolDef_NilSchema(t *testing.T) {
	td := ToolDef{Name: "x", Description: "y"}
	got := td.ToAIToolDef()
	if got.InputSchema != nil {
		t.Errorf("InputSchema = %v, want nil", got.InputSchema)
	}
}
