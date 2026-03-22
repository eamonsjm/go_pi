package tools

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ansiPattern matches ANSI escape sequences (compiled once at package level).
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// Hook interface allows tools to be intercepted and modified.
type Hook interface {
	BeforeExecute(ctx context.Context, toolName string, params map[string]any) error
	AfterExecute(ctx context.Context, toolName string, params map[string]any, result string, err error) (string, error)
}

// HookRegistry manages a collection of hooks that fire on tool execution.
type HookRegistry struct {
	mu    sync.Mutex
	hooks []Hook
}

// NewHookRegistry creates a new hook registry.
func NewHookRegistry() *HookRegistry {
	return &HookRegistry{
		hooks: []Hook{},
	}
}

// Register adds a hook to the registry. Hooks fire in registration order.
func (r *HookRegistry) Register(h Hook) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hooks = append(r.hooks, h)
}

// Before fires all registered "before" hooks. A panicking hook is caught and
// returned as an error instead of crashing the agent process.
func (r *HookRegistry) Before(ctx context.Context, toolName string, params map[string]any) (retErr error) {
	r.mu.Lock()
	hooks := r.hooks
	r.mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("before-hook %q panicked: %v", toolName, r)
		}
	}()

	for _, h := range hooks {
		if err := h.BeforeExecute(ctx, toolName, params); err != nil {
			return fmt.Errorf("before-hook %q: %w", toolName, err)
		}
	}
	return nil
}

// After fires all registered "after" hooks, chaining result modifications. A
// panicking hook is caught and returned as an error instead of crashing the
// agent process.
func (r *HookRegistry) After(ctx context.Context, toolName string, params map[string]any, result string, err error) (retResult string, retErr error) {
	r.mu.Lock()
	hooks := r.hooks
	r.mu.Unlock()

	retResult = result
	retErr = err

	defer func() {
		if r := recover(); r != nil {
			retResult = ""
			retErr = fmt.Errorf("after-hook %q panicked: %v", toolName, r)
		}
	}()

	for _, h := range hooks {
		retResult, retErr = h.AfterExecute(ctx, toolName, params, retResult, retErr)
	}
	return retResult, retErr
}

// ANSIStripper removes ANSI escape sequences from output.
type ANSIStripper struct{}

func (s *ANSIStripper) BeforeExecute(ctx context.Context, toolName string, params map[string]any) error {
	return nil
}

func (s *ANSIStripper) AfterExecute(ctx context.Context, toolName string, params map[string]any, result string, err error) (string, error) {
	if err != nil {
		return result, err
	}
	if toolName != "bash" {
		return result, err
	}
	return stripANSI(result), nil
}

// stripANSI removes ANSI escape sequences using a regex pattern.
func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// Compressor collapses excess whitespace to reduce output size.
type Compressor struct{}

func (c *Compressor) BeforeExecute(ctx context.Context, toolName string, params map[string]any) error {
	return nil
}

func (c *Compressor) AfterExecute(ctx context.Context, toolName string, params map[string]any, result string, err error) (string, error) {
	if err != nil {
		return result, err
	}
	if toolName != "bash" {
		return result, err
	}
	return compressWhitespace(result), nil
}

// compressWhitespace collapses multiple spaces and blank lines.
func compressWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	result := make([]string, 0, len(lines))

	for _, line := range lines {
		// Collapse multiple spaces to single space, trim line
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			// Collapse internal whitespace
			compacted := strings.Join(strings.Fields(trimmed), " ")
			result = append(result, compacted)
		}
	}

	return strings.Join(result, "\n")
}

// CommandCategory represents the type of bash command.
type CommandCategory string

const (
	CategoryGit     CommandCategory = "git"
	CategoryDocker  CommandCategory = "docker"
	CategoryBuild   CommandCategory = "build"
	CategoryPackage CommandCategory = "package"
	CategoryTest    CommandCategory = "test"
	CategoryFile    CommandCategory = "file"
	CategoryOther   CommandCategory = "other"
)

// DetectCategory analyzes a bash command and returns its category.
func DetectCategory(cmd string) CommandCategory {
	cmd = strings.TrimSpace(cmd)
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return CategoryOther
	}

	base := parts[0]

	// Check for common command patterns
	switch {
	case strings.HasPrefix(base, "git"):
		return CategoryGit
	case strings.HasPrefix(base, "docker"):
		return CategoryDocker
	case strings.Contains(cmd, "make ") || strings.Contains(cmd, "cargo ") || strings.Contains(cmd, "go build"):
		return CategoryBuild
	case strings.Contains(cmd, "npm ") || strings.Contains(cmd, "pip ") || strings.Contains(cmd, "apt "):
		return CategoryPackage
	case strings.Contains(cmd, "test") || strings.Contains(cmd, "spec"):
		return CategoryTest
	case strings.HasPrefix(base, "ls") || strings.HasPrefix(base, "cat") || strings.HasPrefix(base, "mkdir"):
		return CategoryFile
	default:
		return CategoryOther
	}
}

// Metrics tracks command execution statistics.
type Metrics struct {
	mu          sync.Mutex
	Commands    map[CommandCategory]*CommandMetrics
	totalTokens int64
	savedTokens int64
}

