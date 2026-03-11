package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// WriteTool writes content to a file, creating parent directories as needed.
type WriteTool struct{}

func (t *WriteTool) Name() string { return "write" }

func (t *WriteTool) Description() string {
	return "Writes content to a file on the filesystem. Creates parent directories if they don't exist. " +
		"Overwrites the file if it already exists."
}

func (t *WriteTool) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to write to the file",
			},
		},
		"required": []string{"file_path", "content"},
	}
}

func (t *WriteTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	filePath, ok := params["file_path"].(string)
	if !ok || filePath == "" {
		return "", fmt.Errorf("file_path is required and must be a string")
	}

	content, ok := params["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required and must be a string")
	}

	// Create parent directories if needed.
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("cannot create directory %s: %v", dir, err)
	}

	// Write the file.
	data := []byte(content)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", fmt.Errorf("cannot write file: %v", err)
	}

	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(data), filePath), nil
}
