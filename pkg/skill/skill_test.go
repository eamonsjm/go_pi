package skill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

const validSkillMD = `---
name: commit
description: Create a git commit with a well-formed message
user_invocable: true
arguments:
  - name: message
    description: Optional commit message override
    required: false
  - name: files
    description: Files to stage
    required: true
trigger: When the user asks to commit
---

Analyze all staged and unstaged changes. Draft a concise commit message.
`

const minimalSkillMD = `---
name: review
description: Review code changes
---

Review the diff for bugs and style issues.
`

const noNameSkillMD = `---
description: Missing name field
---

Body text.
`

const noFrontmatterMD = `Just some markdown without frontmatter.`

const noClosingDelimMD = `---
name: broken
description: Missing closing delimiter

Body text.
`

func TestParseSkill_Full(t *testing.T) {
	s, err := ParseSkill([]byte(validSkillMD))
	if err != nil {
		t.Fatalf("ParseSkill returned error: %v", err)
	}

	if s.Name != "commit" {
		t.Errorf("Name: got %q, want %q", s.Name, "commit")
	}
	if s.Description != "Create a git commit with a well-formed message" {
		t.Errorf("Description: got %q", s.Description)
	}
	if !s.UserInvocable {
		t.Error("UserInvocable: got false, want true")
	}
	if s.Trigger != "When the user asks to commit" {
		t.Errorf("Trigger: got %q", s.Trigger)
	}
	if len(s.Arguments) != 2 {
		t.Fatalf("Arguments: got %d, want 2", len(s.Arguments))
	}
	if s.Arguments[0].Name != "message" {
		t.Errorf("Arguments[0].Name: got %q", s.Arguments[0].Name)
	}
	if s.Arguments[0].Required {
		t.Error("Arguments[0].Required: got true, want false")
	}
	if s.Arguments[1].Name != "files" {
		t.Errorf("Arguments[1].Name: got %q", s.Arguments[1].Name)
	}
	if !s.Arguments[1].Required {
		t.Error("Arguments[1].Required: got false, want true")
	}
	if s.Body != "Analyze all staged and unstaged changes. Draft a concise commit message." {
		t.Errorf("Body: got %q", s.Body)
	}
}

func TestParseSkill_Minimal(t *testing.T) {
	s, err := ParseSkill([]byte(minimalSkillMD))
	if err != nil {
		t.Fatalf("ParseSkill returned error: %v", err)
	}

	if s.Name != "review" {
		t.Errorf("Name: got %q, want %q", s.Name, "review")
	}
	if s.UserInvocable {
		t.Error("UserInvocable: got true, want false (default)")
	}
	if len(s.Arguments) != 0 {
		t.Errorf("Arguments: got %d, want 0", len(s.Arguments))
	}
	if s.Body != "Review the diff for bugs and style issues." {
		t.Errorf("Body: got %q", s.Body)
	}
}

func TestParseSkill_MissingName(t *testing.T) {
	_, err := ParseSkill([]byte(noNameSkillMD))
	if err == nil {
		t.Fatal("expected error for missing name, got nil")
	}
	if !errors.Is(err, ErrMissingName) {
		t.Errorf("error should wrap ErrMissingName, got: %v", err)
	}
}

func TestParseSkill_NoFrontmatter(t *testing.T) {
	_, err := ParseSkill([]byte(noFrontmatterMD))
	if err == nil {
		t.Fatal("expected error for no frontmatter, got nil")
	}
	if !errors.Is(err, ErrInvalidFrontmatter) {
		t.Errorf("error should wrap ErrInvalidFrontmatter, got: %v", err)
	}
}

func TestParseSkill_NoClosingDelimiter(t *testing.T) {
	_, err := ParseSkill([]byte(noClosingDelimMD))
	if err == nil {
		t.Fatal("expected error for no closing delimiter, got nil")
	}
	if !errors.Is(err, ErrInvalidFrontmatter) {
		t.Errorf("error should wrap ErrInvalidFrontmatter, got: %v", err)
	}
}

func TestParseFrontmatterOnly(t *testing.T) {
	s, err := ParseFrontmatterOnly([]byte(validSkillMD))
	if err != nil {
		t.Fatalf("ParseFrontmatterOnly returned error: %v", err)
	}

	if s.Name != "commit" {
		t.Errorf("Name: got %q, want %q", s.Name, "commit")
	}
	if s.Body != "" {
		t.Errorf("Body should be empty for frontmatter-only parse, got %q", s.Body)
	}
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	s := &Skill{Name: "commit", Description: "commit skill"}
	r.Register(s)

	got, ok := r.Get("commit")
	if !ok {
		t.Fatal("expected skill 'commit' to be found")
	}
	if got.Name != "commit" {
		t.Errorf("Name: got %q, want %q", got.Name, "commit")
	}
}

func TestRegistry_GetMissing(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Fatal("expected skill to not be found")
	}
}

func TestRegistry_OverrideSemantics(t *testing.T) {
	r := NewRegistry()
	r.Register(&Skill{Name: "commit", Description: "built-in", Source: "built-in"})
	r.Register(&Skill{Name: "commit", Description: "project", Source: "project"})

	got, ok := r.Get("commit")
	if !ok {
		t.Fatal("expected skill to be found")
	}
	if got.Source != "project" {
		t.Errorf("Source: got %q, want %q (last-write-wins)", got.Source, "project")
	}
}

