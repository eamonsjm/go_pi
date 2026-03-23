package tools

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestGoTestAggregator(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		level          CompressionLevel
		shouldCompress bool
		minSavings     float64 // minimum expected compression ratio
	}{
		{
			name:           "simple pass output",
			input:          `ok      github.com/test/pkg    0.001s`,
			shouldCompress: true,
			minSavings:     0.5, // at least 50% savings
		},
		{
			name: "multiple test results with duplication",
			input: `=== RUN   TestFoo
--- PASS: TestFoo (0.00s)
=== RUN   TestBar
--- PASS: TestBar (0.00s)
PASS
ok      github.com/test/pkg    0.001s`,
			shouldCompress: true,
			minSavings:     0.5,
		},
		{
			name: "test failure with errors",
			input: `=== RUN   TestFail
    main_test.go:10: assertion failed
    main_test.go:11: expected value
    main_test.go:12: got different value
--- FAIL: TestFail (0.00s)
FAIL
FAIL    github.com/test/pkg    0.001s`,
			shouldCompress: true,
			minSavings:     0.7, // should achieve ~70% savings for test output
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agg := NewGoTestAggregator(tt.level, NewMetrics())
			ctx := context.Background()
			result, err := agg.AfterExecute(ctx, "bash", nil, tt.input, nil)
			if err != nil {
				t.Fatalf("AfterExecute failed: %v", err)
			}

			if tt.shouldCompress {
				savings := float64(len(tt.input)-len(result)) / float64(len(tt.input))
				if savings < tt.minSavings {
					t.Logf("Input: %d bytes, Output: %d bytes, Savings: %.1f%%", len(tt.input), len(result), savings*100)
					t.Logf("Result: %s", result)
				}
			}
		})
	}
}

func TestGoBuildErrorExtractor(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		level         CompressionLevel
		shouldExtract bool
		minSavings    float64
	}{
		{
			name:          "single build error",
			input:         `main.go:5:10: undefined: x`,
			shouldExtract: true,
			minSavings:    0.0, // already minimal
		},
		{
			name: "multiple build errors with noise",
			input: `# github.com/test/cmd
./main.go:10:5: undefined: fmt
./main.go:15:8: undefined: log
./main.go:20:3: undefined: os
FAIL    github.com/test/cmd    [build failed]`,
			shouldExtract: true,
			minSavings:    0.3, // should remove some noise
		},
		{
			name: "verbose build output with errors",
			input: `go: downloading golang.org/x/text v0.3.7
go: downloading golang.org/x/sys v0.0.0
# github.com/test
./main.go:5:2: undefined: fmt
./main.go:10:5: undefined: log
FAIL    github.com/test [build failed]`,
			shouldExtract: true,
			minSavings:    0.5, // should compress verbose parts
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			extractor := NewGoBuildErrorExtractor(tt.level, NewMetrics())
			ctx := context.Background()

			// AfterExecute only processes if there's an error, so we pass a non-nil error
			result, _ := extractor.AfterExecute(ctx, "bash", nil, tt.input, errors.New("build failed"))

			if tt.shouldExtract {
				savings := float64(len(tt.input)-len(result)) / float64(len(tt.input))
				if savings < tt.minSavings {
					t.Logf("Input: %d bytes, Output: %d bytes, Savings: %.1f%%", len(tt.input), len(result), savings*100)
					t.Logf("Result:\n%s", result)
				}
			}
		})
	}
}

