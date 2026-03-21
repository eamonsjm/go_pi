package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const (
	defaultReadLimit   = 2000
	maxLineTruncateLen = 2000
)

// ReadTool reads file contents with line numbers.
type ReadTool struct{}

func (t *ReadTool) Name() string { return "read" }

func (t *ReadTool) Description() string {
	return "Reads a file from the filesystem. Returns file contents with line numbers (like cat -n). " +
		"Truncates to 2000 lines by default. Detects and refuses to display binary files."
}

func (t *ReadTool) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to read",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Line number to start reading from (1-based). Defaults to 1.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of lines to read. Defaults to 2000.",
			},
		},
		"required": []string{"file_path"},
	}
}

func (t *ReadTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	filePath, ok := params["file_path"].(string)
	if !ok || filePath == "" {
		return "", fmt.Errorf("file_path is required and must be a string")
	}

	offset := 1
	if v, ok := getInt(params, "offset"); ok {
		if v < 1 {
			return "", fmt.Errorf("offset must be >= 1, got %d", v)
		}
		offset = v
	}

	limit := defaultReadLimit
	if v, ok := getInt(params, "limit"); ok {
		if v < 1 {
			return "", fmt.Errorf("limit must be >= 1, got %d", v)
		}
		limit = v
	}

	// Check file exists and is not a directory.
	info, err := os.Stat(filePath)
	if err != nil {
		return "", fmt.Errorf("cannot read file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory, not a file. Use bash with 'ls' to list directory contents.", filePath)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("cannot read file: %w", err)
	}

	if isBinary(data) {
		return fmt.Sprintf("Binary file detected: %s (%d bytes). Cannot display binary content.", filePath, len(data)), nil
	}

	lines := strings.Split(string(data), "\n")
	// If the file ends with a newline, Split produces an extra empty element; remove it.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	totalLines := len(lines)

	// Apply offset (1-based).
	startIdx := offset - 1
	if startIdx >= totalLines {
		return fmt.Sprintf("File has %d lines, offset %d is beyond end of file.", totalLines, offset), nil
	}

	endIdx := startIdx + limit
	if endIdx > totalLines {
		endIdx = totalLines
	}

	var b strings.Builder
	lineNumWidth := len(fmt.Sprintf("%d", endIdx))
	for i := startIdx; i < endIdx; i++ {
		line := lines[i]
		if len(line) > maxLineTruncateLen {
			line = line[:maxLineTruncateLen] + "..."
		}
		hash := contentHash(line)
		fmt.Fprintf(&b, "%*d\t%s\t%s\n", lineNumWidth, i+1, hash, line)
	}

	if endIdx < totalLines {
		fmt.Fprintf(&b, "\n... (%d more lines not shown. Use offset/limit to read more.)\n", totalLines-endIdx)
	}

	return b.String(), nil
}
