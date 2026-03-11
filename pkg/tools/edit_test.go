package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditTool_SuccessfulReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("hello world\nfoo bar\nbaz qux\n"), 0644)

	tool := &EditTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "foo bar",
		"new_string": "FOO BAR",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Result should contain a diff.
	if !strings.Contains(result, "-foo bar") {
		t.Errorf("expected diff with -foo bar, got:\n%s", result)
	}
	if !strings.Contains(result, "+FOO BAR") {
		t.Errorf("expected diff with +FOO BAR, got:\n%s", result)
	}

	// Verify file was actually changed.
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "FOO BAR") {
		t.Errorf("file should contain 'FOO BAR', got: %s", string(data))
	}
	if strings.Contains(string(data), "foo bar") {
		t.Errorf("file should not contain 'foo bar' anymore")
	}
}

func TestEditTool_OldStringNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("hello world\n"), 0644)

	tool := &EditTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "not in file",
		"new_string": "replacement",
	})
	if err == nil {
		t.Fatal("expected error for old_string not found")
	}
	if !strings.Contains(err.Error(), "old_string not found") {
		t.Errorf("expected 'old_string not found' error, got: %v", err)
	}
}

func TestEditTool_MultipleOccurrences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("aaa\nbbb\naaa\n"), 0644)

	tool := &EditTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "aaa",
		"new_string": "ccc",
	})
	if err == nil {
		t.Fatal("expected error for multiple occurrences")
	}
	if !strings.Contains(err.Error(), "found 2 times") {
		t.Errorf("expected 'found 2 times' error, got: %v", err)
	}
}

func TestEditTool_IdenticalStrings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("hello\n"), 0644)

	tool := &EditTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "hello",
		"new_string": "hello",
	})
	if err == nil {
		t.Fatal("expected error for identical strings")
	}
	if !strings.Contains(err.Error(), "identical") {
		t.Errorf("expected 'identical' error, got: %v", err)
	}
}

func TestEditTool_MissingFile(t *testing.T) {
	tool := &EditTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  "/nonexistent/path/file.txt",
		"old_string": "a",
		"new_string": "b",
	})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestEditTool_MissingFilePath(t *testing.T) {
	tool := &EditTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"old_string": "a",
		"new_string": "b",
	})
	if err == nil {
		t.Fatal("expected error for missing file_path")
	}
}