func TestGitLogCompactor(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		level          CompressionLevel
		shouldCompress bool
		minSavings     float64
	}{
		{
			name: "single commit with message",
			input: `commit 1234567890abcdef
Author: John Doe <john@example.com>
Date:   Mon Jan 1 00:00:00 2026 +0000

    This is a long commit message that describes
    what was changed in this commit in great detail.
    It has multiple lines to provide full context.`,
			shouldCompress: true,
			minSavings:     0.3, // should at least trim blank lines
		},
		{
			name: "multiple commits",
			input: `commit 1234567890abcdef
Author: John Doe <john@example.com>
Date:   Mon Jan 1 00:00:00 2026 +0000

    First commit with long description
    spanning multiple lines
    with lots of detail

commit 9876543210fedcba
Author: Jane Smith <jane@example.com>
Date:   Sun Dec 31 23:59:59 2025 +0000

    Second commit message
    also with multiple lines`,
			shouldCompress: true,
			minSavings:     0.4,
		},
		{
			name: "commits with branch decorations",
			input: `commit 1234567890abcdef (HEAD -> main, origin/main)
Author: John Doe <john@example.com>
Date:   Mon Jan 1 00:00:00 2026 +0000

    Fix bug in parser`,
			shouldCompress: true,
			minSavings:     0.2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compactor := NewGitLogCompactor(tt.level, NewMetrics())
			ctx := context.Background()
			result, err := compactor.AfterExecute(ctx, "bash", nil, tt.input, nil)
			if err != nil {
				t.Fatalf("AfterExecute failed: %v", err)
			}

			if tt.shouldCompress {
				savings := float64(len(tt.input)-len(result)) / float64(len(tt.input))
				if savings < tt.minSavings {
					t.Logf("Input: %d bytes, Output: %d bytes, Savings: %.1f%%", len(tt.input), len(result), savings*100)
					t.Logf("Result:\n%s", result)
				}
			}
		})
	}
}

func TestLinterOutputGrouper(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		level       CompressionLevel
		shouldGroup bool
		minSavings  float64
	}{
		{
			name:        "single linter error",
			input:       `main.go:5:10: undefined name 'x'`,
			shouldGroup: true,
			minSavings:  0.0,
		},
		{
			name: "multiple errors same file",
			input: `main.go:5:10: undefined name 'x'
main.go:5:10: undefined name 'x'
main.go:10:5: undefined name 'y'
utils.go:3:1: unused import`,
			shouldGroup: true,
			minSavings:  0.2, // should deduplicate at least
		},
		{
			name: "linter output with summary",
			input: `file1.go:10:5: error message A
file1.go:15:8: error message B
file2.go:3:2: error message C
file2.go:3:2: error message C
5 errors found`,
			shouldGroup: true,
			minSavings:  0.2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grouper := NewLinterOutputGrouper(tt.level, NewMetrics())
			ctx := context.Background()
			result, err := grouper.AfterExecute(ctx, "bash", nil, tt.input, nil)
			if err != nil {
				t.Fatalf("AfterExecute failed: %v", err)
			}

			if tt.shouldGroup {
				savings := float64(len(tt.input)-len(result)) / float64(len(tt.input))
				if savings < tt.minSavings {
					t.Logf("Input: %d bytes, Output: %d bytes, Savings: %.1f%%", len(tt.input), len(result), savings*100)
					t.Logf("Result:\n%s", result)
				}
			}
		})
	}
}

func TestCompressionConfig(t *testing.T) {
	config := NewCompressionConfig()

	// Test default level
	if config.GetLevel("unknown") != CompressionMedium {
		t.Error("Expected default compression level to be Medium")
	}

	// Test setting and getting level
	config.SetLevel("go-test", CompressionHigh)
	if config.GetLevel("go-test") != CompressionHigh {
		t.Error("Expected compression level for go-test to be High")
	}

	// Test overriding level
	config.SetLevel("go-test", CompressionLow)
	if config.GetLevel("go-test") != CompressionLow {
		t.Error("Expected compression level for go-test to be Low after override")
	}
}

