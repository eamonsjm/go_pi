package tools

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// CompressionLevel defines the intensity of compression.
type CompressionLevel int

const (
	CompressionLow CompressionLevel = iota
	CompressionMedium
	CompressionHigh
)

// GoTestAggregator compresses Go test output while preserving critical information.
type GoTestAggregator struct {
	level CompressionLevel
}

// NewGoTestAggregator creates a test output aggregator.
func NewGoTestAggregator(level CompressionLevel) *GoTestAggregator {
	return &GoTestAggregator{level: level}
}

func (a *GoTestAggregator) BeforeExecute(ctx context.Context, toolName string, params map[string]any) error {
	return nil
}

func (a *GoTestAggregator) AfterExecute(ctx context.Context, toolName string, params map[string]any, result string, err error) (string, error) {
	if err != nil {
		return result, err
	}
	// Only apply to test output
	if !strings.Contains(result, " --- ") && !strings.Contains(result, "ok\t") && !strings.Contains(result, "FAIL\t") {
		return result, err
	}
	return a.compress(result), nil
}

func (a *GoTestAggregator) compress(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	seenTests := make(map[string]bool)
	var passSummary, failSummary string
	hasFailures := false

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Capture package summary line (ok/FAIL)
		if strings.HasPrefix(line, "FAIL\t") {
			failSummary = line
			hasFailures = true
			continue
		}
		if strings.HasPrefix(line, "ok\t") {
			passSummary = line
			continue
		}

		// Skip RUN lines - they're noise
		if strings.HasPrefix(line, "=== RUN") {
			continue
		}

		// Skip PASS lines unless there's an error context - they're redundant
		if strings.HasPrefix(line, "=== PASS") {
			continue
		}

		// Only keep FAIL lines
		if strings.HasPrefix(line, "=== FAIL") {
			if !seenTests[line] {
				result = append(result, line)
				seenTests[line] = true
				hasFailures = true
			}
			continue
		}

		// Skip PASS: lines unless compressed heavily
		if strings.HasPrefix(line, "--- PASS:") {
			if a.level == CompressionLow {
				result = append(result, line)
			}
			continue
		}

		// Keep FAIL: lines (test failures)
		if strings.HasPrefix(line, "--- FAIL:") {
			if !seenTests[line] {
				result = append(result, line)
				seenTests[line] = true
			}
			continue
		}

		// Keep error context lines (file:line references, panic, etc.)
		if strings.Contains(line, ".go:") || strings.Contains(line, "panic") ||
			strings.Contains(line, "Error:") || strings.Contains(line, "error:") {
			result = append(result, line)
			continue
		}

		// Keep assertion failures and test context
		if strings.Contains(line, "expected") || strings.Contains(line, "assertion") ||
			strings.Contains(line, "got ") || strings.Contains(line, "want ") {
			if a.level != CompressionHigh {
				result = append(result, line)
			}
			continue
		}
	}

	// Add summary at the end
	if hasFailures && failSummary != "" {
		result = append(result, failSummary)
	} else if passSummary != "" {
		result = append(result, passSummary)
	}

	compressed := strings.Join(result, "\n")
	GlobalMetrics.Record(CategoryTest, len(output), len(compressed), 0)
	return compressed
}

// GoBuildErrorExtractor pulls out build errors from verbose output.
type GoBuildErrorExtractor struct {
	level CompressionLevel
}

// NewGoBuildErrorExtractor creates a build error extractor.
func NewGoBuildErrorExtractor(level CompressionLevel) *GoBuildErrorExtractor {
	return &GoBuildErrorExtractor{level: level}
}

func (e *GoBuildErrorExtractor) BeforeExecute(ctx context.Context, toolName string, params map[string]any) error {
	return nil
}

func (e *GoBuildErrorExtractor) AfterExecute(ctx context.Context, toolName string, params map[string]any, result string, err error) (string, error) {
	if err == nil {
		return result, err
	}
	// Only apply to build-related output
	if !strings.Contains(result, ".go:") && !strings.Contains(result, "cannot find") {
		return result, err
	}
	return e.extract(result), nil
}

