package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// EditTool performs exact string replacements in files.
type EditTool struct{}

func (t *EditTool) Name() string { return "edit" }

func (t *EditTool) Description() string {
	return "Performs an exact string replacement in a file. The old_string must appear exactly once " +
		"in the file (to avoid ambiguous edits). Returns a unified diff of the change."
}

func (t *EditTool) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to edit",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "The exact text to find and replace. Must be unique within the file.",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "The text to replace old_string with",
			},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	}
}

func (t *EditTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	filePath, ok := params["file_path"].(string)
	if !ok || filePath == "" {
		return "", fmt.Errorf("file_path is required and must be a string")
	}

	oldString, ok := params["old_string"].(string)
	if !ok {
		return "", fmt.Errorf("old_string is required and must be a string")
	}

	newString, ok := params["new_string"].(string)
	if !ok {
		return "", fmt.Errorf("new_string is required and must be a string")
	}

	if oldString == newString {
		return "", fmt.Errorf("old_string and new_string are identical; no change needed")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("cannot read file: %w", err)
	}

	// Detect and strip BOM — we'll re-prepend it on write.
	bom, rawContent := detectBOM(data)
	content := string(rawContent)

	// Detect line endings so we can preserve them.
	lineEnding := detectLineEnding(content)

	// Normalize to LF for matching, then restore original endings on write.
	if lineEnding == "\r\n" {
		content = strings.ReplaceAll(content, "\r\n", "\n")
		oldString = strings.ReplaceAll(oldString, "\r\n", "\n")
		newString = strings.ReplaceAll(newString, "\r\n", "\n")
	}

	matchTarget, normNote, err := resolveMatch(content, oldString, filePath)
	if err != nil {
		return "", err
	}

	newContent := strings.Replace(content, matchTarget, newString, 1)

	if err := writeFilePreservingFormat(filePath, newContent, lineEnding, bom); err != nil {
		return "", err
	}

	// Generate a unified diff (use LF-normalized content for readable diffs).
	originalForDiff := content
	if lineEnding == "\r\n" {
		newContent = strings.ReplaceAll(newContent, "\r\n", "\n")
	}
	diff := unifiedDiff(filePath, originalForDiff, newContent)

	if normNote != "" {
		diff = normNote + "\n" + diff
	}
	return diff, nil
}

// resolveMatch finds the exact substring in content that matches oldString,
// trying exact match first, then hash-based, then fuzzy normalization.
func resolveMatch(content, oldString, filePath string) (matchTarget, normNote string, err error) {
	count := strings.Count(content, oldString)
	switch {
	case count == 1:
		return oldString, "", nil
	case count > 1:
		return "", "", fmt.Errorf("old_string found %d times in %s; it must be unique, provide more surrounding context to make the match unique", count, filePath)
	}

	// Exact match failed — try hash-based edit if oldString looks like a hash.
	hashMatch, hashNote, hashFound := tryHashBasedEdit(content, oldString)
	if hashFound {
		return hashMatch, hashNote, nil
	}

	// Try fuzzy matching with normalization.
	matched, normName, found := fuzzyFind(content, oldString)
	if !found {
		return "", "", fmt.Errorf("old_string not found in %s; make sure the string matches exactly, including whitespace and indentation", filePath)
	}
	return matched, fmt.Sprintf("Note: exact match failed. Matched via %s.", normName), nil
}

// writeFilePreservingFormat writes content to filePath, restoring CRLF line
// endings and BOM prefix if the original file used them.
func writeFilePreservingFormat(filePath, content, lineEnding string, bom []byte) error {
	if lineEnding == "\r\n" {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}

	var output []byte
	if bom != nil {
		output = append(bom, []byte(content)...)
	} else {
		output = []byte(content)
	}

	mode := os.FileMode(0644)
	if info, err := os.Stat(filePath); err == nil {
		mode = info.Mode()
	}

	if err := os.WriteFile(filePath, output, mode); err != nil {
		return fmt.Errorf("cannot write file: %w", err)
	}
	return nil
}

// isValidHashString checks if s is exactly 3 lowercase hex characters,
// matching the format returned by contentHash.
func isValidHashString(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// tryHashBasedEdit attempts to match oldString as a line content hash.
// Supports formats like:
//   - "a1b" (bare hash — exactly 3 hex characters)
//   - "replace line a1b with ..." (descriptive format, extracts hash)
//
// Returns the matched line content, a note, and whether a match was found.
// Returns an error string if the hash matches multiple lines (ambiguous).
func tryHashBasedEdit(content, oldString string) (matchedLine string, note string, found bool) {
	var targetHash string

	// Check if it's a descriptive format like "replace line a1b with ..."
	if strings.HasPrefix(oldString, "replace line ") {
		parts := strings.Fields(oldString)
		if len(parts) >= 3 {
			candidate := parts[2]
			if isValidHashString(candidate) {
				targetHash = candidate
			}
		}
	} else if isValidHashString(oldString) {
		// Bare hash: exactly 3 lowercase hex characters.
		targetHash = oldString
	}

	if targetHash == "" {
		return "", "", false
	}

	// Search for lines with matching hash, checking for collisions.
	lines := strings.Split(content, "\n")
	var matches []string
	for _, line := range lines {
		if contentHash(line) == targetHash {
			matches = append(matches, line)
		}
	}

	switch len(matches) {
	case 0:
		return "", "", false
	case 1:
		return matches[0], fmt.Sprintf("Matched via content hash %s", targetHash), true
	default:
		// Multiple lines share this hash — ambiguous, don't guess.
		return "", "", false
	}
}

// unifiedDiff generates a simple unified diff between two strings.
func unifiedDiff(filePath, oldContent, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")

	// Find the first differing line.
	start := 0
	for start < len(oldLines) && start < len(newLines) && oldLines[start] == newLines[start] {
		start++
	}

	// Find the last differing line (from the end).
	endOld := len(oldLines) - 1
	endNew := len(newLines) - 1
	for endOld > start && endNew > start && oldLines[endOld] == newLines[endNew] {
		endOld--
		endNew--
	}

	// Build the diff with some context.
	contextLines := 3
	diffStart := start - contextLines
	if diffStart < 0 {
		diffStart = 0
	}
	diffEndOld := endOld + contextLines
	if diffEndOld >= len(oldLines) {
		diffEndOld = len(oldLines) - 1
	}
	diffEndNew := endNew + contextLines
	if diffEndNew >= len(newLines) {
		diffEndNew = len(newLines) - 1
	}

	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n", filePath)
	fmt.Fprintf(&b, "+++ %s\n", filePath)
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n",
		diffStart+1, diffEndOld-diffStart+1,
		diffStart+1, diffEndNew-diffStart+1)

	// Context before.
	for i := diffStart; i < start; i++ {
		fmt.Fprintf(&b, " %s\n", oldLines[i])
	}
	// Removed lines.
	for i := start; i <= endOld; i++ {
		fmt.Fprintf(&b, "-%s\n", oldLines[i])
	}
	// Added lines.
	for i := start; i <= endNew; i++ {
		fmt.Fprintf(&b, "+%s\n", newLines[i])
	}
	// Context after.
	afterStart := endOld + 1
	afterEnd := diffEndOld
	if afterStart <= afterEnd && afterStart < len(oldLines) {
		for i := afterStart; i <= afterEnd && i < len(oldLines); i++ {
			fmt.Fprintf(&b, " %s\n", oldLines[i])
		}
	}

	return b.String()
}
