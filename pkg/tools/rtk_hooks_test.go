package tools

import (
	"context"
	"testing"
)

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no ANSI codes",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "simple color code",
			input:    "\x1b[31mred\x1b[0m",
			expected: "red",
		},
		{
			name:     "multiple color codes",
			input:    "\x1b[1;32mgreen\x1b[0m text \x1b[33myellow\x1b[0m",
			expected: "green text yellow",
		},
		{
			name:     "cursor movement",
			input:    "start\x1b[2Aback two lines",
			expected: "startback two lines",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := stripANSI(tt.input)
			if result != tt.expected {
				t.Errorf("stripANSI(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCompressWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no whitespace",
			input:    "hello world",
			expected: "hello world",
		},
		{
			name:     "multiple spaces",
			input:    "hello    world",
			expected: "hello world",
		},
		{
			name:     "blank lines",
			input:    "line1\n\n\nline2",
			expected: "line1\nline2",
		},
		{
			name:     "leading/trailing spaces",
			input:    "  hello  world  ",
			expected: "hello world",
		},
		{
			name:     "mixed whitespace",
			input:    "line1  \n\n  line2\t\tline3",
			expected: "line1\nline2 line3",
		},
		{
			name:     "separated blank lines",
			input:    "Error  \n\n  Message",
			expected: "Error\nMessage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compressWhitespace(tt.input)
			if result != tt.expected {
				t.Errorf("compressWhitespace(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestDetectCategory(t *testing.T) {
	tests := []struct {
		name     string
		cmd      string
		expected CommandCategory
	}{
		{
			name:     "git command",
			cmd:      "git status",
			expected: CategoryGit,
		},
		{
			name:     "docker command",
			cmd:      "docker ps",
			expected: CategoryDocker,
		},
		{
			name:     "make build",
			cmd:      "make build",
			expected: CategoryBuild,
		},
		{
			name:     "go build",
			cmd:      "go build ./...",
			expected: CategoryBuild,
		},
		{
			name:     "npm package",
			cmd:      "npm install",
			expected: CategoryPackage,
		},
		{
			name:     "test command",
			cmd:      "go test ./...",
			expected: CategoryTest,
		},
		{
			name:     "file operation",
			cmd:      "ls -la",
			expected: CategoryFile,
		},
		{
			name:     "unknown command",
			cmd:      "random-tool arg",
			expected: CategoryOther,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectCategory(tt.cmd)
			if result != tt.expected {
				t.Errorf("DetectCategory(%q) = %v, want %v", tt.cmd, result, tt.expected)
			}
		})
	}
}

func TestANSIStripper(t *testing.T) {
	stripper := &ANSIStripper{}
	ctx := context.Background()

	input := "Before\x1b[31mRed Text\x1b[0mAfter"
	result, err := stripper.AfterExecute(ctx, "bash", nil, input, nil)
	if err != nil {
		t.Fatalf("AfterExecute failed: %v", err)
	}

	expected := "BeforeRed TextAfter"
	if result != expected {
		t.Errorf("AfterExecute = %q, want %q", result, expected)
	}
}

func TestCompressor(t *testing.T) {
	compressor := &Compressor{}
	ctx := context.Background()

	input := "line1  \n\n  line2\t\tline3"
	result, err := compressor.AfterExecute(ctx, "bash", nil, input, nil)
	if err != nil {
		t.Fatalf("AfterExecute failed: %v", err)
	}

	expected := "line1\nline2 line3"
	if result != expected {
		t.Errorf("AfterExecute(%q) = %q, want %q", input, result, expected)
	}
}

func TestMetrics(t *testing.T) {
	metrics := NewMetrics()

	// Record some commands
	metrics.Record(CategoryGit, 1000, 800, 0)
	metrics.Record(CategoryGit, 1000, 800, 0)
	metrics.Record(CategoryBuild, 2000, 1500, 0)

	gitMetrics := metrics.Commands[CategoryGit]
	if gitMetrics == nil {
		t.Fatal("Expected git metrics to be recorded")
	}

	if gitMetrics.Count != 2 {
		t.Errorf("git Count = %d, want 2", gitMetrics.Count)
	}

	if gitMetrics.TotalBytes != 2000 {
		t.Errorf("git TotalBytes = %d, want 2000", gitMetrics.TotalBytes)
	}

	if gitMetrics.CompressedBytes != 1600 {
		t.Errorf("git CompressedBytes = %d, want 1600", gitMetrics.CompressedBytes)
	}

	buildMetrics := metrics.Commands[CategoryBuild]
	if buildMetrics == nil {
		t.Fatal("Expected build metrics to be recorded")
	}

	if buildMetrics.Count != 1 {
		t.Errorf("build Count = %d, want 1", buildMetrics.Count)
	}

	if buildMetrics.TotalBytes != 2000 {
		t.Errorf("build TotalBytes = %d, want 2000", buildMetrics.TotalBytes)
	}
}

func TestHookRegistry(t *testing.T) {
	registry := NewHookRegistry()
	ctx := context.Background()

	// Create hooks that modify output
	stripper := &ANSIStripper{}
	compressor := &Compressor{}

	registry.Register(stripper)
	registry.Register(compressor)

	input := "\x1b[31mError  \n\n  Message\x1b[0m"
	result, err := registry.After(ctx, "bash", nil, input, nil)
	if err != nil {
		t.Fatalf("After failed: %v", err)
	}

	expected := "Error\nMessage"
	if result != expected {
		t.Errorf("Hook chain = %q, want %q", result, expected)
	}
}

// panicBeforeHook panics during BeforeExecute.
type panicBeforeHook struct{}

func (h *panicBeforeHook) BeforeExecute(_ context.Context, _ string, _ map[string]any) error {
	panic("boom in before")
}

func (h *panicBeforeHook) AfterExecute(_ context.Context, _ string, _ map[string]any, result string, err error) (string, error) {
	return result, err
}

// panicAfterHook panics during AfterExecute.
type panicAfterHook struct{}

func (h *panicAfterHook) BeforeExecute(_ context.Context, _ string, _ map[string]any) error {
	return nil
}

func (h *panicAfterHook) AfterExecute(_ context.Context, _ string, _ map[string]any, _ string, _ error) (string, error) {
	panic("boom in after")
}

func TestHookRegistryBeforePanicRecovery(t *testing.T) {
	registry := NewHookRegistry()
	registry.Register(&panicBeforeHook{})

	err := registry.Before(context.Background(), "bash", nil)
	if err == nil {
		t.Fatal("expected error from panicking before-hook, got nil")
	}
	if got := err.Error(); got != `before-hook "bash" panicked: boom in before` {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestHookRegistryAfterPanicRecovery(t *testing.T) {
	registry := NewHookRegistry()
	registry.Register(&panicAfterHook{})

	result, err := registry.After(context.Background(), "bash", nil, "input", nil)
	if err == nil {
		t.Fatal("expected error from panicking after-hook, got nil")
	}
	if got := err.Error(); got != `after-hook "bash" panicked: boom in after` {
		t.Errorf("unexpected error message: %s", got)
	}
	if result != "" {
		t.Errorf("expected empty result after panic, got %q", result)
	}
}

func TestHookRegistryPanicDoesNotAffectOtherHooks(t *testing.T) {
	// Verify that a non-panicking hook works normally when no panic occurs.
	registry := NewHookRegistry()
	registry.Register(&ANSIStripper{})

	err := registry.Before(context.Background(), "bash", nil)
	if err != nil {
		t.Fatalf("unexpected error from normal hook: %v", err)
	}

	result, err := registry.After(context.Background(), "bash", nil, "\x1b[31mred\x1b[0m", nil)
	if err != nil {
		t.Fatalf("unexpected error from normal hook: %v", err)
	}
	if result != "red" {
		t.Errorf("expected %q, got %q", "red", result)
	}
}

func TestMakeCommandMapping(t *testing.T) {
	mapping := makeCommandMapping()

	expectedMappings := map[string]string{
		"git":    "rtk git",
		"go":     "rtk go",
		"cargo":  "rtk cargo",
		"npm":    "rtk npm",
		"docker": "rtk docker",
	}

	for cmd, expected := range expectedMappings {
		if mapping[cmd] != expected {
			t.Errorf("mapping[%q] = %q, want %q", cmd, mapping[cmd], expected)
		}
	}
}

func TestRtkCommandTranslatorDetection(t *testing.T) {
	// Test that translator initializes without error
	translator := NewRtkCommandTranslator()
	if translator == nil {
		t.Fatal("NewRtkCommandTranslator returned nil")
	}

	// Check that mapping is initialized
	if len(translator.mapping) == 0 {
		t.Fatal("translator.mapping is empty")
	}

	// Check that detection ran (rtkAvailable should be a boolean)
	_ = translator.rtkAvailable // Should be true or false, no error
}

func TestRtkCommandTranslatorBeforeExecute(t *testing.T) {
	// Create translator with rtk disabled for predictable testing
	translator := &RtkCommandTranslator{
		rtkAvailable: false,
		mapping:      makeCommandMapping(),
	}

	ctx := context.Background()

	// Test 1: Non-bash tools should not be modified
	params := map[string]any{"command": "git status"}
	err := translator.BeforeExecute(ctx, "read", params)
	if err != nil {
		t.Fatalf("BeforeExecute failed: %v", err)
	}
	if params["command"] != "git status" {
		t.Errorf("BeforeExecute modified non-bash tool")
	}

	// Test 2: If rtk unavailable, commands should not be translated
	params = map[string]any{"command": "git status"}
	err = translator.BeforeExecute(ctx, "bash", params)
	if err != nil {
		t.Fatalf("BeforeExecute failed: %v", err)
	}
	if params["command"] != "git status" {
		t.Errorf("BeforeExecute modified command when rtk unavailable")
	}

	// Test 3: With rtk available, matching commands should be translated
	translator.rtkAvailable = true
	params = map[string]any{"command": "git status"}
	err = translator.BeforeExecute(ctx, "bash", params)
	if err != nil {
		t.Fatalf("BeforeExecute failed: %v", err)
	}
	if params["command"] != "rtk git status" {
		t.Errorf("BeforeExecute = %q, want %q", params["command"], "rtk git status")
	}

	// Test 4: Commands not in mapping should not be translated
	params = map[string]any{"command": "ls -la"}
	err = translator.BeforeExecute(ctx, "bash", params)
	if err != nil {
		t.Fatalf("BeforeExecute failed: %v", err)
	}
	if params["command"] != "ls -la" {
		t.Errorf("BeforeExecute modified unmapped command")
	}

	// Test 5: Empty command should not cause error
	params = map[string]any{"command": ""}
	err = translator.BeforeExecute(ctx, "bash", params)
	if err != nil {
		t.Fatalf("BeforeExecute failed: %v", err)
	}

	// Test 6: Multiple arguments should be preserved
	params = map[string]any{"command": "go build -v ./..."}
	translator.rtkAvailable = true
	err = translator.BeforeExecute(ctx, "bash", params)
	if err != nil {
		t.Fatalf("BeforeExecute failed: %v", err)
	}
	if params["command"] != "rtk go build -v ./..." {
		t.Errorf("BeforeExecute = %q, want %q", params["command"], "rtk go build -v ./...")
	}
}

func TestRtkCommandTranslatorAfterExecute(t *testing.T) {
	translator := &RtkCommandTranslator{
		rtkAvailable: true,
		mapping:      makeCommandMapping(),
	}

	ctx := context.Background()

	// AfterExecute should be a no-op
	result, err := translator.AfterExecute(ctx, "bash", nil, "output", nil)
	if err != nil {
		t.Fatalf("AfterExecute failed: %v", err)
	}
	if result != "output" {
		t.Errorf("AfterExecute modified result: %q", result)
	}
}

func TestRtkCommandTranslatorMetrics(t *testing.T) {
	translator := &RtkCommandTranslator{
		rtkAvailable: true,
		mapping:      makeCommandMapping(),
	}

	ctx := context.Background()

	// Test 1: Rewritten command should increment rewritten counter
	params := map[string]any{"command": "git status"}
	err := translator.BeforeExecute(ctx, "bash", params)
	if err != nil {
		t.Fatalf("BeforeExecute failed: %v", err)
	}

	rewritten, native, _ := translator.GetMetrics()
	if rewritten != 1 {
		t.Errorf("Expected 1 rewritten command, got %d", rewritten)
	}
	if native != 0 {
		t.Errorf("Expected 0 native commands, got %d", native)
	}

	// Test 2: Native (non-rewritten) command should increment native counter
	translator = &RtkCommandTranslator{
		rtkAvailable: true,
		mapping:      makeCommandMapping(),
	}

	params = map[string]any{"command": "ls -la"}
	err = translator.BeforeExecute(ctx, "bash", params)
	if err != nil {
		t.Fatalf("BeforeExecute failed: %v", err)
	}

	rewritten, native, _ = translator.GetMetrics()
	if native != 1 {
		t.Errorf("Expected 1 native command, got %d", native)
	}
	if rewritten != 0 {
		t.Errorf("Expected 0 rewritten commands, got %d", rewritten)
	}

	// Test 3: Mixed commands
	translator = &RtkCommandTranslator{
		rtkAvailable: true,
		mapping:      makeCommandMapping(),
	}

	cmds := []string{"git status", "ls -la", "go build", "echo hello"}
	for _, cmd := range cmds {
		params = map[string]any{"command": cmd}
		_ = translator.BeforeExecute(ctx, "bash", params)
	}

	rewritten, native, _ = translator.GetMetrics()
	if rewritten != 2 {
		t.Errorf("Expected 2 rewritten commands, got %d", rewritten)
	}
	if native != 2 {
		t.Errorf("Expected 2 native commands, got %d", native)
	}
	// Note: The actual token savings come from output compression by rtk,
	// which is tracked separately in the compression hooks.
}
