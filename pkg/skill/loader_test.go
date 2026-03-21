package skill

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestLoadEmbed_DiscoverSkills(t *testing.T) {
	embFS := fstest.MapFS{
		"commit.md": &fstest.MapFile{Data: []byte(validSkillMD)},
		"review.md": &fstest.MapFile{Data: []byte(minimalSkillMD)},
	}

	skills, err := LoadEmbed(embFS)
	if err != nil {
		t.Fatalf("LoadEmbed returned error: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("LoadEmbed: got %d skills, want 2", len(skills))
	}

	// All should have Source "built-in" and no Body (lazy load).
	for _, s := range skills {
		if s.Source != "built-in" {
			t.Errorf("skill %q: Source = %q, want %q", s.Name, s.Source, "built-in")
		}
		if s.Body != "" {
			t.Errorf("skill %q: Body should be empty (lazy), got %q", s.Name, s.Body)
		}
	}
}

func TestLoadEmbed_NestedDirs(t *testing.T) {
	embFS := fstest.MapFS{
		"top.md":            &fstest.MapFile{Data: []byte(minimalSkillMD)},
		"sub/nested.md":     &fstest.MapFile{Data: []byte(validSkillMD)},
		"sub/not-a-skill.txt": &fstest.MapFile{Data: []byte("ignore me")},
	}

	skills, err := LoadEmbed(embFS)
	if err != nil {
		t.Fatalf("LoadEmbed returned error: %v", err)
	}
	// Should find 2 .md files (top.md and sub/nested.md), skip .txt.
	if len(skills) != 2 {
		t.Fatalf("LoadEmbed: got %d skills, want 2", len(skills))
	}
}

func TestLoadEmbed_LazyBody(t *testing.T) {
	embFS := fstest.MapFS{
		"commit.md": &fstest.MapFile{Data: []byte(validSkillMD)},
	}

	skills, err := LoadEmbed(embFS)
	if err != nil {
		t.Fatalf("LoadEmbed returned error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(skills))
	}

	// Body should be empty before LoadBody.
	if skills[0].Body != "" {
		t.Fatalf("Body should be empty before LoadBody, got %q", skills[0].Body)
	}

	// LoadBody should work via embed FS.
	body, err := skills[0].LoadBody()
	if err != nil {
		t.Fatalf("LoadBody returned error: %v", err)
	}
	if body != "Analyze all staged and unstaged changes. Draft a concise commit message." {
		t.Errorf("LoadBody: got %q", body)
	}
}

func TestLoadEmbed_Empty(t *testing.T) {
	embFS := fstest.MapFS{}

	skills, err := LoadEmbed(embFS)
	if err != nil {
		t.Fatalf("LoadEmbed returned error: %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("LoadEmbed: got %d skills, want 0", len(skills))
	}
}

func TestLoadEmbed_InvalidSkill(t *testing.T) {
	embFS := fstest.MapFS{
		"bad.md": &fstest.MapFile{Data: []byte(noFrontmatterMD)},
	}

	_, err := LoadEmbed(embFS)
	if err == nil {
		t.Fatal("expected error for invalid skill, got nil")
	}
}

func TestLoadDir_DiscoverSkills(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "commit.md"), validSkillMD)
	writeFile(t, filepath.Join(dir, "review.md"), minimalSkillMD)
	writeFile(t, filepath.Join(dir, "readme.txt"), "not a skill")

	skills, err := LoadDir(dir, "user")
	if err != nil {
		t.Fatalf("LoadDir returned error: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("LoadDir: got %d skills, want 2", len(skills))
	}

	for _, s := range skills {
		if s.Source != "user" {
			t.Errorf("skill %q: Source = %q, want %q", s.Name, s.Source, "user")
		}
		if s.Path == "" {
			t.Errorf("skill %q: Path should be set", s.Name)
		}
		if s.Body != "" {
			t.Errorf("skill %q: Body should be empty (lazy), got %q", s.Name, s.Body)
		}
	}
}

func TestLoadDir_LazyBody(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "commit.md"), validSkillMD)

	skills, err := LoadDir(dir, "project")
	if err != nil {
		t.Fatalf("LoadDir returned error: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("got %d skills, want 1", len(skills))
	}

	body, err := skills[0].LoadBody()
	if err != nil {
		t.Fatalf("LoadBody returned error: %v", err)
	}
	if body != "Analyze all staged and unstaged changes. Draft a concise commit message." {
		t.Errorf("LoadBody: got %q", body)
	}
}

func TestLoadDir_MissingDir(t *testing.T) {
	skills, err := LoadDir("/nonexistent/path/skills", "user")
	if err != nil {
		t.Fatalf("LoadDir should return nil error for missing dir, got: %v", err)
	}
	if skills != nil {
		t.Fatalf("LoadDir: got %v, want nil", skills)
	}
}

func TestLoadDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	skills, err := LoadDir(dir, "user")
	if err != nil {
		t.Fatalf("LoadDir returned error: %v", err)
	}
	if len(skills) != 0 {
		t.Fatalf("LoadDir: got %d skills, want 0", len(skills))
	}
}

