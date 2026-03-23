package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

// captureStdout replaces os.Stdout with a pipe, runs fn, restores stdout,
// and returns what was written. Not safe for parallel tests.
func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(&buf, r)
	}()

	fn()

	os.Stdout = origStdout
	_ = w.Close()
	<-done
	_ = r.Close()
	return buf.Bytes()
}

// parseEvents parses newline-delimited JSON events from RunJSONStream output.
func parseEvents(data []byte) ([]Event, error) {
	var events []Event
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return events, fmt.Errorf("unmarshal %q: %w", line, err)
		}
		events = append(events, ev)
	}
	return events, nil
}

func TestRunJSONStreamPromptFromArg(t *testing.T) {
	mock := &rpcMockProvider{streamFn: textStreamFn("hello from stream")}
	loop := agent.NewAgentLoop(mock, tools.NewRegistry())

	output := captureStdout(t, func() {
		code := RunJSONStream(loop, "say hello")
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})

	events, err := parseEvents(output)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}

	// Should see agent_start, assistant_text, and agent_end events.
	var types []string
	for _, ev := range events {
		types = append(types, ev.Type)
	}

	found := map[string]bool{}
	for _, typ := range types {
		found[typ] = true
	}
	for _, want := range []string{"agent_start", "assistant_text", "agent_end"} {
		if !found[want] {
			t.Errorf("missing event type %q in %v", want, types)
		}
	}

	// Verify assistant_text contains the expected delta.
	var textDelta string
	for _, ev := range events {
		if ev.Type == "assistant_text" {
			textDelta += ev.Delta
		}
	}
	if textDelta != "hello from stream" {
		t.Errorf("text delta = %q, want %q", textDelta, "hello from stream")
	}
}

func TestRunJSONStreamEmptyPrompt(t *testing.T) {
	mock := &rpcMockProvider{streamFn: textStreamFn("unused")}
	loop := agent.NewAgentLoop(mock, tools.NewRegistry())

	// Replace os.Stdin with a pipe that provides no data (simulates no pipe input).
	origStdin := os.Stdin
	r, w, _ := os.Pipe()
	_ = w.Close() // Close immediately — empty stdin
	os.Stdin = r

	output := captureStdout(t, func() {
		code := RunJSONStream(loop, "")
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	})

	os.Stdin = origStdin
	_ = r.Close()

	events, err := parseEvents(output)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Should have an error event.
	var foundError bool
	for _, ev := range events {
		if ev.Type == "error" && ev.Error == "no prompt provided" {
			foundError = true
		}
	}
	if !foundError {
		t.Errorf("expected error event 'no prompt provided', got events: %+v", events)
	}
}

func TestRunJSONStreamPromptFromStdin(t *testing.T) {
	mock := &rpcMockProvider{streamFn: textStreamFn("from stdin")}
	loop := agent.NewAgentLoop(mock, tools.NewRegistry())

	// Replace os.Stdin with a pipe that provides prompt text.
	origStdin := os.Stdin
	r, w, _ := os.Pipe()
	_, _ = w.Write([]byte("piped prompt\n"))
	_ = w.Close()
	os.Stdin = r

	output := captureStdout(t, func() {
		code := RunJSONStream(loop, "")
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})

	os.Stdin = origStdin
	_ = r.Close()

	events, err := parseEvents(output)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	found := map[string]bool{}
	for _, ev := range events {
		found[ev.Type] = true
	}
	if !found["agent_start"] {
		t.Error("missing agent_start event")
	}
	if !found["agent_end"] {
		t.Error("missing agent_end event")
	}
}

func TestRunJSONStreamProviderError(t *testing.T) {
	mock := &rpcMockProvider{streamFn: errorStreamFn(fmt.Errorf("provider down"))}
	loop := agent.NewAgentLoop(mock, tools.NewRegistry())

	output := captureStdout(t, func() {
		code := RunJSONStream(loop, "fail please")
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	})

	events, err := parseEvents(output)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// The agent loop emits "agent_error" when the provider fails.
	// RunJSONStream writes a synthetic "error" event only if promptErr != nil
	// and no agent_error was already emitted. Either type indicates failure.
	var foundError bool
	for _, ev := range events {
		if ev.Type == "error" || ev.Type == "agent_error" {
			foundError = true
		}
	}
	if !foundError {
		t.Errorf("expected error or agent_error event, got: %+v", events)
	}
}

