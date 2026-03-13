package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// --- findAtToken tests ---

func TestFindAtToken_AtEnd(t *testing.T) {
	runes := []rune("hello @pkg/tui/ed")
	partial, start, ok := findAtToken(runes, len(runes))
	if !ok {
		t.Fatal("expected to find @token")
	}
	if partial != "pkg/tui/ed" {
		t.Errorf("partial = %q, want %q", partial, "pkg/tui/ed")
	}
	if start != 6 {
		t.Errorf("start = %d, want 6", start)
	}
}

func TestFindAtToken_AtStart(t *testing.T) {
	runes := []rune("@src/main.go")
	partial, start, ok := findAtToken(runes, len(runes))
	if !ok {
		t.Fatal("expected to find @token")
	}
	if partial != "src/main.go" {
		t.Errorf("partial = %q, want %q", partial, "src/main.go")
	}
	if start != 0 {
		t.Errorf("start = %d, want 0", start)
	}
}

func TestFindAtToken_JustAt(t *testing.T) {
	runes := []rune("look at @")
	partial, _, ok := findAtToken(runes, len(runes))
	if !ok {
		t.Fatal("expected to find @token")
	}
	if partial != "" {
		t.Errorf("partial = %q, want empty", partial)
	}
}

func TestFindAtToken_EmailIgnored(t *testing.T) {
	runes := []rune("user@domain.com")
	_, _, ok := findAtToken(runes, len(runes))
	if ok {
		t.Error("@ in email should not be detected as a file reference")
	}
}

func TestFindAtToken_NoAt(t *testing.T) {
	runes := []rune("hello world")
	_, _, ok := findAtToken(runes, len(runes))
	if ok {
		t.Error("expected no @token found")
	}
}

func TestFindAtToken_CursorMidText(t *testing.T) {
	runes := []rune("@file1 @file2")

	// Cursor at position 6 — right after "file1", still within the token.
	partial, start, ok := findAtToken(runes, 6)
	if !ok {
		t.Fatal("expected to find @file1 token at position 6")
	}
	if partial != "file1" {
		t.Errorf("partial = %q, want %q", partial, "file1")
	}
	if start != 0 {
		t.Errorf("start = %d, want 0", start)
	}

	// Cursor at position 7 — right after the space, between tokens.
	_, _, ok = findAtToken(runes, 7)
	if ok {
		t.Error("cursor between tokens (after space) should not find @token")
	}

	// Cursor at position 13 (end, after @file2).
	partial, start, ok = findAtToken(runes, 13)
	if !ok {
		t.Fatal("expected to find @file2")
	}
	if partial != "file2" {
		t.Errorf("partial = %q, want %q", partial, "file2")
	}
	if start != 7 {
		t.Errorf("start = %d, want 7", start)
	}
}

func TestFindAtToken_EmptyInput(t *testing.T) {
	_, _, ok := findAtToken([]rune(""), 0)
	if ok {
		t.Error("expected no token on empty input")
	}
}

func TestFindAtToken_CursorAtZero(t *testing.T) {
	_, _, ok := findAtToken([]rune("@file"), 0)
	if ok {
		t.Error("expected no token when cursor is at position 0")
	}
}

// --- completeFilePath tests ---

func TestCompleteFilePath_CurrentDir(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, "apple.go", "banana.txt", "cherry/")

	old := chdir(t, dir)
	defer os.Chdir(old)

	matches := completeFilePath("")
	if len(matches) != 3 {
		t.Fatalf("expected 3 matches, got %d: %v", len(matches), matches)
	}
	if matches[0] != "apple.go" {
		t.Errorf("matches[0] = %q, want %q", matches[0], "apple.go")
	}
	if matches[2] != "cherry/" {
		t.Errorf("matches[2] = %q, want %q (directory trailing /)", matches[2], "cherry/")
	}
}