func (e *GoBuildErrorExtractor) extract(output string) string {
	lines := strings.Split(output, "\n")
	var fileErrors []string
	var summaryErrors []string
	seenErrors := make(map[string]bool)

	// Patterns for Go build errors
	errorPattern := regexp.MustCompile(`^([^:]+\.go):(\d+):(\d+): (.+)$`)
	summaryPattern := regexp.MustCompile(`(cannot find|undefined|no such file|type mismatch)`)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || len(line) == 0 {
			continue
		}

		// Skip noise: comments, go version info, "go get" suggestions
		if strings.HasPrefix(line, "go:") || strings.Contains(line, "go get") ||
			strings.HasPrefix(line, "#") && strings.Contains(line, "github.com") {
			continue
		}

		// Skip "FAIL" and redundant summary lines
		if strings.HasPrefix(line, "FAIL") || line == "FAIL" {
			continue
		}

		// Match detailed error format: file.go:line:col: message
		if matches := errorPattern.FindStringSubmatch(line); matches != nil {
			file := matches[1]
			lineNo := matches[2]
			msg := matches[4]

			errKey := file + ":" + lineNo + ":" + msg[:min(len(msg), 40)] // Key by file, line, and first 40 chars of message
			if !seenErrors[errKey] {
				seenErrors[errKey] = true
				fileErrors = append(fileErrors, line)
			}
			continue
		}

		// Keep critical summary errors
		if summaryPattern.MatchString(line) {
			if !seenErrors[line] {
				seenErrors[line] = true
				summaryErrors = append(summaryErrors, line)
			}
		}
	}

	var result []string

	// At high compression, only show first unique error per file prefix
	if e.level == CompressionHigh {
		seenFiles := make(map[string]bool)
		for _, errLine := range fileErrors {
			if matches := errorPattern.FindStringSubmatch(errLine); matches != nil {
				file := matches[1]
				if !seenFiles[file] {
					result = append(result, errLine)
					seenFiles[file] = true
				}
			}
		}
		// Add summary errors if no file errors
		if len(result) == 0 {
			result = summaryErrors
		}
	} else {
		// Medium/Low compression: keep all unique errors
		result = fileErrors
		result = append(result, summaryErrors...)
	}

	compressed := strings.Join(result, "\n")
	GlobalMetrics.Record(CategoryBuild, len(output), len(compressed), 0)
	return compressed
}

// GitLogCompactor reduces git log output size.
type GitLogCompactor struct {
	level CompressionLevel
}

// NewGitLogCompactor creates a git log compactor.
func NewGitLogCompactor(level CompressionLevel) *GitLogCompactor {
	return &GitLogCompactor{level: level}
}

func (c *GitLogCompactor) BeforeExecute(ctx context.Context, toolName string, params map[string]any) error {
	return nil
}

func (c *GitLogCompactor) AfterExecute(ctx context.Context, toolName string, params map[string]any, result string, err error) (string, error) {
	if err != nil {
		return result, err
	}
	// Only apply to git log output
	if !strings.Contains(result, "commit ") {
		return result, err
	}
	return c.compact(result), nil
}

func (c *GitLogCompactor) compact(output string) string {
	lines := strings.Split(output, "\n")
	var result []string
	var inMessage bool
	var messageLines int
	var currentCommit string

	commitPattern := regexp.MustCompile(`^commit ([a-f0-9]+)`)
	authorPattern := regexp.MustCompile(`^Author:\s*(.+)`)

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Skip entirely blank lines
		if trimmed == "" {
			inMessage = false
			continue
		}

		// Extract and compress commit hash
		if matches := commitPattern.FindStringSubmatch(line); matches != nil {
			hash := matches[1]
			// At high compression, only keep short hash
			if c.level == CompressionHigh {
				currentCommit = hash[:7]
				result = append(result, "commit "+currentCommit)
			} else {
				result = append(result, line)
			}
			inMessage = true
			messageLines = 0
			continue
		}

		// Compress Author line
		if matches := authorPattern.FindStringSubmatch(line); matches != nil {
			author := matches[1]
			// At high compression, just show name
			if c.level == CompressionHigh {
				result = append(result, author)
			} else {
				result = append(result, line)
			}
			continue
		}

		// Skip Date line at high compression
		if strings.HasPrefix(trimmed, "Date:") {
			if c.level != CompressionHigh {
				result = append(result, trimmed)
			}
			continue
		}

		// Handle commit message
		if inMessage && strings.HasPrefix(line, "    ") {
			messageLines++
			compactedLine := strings.TrimSpace(line)

			if messageLines == 1 {
				// Always keep first line of message
				result = append(result, compactedLine)
			} else if messageLines == 2 && c.level == CompressionLow {
				// Low: keep second line
				result = append(result, compactedLine)
			} else if messageLines <= 2 && c.level == CompressionMedium {
				// Medium: keep first two lines
				result = append(result, compactedLine)
			}
			// High compression: only keep first line, skip rest
			continue
		}

		// Skip merge/branch decorations (lines starting with spaces or parentheses)
		if strings.HasPrefix(line, "  (") || strings.HasPrefix(line, " ") {
			continue
		}

		inMessage = false
	}

	// Remove duplicate blank lines that may have been created
	var finalResult []string
	var lastWasBlank bool
	for _, line := range result {
		if line == "" {
			if !lastWasBlank {
				finalResult = append(finalResult, line)
				lastWasBlank = true
			}
		} else {
			finalResult = append(finalResult, line)
			lastWasBlank = false
		}
	}

	compressed := strings.Join(finalResult, "\n")
	GlobalMetrics.Record(CategoryGit, len(output), len(compressed), 0)
	return compressed
}