func TestRegistry_Names(t *testing.T) {
	r := NewRegistry()
	r.Register(&Skill{Name: "commit"})
	r.Register(&Skill{Name: "review"})
	r.Register(&Skill{Name: "explain"})

	names := r.Names()
	expected := []string{"commit", "explain", "review"}
	if len(names) != len(expected) {
		t.Fatalf("Names: got %d, want %d", len(names), len(expected))
	}
	for i, name := range expected {
		if names[i] != name {
			t.Errorf("Names[%d]: got %q, want %q", i, names[i], name)
		}
	}
}

func TestRegistry_UserInvocable(t *testing.T) {
	r := NewRegistry()
	r.Register(&Skill{Name: "commit", UserInvocable: true})
	r.Register(&Skill{Name: "internal-tool", UserInvocable: false})
	r.Register(&Skill{Name: "review", UserInvocable: true})

	ui := r.UserInvocable()
	if len(ui) != 2 {
		t.Fatalf("UserInvocable: got %d, want 2", len(ui))
	}
	if ui[0].Name != "commit" {
		t.Errorf("UserInvocable[0].Name: got %q, want %q", ui[0].Name, "commit")
	}
	if ui[1].Name != "review" {
		t.Errorf("UserInvocable[1].Name: got %q, want %q", ui[1].Name, "review")
	}
}

func TestRegistry_All(t *testing.T) {
	r := NewRegistry()
	r.Register(&Skill{Name: "b-skill"})
	r.Register(&Skill{Name: "a-skill"})
	r.Register(&Skill{Name: "c-skill"})

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("All: got %d, want 3", len(all))
	}
	expected := []string{"a-skill", "b-skill", "c-skill"}
	for i, name := range expected {
		if all[i].Name != name {
			t.Errorf("All[%d].Name: got %q, want %q", i, all[i].Name, name)
		}
	}
}

func TestRegistry_Len(t *testing.T) {
	r := NewRegistry()
	if r.Len() != 0 {
		t.Fatalf("Len: got %d, want 0", r.Len())
	}
	r.Register(&Skill{Name: "a"})
	r.Register(&Skill{Name: "b"})
	if r.Len() != 2 {
		t.Fatalf("Len: got %d, want 2", r.Len())
	}
}

func TestLoadBody_FromParsedSkill(t *testing.T) {
	s, err := ParseSkill([]byte(validSkillMD))
	if err != nil {
		t.Fatalf("ParseSkill returned error: %v", err)
	}

	body, err := s.LoadBody(context.Background())
	if err != nil {
		t.Fatalf("LoadBody returned error: %v", err)
	}
	if body != "Analyze all staged and unstaged changes. Draft a concise commit message." {
		t.Errorf("LoadBody: got %q", body)
	}
}

func TestLoadBody_FromFilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	if err := os.WriteFile(path, []byte(validSkillMD), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Simulate a frontmatter-only parse followed by lazy load.
	s, err := ParseFrontmatterOnly([]byte(validSkillMD))
	if err != nil {
		t.Fatalf("ParseFrontmatterOnly: %v", err)
	}
	s.Path = path

	body, err := s.LoadBody(context.Background())
	if err != nil {
		t.Fatalf("LoadBody returned error: %v", err)
	}
	if body != "Analyze all staged and unstaged changes. Draft a concise commit message." {
		t.Errorf("LoadBody: got %q", body)
	}

	// Second call should return cached result.
	body2, err := s.LoadBody(context.Background())
	if err != nil {
		t.Fatalf("LoadBody (cached) returned error: %v", err)
	}
	if body != body2 {
		t.Error("LoadBody returned different result on second call")
	}
}

func TestLoadBody_FromEmbedFS(t *testing.T) {
	embFS := fstest.MapFS{
		"skills/commit.md": &fstest.MapFile{
			Data: []byte(validSkillMD),
		},
	}

	s, err := ParseFrontmatterOnly([]byte(validSkillMD))
	if err != nil {
		t.Fatalf("ParseFrontmatterOnly: %v", err)
	}
	s.SetEmbedSource(embFS, "skills/commit.md")

	body, err := s.LoadBody(context.Background())
	if err != nil {
		t.Fatalf("LoadBody returned error: %v", err)
	}
	if body != "Analyze all staged and unstaged changes. Draft a concise commit message." {
		t.Errorf("LoadBody: got %q", body)
	}
}

func TestLoadBody_NoSource(t *testing.T) {
	s := &Skill{Name: "orphan"}
	_, err := s.LoadBody(context.Background())
	if err == nil {
		t.Fatal("expected error for skill with no source, got nil")
	}
}

func TestSplitFrontmatter_LeadingWhitespace(t *testing.T) {
	input := []byte("\n\n---\nname: test\n---\n\nBody here.\n")
	s, err := ParseSkill(input)
	if err != nil {
		t.Fatalf("ParseSkill returned error: %v", err)
	}
	if s.Name != "test" {
		t.Errorf("Name: got %q, want %q", s.Name, "test")
	}
	if s.Body != "Body here." {
		t.Errorf("Body: got %q", s.Body)
	}
}

func TestSplitFrontmatter_EmptyBody(t *testing.T) {
	input := []byte("---\nname: empty\ndescription: no body\n---\n")
	s, err := ParseSkill(input)
	if err != nil {
		t.Fatalf("ParseSkill returned error: %v", err)
	}
	if s.Body != "" {
		t.Errorf("Body: got %q, want empty string", s.Body)
	}
}
