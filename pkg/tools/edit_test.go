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

func TestEditTool_PreservesFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	os.WriteFile(path, []byte("#!/bin/bash\necho hello\n"), 0755)

	tool := &EditTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "echo hello",
		"new_string": "echo world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("cannot stat file: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("expected permissions 0755, got %04o", info.Mode().Perm())
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

func TestEditTool_HashBasedEdit_BareHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("hello world\nfoo bar\nbaz qux\n"), 0644)

	// Get the hash of "foo bar"
	hash := contentHash("foo bar")

	tool := &EditTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": hash,
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

func TestEditTool_HashBasedEdit_DescriptiveFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("hello world\nfoo bar\nbaz qux\n"), 0644)

	// Get the hash of "foo bar"
	hash := contentHash("foo bar")

	tool := &EditTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "replace line " + hash + " with FOO BAR",
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
}

func TestEditTool_HashBasedEdit_CorruptionPrevention(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("hello world\nfoo bar\nbaz qux\n"), 0644)

	// Use a hash that doesn't exist in the file — "zzz" is not valid hex
	// so it won't even be treated as a hash.
	nonexistentHash := "zzz"

	tool := &EditTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": nonexistentHash,
		"new_string": "FOO BAR",
	})

	// Should fail because "zzz" is not found as content and not valid hex
	if err == nil {
		t.Fatal("expected error for non-matching string")
	}

	// Verify file was NOT changed
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "foo bar") {
		t.Errorf("file should still contain 'foo bar'")
	}
}

func TestEditTool_ContentMatchTakesPriorityOverHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")

	// Get the hash of "echo hello" — we'll craft a file where
	// a short string matches both as content AND as a line's hash.
	targetHash := contentHash("echo hello")

	// Write a file where:
	// - Line "echo hello" has hash targetHash
	// - The literal text targetHash also appears in the file
	content := "echo hello\n" + targetHash + " is the hash\nfoo bar\n"
	os.WriteFile(path, []byte(content), 0644)

	tool := &EditTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": targetHash,                     // Could match as hash OR as content
		"new_string": "REPLACED",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Content matching should win — the literal text targetHash should be replaced,
	// NOT the "echo hello" line whose hash happens to be targetHash.
	data, _ := os.ReadFile(path)
	fileStr := string(data)
	if !strings.Contains(fileStr, "echo hello") {
		t.Errorf("content match should have priority; 'echo hello' should be untouched but was removed.\nResult:\n%s\nDiff:\n%s", fileStr, result)
	}
	if !strings.Contains(fileStr, "REPLACED") {
		t.Errorf("expected literal text to be replaced, got:\n%s", fileStr)
	}
}

func TestEditTool_HashOnlyUsedWhenContentNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")

	// Get the hash of the actual line content (including leading tab).
	line := "\treturn nil"
	hash := contentHash(line)

	// Write file where hash does NOT appear as literal content,
	// so hash-based matching should kick in as fallback.
	os.WriteFile(path, []byte("func foo() error {\n"+line+"\n}\n"), 0644)

	tool := &EditTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": hash,
		"new_string": "\treturn fmt.Errorf(\"failed\")",
	})
	if err != nil {
		t.Fatalf("hash=%q; unexpected error: %v", hash, err)
	}

	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "return nil") {
		t.Errorf("hash-based edit should have replaced 'return nil', got:\n%s\nDiff:\n%s", string(data), result)
	}
}

func TestEditTool_NonHexShortStringNotTreatedAsHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")

	// "err" is 3 chars but not valid hex — should NOT be treated as a hash.
	os.WriteFile(path, []byte("if err != nil {\n\tlog.Fatal(err)\n}\n"), 0644)

	tool := &EditTool{}
	// "err" appears twice, so it should fail with "found N times" error,
	// NOT be treated as a hash lookup.
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "err",
		"new_string": "error",
	})
	if err == nil {
		t.Fatal("expected error — 'err' appears multiple times")
	}
	if !strings.Contains(err.Error(), "found") {
		t.Errorf("expected 'found N times' error, got: %v", err)
	}
}

func TestIsValidHashString(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"a1b", true},
		{"000", true},
		{"fff", true},
		{"abc", true},
		{"0ff", true},
		{"add", true},   // valid hex, even though it's an English word
		{"bad", true},   // valid hex
		{"err", false},  // 'r' is not hex
		{"cmd", false},  // not hex
		{"exit", false}, // 4 chars
		{"ab", false},   // 2 chars
		{"abcd", false}, // 4 chars
		{"ABC", false},  // uppercase
		{"a b", false},  // space
		{"", false},
		{"zzz", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isValidHashString(tt.input)
			if result != tt.expected {
				t.Errorf("isValidHashString(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestEditTool_HashBasedEdit_FallbackToContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "edit.txt")
	os.WriteFile(path, []byte("hello world\nfoo bar\nbaz qux\n"), 0644)

	// Use a multi-line content match (should fall back to content matching)
	tool := &EditTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "hello world\nfoo bar",
		"new_string": "HELLO WORLD\nFOO BAR",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Result should contain a diff.
	if !strings.Contains(result, "-hello world") {
		t.Errorf("expected diff with -hello world, got:\n%s", result)
	}

	// Verify file was actually changed.
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "HELLO WORLD") {
		t.Errorf("file should contain 'HELLO WORLD', got: %s", string(data))
	}
}
