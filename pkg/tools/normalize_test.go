package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFuzzyMatch_SmartQuotes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quotes.go")
	// File has straight quotes.
	os.WriteFile(path, []byte(`fmt.Println("hello world")`+"\n"), 0644)

	tool := &EditTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path": path,
		// old_string uses smart/curly quotes.
		"old_string": "fmt.Println(\u201Chello world\u201D)",
		"new_string": `fmt.Println("goodbye world")`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "smart quotes") {
		t.Errorf("expected normalization note about smart quotes, got:\n%s", result)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"goodbye world"`) {
		t.Errorf("file should contain replacement, got: %s", string(data))
	}
}

func TestFuzzyMatch_UnicodeNormalization(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unicode.txt")
	// File has decomposed e-acute (e + combining acute).
	os.WriteFile(path, []byte("caf\u0065\u0301\n"), 0644)

	tool := &EditTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path": path,
		// old_string uses precomposed e-acute.
		"old_string": "caf\u00E9",
		"new_string": "coffee",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "Unicode NFC") {
		t.Errorf("expected normalization note about Unicode, got:\n%s", result)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "coffee") {
		t.Errorf("file should contain 'coffee', got: %s", string(data))
	}
}

func TestFuzzyMatch_WhitespaceNormalization(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ws.go")
	// File uses tabs for indentation.
	os.WriteFile(path, []byte("func main() {\n\tfmt.Println(\"hi\")\n}\n"), 0644)

	tool := &EditTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path": path,
		// old_string uses spaces instead of tabs.
		"old_string": "    fmt.Println(\"hi\")",
		"new_string": "\tfmt.Println(\"hello\")",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "whitespace") {
		t.Errorf("expected normalization note about whitespace, got:\n%s", result)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "hello") {
		t.Errorf("file should contain 'hello', got: %s", string(data))
	}
}

func TestFuzzyMatch_ExactMatchPreferred(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exact.txt")
	os.WriteFile(path, []byte("hello world\n"), 0644)

	tool := &EditTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "hello world",
		"new_string": "goodbye world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should NOT contain any normalization note.
	if strings.Contains(result, "Note:") {
		t.Errorf("exact match should not produce normalization note, got:\n%s", result)
	}
}

func TestFuzzyMatch_NoMatchEvenFuzzy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "none.txt")
	os.WriteFile(path, []byte("hello world\n"), 0644)

	tool := &EditTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "completely different text",
		"new_string": "replacement",
	})
	if err == nil {
		t.Fatal("expected error for no match")
	}
	if !strings.Contains(err.Error(), "old_string not found") {
		t.Errorf("expected 'old_string not found' error, got: %v", err)
	}
}

func TestFuzzyMatch_MultipleMatchesStillError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi.txt")
	os.WriteFile(path, []byte("aaa\nbbb\naaa\n"), 0644)

	tool := &EditTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "aaa",
		"new_string": "ccc",
	})
	if err == nil {
		t.Fatal("expected error for multiple matches")
	}
	if !strings.Contains(err.Error(), "found 2 times") {
		t.Errorf("expected 'found 2 times' error, got: %v", err)
	}
}

func TestBOMPreservation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bom.txt")
	// Write file with UTF-8 BOM.
	bom := []byte{0xEF, 0xBB, 0xBF}
	content := append(bom, []byte("hello world\n")...)
	os.WriteFile(path, content, 0644)

	tool := &EditTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "hello world",
		"new_string": "goodbye world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(path)
	// BOM should still be present.
	if len(data) < 3 || data[0] != 0xEF || data[1] != 0xBB || data[2] != 0xBF {
		t.Error("BOM was not preserved")
	}
	if !strings.Contains(string(data[3:]), "goodbye world") {
		t.Errorf("replacement not applied, got: %s", string(data[3:]))
	}
}

func TestCRLFPreservation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crlf.txt")
	// Write file with CRLF line endings.
	os.WriteFile(path, []byte("hello world\r\nfoo bar\r\nbaz qux\r\n"), 0644)

	tool := &EditTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "foo bar",
		"new_string": "FOO BAR",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	// CRLF should be preserved.
	if !strings.Contains(content, "\r\n") {
		t.Error("CRLF line endings were not preserved")
	}
	if strings.Contains(content, "foo bar") {
		t.Error("old string should have been replaced")
	}
	if !strings.Contains(content, "FOO BAR") {
		t.Error("new string should be present")
	}
}

func TestNoBOMFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nobom.txt")
	os.WriteFile(path, []byte("hello world\n"), 0644)

	tool := &EditTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "hello world",
		"new_string": "goodbye world",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(path)
	// Should NOT have a BOM.
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		t.Error("BOM was incorrectly added to non-BOM file")
	}
}

func TestNormalizeQuotes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"left double", "\u201Chello\u201D", `"hello"`},
		{"left single", "\u2018it\u2019s\u2019", "'it's'"},
		{"guillemets", "\u00ABbonjour\u00BB", `"bonjour"`},
		{"no change", `"hello"`, `"hello"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeQuotes(tc.input)
			if got != tc.expected {
				t.Errorf("normalizeQuotes(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"tab to spaces", "\thello", "    hello"},
		{"multiple tabs", "\t\thello", "        hello"},
		{"no tabs", "    hello", "    hello"},
		{"mixed", "\t world", "     world"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeWhitespace(tc.input)
			if got != tc.expected {
				t.Errorf("normalizeWhitespace(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestDetectBOM(t *testing.T) {
	t.Run("with BOM", func(t *testing.T) {
		data := []byte{0xEF, 0xBB, 0xBF, 'h', 'i'}
		bom, content := detectBOM(data)
		if bom == nil {
			t.Fatal("expected BOM to be detected")
		}
		if string(content) != "hi" {
			t.Errorf("expected content 'hi', got %q", string(content))
		}
	})

	t.Run("without BOM", func(t *testing.T) {
		data := []byte("hi")
		bom, content := detectBOM(data)
		if bom != nil {
			t.Fatal("expected no BOM")
		}
		if string(content) != "hi" {
			t.Errorf("expected content 'hi', got %q", string(content))
		}
	})
}

func TestDetectLineEnding(t *testing.T) {
	if got := detectLineEnding("hello\r\nworld\r\n"); got != "\r\n" {
		t.Errorf("expected CRLF, got %q", got)
	}
	if got := detectLineEnding("hello\nworld\n"); got != "\n" {
		t.Errorf("expected LF, got %q", got)
	}
}

func TestFuzzyMatch_CombinedNormalization(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "combined.go")
	// File has straight quotes + tabs.
	os.WriteFile(path, []byte("func main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)

	tool := &EditTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"file_path": path,
		// old_string uses smart quotes + spaces instead of tabs.
		"old_string": "    fmt.Println(\u201Chello\u201D)",
		"new_string": "\tfmt.Println(\"world\")",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "combined") {
		t.Errorf("expected normalization note about combined, got:\n%s", result)
	}

	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "world") {
		t.Errorf("file should contain 'world', got: %s", string(data))
	}
}
