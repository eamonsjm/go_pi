package tools

import (
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Package-level compiled regexps (avoid recompiling on every call).
var (
	buildErrorPattern   = regexp.MustCompile(`^([^:]+\.go):(\d+):(\d+): (.+)$`)
	buildSummaryPattern = regexp.MustCompile(`(cannot find|undefined|no such file|type mismatch)`)
	gitCommitPattern    = regexp.MustCompile(`^commit ([a-f0-9]+)`)
	gitAuthorPattern    = regexp.MustCompile(`^Author:\s*(.+)`)
	linterFilePattern   = regexp.MustCompile(`^([^:]+):(\d+)?:?(\d+)?: (.+)$`)
)

// CompressionLevel defines the intensity of compression.
type CompressionLevel int

const (
	CompressionLow CompressionLevel = iota
	CompressionMedium
	CompressionHigh
)

// MetricsRecorder is the interface for recording compression metrics.
type MetricsRecorder interface {
	Record(category CommandCategory, originalSize, compressedSize int, duration time.Duration)
}

// Compile-time interface check.
var _ Hook = (*GoTestAggregator)(nil)

// GoTestAggregator compresses Go test output while preserving critical information.
type GoTestAggregator struct {
	level   CompressionLevel
	metrics MetricsRecorder
}

// NewGoTestAggregator creates a test output aggregator.
func NewGoTestAggregator(level CompressionLevel, metrics MetricsRecorder) *GoTestAggregator {
	return &GoTestAggregator{level: level, metrics: metrics}
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

// testScanResult holds the output of scanning test lines.
type testScanResult struct {
	lines       []string
	hasFailures bool
	passSummary string
	failSummary string
}

func (a *GoTestAggregator) compress(output string) string {
	lines := strings.Split(output, "\n")
	scan := a.scanTestOutput(lines)

	if scan.hasFailures && scan.failSummary != "" {
		scan.lines = append(scan.lines, scan.failSummary)
	} else if scan.passSummary != "" {
		scan.lines = append(scan.lines, scan.passSummary)
	}

	compressed := strings.Join(scan.lines, "\n")
	a.metrics.Record(CategoryTest, len(output), len(compressed), 0)
	return compressed
}

// scanTestOutput classifies each test output line and collects the relevant ones.
func (a *GoTestAggregator) scanTestOutput(lines []string) testScanResult {
	result := testScanResult{lines: make([]string, 0, len(lines))}
	seenTests := make(map[string]bool)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Capture package summary line (ok/FAIL)
		if strings.HasPrefix(line, "FAIL\t") {
			result.failSummary = line
			result.hasFailures = true
			continue
		}
		if strings.HasPrefix(line, "ok\t") {
			result.passSummary = line
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
				result.lines = append(result.lines, line)
				seenTests[line] = true
				result.hasFailures = true
			}
			continue
		}

		// Skip PASS: lines unless compressed heavily
		if strings.HasPrefix(line, "--- PASS:") {
			if a.level == CompressionLow {
				result.lines = append(result.lines, line)
			}
			continue
		}

		// Keep FAIL: lines (test failures)
		if strings.HasPrefix(line, "--- FAIL:") {
			if !seenTests[line] {
				result.lines = append(result.lines, line)
				seenTests[line] = true
			}
			continue
		}

		// Keep error context lines (file:line references, panic, etc.)
		if strings.Contains(line, ".go:") || strings.Contains(line, "panic") ||
			strings.Contains(line, "Error:") || strings.Contains(line, "error:") {
			result.lines = append(result.lines, line)
			continue
		}

		// Keep assertion failures and test context
		if strings.Contains(line, "expected") || strings.Contains(line, "assertion") ||
			strings.Contains(line, "got ") || strings.Contains(line, "want ") {
			if a.level != CompressionHigh {
				result.lines = append(result.lines, line)
			}
			continue
		}
	}

	return result
}

// GoBuildErrorExtractor pulls out build errors from verbose output.
type GoBuildErrorExtractor struct {
	level   CompressionLevel
	metrics MetricsRecorder
}

// NewGoBuildErrorExtractor creates a build error extractor.
func NewGoBuildErrorExtractor(level CompressionLevel, metrics MetricsRecorder) *GoBuildErrorExtractor {
	return &GoBuildErrorExtractor{level: level, metrics: metrics}
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
	return e.extract(result), err
}

func (e *GoBuildErrorExtractor) extract(output string) string {
	lines := strings.Split(output, "\n")
	fileErrors, summaryErrors := parseBuildErrors(lines)
	result := formatBuildErrors(fileErrors, summaryErrors, e.level)

	compressed := strings.Join(result, "\n")
	e.metrics.Record(CategoryBuild, len(output), len(compressed), 0)
	return compressed
}

// parseBuildErrors scans build output lines and returns deduplicated file-level
// errors and summary errors.
func parseBuildErrors(lines []string) (fileErrors, summaryErrors []string) {
	fileErrors = make([]string, 0, len(lines))
	summaryErrors = make([]string, 0)
	seenErrors := make(map[string]bool)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
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
		if matches := buildErrorPattern.FindStringSubmatch(line); matches != nil {
			file := matches[1]
			lineNo := matches[2]
			msg := matches[4]

			errKey := file + ":" + lineNo + ":" + msg[:min(len(msg), 40)]
			if !seenErrors[errKey] {
				seenErrors[errKey] = true
				fileErrors = append(fileErrors, line)
			}
			continue
		}

		// Keep critical summary errors
		if buildSummaryPattern.MatchString(line) {
			if !seenErrors[line] {
				seenErrors[line] = true
				summaryErrors = append(summaryErrors, line)
			}
		}
	}

	return fileErrors, summaryErrors
}