func TestCompleteFilePath_Prefix(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, "alpha.go", "beta.go", "abc.txt")

	old := chdir(t, dir)
	defer os.Chdir(old)

	matches := completeFilePath("a")
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d: %v", len(matches), matches)
	}
	if matches[0] != "abc.txt" {
		t.Errorf("matches[0] = %q, want %q", matches[0], "abc.txt")
	}
	if matches[1] != "alpha.go" {
		t.Errorf("matches[1] = %q, want %q", matches[1], "alpha.go")
	}
}

func TestCompleteFilePath_Subdirectory(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, "src/main.go", "src/lib.go", "src/test/")

	old := chdir(t, dir)
	defer os.Chdir(old)

	matches := completeFilePath("src/")
	if len(matches) != 3 {
		t.Fatalf("expected 3 matches, got %d: %v", len(matches), matches)
	}
	// Should be: src/lib.go, src/main.go, src/test/
	if matches[0] != "src/lib.go" {
		t.Errorf("matches[0] = %q, want %q", matches[0], "src/lib.go")
	}
	if matches[2] != "src/test/" {
		t.Errorf("matches[2] = %q, want %q", matches[2], "src/test/")
	}
}

func TestCompleteFilePath_PartialInSubdir(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, "pkg/tui/editor.go", "pkg/tui/editor_test.go", "pkg/tui/app.go")

	old := chdir(t, dir)
	defer os.Chdir(old)

	matches := completeFilePath("pkg/tui/ed")
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d: %v", len(matches), matches)
	}
	if matches[0] != "pkg/tui/editor.go" {
		t.Errorf("matches[0] = %q, want %q", matches[0], "pkg/tui/editor.go")
	}
	if matches[1] != "pkg/tui/editor_test.go" {
		t.Errorf("matches[1] = %q, want %q", matches[1], "pkg/tui/editor_test.go")
	}
}

func TestCompleteFilePath_HiddenExcluded(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, ".hidden", "visible.go")

	old := chdir(t, dir)
	defer os.Chdir(old)

	matches := completeFilePath("")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match (hidden excluded), got %d: %v", len(matches), matches)
	}
	if matches[0] != "visible.go" {
		t.Errorf("matches[0] = %q, want %q", matches[0], "visible.go")
	}
}

func TestCompleteFilePath_HiddenIncludedWhenDotTyped(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, ".hidden", ".git/", "visible.go")

	old := chdir(t, dir)
	defer os.Chdir(old)

	matches := completeFilePath(".")
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches (dot-prefixed), got %d: %v", len(matches), matches)
	}
}

func TestCompleteFilePath_NoMatches(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, "foo.go")

	old := chdir(t, dir)
	defer os.Chdir(old)

	matches := completeFilePath("zzz")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d: %v", len(matches), matches)
	}
}

func TestCompleteFilePath_NonExistentDir(t *testing.T) {
	dir := t.TempDir()
	old := chdir(t, dir)
	defer os.Chdir(old)

	matches := completeFilePath("nonexistent/foo")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for nonexistent dir, got %d: %v", len(matches), matches)
	}
}

// --- Editor tab completion integration tests ---

func TestEditor_TabCompletion_SingleMatch(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, "unique_file.go")

	old := chdir(t, dir)
	defer os.Chdir(old)

	e := NewEditor()
	e.textarea.SetValue("@uniq")
	e.Update(tea.KeyMsg{Type: tea.KeyTab})

	got := e.Value()
	if got != "@unique_file.go" {
		t.Errorf("expected %q, got %q", "@unique_file.go", got)
	}
}

func TestEditor_TabCompletion_CycleMatches(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, "aa.go", "ab.go", "ac.go")

	old := chdir(t, dir)
	defer os.Chdir(old)

	e := NewEditor()
	e.textarea.SetValue("@a")

	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := e.Value(); got != "@aa.go" {
		t.Errorf("first Tab: expected %q, got %q", "@aa.go", got)
	}

	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := e.Value(); got != "@ab.go" {
		t.Errorf("second Tab: expected %q, got %q", "@ab.go", got)
	}

	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := e.Value(); got != "@ac.go" {
		t.Errorf("third Tab: expected %q, got %q", "@ac.go", got)
	}

	// Wraps around.
	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := e.Value(); got != "@aa.go" {
		t.Errorf("fourth Tab (wrap): expected %q, got %q", "@aa.go", got)
	}
}

