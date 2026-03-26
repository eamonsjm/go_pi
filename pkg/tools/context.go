package tools

import (
	"context"
	"log"
	"os"
	"path/filepath"
)

type workingDirKey struct{}

// ContextWithWorkingDir returns a new context with the given working directory.
func ContextWithWorkingDir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, workingDirKey{}, dir)
}

// WorkingDirFromContext returns the working directory from the context,
// or the process's current directory if none is set.
func WorkingDirFromContext(ctx context.Context) string {
	if dir, ok := ctx.Value(workingDirKey{}).(string); ok && dir != "" {
		return dir
	}
	dir, err := os.Getwd()
	if err != nil {
		log.Printf("tools: getwd: %v", err)
	}
	return dir
}

// ResolvePath resolves a path against the working directory from the context.
// Absolute paths are returned as-is. Relative paths are joined with the
// context's working directory.
func ResolvePath(ctx context.Context, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(WorkingDirFromContext(ctx), path)
}