func TestLoadDir_SkipsSubdirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "commit.md"), validSkillMD)
	subdir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(subdir, "nested.md"), minimalSkillMD)

	skills, err := LoadDir(dir, "user")
	if err != nil {
		t.Fatalf("LoadDir returned error: %v", err)
	}
	// Should only find commit.md, not subdir/nested.md (non-recursive).
	if len(skills) != 1 {
		t.Fatalf("LoadDir: got %d skills, want 1", len(skills))
	}
	if skills[0].Name != "commit" {
		t.Errorf("Name: got %q, want %q", skills[0].Name, "commit")
	}
}

func TestLoadDir_InvalidSkill(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "bad.md"), noFrontmatterMD)

	_, err := LoadDir(dir, "user")
	if err == nil {
		t.Fatal("expected error for invalid skill, got nil")
	}
}

func TestOverrideSemantics_LoadOrder(t *testing.T) {
	// Simulate the three-tier override: built-in < user < project.
	embFS := fstest.MapFS{
		"commit.md": &fstest.MapFile{Data: []byte(`---
name: commit
description: built-in commit
---

Built-in body.
`)},
	}

	userDir := t.TempDir()
	writeFile(t, filepath.Join(userDir, "commit.md"), `---
name: commit
description: user commit
---

User body.
`)

	projectDir := t.TempDir()
	writeFile(t, filepath.Join(projectDir, "commit.md"), `---
name: commit
description: project commit
---

Project body.
`)

	reg := NewRegistry()

	// Load in override order: built-in first, then user, then project.
	builtins, err := LoadEmbed(embFS)
	if err != nil {
		t.Fatalf("LoadEmbed: %v", err)
	}
	for _, s := range builtins {
		reg.Register(s)
	}

	userSkills, err := LoadDir(userDir, "user")
	if err != nil {
		t.Fatalf("LoadDir user: %v", err)
	}
	for _, s := range userSkills {
		reg.Register(s)
	}

	projectSkills, err := LoadDir(projectDir, "project")
	if err != nil {
		t.Fatalf("LoadDir project: %v", err)
	}
	for _, s := range projectSkills {
		reg.Register(s)
	}

	// Project should win.
	got, ok := reg.Get("commit")
	if !ok {
		t.Fatal("expected skill 'commit' to be found")
	}
	if got.Source != "project" {
		t.Errorf("Source: got %q, want %q", got.Source, "project")
	}
	if got.Description != "project commit" {
		t.Errorf("Description: got %q, want %q", got.Description, "project commit")
	}
}

func TestOverrideSemantics_UserOverridesBuiltIn(t *testing.T) {
	embFS := fstest.MapFS{
		"review.md": &fstest.MapFile{Data: []byte(`---
name: review
description: built-in review
---

Built-in body.
`)},
	}

	userDir := t.TempDir()
	writeFile(t, filepath.Join(userDir, "review.md"), `---
name: review
description: user review
---

User body.
`)

	reg := NewRegistry()

	builtins, err := LoadEmbed(embFS)
	if err != nil {
		t.Fatalf("LoadEmbed: %v", err)
	}
	for _, s := range builtins {
		reg.Register(s)
	}

	userSkills, err := LoadDir(userDir, "user")
	if err != nil {
		t.Fatalf("LoadDir user: %v", err)
	}
	for _, s := range userSkills {
		reg.Register(s)
	}

	got, ok := reg.Get("review")
	if !ok {
		t.Fatal("expected skill 'review' to be found")
	}
	if got.Source != "user" {
		t.Errorf("Source: got %q, want %q", got.Source, "user")
	}
}

func TestOverrideSemantics_NonOverlapping(t *testing.T) {
	embFS := fstest.MapFS{
		"commit.md": &fstest.MapFile{Data: []byte(validSkillMD)},
	}

	userDir := t.TempDir()
	writeFile(t, filepath.Join(userDir, "review.md"), minimalSkillMD)

	reg := NewRegistry()

	builtins, err := LoadEmbed(embFS)
	if err != nil {
		t.Fatalf("LoadEmbed: %v", err)
	}
	for _, s := range builtins {
		reg.Register(s)
	}

	userSkills, err := LoadDir(userDir, "user")
	if err != nil {
		t.Fatalf("LoadDir user: %v", err)
	}
	for _, s := range userSkills {
		reg.Register(s)
	}

	// Both should be present.
	if reg.Len() != 2 {
		t.Fatalf("Len: got %d, want 2", reg.Len())
	}

	commit, ok := reg.Get("commit")
	if !ok {
		t.Fatal("expected 'commit' to be found")
	}
	if commit.Source != "built-in" {
		t.Errorf("commit Source: got %q, want %q", commit.Source, "built-in")
	}

	review, ok := reg.Get("review")
	if !ok {
		t.Fatal("expected 'review' to be found")
	}
	if review.Source != "user" {
		t.Errorf("review Source: got %q, want %q", review.Source, "user")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}