func TestEditor_TabCompletion_DirectoryDrillDown(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, "src/main.go")

	old := chdir(t, dir)
	defer os.Chdir(old)

	e := NewEditor()
	e.textarea.SetValue("@s")

	// First Tab completes to "src/" (directory).
	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := e.Value(); got != "@src/" {
		t.Errorf("first Tab: expected %q, got %q", "@src/", got)
	}

	// Second Tab drills into the directory.
	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := e.Value(); got != "@src/main.go" {
		t.Errorf("second Tab (drill): expected %q, got %q", "@src/main.go", got)
	}
}

func TestEditor_TabCompletion_ResetOnOtherKey(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, "aa.go", "ab.go")

	old := chdir(t, dir)
	defer os.Chdir(old)

	e := NewEditor()
	e.textarea.SetValue("@a")

	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if e.completer == nil {
		t.Fatal("completer should be active after Tab")
	}

	// Any other key resets completion.
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if e.completer != nil {
		t.Error("completer should be nil after non-Tab key")
	}
}

func TestEditor_TabCompletion_NoAt(t *testing.T) {
	e := NewEditor()
	e.textarea.SetValue("hello world")

	e.Update(tea.KeyMsg{Type: tea.KeyTab})
	if e.completer != nil {
		t.Error("Tab without @ should not activate completion")
	}
}

func TestEditor_TabCompletion_PreservesTextAround(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, "file.go")

	old := chdir(t, dir)
	defer os.Chdir(old)

	e := NewEditor()
	// Place cursor at end — simulates "fix @fi then test"
	// but since textarea cursor is at end after SetValue, we test the simple
	// case: "@fi" at end with text before it.
	e.textarea.SetValue("fix @fi")
	e.Update(tea.KeyMsg{Type: tea.KeyTab})

	if got := e.Value(); got != "fix @file.go" {
		t.Errorf("expected %q, got %q", "fix @file.go", got)
	}
}

func TestEditor_TabCompletion_JustAt(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, "hello.txt")

	old := chdir(t, dir)
	defer os.Chdir(old)

	e := NewEditor()
	e.textarea.SetValue("@")
	e.Update(tea.KeyMsg{Type: tea.KeyTab})

	if got := e.Value(); got != "@hello.txt" {
		t.Errorf("expected %q, got %q", "@hello.txt", got)
	}
}

func TestEditor_TabCompletion_HintShownForMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, "aa.go", "ab.go", "ac.go")

	old := chdir(t, dir)
	defer os.Chdir(old)

	e := NewEditor()
	e.SetWidth(80)
	e.textarea.SetValue("@a")
	e.Update(tea.KeyMsg{Type: tea.KeyTab})

	hint := e.fileCompletionHint()
	stripped := stripAnsi(hint)
	if !strings.Contains(stripped, "aa.go") || !strings.Contains(stripped, "ab.go") {
		t.Errorf("hint should list matches, got %q", stripped)
	}
}

func TestEditor_TabCompletion_NoHintForSingleMatch(t *testing.T) {
	dir := t.TempDir()
	createTestFiles(t, dir, "unique.go")

	old := chdir(t, dir)
	defer os.Chdir(old)

	e := NewEditor()
	e.textarea.SetValue("@u")
	e.Update(tea.KeyMsg{Type: tea.KeyTab})

	hint := e.fileCompletionHint()
	if hint != "" {
		t.Errorf("expected no hint for single match, got %q", hint)
	}
}

// --- helpers ---

// createTestFiles creates files and directories in dir. Paths ending with /
// are created as directories; others as empty files. Intermediate directories
// are created as needed.
func createTestFiles(t *testing.T, dir string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(dir, p)
		if strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// chdir changes to dir and returns the old directory for restoration.
func chdir(t *testing.T, dir string) string {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return old
}
