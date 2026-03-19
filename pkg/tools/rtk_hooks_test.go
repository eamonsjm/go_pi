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