// LinterOutputGrouper aggregates linter output by file.
type LinterOutputGrouper struct {
	level CompressionLevel
}

// NewLinterOutputGrouper creates a linter output grouper.
func NewLinterOutputGrouper(level CompressionLevel) *LinterOutputGrouper {
	return &LinterOutputGrouper{level: level}
}

func (g *LinterOutputGrouper) BeforeExecute(ctx context.Context, toolName string, params map[string]any) error {
	return nil
}

func (g *LinterOutputGrouper) AfterExecute(ctx context.Context, toolName string, params map[string]any, result string, err error) (string, error) {
	if err == nil && !strings.Contains(result, "error") && !strings.Contains(result, "warning") {
		return result, err
	}
	return g.group(result), nil
}

func (g *LinterOutputGrouper) group(output string) string {
	lines := strings.Split(output, "\n")

	// Pattern for linter output: file:line:col: message or file: error
	filePattern := regexp.MustCompile(`^([^:]+):(\d+)?:?(\d+)?: (.+)$`)

	errorsByFile := make(map[string][]string)
	errorCounts := make(map[string]int)
	seenErrors := make(map[string]bool)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Skip summary lines like "N errors found", "warnings:", etc
		if strings.Contains(line, " errors found") || strings.Contains(line, " warnings") ||
			strings.HasSuffix(line, "errors") || strings.HasSuffix(line, "warnings") {
			continue
		}

		// Try to parse as file:line:col: error format
		if matches := filePattern.FindStringSubmatch(line); matches != nil {
			file := matches[1]
			lineNo := matches[2]
			msg := matches[4]

			// Deduplicate: same file, line, and message
			errKey := file + ":" + lineNo + ":" + msg[:min(len(msg), 30)]
			if !seenErrors[errKey] {
				seenErrors[errKey] = true
				errorsByFile[file] = append(errorsByFile[file], line)
				errorCounts[file]++
			}
		}
	}

	// Build result with files grouped
	var result []string

	// Sort files for consistent output (using a slice to maintain order)
	var fileList []string
	for file := range errorsByFile {
		fileList = append(fileList, file)
	}

	// At high compression, only show files with most errors
	if g.level == CompressionHigh && len(fileList) > 5 {
		// Compress: show only first 5 files
		fileList = fileList[:5]
	}

	for _, file := range fileList {
		errors := errorsByFile[file]
		result = append(result, file)

		// Limit errors per file based on compression level
		maxErrors := len(errors)
		if g.level == CompressionHigh && maxErrors > 2 {
			maxErrors = 2
		} else if g.level == CompressionMedium && maxErrors > 5 {
			maxErrors = 5
		}

		for i := 0; i < maxErrors && i < len(errors); i++ {
			result = append(result, "  "+errors[i])
		}

		if g.level == CompressionHigh && len(errors) > maxErrors {
			result = append(result, "  ... and "+strconv.Itoa(len(errors)-maxErrors)+" more")
		}
	}

	compressed := strings.Join(result, "\n")
	GlobalMetrics.Record(CategoryOther, len(output), len(compressed), 0)
	return compressed
}

// CompressionConfig allows configuring compression per tool.
type CompressionConfig struct {
	mu     sync.RWMutex
	levels map[string]CompressionLevel
}

// NewCompressionConfig creates a new configuration.
func NewCompressionConfig() *CompressionConfig {
	return &CompressionConfig{
		levels: make(map[string]CompressionLevel),
	}
}

// SetLevel sets compression level for a tool.
func (cc *CompressionConfig) SetLevel(tool string, level CompressionLevel) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.levels[tool] = level
}

// GetLevel gets compression level for a tool (defaults to Medium).
func (cc *CompressionConfig) GetLevel(tool string) CompressionLevel {
	cc.mu.RLock()
	defer cc.mu.RUnlock()
	if level, exists := cc.levels[tool]; exists {
		return level
	}
	return CompressionMedium
}

// GlobalCompressionConfig is the package-level configuration.
var GlobalCompressionConfig = NewCompressionConfig()
