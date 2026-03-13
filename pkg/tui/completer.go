package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fileCompletion holds state for an active @filepath tab completion cycle.
type fileCompletion struct {
	matches    []string // sorted completion candidates
	idx        int      // current cycling index
	textBefore string   // text before the @ (fixed across cycles)
	textAfter  string   // text after the original cursor (fixed across cycles)
}

// findAtToken scans backwards from the cursor position (rune offset) looking
// for an @-prefixed filepath token. The @ must be at the start of text or
// preceded by whitespace. Returns the partial path (without @), the rune
// offset of the @, and whether a token was found.
func findAtToken(runes []rune, cursor int) (partial string, atStart int, ok bool) {
	if cursor <= 0 || cursor > len(runes) {
		return "", 0, false
	}

	for i := cursor - 1; i >= 0; i-- {
		ch := runes[i]
		if ch == '@' {
			if i > 0 && runes[i-1] != ' ' && runes[i-1] != '\n' && runes[i-1] != '\t' {
				return "", 0, false
			}
			return string(runes[i+1 : cursor]), i, true
		}
		if ch == ' ' || ch == '\n' || ch == '\t' {
			return "", 0, false
		}
	}
	return "", 0, false
}

// completeFilePath returns file and directory names matching a partial path.
// Directories include a trailing slash. Hidden entries (dot-prefixed) are
// excluded unless the partial basename starts with a dot.
func completeFilePath(partial string) []string {
	var dir, base string

	switch {
	case partial == "":
		dir, base = ".", ""
	case partial[len(partial)-1] == '/' || partial[len(partial)-1] == filepath.Separator:
		dir, base = partial, ""
	default:
		dir = filepath.Dir(partial)
		base = filepath.Base(partial)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	showHidden := strings.HasPrefix(base, ".")

	var matches []string
	for _, e := range entries {
		name := e.Name()
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if !strings.HasPrefix(name, base) {
			continue
		}

		result := filepath.Join(dir, name)
		if e.IsDir() {
			result += "/"
		}
		// Remove leading "./" for paths relative to current directory.
		result = strings.TrimPrefix(result, "./")
		matches = append(matches, result)
	}

	sort.Strings(matches)
	return matches
}
