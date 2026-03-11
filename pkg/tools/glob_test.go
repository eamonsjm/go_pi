package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupGlobDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Create directory structure:
	// dir/
	//   a.go
	//   b.txt
	//   sub/
	//     c.go
	//     d.txt
	//     deep/
	//       e.go
	dirs := []string{
		filepath.Join(dir, "sub"),
		filepath.Join(dir, "sub", "deep"),
	}
	for _, d := range dirs {
		os.MkdirAll(d, 0755)
	}

	files := map[string]string{
		filepath.Join(dir, "a.go"):                "package a",
		filepath.Join(dir, "b.txt"):               "text",
		filepath.Join(dir, "sub", "c.go"):         "package c",
		filepath.Join(dir, "sub", "d.txt"):        "text",
		filepath.Join(dir, "sub", "deep", "e.go"): "package e",
	}
	for path, content := range files {
		os.WriteFile(path, []byte(content), 0644)
	}

	return dir
}

func TestGlobTool_SimplePattern(t *testing.T) {
	dir := setupGlobDir(t)

	tool := &GlobTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "a.go") {
		t.Errorf("expected a.go in result, got:\n%s", result)
	}
	// Simple *.go should not match recursively.
	if strings.Contains(result, "c.go") {
		t.Errorf("simple *.go should not match sub/c.go, got:\n%s", result)
	}
}

func TestGlobTool_RecursiveDoublestar(t *testing.T) {
	dir := setupGlobDir(t)

	tool := &GlobTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "**/*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "a.go") {
		t.Errorf("expected a.go, got:\n%s", result)
	}
	if !strings.Contains(result, "c.go") {
		t.Errorf("expected c.go, got:\n%s", result)
	}
	if !strings.Contains(result, "e.go") {
		t.Errorf("expected e.go, got:\n%s", result)
	}
	// Should not include .txt files.
	if strings.Contains(result, ".txt") {
		t.Errorf("should not include .txt files, got:\n%s", result)
	}
}

func TestGlobTool_NoMatches(t *testing.T) {
	dir := setupGlobDir(t)

	tool := &GlobTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "*.xyz",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "No files matched the pattern." {
		t.Errorf("expected no-match message, got: %s", result)
	}
}

func TestGlobTool_SkipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	hiddenDir := filepath.Join(dir, ".hidden")
	os.MkdirAll(hiddenDir, 0755)
	os.WriteFile(filepath.Join(hiddenDir, "secret.go"), []byte("package s"), 0644)
	os.WriteFile(filepath.Join(dir, "visible.go"), []byte("package v"), 0644)

	tool := &GlobTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "**/*.go",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "visible.go") {
		t.Errorf("expected visible.go, got:\n%s", result)
	}
	if strings.Contains(result, "secret.go") {
		t.Errorf("should skip hidden dir files, got:\n%s", result)
	}
}

func TestGlobTool_MissingPattern(t *testing.T) {
	tool := &GlobTool{}
	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing pattern")
	}
}

func TestMatchDoublestar(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"**/*.go", "main.go", true},
		{"**/*.go", "pkg/tools/tool.go", true},
		{"**/*.go", "readme.txt", false},
		{"*.go", "main.go", true},
		{"*.go", "sub/main.go", false},
		{"src/**", "src/a.go", true},
		{"src/**", "src/sub/b.go", true},
		{"src/**/*.ts", "src/index.ts", true},
		{"src/**/*.ts", "src/sub/index.ts", true},
		{"src/**/*.ts", "lib/index.ts", false},
		{"**", "anything/goes/here.txt", true},
		{"a?c.go", "abc.go", true},
		{"a?c.go", "aXc.go", true},
		{"a?c.go", "ac.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.name, func(t *testing.T) {
			got := matchDoublestar(tt.pattern, tt.name)
			if got != tt.want {
				t.Errorf("matchDoublestar(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
			}
		})
	}
}
