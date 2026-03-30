package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSystemPrompt_WalksDirectoryTree(t *testing.T) {
	// Create a temp directory structure:
	// root/CLAUDE.md
	// root/sub/CLAUDE.md (different content)
	// root/sub/AGENTS.md
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("root instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "CLAUDE.md"), []byte("sub instructions"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("agents instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Change to sub directory
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Logf("Warning: failed to restore directory: %v", err)
		}
	}()

	prompt := buildSystemPrompt("test base")

	// Deepest first: sub/CLAUDE.md before root/CLAUDE.md
	subIdx := strings.Index(prompt, "sub instructions")
	rootIdx := strings.Index(prompt, "root instructions")
	agentsIdx := strings.Index(prompt, "agents instructions")

	if subIdx == -1 {
		t.Fatal("sub/CLAUDE.md content not found in prompt")
	}
	if rootIdx == -1 {
		t.Fatal("root/CLAUDE.md content not found in prompt")
	}
	if agentsIdx == -1 {
		t.Fatal("sub/AGENTS.md content not found in prompt")
	}
	if subIdx > rootIdx {
		t.Error("sub/CLAUDE.md should appear before root/CLAUDE.md (deepest first)")
	}
}

func TestBuildSystemPrompt_DeduplicatesByContent(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// Same content in both
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("same content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "CLAUDE.md"), []byte("same content"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Logf("Warning: failed to restore directory: %v", err)
		}
	}()

	prompt := buildSystemPrompt("test base")

	// Should only appear once
	count := strings.Count(prompt, "same content")
	if count != 1 {
		t.Errorf("duplicate content should be deduplicated, found %d occurrences", count)
	}
}

func TestBuildSystemPrompt_AppendSystemMd(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(sub, "APPEND_SYSTEM.md"), []byte("appended content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "APPEND_SYSTEM.md"), []byte("root appended"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Logf("Warning: failed to restore directory: %v", err)
		}
	}()

	prompt := buildSystemPrompt("test base")

	if !strings.Contains(prompt, "appended content") {
		t.Error("APPEND_SYSTEM.md from sub not found")
	}
	if !strings.Contains(prompt, "root appended") {
		t.Error("APPEND_SYSTEM.md from root not found")
	}
}

func TestBuildSystemPrompt_DotClaudeSystemMd(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "SYSTEM.md"), []byte("claude system"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(orig); err != nil {
			t.Logf("Warning: failed to restore directory: %v", err)
		}
	}()

	prompt := buildSystemPrompt("test base")

	if !strings.Contains(prompt, "claude system") {
		t.Error(".claude/SYSTEM.md content not found in prompt")
	}
}