// CommandMetrics holds stats for a specific command category.
type CommandMetrics struct {
	Count           int64
	TotalBytes      int64
	CompressedBytes int64
	TotalTime       time.Duration
	AvgTime         time.Duration
}

// NewMetrics creates a new metrics collector.
func NewMetrics() *Metrics {
	return &Metrics{
		Commands: make(map[CommandCategory]*CommandMetrics),
	}
}

// Record tracks a command execution.
func (m *Metrics) Record(category CommandCategory, originalSize, compressedSize int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.Commands[category]; !exists {
		m.Commands[category] = &CommandMetrics{}
	}

	cm := m.Commands[category]
	cm.Count++
	cm.TotalBytes += int64(originalSize)
	cm.CompressedBytes += int64(compressedSize)
	cm.TotalTime += duration
	if cm.Count > 0 {
		cm.AvgTime = time.Duration(int64(cm.TotalTime) / cm.Count)
	}

	m.totalTokens += int64(originalSize)
	m.savedTokens += int64(originalSize - compressedSize)
}

// RtkCommandTranslator detects rtk binary and translates commands to rtk equivalents.
type RtkCommandTranslator struct {
	mu              sync.Mutex
	rtkAvailable    bool
	mapping         map[string]string
	rewrittenCount  int64
	nativeCount     int64
	totalBytesSaved int64
}

// NewRtkCommandTranslator creates a new RTK command translator.
// It detects rtk availability and sets up command mappings.
func NewRtkCommandTranslator() *RtkCommandTranslator {
	t := &RtkCommandTranslator{
		mapping: makeCommandMapping(),
	}
	t.rtkAvailable = t.detectRtkBinary()
	return t
}

// detectRtkBinary checks if the rtk binary is available in PATH.
func (t *RtkCommandTranslator) detectRtkBinary() bool {
	_, err := exec.LookPath("rtk")
	return err == nil
}

// makeCommandMapping returns a map of native commands to rtk equivalents.
func makeCommandMapping() map[string]string {
	return map[string]string{
		"git":    "rtk git",
		"go":     "rtk go",
		"cargo":  "rtk cargo",
		"npm":    "rtk npm",
		"docker": "rtk docker",
	}
}

// BeforeExecute translates bash commands to rtk equivalents if rtk is available.
func (t *RtkCommandTranslator) BeforeExecute(ctx context.Context, toolName string, params map[string]any) error {
	if toolName != "bash" {
		return nil
	}

	cmd, ok := params["command"].(string)
	if !ok || cmd == "" {
		return nil
	}

	// Parse the command and check if it starts with a mapped command
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		t.recordMetric(cmd, false, 0)
		return nil
	}

	// Check if rtk is available and the first part is in our mapping
	if t.rtkAvailable {
		if rtkCmd, exists := t.mapping[parts[0]]; exists {
			// Replace the original command with rtk equivalent
			originalLen := len(cmd)
			rtkCommand := rtkCmd + " " + strings.Join(parts[1:], " ")
			params["command"] = rtkCommand

			// Track the rewrite
			savedBytes := originalLen - len(rtkCommand)
			t.recordMetric(cmd, true, int64(savedBytes))
			return nil
		}
	}

	// Record native (non-rewritten) command
	t.recordMetric(cmd, false, 0)
	return nil
}

// recordMetric tracks command rewriting statistics.
func (t *RtkCommandTranslator) recordMetric(cmd string, rewritten bool, bytesSaved int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if rewritten {
		t.rewrittenCount++
		t.totalBytesSaved += bytesSaved
	} else {
		t.nativeCount++
	}
}

// GetMetrics returns the current RTK translation metrics.
func (t *RtkCommandTranslator) GetMetrics() (rewritten, native int64, bytesSaved int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rewrittenCount, t.nativeCount, t.totalBytesSaved
}

// AfterExecute is a no-op for the RTK translator.
func (t *RtkCommandTranslator) AfterExecute(ctx context.Context, toolName string, params map[string]any, result string, err error) (string, error) {
	return result, err
}

// GetTotalTokens returns the total number of tokens processed.
func (m *Metrics) GetTotalTokens() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalTokens
}

// GetSavedTokens returns the total number of tokens saved by compression.
func (m *Metrics) GetSavedTokens() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.savedTokens
}

// GetCommandMetrics returns a copy of the command metrics map.
func (m *Metrics) GetCommandMetrics() map[CommandCategory]*CommandMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Deep copy: copy structs by value to avoid sharing mutable pointers
	result := make(map[CommandCategory]*CommandMetrics, len(m.Commands))
	for k, v := range m.Commands {
		cp := *v
		result[k] = &cp
	}
	return result
}

// RegisterDefaultHooks creates and registers all standard compression hooks.
func RegisterDefaultHooks(registry *HookRegistry, config *CompressionConfig, metrics *Metrics) {
	// Always strip ANSI codes first
	registry.Register(&ANSIStripper{})

	// Register language-specific compressors
	registry.Register(NewGoTestAggregator(config.GetLevel("go-test"), metrics))
	registry.Register(NewGoBuildErrorExtractor(config.GetLevel("go-build"), metrics))
	registry.Register(NewGitLogCompactor(config.GetLevel("git-log"), metrics))
	registry.Register(NewLinterOutputGrouper(config.GetLevel("linter"), metrics))

	// Generic compression as fallback
	registry.Register(&Compressor{})
}
