package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTool_WriteFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.txt")

	tool := &WriteTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path": path,
		"content":   "hello world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Successfully wrote") {
		t.Errorf("expected success message, got: %s", result)
	}
	if !strings.Contains(result, "11 bytes") {
		t.Errorf("expected '11 bytes', got: %s", result)
	}

	// Verify file contents.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read written file: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("file contents = %q, want %q", string(data), "hello world")
	}
}

func TestWriteTool_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "deep.txt")

	tool := &WriteTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path": path,
		"content":   "deep content",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read written file: %v", err)
	}
	if string(data) != "deep content" {
		t.Errorf("file contents = %q, want %q", string(data), "deep content")
	}
}

func TestWriteTool_MissingFilePath(t *testing.T) {
	tool := &WriteTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"content": "stuff",
	})
	if err == nil {
		t.Fatal("expected error for missing file_path")
	}
}

func TestWriteTool_MissingContent(t *testing.T) {
	tool := &WriteTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path": "/tmp/whatever.txt",
	})
	if err == nil {
		t.Fatal("expected error for missing content")
	}
}

func TestWriteTool_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overwrite.txt")
	os.WriteFile(path, []byte("old content"), 0644)

	tool := &WriteTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path": path,
		"content":   "new content",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "new content" {
		t.Errorf("file contents = %q, want %q", string(data), "new content")
	}
}
