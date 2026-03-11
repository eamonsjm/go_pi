package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupGrepDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "main.go"), []byte(
		"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n",
	), 0644)

	os.WriteFile(filepath.Join(dir, "utils.go"), []byte(
		"package main\n\nfunc add(a, b int) int {\n\treturn a + b\n}\n",
	), 0644)

	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte(
		"This is a readme file.\nIt has multiple lines.\nNothing special here.\n",
	), 0644)

	return dir
}

func TestGrepTool_BasicSearch(t *testing.T) {
	dir := setupGrepDir(t)

	tool := &GrepTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "func",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "func main()") {
		t.Errorf("expected 'func main()' match, got:\n%s", result)
	}
	if !strings.Contains(result, "func add") {
		t.Errorf("expected 'func add' match, got:\n%s", result)
	}
}

func TestGrepTool_ContextLines(t *testing.T) {
	dir := setupGrepDir(t)

	tool := &GrepTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern":       "func add",
		"path":          dir,
		"context_lines": float64(1),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Context lines use - separator, match lines use : separator.
	if !strings.Contains(result, "func add") {
		t.Errorf("expected match line, got:\n%s", result)
	}
	// Should have context line(s) with the - separator.
	lines := strings.Split(strings.TrimSpace(result), "\n")
	hasContext := false
	for _, line := range lines {
		// Context lines have file-linenum-text format.
		parts := strings.SplitN(line, "-", 3)
		if len(parts) >= 3 {
			hasContext = true
			break
		}
	}
	if !hasContext {
		t.Errorf("expected context lines in output, got:\n%s", result)
	}
}

func TestGrepTool_IncludeFilter(t *testing.T) {
	dir := setupGrepDir(t)

	tool := &GrepTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": ".*",
		"path":    dir,
		"include": "*.go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "main.go") {
		t.Errorf("expected main.go in results, got:\n%s", result)
	}
	// Should not include .txt files.
	if strings.Contains(result, "readme.txt") {
		t.Errorf("should not include readme.txt with *.go filter, got:\n%s", result)
	}
}

func TestGrepTool_NoMatches(t *testing.T) {
	dir := setupGrepDir(t)

	tool := &GrepTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "zzzznotfound",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "No matches found." {
		t.Errorf("expected 'No matches found.', got: %s", result)
	}
}

func TestGrepTool_BinaryFileSkipping(t *testing.T) {
	dir := t.TempDir()

	// Create a binary file with null bytes.
	binData := make([]byte, 100)
	binData[0] = 0 // null byte triggers binary detection
	binData[10] = 'h'
	binData[11] = 'e'
	binData[12] = 'l'
	binData[13] = 'l'
	binData[14] = 'o'
	os.WriteFile(filepath.Join(dir, "binary.dat"), binData, 0644)

	// Create a text file with "hello".
	os.WriteFile(filepath.Join(dir, "text.txt"), []byte("hello world\n"), 0644)

	tool := &GrepTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "hello",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "text.txt") {
		t.Errorf("expected text.txt match, got:\n%s", result)
	}
	if strings.Contains(result, "binary.dat") {
		t.Errorf("should skip binary file, got:\n%s", result)
	}
}

func TestGrepTool_SingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.txt")
	os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0644)

	tool := &GrepTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "beta",
		"path":    path,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "beta") {
		t.Errorf("expected 'beta' match, got:\n%s", result)
	}
}

func TestGrepTool_SingleBinaryFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin.dat")
	data := make([]byte, 100)
	data[0] = 0
	os.WriteFile(path, data, 0644)

	tool := &GrepTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "anything",
		"path":    path,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Binary file") {
		t.Errorf("expected binary file message, got: %s", result)
	}
}

func TestGrepTool_InvalidRegex(t *testing.T) {
	tool := &GrepTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "[invalid",
	})
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestGrepTool_MissingPattern(t *testing.T) {
	tool := &GrepTool{}
	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing pattern")
	}
}

func TestMatchInclude(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    bool
	}{
		{"main.go", "*.go", true},
		{"main.go", "*.txt", false},
		{"index.ts", "*.{ts,tsx}", true},
		{"app.tsx", "*.{ts,tsx}", true},
		{"style.css", "*.{ts,tsx}", false},
		{"Makefile", "Makefile", true},
		{"Makefile", "makefile", false},
	}

	for _, tt := range tests {
		t.Run(tt.name+"_"+tt.pattern, func(t *testing.T) {
			got := matchInclude(tt.name, tt.pattern)
			if got != tt.want {
				t.Errorf("matchInclude(%q, %q) = %v, want %v", tt.name, tt.pattern, got, tt.want)
			}
		})
	}
}
