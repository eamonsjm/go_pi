package tools

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultTimeout = 120000 // 120 seconds in milliseconds
	maxOutputBytes = 100000
)

// BashTool executes shell commands via /bin/bash.
type BashTool struct{}

func (t *BashTool) Name() string { return "bash" }

func (t *BashTool) Description() string {
	return "Executes a shell command via /bin/bash -c. Captures combined stdout and stderr. " +
		"Enforces a timeout (default 120s). Truncates output to 100KB."
}

func (t *BashTool) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The bash command to execute",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in milliseconds. Defaults to 120000 (2 minutes). Max 600000 (10 minutes).",
			},
		},
		"required": []string{"command"},
	}
}

func (t *BashTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	command, ok := params["command"].(string)
	if !ok || command == "" {
		return "", fmt.Errorf("command is required and must be a string")
	}

	timeoutMs := defaultTimeout
	if v, ok := getInt(params, "timeout"); ok {
		if v < 1 {
			return "", fmt.Errorf("timeout must be positive, got %d", v)
		}
		if v > 600000 {
			v = 600000
		}
		timeoutMs = v
	}

	timeout := time.Duration(timeoutMs) * time.Millisecond
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "/bin/bash", "-c", command)

	// Configure process group (platform-specific: Unix uses process groups, Windows does not).
	configureProcessGroup(cmd)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Start()
	if err != nil {
		return "", fmt.Errorf("failed to start command: %w", err)
	}

	// Wait for completion.
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	var exitCode int
	var timedOut bool

	select {
	case <-cmdCtx.Done():
		// Timeout or context cancellation - kill the process group.
		timedOut = true
		if cmd.Process != nil {
			if err := killProcessGroup(cmd.Process.Pid); err != nil {
				log.Printf("bash: cleanup: failed to kill process group (pid %d): %v", cmd.Process.Pid, err)
			}
		}
		<-done // Wait for the process to actually exit.
		exitCode = -1
	case waitErr := <-done:
		if waitErr != nil {
			if exitErr, ok := waitErr.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			} else {
				return "", fmt.Errorf("command error: %w", waitErr)
			}
		}
	}

	output := buf.Bytes()
	truncated := false
	if len(output) > maxOutputBytes {
		output = output[:maxOutputBytes]
		truncated = true
	}

	var result strings.Builder
	if timedOut {
		fmt.Fprintf(&result, "Command timed out after %v\n\n", timeout)
	}
	fmt.Fprintf(&result, "Exit code: %d\n", exitCode)
	if truncated {
		fmt.Fprintf(&result, "(Output truncated to %d bytes)\n", maxOutputBytes)
	}
	if len(output) > 0 {
		fmt.Fprintf(&result, "\n%s", string(output))
	}

	return result.String(), nil
}
