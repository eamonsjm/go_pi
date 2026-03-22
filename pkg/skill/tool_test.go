package skill

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSkillTool_Name(t *testing.T) {
	st := NewSkillTool(NewRegistry(), "test-model")
	if st.Name() != "Skill" {
		t.Errorf("Name() = %q, want %q", st.Name(), "Skill")
	}
}

func TestSkillTool_Description(t *testing.T) {
	st := NewSkillTool(NewRegistry(), "test-model")
	desc := st.Description()
	if !strings.Contains(desc, "Execute a skill") {
		t.Errorf("Description() should mention executing skills")
	}
}

func TestSkillTool_Schema(t *testing.T) {
	st := NewSkillTool(NewRegistry(), "test-model")
	schema := st.Schema()
	m, ok := schema.(map[string]any)
	if !ok {
		t.Fatalf("Schema() should return map[string]any")
	}
	if m["type"] != "object" {
		t.Errorf("Schema type = %v, want object", m["type"])
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatalf("Schema properties should be a map")
	}
	if _, ok := props["skill"]; !ok {
		t.Error("Schema missing 'skill' property")
	}
	if _, ok := props["args"]; !ok {
		t.Error("Schema missing 'args' property")
	}
}

func TestSkillTool_Execute_MissingSkill(t *testing.T) {
	st := NewSkillTool(NewRegistry(), "test-model")
	_, err := st.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing skill parameter")
	}
	if !errors.Is(err, ErrMissingSkillParam) {
		t.Errorf("error should be ErrMissingSkillParam, got: %v", err)
	}
}

func TestSkillTool_Execute_UnknownSkill(t *testing.T) {
	st := NewSkillTool(NewRegistry(), "test-model")
	_, err := st.Execute(context.Background(), map[string]any{"skill": "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
	if !errors.Is(err, ErrUnknownSkill) {
		t.Errorf("error should be ErrUnknownSkill, got: %v", err)
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Errorf("error should contain skill name, got: %v", err)
	}
}

func TestSkillTool_Execute_Success(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&Skill{
		Name:        "greet",
		Description: "Greet someone",
		Body:        "Hello {{name}}, you are using {{model}}!",
		Arguments: []Argument{
			{Name: "name", Description: "Who to greet", Required: true},
		},
	})

	st := NewSkillTool(reg, "claude-3")
	result, err := st.Execute(context.Background(), map[string]any{
		"skill": "greet",
		"args":  "World",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Hello World") {
		t.Errorf("result = %q, should contain 'Hello World'", result)
	}
	if !strings.Contains(result, "claude-3") {
		t.Errorf("result = %q, should contain model name 'claude-3'", result)
	}
}

func TestSkillTool_Execute_NoArgs(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&Skill{
		Name:        "simple",
		Description: "Simple skill",
		Body:        "Do the thing.",
	})

	st := NewSkillTool(reg, "test-model")
	result, err := st.Execute(context.Background(), map[string]any{
		"skill": "simple",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Do the thing." {
		t.Errorf("result = %q, want %q", result, "Do the thing.")
	}
}

func TestSkillTool_Execute_MissingRequiredArg(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&Skill{
		Name:        "needs-arg",
		Description: "Needs an arg",
		Body:        "File: {{file}}",
		Arguments: []Argument{
			{Name: "file", Description: "File to process", Required: true},
		},
	})

	st := NewSkillTool(reg, "test-model")
	_, err := st.Execute(context.Background(), map[string]any{
		"skill": "needs-arg",
	})
	if err == nil {
		t.Fatal("expected error for missing required argument")
	}
	if !strings.Contains(err.Error(), "missing required argument") {
		t.Errorf("error = %v, want 'missing required argument'", err)
	}
}

func TestSkillTool_Execute_WithConditionals(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&Skill{
		Name:        "review",
		Description: "Review code",
		Body:        "Review code.{{#if file}} Focus on: {{file}}{{/if}}",
		Arguments: []Argument{
			{Name: "file", Description: "File to review"},
		},
	})

	st := NewSkillTool(reg, "test-model")

	// Without the optional arg
	result, err := st.Execute(context.Background(), map[string]any{
		"skill": "review",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Review code." {
		t.Errorf("result = %q, want %q", result, "Review code.")
	}

	// With the optional arg
	result, err = st.Execute(context.Background(), map[string]any{
		"skill": "review",
		"args":  "main.go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Focus on: main.go") {
		t.Errorf("result = %q, should contain 'Focus on: main.go'", result)
	}
}

func TestSkillTool_SetModel(t *testing.T) {
	st := NewSkillTool(NewRegistry(), "old-model")
	st.SetModel("new-model")
	if st.model != "new-model" {
		t.Errorf("model = %q, want %q", st.model, "new-model")
	}
}

func TestSkillSystemReminder_Empty(t *testing.T) {
	reg := NewRegistry()
	result := SkillSystemReminder(reg)
	if result != "" {
		t.Errorf("expected empty string for empty registry, got %q", result)
	}
}

func TestSkillSystemReminder_NonInvocable(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&Skill{
		Name:          "internal",
		Description:   "Internal skill",
		UserInvocable: false,
	})
	result := SkillSystemReminder(reg)
	if result != "" {
		t.Errorf("expected empty string for non-invocable skills, got %q", result)
	}
}

func TestSkillSystemReminder_WithSkills(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&Skill{
		Name:          "commit",
		Description:   "Commit changes",
		UserInvocable: true,
	})
	reg.Register(&Skill{
		Name:          "review",
		Description:   "Review code",
		UserInvocable: true,
		Trigger:       "when user asks to review",
	})
	reg.Register(&Skill{
		Name:          "internal",
		Description:   "Internal only",
		UserInvocable: false,
	})

	result := SkillSystemReminder(reg)
	if !strings.Contains(result, "- commit: Commit changes") {
		t.Errorf("result should contain commit skill")
	}
	if !strings.Contains(result, "- review: Review code") {
		t.Errorf("result should contain review skill")
	}
	if !strings.Contains(result, "TRIGGER when: when user asks to review") {
		t.Errorf("result should contain trigger info for review skill")
	}
	if strings.Contains(result, "internal") {
		t.Errorf("result should not contain non-invocable internal skill")
	}
}