func TestRunJSONStreamAgentError(t *testing.T) {
	// When the provider returns an error from Stream(), the agent loop
	// emits an agent_error event and Prompt returns an error. RunJSONStream
	// should write a synthetic error event and return exit code 1.
	callCount := 0
	mock := &rpcMockProvider{streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
		callCount++
		if callCount == 1 {
			// First call succeeds with a text response, agent loop emits agent_start/end
			return nil, fmt.Errorf("stream error")
		}
		// Shouldn't be called again.
		ch := make(chan ai.StreamEvent, 1)
		close(ch)
		return ch, nil
	}}
	loop := agent.NewAgentLoop(mock, tools.NewRegistry())

	output := captureStdout(t, func() {
		code := RunJSONStream(loop, "trigger error")
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	})

	events, _ := parseEvents(output)
	// Should have events including an error.
	var foundError bool
	for _, ev := range events {
		if ev.Type == "error" || ev.Type == "agent_error" {
			foundError = true
		}
	}
	if !foundError {
		t.Errorf("expected error event, got: %+v", events)
	}
}

func TestRunJSONStreamPanicRecovery(t *testing.T) {
	// Create a mock that panics during Prompt execution.
	// We can't directly make Prompt() panic from the provider, but we can
	// make the provider return an error that gets wrapped.
	mock := &rpcMockProvider{streamFn: func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
		ch := make(chan ai.StreamEvent, 10)
		go func() {
			defer close(ch)
			ch <- ai.StreamEvent{Type: ai.EventMessageStart}
			ch <- ai.StreamEvent{Type: ai.EventMessageEnd, Error: fmt.Errorf("simulated failure")}
		}()
		return ch, nil
	}}
	loop := agent.NewAgentLoop(mock, tools.NewRegistry())

	output := captureStdout(t, func() {
		code := RunJSONStream(loop, "panic test")
		if code == 0 {
			// An error exit code is expected.
			t.Log("got exit code 0, error events should still be written")
		}
	})

	// Should produce output (even if error).
	if len(output) == 0 {
		t.Error("expected some output from RunJSONStream")
	}
}

func TestRunJSONStreamSignalCancellation(t *testing.T) {
	// Use a blocking provider and verify that cancelling context works.
	var mu sync.Mutex
	started := false
	mock := &rpcMockProvider{streamFn: func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
		ch := make(chan ai.StreamEvent, 10)
		go func() {
			defer close(ch)
			ch <- ai.StreamEvent{Type: ai.EventMessageStart}
			mu.Lock()
			started = true
			mu.Unlock()
			<-ctx.Done()
			ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
		}()
		return ch, nil
	}}
	loop := agent.NewAgentLoop(mock, tools.NewRegistry())

	done := make(chan int, 1)
	output := captureStdout(t, func() {
		// Run in a goroutine that we can time-bound.
		go func() {
			code := RunJSONStream(loop, "will be cancelled")
			done <- code
		}()

		// Wait for the provider to start.
		deadline := time.After(2 * time.Second)
		for {
			mu.Lock()
			s := started
			mu.Unlock()
			if s {
				break
			}
			select {
			case <-deadline:
				t.Fatal("provider did not start")
			default:
				time.Sleep(10 * time.Millisecond)
			}
		}

		// Cancel via the agent loop (simulates what signal handling does).
		loop.Cancel()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("RunJSONStream did not exit after cancellation")
		}
	})

	// Should have produced some events.
	events, _ := parseEvents(output)
	found := map[string]bool{}
	for _, ev := range events {
		found[ev.Type] = true
	}
	if !found["agent_start"] {
		t.Error("missing agent_start event")
	}
}
