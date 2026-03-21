package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxGrepMatches = 500
)

// GrepTool searches file contents using regular expressions.
type GrepTool struct{}

func (t *GrepTool) Name() string { return "grep" }

func (t *GrepTool) Description() string {
	return "Searches file contents using a regular expression pattern. Returns matching lines with " +
		"file paths and line numbers. Skips binary files and .git directories. Limits output to 500 matches."
}

func (t *GrepTool) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regular expression pattern to search for",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "File or directory to search in. Defaults to current working directory.",
			},
			"include": map[string]any{
				"type":        "string",
				"description": "Glob pattern to filter files (e.g. \"*.go\", \"*.{ts,tsx}\")",
			},
			"context_lines": map[string]any{
				"type":        "integer",
				"description": "Number of context lines to show before and after each match",
			},
		},
		"required": []string{"pattern"},
	}
}

func (t *GrepTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	patternStr, ok := params["pattern"].(string)
	if !ok || patternStr == "" {
		return "", fmt.Errorf("pattern is required and must be a string")
	}

	re, err := regexp.Compile(patternStr)
	if err != nil {
		return "", fmt.Errorf("invalid regex pattern: %w", err)
	}

	searchPath := "."
	if v, ok := getString(params, "path"); ok && v != "" {
		searchPath = v
	}

	absPath, err := filepath.Abs(searchPath)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path: %w", err)
	}

	include := ""
	if v, ok := getString(params, "include"); ok {
		include = v
	}

	contextLines := 0
	if v, ok := getInt(params, "context_lines"); ok {
		if v < 0 {
			v = 0
		}
		if v > 10 {
			v = 10
		}
		contextLines = v
	}

	var results []grepResult
	totalMatches := 0

	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("path does not exist: %w", err)
	}

	if info.IsDir() {
		err = filepath.Walk(absPath, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return nil
			}

			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			// Skip .git directories and other hidden directories.
			if fi.IsDir() {
				name := filepath.Base(path)
				if strings.HasPrefix(name, ".") && path != absPath {
					return filepath.SkipDir
				}
				return nil
			}

			// Skip non-regular files.
			if !fi.Mode().IsRegular() {
				return nil
			}

			// Apply include filter.
			if include != "" && !matchInclude(filepath.Base(path), include) {
				return nil
			}

			// Skip binary files.
			if isBinaryFile(path) {
				return nil
			}

			if totalMatches >= maxGrepMatches {
				return filepath.SkipAll
			}

			fileResults, n := searchFile(path, re, contextLines, maxGrepMatches-totalMatches)
			if len(fileResults) > 0 {
				results = append(results, fileResults...)
				totalMatches += n
			}

			return nil
		})
	} else {
		if isBinaryFile(absPath) {
			return "Binary file, skipping.", nil
		}
		fileResults, n := searchFile(absPath, re, contextLines, maxGrepMatches)
		results = fileResults
		totalMatches = n
	}

	if err != nil {
		return "", fmt.Errorf("search error: %w", err)
	}

	if len(results) == 0 {
		return "No matches found.", nil
	}

	var b strings.Builder
	for _, r := range results {
		if r.isContext {
			fmt.Fprintf(&b, "%s-%d-%s\n", r.file, r.line, r.text)
		} else {
			fmt.Fprintf(&b, "%s:%d:%s\n", r.file, r.line, r.text)
		}
	}

	if totalMatches >= maxGrepMatches {
		fmt.Fprintf(&b, "\n... results truncated at %d matches.\n", maxGrepMatches)
	}

	return b.String(), nil
}

type grepResult struct {
	file      string
	line      int
	text      string
	isContext bool
}

// searchFile searches a single file for regex matches.
func searchFile(path string, re *regexp.Regexp, contextLines, maxMatches int) ([]grepResult, int) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	defer func() { _ = f.Close() }()

	var allLines []string
	var matchLineNums []int

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 1024*1024) // up to 1MB lines
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		allLines = append(allLines, line)
		if re.MatchString(line) {
			matchLineNums = append(matchLineNums, lineNum)
			if len(matchLineNums) >= maxMatches {
				break
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, 0
	}

	if len(matchLineNums) == 0 {
		return nil, 0
	}

	// Build results with context.
	var results []grepResult
	emittedLines := make(map[int]bool)

	for _, matchLine := range matchLineNums {
		startLine := matchLine - contextLines
		if startLine < 1 {
			startLine = 1
		}
		endLine := matchLine + contextLines
		if endLine > len(allLines) {
			endLine = len(allLines)
		}

		for ln := startLine; ln <= endLine; ln++ {
			if emittedLines[ln] {
				continue
			}
			emittedLines[ln] = true
			results = append(results, grepResult{
				file:      path,
				line:      ln,
				text:      allLines[ln-1],
				isContext: ln != matchLine,
			})
		}
	}

	return results, len(matchLineNums)
}

// matchInclude checks if a filename matches an include glob pattern.
// Supports simple patterns like "*.go" and brace expansion like "*.{ts,tsx}".
func matchInclude(name, pattern string) bool {
	// Handle brace expansion: *.{ts,tsx} -> try *.ts and *.tsx
	if idx := strings.Index(pattern, "{"); idx != -1 {
		end := strings.Index(pattern[idx:], "}")
		if end != -1 {
			prefix := pattern[:idx]
			suffix := pattern[idx+end+1:]
			alternatives := strings.Split(pattern[idx+1:idx+end], ",")
			for _, alt := range alternatives {
				expanded := prefix + alt + suffix
				matched, _ := filepath.Match(expanded, name)
				if matched {
					return true
				}
			}
			return false
		}
	}

	matched, _ := filepath.Match(pattern, name)
	return matched
}