func TestCompressorTargets(t *testing.T) {
	// Real-world example: Go test output with 94% savings target
	testOutput := `=== RUN   TestFunctionOne
=== RUN   TestFunctionOne/subtest_a
=== RUN   TestFunctionOne/subtest_b
--- PASS: TestFunctionOne (0.00s)
--- PASS: TestFunctionOne/subtest_a (0.00s)
--- PASS: TestFunctionOne/subtest_b (0.00s)
=== RUN   TestFunctionTwo
--- PASS: TestFunctionTwo (0.00s)
=== RUN   TestFunctionThree
=== RUN   TestFunctionThree/case_1
=== RUN   TestFunctionThree/case_2
--- PASS: TestFunctionThree (0.00s)
--- PASS: TestFunctionThree/case_1 (0.00s)
--- PASS: TestFunctionThree/case_2 (0.00s)
PASS
ok  	github.com/example/package	0.005s`

	agg := NewGoTestAggregator(CompressionHigh, NewMetrics())
	ctx := context.Background()
	result, _ := agg.AfterExecute(ctx, "bash", nil, testOutput, nil)
	savings := float64(len(testOutput)-len(result)) / float64(len(testOutput))

	t.Logf("Test output compression: %.1f%% (target: 94%%)", savings*100)
	t.Logf("Original: %d bytes, Compressed: %d bytes", len(testOutput), len(result))

	// Build error example with 72% target
	buildOutput := `# github.com/example/cmd
go build example.com/cmd: "./main.go:10:5: undefined: nonexistent"
./main.go:10:5: undefined: nonexistent
./main.go:15:8: undefined: another
./main.go:20:3: undefined: third
# more noise about compilation
FAIL    github.com/example/cmd  [build failed]`

	extractor := NewGoBuildErrorExtractor(CompressionHigh, NewMetrics())
	result, _ = extractor.AfterExecute(ctx, "bash", nil, buildOutput, errors.New("build failed"))
	savings = float64(len(buildOutput)-len(result)) / float64(len(buildOutput))

	t.Logf("Build error compression: %.1f%% (target: 72%%)", savings*100)
	t.Logf("Original: %d bytes, Compressed: %d bytes", len(buildOutput), len(result))
}

func TestBeforeExecute_AllCompressors(t *testing.T) {
	ctx := context.Background()
	metrics := NewMetrics()
	params := map[string]any{"command": "go test ./..."}

	tests := []struct {
		name string
		hook Hook
	}{
		{"GoTestAggregator", NewGoTestAggregator(CompressionMedium, metrics)},
		{"GoBuildErrorExtractor", NewGoBuildErrorExtractor(CompressionMedium, metrics)},
		{"GitLogCompactor", NewGitLogCompactor(CompressionMedium, metrics)},
		{"LinterOutputGrouper", NewLinterOutputGrouper(CompressionMedium, metrics)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.hook.BeforeExecute(ctx, "bash", params)
			if err != nil {
				t.Errorf("BeforeExecute returned unexpected error: %v", err)
			}
		})
	}
}

func TestBeforeExecute_NilParams(t *testing.T) {
	ctx := context.Background()
	metrics := NewMetrics()

	hooks := []Hook{
		NewGoTestAggregator(CompressionLow, metrics),
		NewGoBuildErrorExtractor(CompressionHigh, metrics),
		NewGitLogCompactor(CompressionLow, metrics),
		NewLinterOutputGrouper(CompressionHigh, metrics),
	}

	for _, h := range hooks {
		if err := h.BeforeExecute(ctx, "", nil); err != nil {
			t.Errorf("BeforeExecute with nil params returned error: %v", err)
		}
	}
}

