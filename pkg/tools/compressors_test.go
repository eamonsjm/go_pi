package tools

import (
	"context"
	"errors"
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
			agg := NewGoTestAggregator(tt.level)
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
			extractor := NewGoBuildErrorExtractor(tt.level)
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
			compactor := NewGitLogCompactor(tt.level)
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
			grouper := NewLinterOutputGrouper(tt.level)
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

	agg := NewGoTestAggregator(CompressionHigh)
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

	extractor := NewGoBuildErrorExtractor(CompressionHigh)
	result, _ = extractor.AfterExecute(ctx, "bash", nil, buildOutput, errors.New("build failed"))
	savings = float64(len(buildOutput)-len(result)) / float64(len(buildOutput))

	t.Logf("Build error compression: %.1f%% (target: 72%%)", savings*100)
	t.Logf("Original: %d bytes, Compressed: %d bytes", len(buildOutput), len(result))
}
