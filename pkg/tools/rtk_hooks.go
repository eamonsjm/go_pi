package tools

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"
)

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

// Before fires all registered "before" hooks.
func (r *HookRegistry) Before(ctx context.Context, toolName string, params map[string]any) error {
	r.mu.Lock()
	hooks := r.hooks
	r.mu.Unlock()

	for _, h := range hooks {
		if err := h.BeforeExecute(ctx, toolName, params); err != nil {
			return err
		}
	}
	return nil
}

// After fires all registered "after" hooks, chaining result modifications.
func (r *HookRegistry) After(ctx context.Context, toolName string, params map[string]any, result string, err error) (string, error) {
	r.mu.Lock()
	hooks := r.hooks
	r.mu.Unlock()

	for _, h := range hooks {
		result, err = h.AfterExecute(ctx, toolName, params, result, err)
	}
	return result, err
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
	// Match ANSI escape sequences: ESC [ ... (letters)
	ansiRe := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	return ansiRe.ReplaceAllString(s, "")
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
	var result []string

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
	mu           sync.Mutex
	Commands     map[CommandCategory]*CommandMetrics
	totalTokens  int64
	savedTokens  int64
}

// CommandMetrics holds stats for a specific command category.
type CommandMetrics struct {
	Count        int64
	TotalBytes   int64
	CompressedBytes int64
	TotalTime    time.Duration
	AvgTime      time.Duration
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

// GlobalMetrics is the package-level metrics collector.
var GlobalMetrics = NewMetrics()
