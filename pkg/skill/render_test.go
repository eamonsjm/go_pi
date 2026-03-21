package skill

import (
	"testing"
)

func TestRenderTemplate_SimpleSubstitution(t *testing.T) {
	body := "Hello {{name}}, welcome to {{project}}."
	vars := map[string]string{"name": "Alice", "project": "GoPI"}
	got := RenderTemplate(body, vars)
	want := "Hello Alice, welcome to GoPI."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderTemplate_UndefinedVarsLeftAsIs(t *testing.T) {
	body := "Branch: {{branch}}, Unknown: {{unknown}}"
	vars := map[string]string{"branch": "main"}
	got := RenderTemplate(body, vars)
	want := "Branch: main, Unknown: {{unknown}}"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderTemplate_EmptyVars(t *testing.T) {
	body := "No vars here."
	got := RenderTemplate(body, nil)
	if got != body {
		t.Errorf("got %q, want %q", got, body)
	}
}

func TestRenderTemplate_ConditionalPresent(t *testing.T) {
	body := "Start.{{#if debug}} Debug mode is on.{{/if}} End."
	vars := map[string]string{"debug": "true"}
	got := RenderTemplate(body, vars)
	want := "Start. Debug mode is on. End."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderTemplate_ConditionalAbsent(t *testing.T) {
	body := "Start.{{#if debug}} Debug mode is on.{{/if}} End."
	vars := map[string]string{}
	got := RenderTemplate(body, vars)
	want := "Start. End."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderTemplate_ConditionalEmptyValue(t *testing.T) {
	body := "A{{#if x}}B{{/if}}C"
	vars := map[string]string{"x": ""}
	got := RenderTemplate(body, vars)
	want := "AC"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderTemplate_ConditionalWithVarSubstitution(t *testing.T) {
	body := "{{#if args}}Args: {{args}}{{/if}}"
	vars := map[string]string{"args": "-m 'fix bug'"}
	got := RenderTemplate(body, vars)
	want := "Args: -m 'fix bug'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderTemplate_MultipleConditionals(t *testing.T) {
	body := "{{#if a}}A{{/if}}-{{#if b}}B{{/if}}-{{#if c}}C{{/if}}"
	vars := map[string]string{"a": "1", "c": "1"}
	got := RenderTemplate(body, vars)
	want := "A--C"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderTemplate_MultilineConditional(t *testing.T) {
	body := "Before\n{{#if verbose}}\nExtra detail line 1.\nExtra detail line 2.\n{{/if}}\nAfter"
	vars := map[string]string{"verbose": "yes"}
	got := RenderTemplate(body, vars)
	want := "Before\n\nExtra detail line 1.\nExtra detail line 2.\n\nAfter"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderTemplate_NoVarsNoConditionals(t *testing.T) {
	body := "Plain text with no templates."
	got := RenderTemplate(body, map[string]string{})
	if got != body {
		t.Errorf("got %q, want %q", got, body)
	}
}

func TestContextVars(t *testing.T) {
	vars := ContextVars("claude-sonnet-4-6")
	if vars["model"] != "claude-sonnet-4-6" {
		t.Errorf("model: got %q, want %q", vars["model"], "claude-sonnet-4-6")
	}
	if vars["cwd"] == "" {
		t.Error("cwd should not be empty")
	}
	// branch may be empty in non-git environments; just check key exists
	// after ContextVars call — it's best-effort.
}

func TestContextVars_EmptyModel(t *testing.T) {
	vars := ContextVars("")
	if _, ok := vars["model"]; ok {
		t.Error("model key should not be set when model is empty")
	}
}

// --- ParseSkillArgs tests ---

func TestParseSkillArgs_Positional(t *testing.T) {
	defs := []Argument{
		{Name: "message", Required: false},
		{Name: "files", Required: true},
	}
	got, err := ParseSkillArgs(defs, "fix-typo src/main.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["message"] != "fix-typo" {
		t.Errorf("message: got %q, want %q", got["message"], "fix-typo")
	}
	if got["files"] != "src/main.go" {
		t.Errorf("files: got %q, want %q", got["files"], "src/main.go")
	}
}

func TestParseSkillArgs_NamedFlags(t *testing.T) {
	defs := []Argument{
		{Name: "message", Required: false},
		{Name: "files", Required: true},
	}
	got, err := ParseSkillArgs(defs, "-files src/main.go -message 'fix typo'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["message"] != "fix typo" {
		t.Errorf("message: got %q, want %q", got["message"], "fix typo")
	}
	if got["files"] != "src/main.go" {
		t.Errorf("files: got %q, want %q", got["files"], "src/main.go")
	}
}

func TestParseSkillArgs_NamedFlagEqualsForm(t *testing.T) {
	defs := []Argument{
		{Name: "message", Required: false},
	}
	got, err := ParseSkillArgs(defs, "-message=hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["message"] != "hello" {
		t.Errorf("message: got %q, want %q", got["message"], "hello")
	}
}

func TestParseSkillArgs_MixedPositionalAndNamed(t *testing.T) {
	defs := []Argument{
		{Name: "target", Required: true},
		{Name: "verbose", Required: false},
	}
	got, err := ParseSkillArgs(defs, "-verbose yes main.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["verbose"] != "yes" {
		t.Errorf("verbose: got %q, want %q", got["verbose"], "yes")
	}
	if got["target"] != "main.go" {
		t.Errorf("target: got %q, want %q", got["target"], "main.go")
	}
}

func TestParseSkillArgs_MissingRequired(t *testing.T) {
	defs := []Argument{
		{Name: "files", Required: true},
	}
	_, err := ParseSkillArgs(defs, "")
	if err == nil {
		t.Fatal("expected error for missing required arg, got nil")
	}
}

func TestParseSkillArgs_EmptyInputOptionalOnly(t *testing.T) {
	defs := []Argument{
		{Name: "message", Required: false},
	}
	got, err := ParseSkillArgs(defs, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestParseSkillArgs_NoDefs(t *testing.T) {
	got, err := ParseSkillArgs(nil, "some args here")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestParseSkillArgs_QuotedStrings(t *testing.T) {
	defs := []Argument{
		{Name: "message", Required: true},
	}
	got, err := ParseSkillArgs(defs, `"hello world"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["message"] != "hello world" {
		t.Errorf("message: got %q, want %q", got["message"], "hello world")
	}
}

func TestParseSkillArgs_ExtraPositionalsJoinIntoLast(t *testing.T) {
	defs := []Argument{
		{Name: "files", Required: true},
	}
	got, err := ParseSkillArgs(defs, "a.go b.go c.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["files"] != "a.go b.go c.go" {
		t.Errorf("files: got %q, want %q", got["files"], "a.go b.go c.go")
	}
}

func TestParseSkillArgs_UnknownFlagTreatedAsPositional(t *testing.T) {
	defs := []Argument{
		{Name: "target", Required: true},
	}
	got, err := ParseSkillArgs(defs, "-unknown-flag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["target"] != "-unknown-flag" {
		t.Errorf("target: got %q, want %q", got["target"], "-unknown-flag")
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"hello world", []string{"hello", "world"}},
		{`"hello world"`, []string{"hello world"}},
		{`'hello world'`, []string{"hello world"}},
		{`-m "fix bug" file.go`, []string{"-m", "fix bug", "file.go"}},
		{"  spaced  out  ", []string{"spaced", "out"}},
		{"", nil},
		{`a "b c" d`, []string{"a", "b c", "d"}},
	}
	for _, tt := range tests {
		got := tokenize(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("tokenize(%q): got %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range tt.want {
			if got[i] != tt.want[i] {
				t.Errorf("tokenize(%q)[%d]: got %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}
