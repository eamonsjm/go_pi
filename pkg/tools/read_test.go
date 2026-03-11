package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTool_ReadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644)

	tool := &ReadTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path": path,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain line numbers and content.
	if !strings.Contains(result, "1\tline1") {
		t.Errorf("expected line 1 content, got:\n%s", result)
	}
	if !strings.Contains(result, "2\tline2") {
		t.Errorf("expected line 2 content, got:\n%s", result)
	}
	if !strings.Contains(result, "3\tline3") {
		t.Errorf("expected line 3 content, got:\n%s", result)
	}
}

func TestReadTool_OffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi.txt")
	var lines []string
	for i := 1; i <= 10; i++ {
		lines = append(lines, "line"+string(rune('0'+i)))
	}
	// Use explicit line content for clarity.
	content := "aaa\nbbb\nccc\nddd\neee\nfff\nggg\nhhh\niii\njjj\n"
	os.WriteFile(path, []byte(content), 0644)

	tool := &ReadTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path": path,
		"offset":    float64(3), // JSON numbers come as float64
		"limit":     float64(2),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "ccc") {
		t.Errorf("expected line 3 (ccc), got:\n%s", result)
	}
	if !strings.Contains(result, "ddd") {
		t.Errorf("expected line 4 (ddd), got:\n%s", result)
	}
	if strings.Contains(result, "eee") {
		t.Errorf("should not contain line 5 (eee), got:\n%s", result)
	}
	if strings.Contains(result, "bbb") {
		t.Errorf("should not contain line 2 (bbb), got:\n%s", result)
	}

	// Should show truncation message since there are more lines.
	if !strings.Contains(result, "more lines not shown") {
		t.Errorf("expected truncation message, got:\n%s", result)
	}
}

func TestReadTool_BinaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.bin")
	// Create data with many null bytes (> 1% threshold).
	data := make([]byte, 1000)
	for i := range data {
		if i%5 == 0 {
			data[i] = 0
		} else {
			data[i] = 'A'
		}
	}
	os.WriteFile(path, data, 0644)

	tool := &ReadTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path": path,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Binary file detected") {
		t.Errorf("expected binary detection, got: %s", result)
	}
}

func TestReadTool_MissingFile(t *testing.T) {
	tool := &ReadTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path": "/nonexistent/file/path.txt",
	})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "cannot read file") {
		t.Errorf("expected 'cannot read file' error, got: %v", err)
	}
}

func TestReadTool_DirectoryReturnsError(t *testing.T) {
	dir := t.TempDir()

	tool := &ReadTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path": dir,
	})
	if err == nil {
		t.Fatal("expected error for directory")
	}
	if !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("expected 'is a directory' error, got: %v", err)
	}
}

func TestReadTool_MissingFilePath(t *testing.T) {
	tool := &ReadTool{}
	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing file_path")
	}
}

func TestGetInt(t *testing.T) {
	tests := []struct {
		name  string
		val   any
		want  int
		wantOK bool
	}{
		{"float64", float64(42), 42, true},
		{"int", int(7), 7, true},
		{"int64", int64(99), 99, true},
		{"string", "nope", 0, false},
		{"nil", nil, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]any{"key": tt.val}
			got, ok := getInt(params, "key")
			if ok != tt.wantOK {
				t.Errorf("getInt ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("getInt = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGetInt_MissingKey(t *testing.T) {
	params := map[string]any{}
	_, ok := getInt(params, "missing")
	if ok {
		t.Error("expected false for missing key")
	}
}

func TestGetString(t *testing.T) {
	params := map[string]any{"name": "hello"}
	got, ok := getString(params, "name")
	if !ok || got != "hello" {
		t.Errorf("getString = %q, %v; want %q, true", got, ok, "hello")
	}

	_, ok = getString(params, "missing")
	if ok {
		t.Error("expected false for missing key")
	}

	params2 := map[string]any{"num": 42}
	_, ok = getString(params2, "num")
	if ok {
		t.Error("expected false for non-string value")
	}
}
