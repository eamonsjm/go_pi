package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxGlobResults = 1000

// GlobTool finds files matching a glob pattern.
type GlobTool struct{}

func (t *GlobTool) Name() string { return "glob" }

func (t *GlobTool) Description() string {
	return "Finds files matching a glob pattern. Supports ** for recursive directory matching. " +
		"Skips hidden directories (starting with .) by default. Returns sorted file paths."
}

func (t *GlobTool) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Glob pattern to match files (e.g. \"**/*.go\", \"src/**/*.ts\")",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Base directory to search in. Defaults to current working directory.",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *GlobTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	pattern, ok := params["pattern"].(string)
	if !ok || pattern == "" {
		return "", fmt.Errorf("pattern is required and must be a string")
	}

	basePath := "."
	if v, ok := getString(params, "path"); ok && v != "" {
		basePath = v
	}

	// Resolve to absolute path.
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path: %v", err)
	}

	info, err := os.Stat(absBase)
	if err != nil {
		return "", fmt.Errorf("path does not exist: %v", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", absBase)
	}

	var matches []string

	// Check if the pattern contains ** for recursive matching.
	if strings.Contains(pattern, "**") {
		matches, err = globRecursive(ctx, absBase, pattern)
	} else {
		// Simple glob - join base path with pattern.
		fullPattern := filepath.Join(absBase, pattern)
		matches, err = filepath.Glob(fullPattern)
	}
	if err != nil {
		return "", fmt.Errorf("glob error: %v", err)
	}

	sort.Strings(matches)

	if len(matches) == 0 {
		return "No files matched the pattern.", nil
	}

	if len(matches) > maxGlobResults {
		matches = matches[:maxGlobResults]
		var b strings.Builder
		b.WriteString(strings.Join(matches, "\n"))
		fmt.Fprintf(&b, "\n\n... truncated to %d results.", maxGlobResults)
		return b.String(), nil
	}

	return strings.Join(matches, "\n"), nil
}

// globRecursive handles ** patterns by walking the directory tree.
func globRecursive(ctx context.Context, basePath, pattern string) ([]string, error) {
	var matches []string

	// Split the pattern to handle ** segments.
	// We walk the tree and match each file against the pattern.
	err := filepath.Walk(basePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip files we can't access.
		}

		// Check context cancellation.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Skip hidden directories (but not the base path itself).
		if info.IsDir() && path != basePath {
			name := filepath.Base(path)
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
		}

		// Get relative path from base.
		relPath, err := filepath.Rel(basePath, path)
		if err != nil {
			return nil
		}

		// Skip directories themselves (we only want files).
		if info.IsDir() {
			return nil
		}

		// Match against the pattern.
		if matchDoublestar(pattern, relPath) {
			matches = append(matches, path)
		}

		return nil
	})

	return matches, err
}

// matchDoublestar implements basic ** glob matching.
// Supports: *, **, and ? wildcards.
func matchDoublestar(pattern, name string) bool {
	// Normalize separators.
	pattern = filepath.ToSlash(pattern)
	name = filepath.ToSlash(name)
	return doMatch(pattern, name)
}

func doMatch(pattern, name string) bool {
	for len(pattern) > 0 {
		switch {
		case strings.HasPrefix(pattern, "**/"): // ** at start or middle
			pattern = pattern[3:]
			// ** matches zero or more path segments.
			// Try matching the rest of the pattern against every suffix of name.
			if doMatch(pattern, name) {
				return true
			}
			// Consume one path segment from name and retry.
			for i := 0; i < len(name); i++ {
				if name[i] == '/' {
					if doMatch(pattern, name[i+1:]) {
						return true
					}
				}
			}
			return false

		case pattern == "**": // ** at end
			return true

		case len(name) == 0:
			return false

		case pattern[0] == '*':
			pattern = pattern[1:]
			// * matches everything except /.
			for i := 0; i <= len(name); i++ {
				if doMatch(pattern, name[i:]) {
					return true
				}
				if i < len(name) && name[i] == '/' {
					break
				}
			}
			return false

		case pattern[0] == '?':
			if name[0] == '/' {
				return false
			}
			pattern = pattern[1:]
			name = name[1:]

		case pattern[0] == name[0]:
			pattern = pattern[1:]
			name = name[1:]

		default:
			return false
		}
	}
	return len(name) == 0
}
