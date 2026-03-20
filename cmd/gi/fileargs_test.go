package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessFileArgs_NoArgs(t *testing.T) {
	result, err := processFileArgs(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Fatalf("expected empty string, got %q", result)
	}
}

func TestProcessFileArgs_TextOnly(t *testing.T) {
	result, err := processFileArgs([]string{"hello", "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello world" {
		t.Fatalf("expected %q, got %q", "hello world", result)
	}
}

func TestProcessFileArgs_SingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte("file contents here"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := processFileArgs([]string{"@" + path})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "file contents here") {
		t.Fatalf("expected file contents in result, got %q", result)
	}
	if !strings.Contains(result, "<file path=") {
		t.Fatalf("expected <file> wrapper in result, got %q", result)
	}
}

func TestProcessFileArgs_MultipleFiles(t *testing.T) {
	dir := t.TempDir()
	path1 := filepath.Join(dir, "a.txt")
	path2 := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(path1, []byte("alpha"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path2, []byte("bravo"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := processFileArgs([]string{"@" + path1, "@" + path2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "alpha") || !strings.Contains(result, "bravo") {
		t.Fatalf("expected both file contents, got %q", result)
	}
}

func TestProcessFileArgs_FilesAndText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	if err := os.WriteFile(path, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := processFileArgs([]string{"@" + path, "explain", "this"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "package main") {
		t.Fatalf("expected file contents, got %q", result)
	}
	if !strings.Contains(result, "explain this") {
		t.Fatalf("expected text args, got %q", result)
	}
}

func TestProcessFileArgs_FileNotFound(t *testing.T) {
	_, err := processFileArgs([]string{"@/nonexistent/file.txt"})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "cannot read") {
		t.Fatalf("expected 'cannot read' error, got %v", err)
	}
}

func TestProcessFileArgs_BareAt(t *testing.T) {
	// A bare "@" with no path should be skipped
	result, err := processFileArgs([]string{"@", "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Fatalf("expected %q, got %q", "hello", result)
	}
}