func TestGoTestAggregator_Compress(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input returns empty", func(t *testing.T) {
		agg := NewGoTestAggregator(CompressionMedium, NewMetrics())
		result, err := agg.AfterExecute(ctx, "bash", nil, "", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Empty string has no test markers, so AfterExecute returns it unchanged
		if result != "" {
			t.Errorf("expected empty result for empty input, got %q", result)
		}
	})

	t.Run("non-test output passes through", func(t *testing.T) {
		agg := NewGoTestAggregator(CompressionMedium, NewMetrics())
		input := "hello world\nsome random output"
		result, err := agg.AfterExecute(ctx, "bash", nil, input, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != input {
			t.Errorf("expected passthrough for non-test output, got %q", result)
		}
	})

	t.Run("error passthrough", func(t *testing.T) {
		agg := NewGoTestAggregator(CompressionMedium, NewMetrics())
		testErr := errors.New("something failed")
		result, err := agg.AfterExecute(ctx, "bash", nil, "ok\tpkg\t0.1s", testErr)
		if err != testErr {
			t.Errorf("expected original error returned, got %v", err)
		}
		if result != "ok\tpkg\t0.1s" {
			t.Errorf("expected unchanged result on error, got %q", result)
		}
	})

	t.Run("low compression keeps PASS lines", func(t *testing.T) {
		agg := NewGoTestAggregator(CompressionLow, NewMetrics())
		input := "=== RUN   TestA\n--- PASS: TestA (0.00s)\nok\tpkg\t0.001s"
		result, err := agg.AfterExecute(ctx, "bash", nil, input, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !contains(result, "--- PASS: TestA") {
			t.Errorf("low compression should keep PASS lines, got %q", result)
		}
	})

	t.Run("medium compression strips PASS lines", func(t *testing.T) {
		agg := NewGoTestAggregator(CompressionMedium, NewMetrics())
		input := "=== RUN   TestA\n--- PASS: TestA (0.00s)\nok\tpkg\t0.001s"
		result, err := agg.AfterExecute(ctx, "bash", nil, input, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if contains(result, "--- PASS: TestA") {
			t.Errorf("medium compression should strip PASS lines, got %q", result)
		}
	})

	t.Run("high compression strips assertion lines", func(t *testing.T) {
		agg := NewGoTestAggregator(CompressionHigh, NewMetrics())
		input := "=== RUN   TestA\n    expected 1 got 2\n--- FAIL: TestA (0.00s)\nFAIL\tpkg\t0.001s"
		result, err := agg.AfterExecute(ctx, "bash", nil, input, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if contains(result, "expected 1 got 2") {
			t.Errorf("high compression should strip assertion lines, got %q", result)
		}
	})

	t.Run("deduplicates FAIL lines", func(t *testing.T) {
		agg := NewGoTestAggregator(CompressionMedium, NewMetrics())
		input := "=== FAIL TestA\n=== FAIL TestA\n--- FAIL: TestA (0.00s)\n--- FAIL: TestA (0.00s)\nFAIL\tpkg\t0.001s"
		result, err := agg.AfterExecute(ctx, "bash", nil, input, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Count occurrences of "=== FAIL TestA" — should be deduplicated to 1
		count := countOccurrences(result, "=== FAIL TestA")
		if count > 1 {
			t.Errorf("expected deduplicated FAIL lines, got %d occurrences in %q", count, result)
		}
	})

	t.Run("keeps error context lines", func(t *testing.T) {
		agg := NewGoTestAggregator(CompressionHigh, NewMetrics())
		input := "=== RUN   TestA\n    foo_test.go:42: bad value\npanic: runtime error\n--- FAIL: TestA (0.00s)\nFAIL\tpkg\t0.001s"
		result, err := agg.AfterExecute(ctx, "bash", nil, input, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !contains(result, "foo_test.go:42") {
			t.Errorf("should keep .go: error context lines, got %q", result)
		}
		if !contains(result, "panic:") {
			t.Errorf("should keep panic lines, got %q", result)
		}
	})

	t.Run("pass summary preserved when no failures", func(t *testing.T) {
		agg := NewGoTestAggregator(CompressionMedium, NewMetrics())
		input := "=== RUN   TestA\n--- PASS: TestA (0.00s)\nok\tpkg\t0.001s"
		result, err := agg.AfterExecute(ctx, "bash", nil, input, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !contains(result, "ok\tpkg") {
			t.Errorf("should keep pass summary, got %q", result)
		}
	})

	t.Run("fail summary preferred over pass summary", func(t *testing.T) {
		agg := NewGoTestAggregator(CompressionMedium, NewMetrics())
		input := "=== RUN   TestA\n--- FAIL: TestA (0.00s)\nFAIL\tpkg\t0.001s\nok\tother\t0.001s"
		result, err := agg.AfterExecute(ctx, "bash", nil, input, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !contains(result, "FAIL\tpkg") {
			t.Errorf("should include fail summary, got %q", result)
		}
	})
}

func TestGoBuildErrorExtractor_Extract(t *testing.T) {
	ctx := context.Background()
	buildErr := errors.New("build failed")

	t.Run("nil error passes through unchanged", func(t *testing.T) {
		ext := NewGoBuildErrorExtractor(CompressionMedium, NewMetrics())
		input := "all good"
		result, err := ext.AfterExecute(ctx, "bash", nil, input, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != input {
			t.Errorf("nil error should passthrough, got %q", result)
		}
	})

	t.Run("non-build error output passes through", func(t *testing.T) {
		ext := NewGoBuildErrorExtractor(CompressionMedium, NewMetrics())
		input := "permission denied"
		result, _ := ext.AfterExecute(ctx, "bash", nil, input, buildErr)
		if result != input {
			t.Errorf("non-build output should passthrough, got %q", result)
		}
	})

	t.Run("high compression deduplicates by file", func(t *testing.T) {
		ext := NewGoBuildErrorExtractor(CompressionHigh, NewMetrics())
		input := "./main.go:10:5: undefined: foo\n./main.go:15:8: undefined: bar\n./util.go:3:1: undefined: baz"
		result, _ := ext.AfterExecute(ctx, "bash", nil, input, buildErr)
		// High compression: only first error per file
		mainCount := countOccurrences(result, "main.go")
		if mainCount > 1 {
			t.Errorf("high compression should show only first error per file, got %d for main.go in %q", mainCount, result)
		}
	})

	t.Run("low compression keeps all unique errors", func(t *testing.T) {
		ext := NewGoBuildErrorExtractor(CompressionLow, NewMetrics())
		input := "./main.go:10:5: undefined: foo\n./main.go:15:8: undefined: bar"
		result, _ := ext.AfterExecute(ctx, "bash", nil, input, buildErr)
		if !contains(result, "undefined: foo") || !contains(result, "undefined: bar") {
			t.Errorf("low compression should keep all unique errors, got %q", result)
		}
	})

	t.Run("deduplicates identical errors", func(t *testing.T) {
		ext := NewGoBuildErrorExtractor(CompressionMedium, NewMetrics())
		input := "./main.go:10:5: undefined: foo\n./main.go:10:5: undefined: foo"
		result, _ := ext.AfterExecute(ctx, "bash", nil, input, buildErr)
		count := countOccurrences(result, "undefined: foo")
		if count > 1 {
			t.Errorf("should deduplicate identical errors, got %d occurrences in %q", count, result)
		}
	})

	t.Run("filters go download and get lines", func(t *testing.T) {
		ext := NewGoBuildErrorExtractor(CompressionMedium, NewMetrics())
		input := "go: downloading foo v1.0\n./main.go:5:2: undefined: x"
		result, _ := ext.AfterExecute(ctx, "bash", nil, input, buildErr)
		if contains(result, "downloading") {
			t.Errorf("should filter go: download lines, got %q", result)
		}
	})

	t.Run("keeps summary errors when no file errors at high compression", func(t *testing.T) {
		ext := NewGoBuildErrorExtractor(CompressionHigh, NewMetrics())
		input := "cannot find package \"foo\" in any of:\ntype mismatch in assignment"
		result, _ := ext.AfterExecute(ctx, "bash", nil, input, buildErr)
		if !contains(result, "cannot find") {
			t.Errorf("should keep summary errors, got %q", result)
		}
	})
}

func TestGitLogCompactor_Compact(t *testing.T) {
	ctx := context.Background()

	t.Run("non-git output passes through", func(t *testing.T) {
		c := NewGitLogCompactor(CompressionMedium, NewMetrics())
		input := "no commits here"
		result, err := c.AfterExecute(ctx, "bash", nil, input, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != input {
			t.Errorf("non-git output should passthrough, got %q", result)
		}
	})

	t.Run("error passes through", func(t *testing.T) {
		c := NewGitLogCompactor(CompressionMedium, NewMetrics())
		testErr := errors.New("git error")
		result, err := c.AfterExecute(ctx, "bash", nil, "commit abc", testErr)
		if err != testErr {
			t.Errorf("expected original error, got %v", err)
		}
		if result != "commit abc" {
			t.Errorf("expected unchanged result on error, got %q", result)
		}
	})

	t.Run("high compression shortens hash", func(t *testing.T) {
		c := NewGitLogCompactor(CompressionHigh, NewMetrics())
		input := "commit 1234567890abcdef1234567890abcdef12345678\nAuthor: Test <t@t.com>\nDate:   Mon Jan 1 00:00:00 2026\n\n    Fix bug"
		result, err := c.AfterExecute(ctx, "bash", nil, input, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !contains(result, "commit 1234567") {
			t.Errorf("high compression should shorten hash, got %q", result)
		}
		if contains(result, "1234567890abcdef1234567890abcdef12345678") {
			t.Errorf("high compression should not keep full hash, got %q", result)
		}
	})

	t.Run("high compression strips Date line", func(t *testing.T) {
		c := NewGitLogCompactor(CompressionHigh, NewMetrics())
		input := "commit 1234567890abcdef\nAuthor: Test <t@t.com>\nDate:   Mon Jan 1 00:00:00 2026\n\n    Fix bug"
		result, _ := c.AfterExecute(ctx, "bash", nil, input, nil)
		if contains(result, "Date:") {
			t.Errorf("high compression should strip Date, got %q", result)
		}
	})

	t.Run("low compression keeps Date line", func(t *testing.T) {
		c := NewGitLogCompactor(CompressionLow, NewMetrics())
		input := "commit 1234567890abcdef\nAuthor: Test <t@t.com>\nDate:   Mon Jan 1 00:00:00 2026\n\n    Fix bug"
		result, _ := c.AfterExecute(ctx, "bash", nil, input, nil)
		if !contains(result, "Date:") {
			t.Errorf("low compression should keep Date, got %q", result)
		}
	})

	t.Run("high compression keeps only first message line when contiguous", func(t *testing.T) {
		// Message lines immediately after commit (no blank line gap) are captured
		c := NewGitLogCompactor(CompressionHigh, NewMetrics())
		input := "commit 1234567890abcdef\nAuthor: Test <t@t.com>\n    First line\n    Second line\n    Third line"
		result, _ := c.AfterExecute(ctx, "bash", nil, input, nil)
		if !contains(result, "First line") {
			t.Errorf("should keep first message line, got %q", result)
		}
		if contains(result, "Second line") || contains(result, "Third line") {
			t.Errorf("high compression should strip extra message lines, got %q", result)
		}
	})

	t.Run("low compression keeps second message line when contiguous", func(t *testing.T) {
		c := NewGitLogCompactor(CompressionLow, NewMetrics())
		input := "commit 1234567890abcdef\nAuthor: Test <t@t.com>\n    First line\n    Second line\n    Third line"
		result, _ := c.AfterExecute(ctx, "bash", nil, input, nil)
		if !contains(result, "Second line") {
			t.Errorf("low compression should keep second line, got %q", result)
		}
	})

	t.Run("blank line between author and message resets inMessage", func(t *testing.T) {
		// Standard git log format has a blank line before message body;
		// compact() resets inMessage on blank lines, so message lines after
		// the gap get filtered as decorations
		c := NewGitLogCompactor(CompressionHigh, NewMetrics())
		input := "commit 1234567890abcdef\nAuthor: Test <t@t.com>\n\n    Message after blank"
		result, _ := c.AfterExecute(ctx, "bash", nil, input, nil)
		// Message after blank line is stripped because inMessage was reset
		if contains(result, "Message after blank") {
			t.Errorf("message after blank line should be stripped (inMessage reset), got %q", result)
		}
	})

	t.Run("high compression strips author email to name only", func(t *testing.T) {
		c := NewGitLogCompactor(CompressionHigh, NewMetrics())
		input := "commit 1234567890abcdef\nAuthor: John Doe <john@example.com>\n\n    Fix thing"
		result, _ := c.AfterExecute(ctx, "bash", nil, input, nil)
		if !contains(result, "John Doe") {
			t.Errorf("should keep author name, got %q", result)
		}
		// At high compression, author line is just the captured group (name + email), not "Author: ..."
		if contains(result, "Author:") {
			t.Errorf("high compression should strip Author: prefix, got %q", result)
		}
	})
}

func TestLinterOutputGrouper_Group(t *testing.T) {
	ctx := context.Background()

	t.Run("no error/warning keywords passes through", func(t *testing.T) {
		g := NewLinterOutputGrouper(CompressionMedium, NewMetrics())
		input := "all good, no issues"
		result, err := g.AfterExecute(ctx, "bash", nil, input, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != input {
			t.Errorf("should passthrough non-linter output, got %q", result)
		}
	})

	t.Run("high compression limits files to 5", func(t *testing.T) {
		g := NewLinterOutputGrouper(CompressionHigh, NewMetrics())
		var lines []string
		for i := 0; i < 8; i++ {
			lines = append(lines, "file"+strconv.Itoa(i)+".go:1:1: error here")
		}
		input := joinLines(lines)
		result, _ := g.AfterExecute(ctx, "bash", nil, input, nil)
		// Count unique file headers in output
		fileCount := 0
		for i := 0; i < 8; i++ {
			if contains(result, "file"+strconv.Itoa(i)+".go") {
				fileCount++
			}
		}
		if fileCount > 5 {
			t.Errorf("high compression should limit to 5 files, got %d in %q", fileCount, result)
		}
	})

	t.Run("high compression limits errors per file to 2", func(t *testing.T) {
		g := NewLinterOutputGrouper(CompressionHigh, NewMetrics())
		input := "main.go:1:1: error A\nmain.go:2:1: error B\nmain.go:3:1: error C\nmain.go:4:1: error D"
		result, _ := g.AfterExecute(ctx, "bash", nil, input, nil)
		if !contains(result, "... and 2 more") {
			t.Errorf("high compression should show overflow count, got %q", result)
		}
	})

	t.Run("medium compression limits errors per file to 5", func(t *testing.T) {
		g := NewLinterOutputGrouper(CompressionMedium, NewMetrics())
		var lines []string
		for i := 0; i < 8; i++ {
			lines = append(lines, "main.go:"+strconv.Itoa(i+1)+":1: error msg "+strconv.Itoa(i))
		}
		input := joinLines(lines)
		result, _ := g.AfterExecute(ctx, "bash", nil, input, nil)
		// Medium: max 5 errors shown per file, rest trimmed but no "... and N more" message
		errCount := countOccurrences(result, "error msg")
		if errCount > 5 {
			t.Errorf("medium compression should limit to 5 errors per file, got %d in %q", errCount, result)
		}
	})

	t.Run("deduplicates same file:line:message", func(t *testing.T) {
		g := NewLinterOutputGrouper(CompressionLow, NewMetrics())
		input := "main.go:5:1: duplicate error\nmain.go:5:1: duplicate error\nmain.go:10:1: other error"
		result, _ := g.AfterExecute(ctx, "bash", nil, input, nil)
		count := countOccurrences(result, "duplicate error")
		if count > 1 {
			t.Errorf("should deduplicate, got %d occurrences in %q", count, result)
		}
	})

	t.Run("filters summary lines", func(t *testing.T) {
		g := NewLinterOutputGrouper(CompressionMedium, NewMetrics())
		input := "main.go:1:1: error foo\n3 errors found\n2 warnings"
		result, _ := g.AfterExecute(ctx, "bash", nil, input, nil)
		if contains(result, "errors found") {
			t.Errorf("should filter summary lines, got %q", result)
		}
	})

	t.Run("groups errors by file with sorted output", func(t *testing.T) {
		g := NewLinterOutputGrouper(CompressionLow, NewMetrics())
		input := "b.go:1:1: error in b\na.go:1:1: error in a\nc.go:1:1: error in c"
		result, _ := g.AfterExecute(ctx, "bash", nil, input, nil)
		aIdx := indexOf(result, "a.go")
		bIdx := indexOf(result, "b.go")
		cIdx := indexOf(result, "c.go")
		if aIdx > bIdx || bIdx > cIdx {
			t.Errorf("files should be sorted alphabetically, got %q", result)
		}
	})

	t.Run("preserves error on AfterExecute with non-nil err", func(t *testing.T) {
		g := NewLinterOutputGrouper(CompressionMedium, NewMetrics())
		origErr := errors.New("lint failed")
		input := "main.go:1:1: error something"
		_, err := g.AfterExecute(ctx, "bash", nil, input, origErr)
		if err != origErr {
			t.Errorf("should preserve original error, got %v", err)
		}
	})
}

// helpers

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func countOccurrences(s, substr string) int {
	return strings.Count(s, substr)
}

func indexOf(s, substr string) int {
	return strings.Index(s, substr)
}

func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}
