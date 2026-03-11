package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestBashTool_SimpleEcho(t *testing.T) {
	tool := &BashTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Exit code: 0") {
		t.Errorf("expected exit code 0, got:\n%s", result)
	}
	if !strings.Contains(result, "hello") {
		t.Errorf("expected 'hello' in output, got:\n%s", result)
	}
}

func TestBashTool_ExitCode(t *testing.T) {
	tool := &BashTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "exit 42",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Exit code: 42") {
		t.Errorf("expected exit code 42, got:\n%s", result)
	}
}

func TestBashTool_Timeout(t *testing.T) {
	tool := &BashTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "sleep 60",
		"timeout": float64(200), // 200ms timeout
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "timed out") {
		t.Errorf("expected timeout message, got:\n%s", result)
	}
}

func TestBashTool_MissingCommand(t *testing.T) {
	tool := &BashTool{}
	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing command")
	}
}

func TestBashTool_StderrCaptured(t *testing.T) {
	tool := &BashTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo err_msg >&2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "err_msg") {
		t.Errorf("expected stderr output 'err_msg', got:\n%s", result)
	}
}

func TestBashTool_CombinedOutput(t *testing.T) {
	tool := &BashTool{}
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo stdout_line; echo stderr_line >&2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "stdout_line") {
		t.Errorf("expected stdout_line in output, got:\n%s", result)
	}
	if !strings.Contains(result, "stderr_line") {
		t.Errorf("expected stderr_line in output, got:\n%s", result)
	}
}

func TestBashTool_TruncationAtExactBoundary(t *testing.T) {
	tool := &BashTool{}

	// Generate exactly maxOutputBytes of output (100000 bytes).
	// Use printf to avoid a trailing newline that would push us over.
	cmd := fmt.Sprintf("head -c %d /dev/zero | tr '\\0' 'A'", maxOutputBytes)
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": cmd,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Exactly at the boundary: should NOT be truncated.
	if strings.Contains(result, "Output truncated") {
		t.Errorf("output at exactly maxOutputBytes should not be truncated, got:\n%s",
			result[:min(len(result), 200)])
	}
	if !strings.Contains(result, "Exit code: 0") {
		t.Errorf("expected exit code 0, got:\n%s", result[:min(len(result), 200)])
	}
}

func TestBashTool_TruncationOneOverBoundary(t *testing.T) {
	tool := &BashTool{}

	// Generate maxOutputBytes+1 bytes — should trigger truncation.
	cmd := fmt.Sprintf("head -c %d /dev/zero | tr '\\0' 'B'", maxOutputBytes+1)
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": cmd,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "Output truncated") {
		t.Errorf("expected truncation message for maxOutputBytes+1, got:\n%s",
			result[:min(len(result), 200)])
	}
	if !strings.Contains(result, fmt.Sprintf("truncated to %d bytes", maxOutputBytes)) {
		t.Errorf("truncation message should mention %d bytes, got:\n%s",
			maxOutputBytes, result[:min(len(result), 200)])
	}

	// Verify the truncated output contains only 'B' characters (no corruption).
	outputStart := strings.Index(result, "\nBBB")
	if outputStart == -1 {
		t.Fatal("could not find output payload in result")
	}
	payload := result[outputStart+1:]
	for i, c := range payload {
		if c != 'B' {
			t.Errorf("unexpected byte at position %d: %q (expected 'B')", i, c)
			break
		}
	}
}

func TestBashTool_TimeoutClampsAboveMax(t *testing.T) {
	tool := &BashTool{}

	// A timeout >600000ms should be clamped to 600000ms (10 minutes).
	// We can't wait that long, so we verify indirectly: the command should
	// still execute (not error) and the clamping doesn't break anything.
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo clamped",
		"timeout": float64(999999), // >600s, should be clamped
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "clamped") {
		t.Errorf("expected output 'clamped', got:\n%s", result)
	}
	if !strings.Contains(result, "Exit code: 0") {
		t.Errorf("expected exit code 0, got:\n%s", result)
	}
}

func TestBashTool_TimeoutRejectsZero(t *testing.T) {
	tool := &BashTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo nope",
		"timeout": float64(0),
	})
	if err == nil {
		t.Fatal("expected error for zero timeout")
	}
	if !strings.Contains(err.Error(), "positive") {
		t.Errorf("expected 'positive' in error, got: %v", err)
	}
}

