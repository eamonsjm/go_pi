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

	// Read the file.
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("cannot read file: %v", err)
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

	// Try exact match first.
	var matchTarget string
	var normNote string

	count := strings.Count(content, oldString)
	switch {
	case count == 1:
		matchTarget = oldString
	case count > 1:
		return "", fmt.Errorf("old_string found %d times in %s. It must be unique. Provide more surrounding context to make the match unique.", count, filePath)
	default:
		// Exact match failed — try fuzzy matching with normalization.
		matched, normName, found := fuzzyFind(content, oldString)
		if !found {
			return "", fmt.Errorf("old_string not found in %s. Make sure the string matches exactly, including whitespace and indentation.", filePath)
		}
		matchTarget = matched
		normNote = fmt.Sprintf("Note: exact match failed. Matched via %s.", normName)
	}

	// Perform the replacement.
	newContent := strings.Replace(content, matchTarget, newString, 1)

	// Restore CRLF line endings if the file originally used them.
	if lineEnding == "\r\n" {
		newContent = strings.ReplaceAll(newContent, "\n", "\r\n")
	}

	// Re-prepend BOM if present.
	var output []byte
	if bom != nil {
		output = append(bom, []byte(newContent)...)
	} else {
		output = []byte(newContent)
	}

	// Preserve original file permissions.
	mode := os.FileMode(0644)
	if info, err := os.Stat(filePath); err == nil {
		mode = info.Mode()
	}

	// Write the file back.
	if err := os.WriteFile(filePath, output, mode); err != nil {
		return "", fmt.Errorf("cannot write file: %v", err)
	}

	// Generate a unified diff (use LF-normalized content for readable diffs).
	originalForDiff := content
	if lineEnding == "\r\n" {
		// Re-normalize for diff display.
		newContent = strings.ReplaceAll(newContent, "\r\n", "\n")
	}
	diff := unifiedDiff(filePath, originalForDiff, newContent)

	if normNote != "" {
		diff = normNote + "\n" + diff
	}
	return diff, nil
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