// formatBuildErrors selects which errors to include based on compression level.
func formatBuildErrors(fileErrors, summaryErrors []string, level CompressionLevel) []string {
	result := make([]string, 0, len(fileErrors)+len(summaryErrors))

	if level == CompressionHigh {
		seenFiles := make(map[string]bool)
		for _, errLine := range fileErrors {
			if matches := buildErrorPattern.FindStringSubmatch(errLine); matches != nil {
				file := matches[1]
				if !seenFiles[file] {
					result = append(result, errLine)
					seenFiles[file] = true
				}
			}
		}
		if len(result) == 0 {
			result = summaryErrors
		}
	} else {
		result = fileErrors
		result = append(result, summaryErrors...)
	}

	return result
}

// GitLogCompactor reduces git log output size.
type GitLogCompactor struct {
	level   CompressionLevel
	metrics MetricsRecorder
}

// NewGitLogCompactor creates a git log compactor.
func NewGitLogCompactor(level CompressionLevel, metrics MetricsRecorder) *GitLogCompactor {
	return &GitLogCompactor{level: level, metrics: metrics}
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
	result := c.classifyLogLines(lines)
	result = deduplicateBlankLines(result)

	compressed := strings.Join(result, "\n")
	c.metrics.Record(CategoryGit, len(output), len(compressed), 0)
	return compressed
}

// classifyLogLines walks git log output and selects lines to keep based on
// the compression level.
func (c *GitLogCompactor) classifyLogLines(lines []string) []string {
	result := make([]string, 0, len(lines))
	var inMessage bool
	var messageLines int

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			inMessage = false
			continue
		}

		// Extract and compress commit hash
		if matches := gitCommitPattern.FindStringSubmatch(line); matches != nil {
			hash := matches[1]
			if c.level == CompressionHigh {
				result = append(result, "commit "+hash[:7])
			} else {
				result = append(result, line)
			}
			inMessage = true
			messageLines = 0
			continue
		}

		// Compress Author line
		if matches := gitAuthorPattern.FindStringSubmatch(line); matches != nil {
			author := matches[1]
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
				result = append(result, compactedLine)
			} else if messageLines == 2 && c.level == CompressionLow {
				result = append(result, compactedLine)
			} else if messageLines <= 2 && c.level == CompressionMedium {
				result = append(result, compactedLine)
			}
			continue
		}

		// Skip merge/branch decorations
		if strings.HasPrefix(line, "  (") || strings.HasPrefix(line, " ") {
			continue
		}

		inMessage = false
	}

	return result
}

// deduplicateBlankLines collapses consecutive blank lines into a single blank.
func deduplicateBlankLines(lines []string) []string {
	result := make([]string, 0, len(lines))
	var lastWasBlank bool
	for _, line := range lines {
		if line == "" {
			if !lastWasBlank {
				result = append(result, line)
				lastWasBlank = true
			}
		} else {
			result = append(result, line)
			lastWasBlank = false
		}
	}
	return result
}

// LinterOutputGrouper aggregates linter output by file.
type LinterOutputGrouper struct {
	level   CompressionLevel
	metrics MetricsRecorder
}

// NewLinterOutputGrouper creates a linter output grouper.
func NewLinterOutputGrouper(level CompressionLevel, metrics MetricsRecorder) *LinterOutputGrouper {
	return &LinterOutputGrouper{level: level, metrics: metrics}
}

func (g *LinterOutputGrouper) BeforeExecute(ctx context.Context, toolName string, params map[string]any) error {
	return nil
}

func (g *LinterOutputGrouper) AfterExecute(ctx context.Context, toolName string, params map[string]any, result string, err error) (string, error) {
	if !strings.Contains(result, "error") && !strings.Contains(result, "warning") {
		return result, err
	}
	return g.group(result), err
}

func (g *LinterOutputGrouper) group(output string) string {
	lines := strings.Split(output, "\n")
	errorsByFile := parseLinterErrors(lines)
	result := g.formatLinterOutput(errorsByFile)

	compressed := strings.Join(result, "\n")
	g.metrics.Record(CategoryOther, len(output), len(compressed), 0)
	return compressed
}

// parseLinterErrors scans linter output lines and returns deduplicated errors
// grouped by file.
func parseLinterErrors(lines []string) map[string][]string {
	errorsByFile := make(map[string][]string)
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

		if matches := linterFilePattern.FindStringSubmatch(line); matches != nil {
			file := matches[1]
			lineNo := matches[2]
			msg := matches[4]

			errKey := file + ":" + lineNo + ":" + msg[:min(len(msg), 30)]
			if !seenErrors[errKey] {
				seenErrors[errKey] = true
				errorsByFile[file] = append(errorsByFile[file], line)
			}
		}
	}

	return errorsByFile
}

// formatLinterOutput builds grouped output from parsed errors, applying
// compression limits.
func (g *LinterOutputGrouper) formatLinterOutput(errorsByFile map[string][]string) []string {
	fileList := make([]string, 0, len(errorsByFile))
	for file := range errorsByFile {
		fileList = append(fileList, file)
	}
	sort.Strings(fileList)

	if g.level == CompressionHigh && len(fileList) > 5 {
		fileList = fileList[:5]
	}

	var result []string
	for _, file := range fileList {
		errors := errorsByFile[file]
		result = append(result, file)

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

	return result
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

