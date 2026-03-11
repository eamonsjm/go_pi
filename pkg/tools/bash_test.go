package tools

import (
	"context"
	"strings"
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