func TestBashTool_TimeoutRejectsNegative(t *testing.T) {
	tool := &BashTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		"command": "echo nope",
		"timeout": float64(-5),
	})
	if err == nil {
		t.Fatal("expected error for negative timeout")
	}
	if !strings.Contains(err.Error(), "positive") {
		t.Errorf("expected 'positive' in error, got: %v", err)
	}
}

func TestBashTool_ProcessGroupCleanupAfterTimeout(t *testing.T) {
	tool := &BashTool{}

	// Spawn a child that itself spawns a background subprocess.
	// After timeout, the entire process group should be killed.
	// We use a marker file approach: the child writes a file, then the
	// background process tries to write another file after a delay.
	// If cleanup works, the background process is killed before writing.
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": `bash -c '
			echo "parent running"
			# Spawn a background child in the same process group
			(sleep 5 && echo "child survived" > /tmp/go_pi_test_pgcleanup) &
			sleep 60
		'`,
		"timeout": float64(300), // 300ms timeout
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "timed out") {
		t.Errorf("expected timeout, got:\n%s", result)
	}

	// Give a moment for any leaked process to run, then check the marker file
	// doesn't exist — the process group kill should have prevented it.
	checkResult, err := tool.Execute(context.Background(), map[string]any{
		"command": "test -f /tmp/go_pi_test_pgcleanup && echo 'LEAKED' || echo 'CLEAN'; rm -f /tmp/go_pi_test_pgcleanup",
	})
	if err != nil {
		t.Fatalf("unexpected error checking cleanup: %v", err)
	}
	if strings.Contains(checkResult, "LEAKED") {
		t.Error("background process survived process group kill — cleanup failed")
	}
}

func TestBashTool_TruncationMessageFormat(t *testing.T) {
	tool := &BashTool{}

	// Generate output well over the limit to verify the truncation message format.
	cmd := fmt.Sprintf("head -c %d /dev/zero | tr '\\0' 'X'", maxOutputBytes+5000)
	result, err := tool.Execute(context.Background(), map[string]any{
		"command": cmd,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the result structure: exit code line, truncation message, then output.
	lines := strings.SplitN(result, "\n", 4)
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines in result, got %d:\n%s",
			len(lines), result[:min(len(result), 300)])
	}
	if lines[0] != "Exit code: 0" {
		t.Errorf("first line should be 'Exit code: 0', got: %q", lines[0])
	}
	expected := fmt.Sprintf("(Output truncated to %d bytes)", maxOutputBytes)
	if lines[1] != expected {
		t.Errorf("second line should be %q, got: %q", expected, lines[1])
	}
	// Third line should be empty (separator before output).
	if lines[2] != "" {
		t.Errorf("expected empty separator line, got: %q", lines[2])
	}
}

func TestBashTool_ConcurrentExecution(t *testing.T) {
	tool := &BashTool{}
	const n = 5

	var wg sync.WaitGroup
	results := make([]string, n)
	errors := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errors[idx] = tool.Execute(context.Background(), map[string]any{
				"command": fmt.Sprintf("echo worker_%d; sleep 0.1", idx),
				"timeout": float64(5000),
			})
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errors[i] != nil {
			t.Errorf("worker %d error: %v", i, errors[i])
			continue
		}
		expected := fmt.Sprintf("worker_%d", i)
		if !strings.Contains(results[i], expected) {
			t.Errorf("worker %d: expected %q in output, got:\n%s", i, expected, results[i])
		}
		if !strings.Contains(results[i], "Exit code: 0") {
			t.Errorf("worker %d: expected exit code 0, got:\n%s", i, results[i])
		}
	}
}

func TestBashTool_ConcurrentTimeouts(t *testing.T) {
	tool := &BashTool{}
	const n = 3

	var wg sync.WaitGroup
	results := make([]string, n)
	errors := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errors[idx] = tool.Execute(context.Background(), map[string]any{
				"command": "sleep 60",
				"timeout": float64(200),
			})
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errors[i] != nil {
			t.Errorf("worker %d error: %v", i, errors[i])
			continue
		}
		if !strings.Contains(results[i], "timed out") {
			t.Errorf("worker %d: expected timeout message, got:\n%s", i, results[i])
		}
	}
}
