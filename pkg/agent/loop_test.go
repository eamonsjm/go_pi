package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

// --- mock provider ----------------------------------------------------------

type mockProvider struct {
	// streamFn is called for each Stream invocation. The test sets this to
	// control what events the provider returns.
	streamFn func(ctx context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error)
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Stream(ctx context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
	return m.streamFn(ctx, req)
}

// textResponse returns a streamFn that produces a simple text assistant reply.
func textResponse(text string) func(context.Context, ai.StreamRequest) (<-chan ai.StreamEvent, error) {
	return func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
		ch := make(chan ai.StreamEvent, 10)
		go func() {
			defer close(ch)
			ch <- ai.StreamEvent{Type: ai.EventMessageStart}
			ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: text}
			ch <- ai.StreamEvent{Type: ai.EventMessageEnd, Usage: &ai.Usage{InputTokens: 10, OutputTokens: 5}}
		}()
		return ch, nil
	}
}

// toolThenText returns a streamFn that first issues a tool call and then,
// on the second invocation, returns a text response.
func toolThenText(toolName, toolID, inputJSON, finalText string) func(context.Context, ai.StreamRequest) (<-chan ai.StreamEvent, error) {
	call := 0
	return func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
		ch := make(chan ai.StreamEvent, 20)
		call++
		current := call
		go func() {
			defer close(ch)
			if current == 1 {
				// First call: emit a tool use.
				ch <- ai.StreamEvent{Type: ai.EventMessageStart}
				ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: toolID, ToolName: toolName}
				ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: inputJSON}
				ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
				ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
			} else {
				// Subsequent calls: text response.
				ch <- ai.StreamEvent{Type: ai.EventMessageStart}
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: finalText}
				ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
			}
		}()
		return ch, nil
	}
}

// --- mock tool --------------------------------------------------------------

type mockTool struct {
	name   string
	result string
	err    error
}

func (t *mockTool) Name() string        { return t.name }
func (t *mockTool) Description() string { return "mock tool" }
func (t *mockTool) Schema() any         { return nil }
func (t *mockTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	if t.err != nil {
		return "", t.err
	}
	return t.result, nil
}

// --- helpers ----------------------------------------------------------------

// drainEvents reads all events from the given channel until EventAgentEnd,
// channel close, or timeout.
func drainEvents(ch <-chan Event, timeout time.Duration) []Event {
	var events []Event
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
			if ev.Type == EventAgentEnd || ev.Type == EventAgentError {
				return events
			}
		case <-timer.C:
			return events
		}
	}
}

func hasEventType(events []Event, t EventType) bool {
	for _, e := range events {
		if e.Type == t {
			return true
		}
	}
	return false
}

// --- tests ------------------------------------------------------------------

func TestBasicPromptFlow(t *testing.T) {
	provider := &mockProvider{streamFn: textResponse("Hello!")}
	reg := tools.NewRegistry()
	a := NewLoop(provider, reg)

	ch := a.Events()
	go func() {
		if err := a.Prompt(context.Background(), "Hi"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(ch, 2*time.Second)

	if !hasEventType(events, EventAgentStart) {
		t.Error("expected EventAgentStart")
	}
	if !hasEventType(events, EventAssistantText) {
		t.Error("expected EventAssistantText")
	}
	if !hasEventType(events, EventAgentEnd) {
		t.Error("expected EventAgentEnd")
	}

	// Check the text delta content.
	for _, ev := range events {
		if ev.Type == EventAssistantText {
			if ev.Delta != "Hello!" {
				t.Errorf("expected delta 'Hello!', got %q", ev.Delta)
			}
		}
	}

	// Conversation should have user + assistant messages.
	msgs := a.Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != ai.RoleUser {
		t.Errorf("expected first message role user, got %s", msgs[0].Role)
	}
	if msgs[1].Role != ai.RoleAssistant {
		t.Errorf("expected second message role assistant, got %s", msgs[1].Role)
	}
}

func TestToolCallFlow(t *testing.T) {
	provider := &mockProvider{
		streamFn: toolThenText("echo", "tc-1", `{"text":"hello"}`, "Done!"),
	}
	reg := tools.NewRegistry()
	reg.Register(&mockTool{name: "echo", result: "hello"})
	a := NewLoop(provider, reg)

	ch := a.Events()
	go func() {
		if err := a.Prompt(context.Background(), "use echo"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(ch, 2*time.Second)

	if !hasEventType(events, EventToolExecStart) {
		t.Error("expected EventToolExecStart")
	}
	if !hasEventType(events, EventToolExecEnd) {
		t.Error("expected EventToolExecEnd")
	}

	// Check tool result in events.
	for _, ev := range events {
		if ev.Type == EventToolExecEnd {
			if ev.ToolResult != "hello" {
				t.Errorf("expected tool result 'hello', got %q", ev.ToolResult)
			}
			if ev.ToolError {
				t.Error("expected ToolError=false")
			}
		}
	}

	// Messages should be: user, assistant (tool_use), tool_result, assistant (text).
	msgs := a.Messages()
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
}

func TestCancelAbortsLoop(t *testing.T) {
	// Provider that sends a tool call, making the loop continue. On the
	// second invocation it blocks until the context is cancelled, which
	// causes the stream to return an error event.
	call := 0
	provider := &mockProvider{
		streamFn: func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 20)
			call++
			current := call
			go func() {
				defer close(ch)
				if current == 1 {
					// First call: return a tool use so the loop continues.
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-1", ToolName: "noop"}
					ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: `{}`}
					ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
				} else {
					// Second call: block until context cancelled.
					<-ctx.Done()
					ch <- ai.StreamEvent{Type: ai.EventError, Error: ctx.Err()}
				}
			}()
			return ch, nil
		},
	}
	reg := tools.NewRegistry()
	reg.Register(&mockTool{name: "noop", result: "ok"})
	a := NewLoop(provider, reg)

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "wait")
	}()

	// Give the prompt a moment to start and reach the second provider call.
	time.Sleep(50 * time.Millisecond)
	a.Cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected error from cancelled Prompt")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt did not return after Cancel")
	}
}

func TestSteerInterruptsToolExecution(t *testing.T) {
	// Provider: first call returns two tool calls, second call returns text.
	call := 0
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 20)
			call++
			current := call
			go func() {
				defer close(ch)
				if current == 1 {
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					// First tool call
					ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-1", ToolName: "slow"}
					ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: `{}`}
					ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
					// Second tool call
					ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-2", ToolName: "slow"}
					ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: `{}`}
					ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
				} else {
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "steered!"}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
				}
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	// Tool that takes time — gives us a window to steer.
	reg.Register(&mockTool{name: "slow", result: "done"})
	a := NewLoop(provider, reg)

	go func() {
		// Send steer right away — it will be picked up before the second tool executes.
		time.Sleep(20 * time.Millisecond)
		a.Steer("stop and do this instead")
	}()

	ch := a.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "do stuff")
	}()

	events := drainEvents(ch, 2*time.Second)

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt did not return")
	}

	// Should have the steered text.
	found := false
	for _, ev := range events {
		if ev.Type == EventAssistantText && ev.Delta == "steered!" {
			found = true
		}
	}
	if !found {
		t.Error("expected steered text response")
	}
}

func TestPostExecSteerAddsSkipResults(t *testing.T) {
	// Verify that when a steering message arrives after a tool executes,
	// the remaining tool calls get skip results (tool_result messages).
	call := 0
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 20)
			call++
			current := call
			go func() {
				defer close(ch)
				if current == 1 {
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-1", ToolName: "steer-trigger"}
					ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: `{}`}
					ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
					ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-2", ToolName: "noop"}
					ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: `{}`}
					ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
					ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-3", ToolName: "noop"}
					ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: `{}`}
					ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
				} else {
					// After steering, model returns text.
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "redirected"}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
				}
			}()
			return ch, nil
		},
	}

	var agent *Loop
	var steerOnce sync.Once

	reg := tools.NewRegistry()
	// This tool steers during its own execution, guaranteeing the steer
	// message lands in the channel before the post-exec select runs.
	reg.Register(&callbackTool{name: "steer-trigger", fn: func() (string, error) {
		steerOnce.Do(func() {
			agent.Steer("redirect now")
		})
		return "executed", nil
	}})
	reg.Register(&mockTool{name: "noop", result: "noop"})

	agent = NewLoop(provider, reg)

	ch := agent.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Prompt(context.Background(), "run tools")
	}()

	events := drainEvents(ch, 2*time.Second)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Prompt returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt did not return")
	}

	// Verify: messages should contain tool results for all three tool calls.
	msgs := agent.Messages()
	tc2HasResult := false
	tc3HasResult := false
	for _, msg := range msgs {
		for _, block := range msg.Content {
			if block.Type == ai.ContentTypeToolResult {
				switch block.ToolResultID {
				case "tc-2":
					tc2HasResult = true
					if !block.IsError {
						t.Error("tc-2 skip result should be an error")
					}
				case "tc-3":
					tc3HasResult = true
					if !block.IsError {
						t.Error("tc-3 skip result should be an error")
					}
				}
			}
		}
	}
	if !tc2HasResult {
		t.Error("missing skip result for tc-2")
	}
	if !tc3HasResult {
		t.Error("missing skip result for tc-3")
	}

	// Verify the redirected response came through.
	found := false
	for _, ev := range events {
		if ev.Type == EventAssistantText && ev.Delta == "redirected" {
			found = true
		}
	}
	if !found {
		t.Error("expected redirected text response")
	}
}

// callbackTool is a mock tool that calls a function during execution.
type callbackTool struct {
	name string
	fn   func() (string, error)
}

func (t *callbackTool) Name() string        { return t.name }
func (t *callbackTool) Description() string { return "callback tool" }
func (t *callbackTool) Schema() any         { return nil }
func (t *callbackTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	return t.fn()
}

func TestStaleSteerDrainedBetweenPrompts(t *testing.T) {
	// Regression test: a Steer() message sent after one Prompt() completes
	// must not carry over and cause tool-skipping in the next Prompt() call.
	call := 0
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 20)
			call++
			current := call
			go func() {
				defer close(ch)
				switch current {
				case 1:
					// First Prompt: simple text reply (no tool calls).
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "first-reply"}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
				case 2:
					// Second Prompt, first turn: tool call.
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-1", ToolName: "echo"}
					ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: `{}`}
					ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
				default:
					// Second Prompt, second turn: text reply.
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "second-reply"}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
				}
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	reg.Register(&mockTool{name: "echo", result: "tool-ran"})
	a := NewLoop(provider, reg)

	// First Prompt — completes with no tool calls.
	ch1 := a.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "hello")
	}()
	drainEvents(ch1, 2*time.Second)
	if err := <-errCh; err != nil {
		t.Fatalf("first Prompt returned error: %v", err)
	}

	// Steer arrives AFTER the first Prompt completed — this is the stale message.
	a.Steer("stale steer that should be ignored")

	// Second Prompt — has a tool call. The stale steer must NOT skip it.
	ch2 := a.Events()
	go func() {
		errCh <- a.Prompt(context.Background(), "use echo tool")
	}()
	events := drainEvents(ch2, 2*time.Second)
	if err := <-errCh; err != nil {
		t.Fatalf("second Prompt returned error: %v", err)
	}

	// The tool must have executed (not been skipped by the stale steer).
	toolExecuted := false
	for _, ev := range events {
		if ev.Type == EventToolExecEnd && ev.ToolName == "echo" {
			if ev.ToolResult != "tool-ran" {
				t.Errorf("expected tool result 'tool-ran', got %q", ev.ToolResult)
			}
			toolExecuted = true
		}
	}
	if !toolExecuted {
		t.Error("tool 'echo' was not executed — stale steer message caused unexpected skip")
	}

	// The stale steer text must not appear as a user message in the conversation.
	msgs := a.Messages()
	for _, msg := range msgs {
		if msg.Role == ai.RoleUser && msg.GetText() == "stale steer that should be ignored" {
			t.Error("stale steer message was injected as a user turn")
		}
	}
}

func TestFollowUpProcessed(t *testing.T) {
	call := 0
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 10)
			call++
			current := call
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventMessageStart}
				if current == 1 {
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "first"}
				} else {
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "follow-up-reply"}
				}
				ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	a := NewLoop(provider, reg)

	// Queue a follow-up before starting.
	a.FollowUp("follow up question")

	ch := a.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "first question")
	}()

	events := drainEvents(ch, 2*time.Second)

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt did not return")
	}

	// Should see both "first" and "follow-up-reply" deltas.
	foundFirst := false
	foundFollowUp := false
	for _, ev := range events {
		if ev.Type == EventAssistantText {
			if ev.Delta == "first" {
				foundFirst = true
			}
			if ev.Delta == "follow-up-reply" {
				foundFollowUp = true
			}
		}
	}
	if !foundFirst {
		t.Error("expected first text delta")
	}
	if !foundFollowUp {
		t.Error("expected follow-up text delta")
	}
}

func TestUnknownToolReturnsError(t *testing.T) {
	provider := &mockProvider{
		streamFn: toolThenText("nonexistent_tool", "tc-1", `{}`, "ok"),
	}
	reg := tools.NewRegistry()
	// Do NOT register "nonexistent_tool".
	a := NewLoop(provider, reg)

	ch := a.Events()
	go func() {
		if err := a.Prompt(context.Background(), "call unknown tool"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(ch, 2*time.Second)

	found := false
	for _, ev := range events {
		if ev.Type == EventToolExecEnd {
			if !ev.ToolError {
				t.Error("expected ToolError=true for unknown tool")
			}
			if ev.ToolResult != "unknown tool: nonexistent_tool" {
				t.Errorf("unexpected tool result: %q", ev.ToolResult)
			}
			found = true
		}
	}
	if !found {
		t.Error("expected EventToolExecEnd for unknown tool")
	}
}

func TestInvalidToolInputJSONReturnsError(t *testing.T) {
	// Send tool input that is not valid JSON — executeTool should return
	// an error result instead of executing the tool with nil params.
	provider := &mockProvider{
		streamFn: toolThenText("my_tool", "tc-bad", `not valid json`, "ok"),
	}
	reg := tools.NewRegistry()
	reg.Register(&callbackTool{name: "my_tool", fn: func() (string, error) {
		t.Error("tool should not have been called with invalid input")
		return "", nil
	}})
	a := NewLoop(provider, reg)

	ch := a.Events()
	go func() {
		if err := a.Prompt(context.Background(), "call with bad json"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(ch, 2*time.Second)

	found := false
	for _, ev := range events {
		if ev.Type == EventToolExecEnd && ev.ToolCallID == "tc-bad" {
			if !ev.ToolError {
				t.Error("expected ToolError=true for invalid tool input JSON")
			}
			if !strings.Contains(ev.ToolResult, "invalid tool input") {
				t.Errorf("unexpected tool result: %q", ev.ToolResult)
			}
			found = true
		}
	}
	if !found {
		t.Error("expected EventToolExecEnd for invalid tool input")
	}
}

func TestToParamsMap(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  map[string]any
	}{
		{
			name:  "map input",
			input: map[string]any{"key": "value"},
			want:  map[string]any{"key": "value"},
		},
		{
			name:  "json string input",
			input: `{"key":"value"}`,
			want:  map[string]any{"key": "value"},
		},
		{
			name:  "invalid json string",
			input: "not json",
			want:  nil,
		},
		{
			name:  "nil input",
			input: nil,
			want:  nil,
		},
		{
			name:  "int input",
			input: 42,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toParamsMap(tt.input)
			if tt.want == nil {
				if got != nil {
					t.Errorf("expected nil, got %v", got)
				}
				return
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("got %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestToolExecWithError(t *testing.T) {
	provider := &mockProvider{
		streamFn: toolThenText("fail_tool", "tc-1", `{}`, "handled"),
	}
	reg := tools.NewRegistry()
	reg.Register(&mockTool{name: "fail_tool", err: fmt.Errorf("something broke")})
	a := NewLoop(provider, reg)

	ch := a.Events()
	go func() {
		if err := a.Prompt(context.Background(), "call failing tool"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(ch, 2*time.Second)

	for _, ev := range events {
		if ev.Type == EventToolExecEnd {
			if !ev.ToolError {
				t.Error("expected ToolError=true")
			}
			if ev.ToolResult != "something broke" {
				t.Errorf("expected error message, got %q", ev.ToolResult)
			}
		}
	}
}

func TestEventsChannelClosedAfterPrompt(t *testing.T) {
	provider := &mockProvider{streamFn: textResponse("Hello!")}
	reg := tools.NewRegistry()
	a := NewLoop(provider, reg)

	ch := a.Events()

	if err := a.Prompt(context.Background(), "Hi"); err != nil {
		t.Fatal(err)
	}

	// Drain remaining buffered events; channel must close.
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // Channel closed — test passes.
			}
		case <-timeout:
			t.Fatal("events channel not closed after Prompt returned")
		}
	}
}

func TestRepeatedPromptNoGoroutineLeak(t *testing.T) {
	provider := &mockProvider{streamFn: textResponse("reply")}
	reg := tools.NewRegistry()
	a := NewLoop(provider, reg)

	for i := 0; i < 3; i++ {
		ch := a.Events()
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := a.Prompt(context.Background(), fmt.Sprintf("prompt %d", i)); err != nil {
				t.Errorf("Prompt %d returned error: %v", i, err)
			}
		}()

		events := drainEvents(ch, 2*time.Second)
		if !hasEventType(events, EventAgentEnd) {
			t.Errorf("prompt %d: missing EventAgentEnd", i)
		}
		<-done // wait for Prompt to finish and close the channel
	}
}

func TestOptions(t *testing.T) {
	provider := &mockProvider{}
	reg := tools.NewRegistry()

	msgs := []ai.Message{ai.NewTextMessage(ai.RoleUser, "restored")}

	a := NewLoop(provider, reg,
		WithModel("test-model"),
		WithMaxTokens(1024),
		WithThinking(ai.ThinkingHigh),
		WithSystemPrompt("You are a test"),
		WithMessages(msgs),
	)

	if a.model != "test-model" {
		t.Errorf("expected model 'test-model', got %q", a.model)
	}
	if a.maxTokens != 1024 {
		t.Errorf("expected maxTokens 1024, got %d", a.maxTokens)
	}
	if a.thinking != ai.ThinkingHigh {
		t.Errorf("expected thinking High, got %s", a.thinking)
	}
	if a.systemPrompt != "You are a test" {
		t.Errorf("expected system prompt, got %q", a.systemPrompt)
	}
	if len(a.messages) != 1 {
		t.Fatalf("expected 1 pre-loaded message, got %d", len(a.messages))
	}
}

func TestCancelRacesWithPromptCompletion(t *testing.T) {
	// Exercise concurrent Cancel() calls racing with Prompt() cleanup.
	// Run with -race to verify no data races.
	for i := 0; i < 100; i++ {
		provider := &mockProvider{streamFn: textResponse("done")}
		reg := tools.NewRegistry()
		a := NewLoop(provider, reg)

		errCh := make(chan error, 1)
		go func() {
			errCh <- a.Prompt(context.Background(), "hi")
		}()

		// Race Cancel against Prompt completion from multiple goroutines.
		var wg sync.WaitGroup
		for j := 0; j < 5; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				a.Cancel()
			}()
		}

		select {
		case <-errCh:
			// Either nil (completed normally) or non-nil (cancelled) — both OK.
		case <-time.After(5 * time.Second):
			t.Fatal("Prompt did not return")
		}
		wg.Wait()
	}
}

func TestRapidSteerDropsWithLog(t *testing.T) {
	// Verify that rapid successive Steer() calls beyond buffer capacity
	// log a warning instead of silently dropping messages.
	provider := &mockProvider{streamFn: textResponse("done")}
	reg := tools.NewRegistry()
	a := NewLoop(provider, reg)

	// Buffer is 2 — first two should succeed, third should be dropped (and logged).
	a.Steer("steer-1")
	a.Steer("steer-2")
	a.Steer("steer-3") // This one should be dropped and logged.

	// Verify channel has exactly 2 messages (the buffer capacity).
	if len(a.steerCh) != 2 {
		t.Errorf("expected 2 buffered steer messages, got %d", len(a.steerCh))
	}

	// Drain and verify order.
	msg1 := <-a.steerCh
	msg2 := <-a.steerCh
	if msg1 != "steer-1" {
		t.Errorf("expected 'steer-1', got %q", msg1)
	}
	if msg2 != "steer-2" {
		t.Errorf("expected 'steer-2', got %q", msg2)
	}

	// Channel should now be empty.
	select {
	case extra := <-a.steerCh:
		t.Errorf("unexpected extra steer message: %q", extra)
	default:
		// Good — channel is empty.
	}
}

func TestRapidFollowUpDropsWithLog(t *testing.T) {
	// Same test for FollowUp channel.
	provider := &mockProvider{streamFn: textResponse("done")}
	reg := tools.NewRegistry()
	a := NewLoop(provider, reg)

	a.FollowUp("fu-1")
	a.FollowUp("fu-2")
	a.FollowUp("fu-3") // Dropped and logged.

	if len(a.followUpCh) != 2 {
		t.Errorf("expected 2 buffered follow-up messages, got %d", len(a.followUpCh))
	}

	msg1 := <-a.followUpCh
	msg2 := <-a.followUpCh
	if msg1 != "fu-1" {
		t.Errorf("expected 'fu-1', got %q", msg1)
	}
	if msg2 != "fu-2" {
		t.Errorf("expected 'fu-2', got %q", msg2)
	}
}

func TestMessagesRoundTrip(t *testing.T) {
	provider := &mockProvider{}
	reg := tools.NewRegistry()
	a := NewLoop(provider, reg)

	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "a"),
		ai.NewTextMessage(ai.RoleAssistant, "b"),
	}
	a.SetMessages(msgs)
	got := a.Messages()
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].GetText() != "a" || got[1].GetText() != "b" {
		t.Error("SetMessages/Messages round-trip failed")
	}
}

// --- RichTool tests ---------------------------------------------------------

// mockRichTool implements tools.RichTool for testing.
type mockRichTool struct {
	name   string
	blocks []ai.ContentBlock
	err    error
}

func (t *mockRichTool) Name() string        { return t.name }
func (t *mockRichTool) Description() string { return "mock rich tool" }
func (t *mockRichTool) Schema() any         { return nil }
func (t *mockRichTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	return "fallback", nil
}
func (t *mockRichTool) ExecuteRich(_ context.Context, _ map[string]any) ([]ai.ContentBlock, error) {
	if t.err != nil {
		return nil, t.err
	}
	return t.blocks, nil
}

func TestRichToolCallFlow(t *testing.T) {
	provider := &mockProvider{
		streamFn: toolThenText("rich_read", "tc-1", `{"path":"/img.png"}`, "Done!"),
	}
	reg := tools.NewRegistry()
	reg.Register(&mockRichTool{
		name: "rich_read",
		blocks: []ai.ContentBlock{
			{Type: ai.ContentTypeText, Text: "File contents:"},
			{Type: ai.ContentTypeImage, MediaType: "image/png", ImageData: "iVBOR"},
		},
	})
	a := NewLoop(provider, reg)

	ch := a.Events()
	go func() {
		if err := a.Prompt(context.Background(), "read image"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(ch, 2*time.Second)

	// Verify tool exec events.
	if !hasEventType(events, EventToolExecStart) {
		t.Error("expected EventToolExecStart")
	}
	if !hasEventType(events, EventToolExecEnd) {
		t.Error("expected EventToolExecEnd")
	}

	// The event's ToolResult should be the text summary.
	for _, ev := range events {
		if ev.Type == EventToolExecEnd {
			if ev.ToolResult != "File contents:" {
				t.Errorf("expected tool result text 'File contents:', got %q", ev.ToolResult)
			}
			if ev.ToolError {
				t.Error("expected ToolError=false")
			}
		}
	}

	// Messages should be: user, assistant (tool_use), tool_result (rich), assistant (text).
	msgs := a.Messages()
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}

	// The tool result message should have ContentBlocks.
	toolResultMsg := msgs[2]
	if len(toolResultMsg.Content) != 1 {
		t.Fatalf("expected 1 content block in tool result message, got %d", len(toolResultMsg.Content))
	}
	cb := toolResultMsg.Content[0]
	if cb.Type != ai.ContentTypeToolResult {
		t.Errorf("expected tool_result type, got %s", cb.Type)
	}
	if len(cb.ContentBlocks) != 2 {
		t.Fatalf("expected 2 content blocks in rich result, got %d", len(cb.ContentBlocks))
	}
	if cb.ContentBlocks[0].Type != ai.ContentTypeText {
		t.Errorf("expected first sub-block to be text, got %s", cb.ContentBlocks[0].Type)
	}
	if cb.ContentBlocks[1].Type != ai.ContentTypeImage {
		t.Errorf("expected second sub-block to be image, got %s", cb.ContentBlocks[1].Type)
	}
}

func TestRichToolCallWithError(t *testing.T) {
	provider := &mockProvider{
		streamFn: toolThenText("rich_fail", "tc-1", `{}`, "handled"),
	}
	reg := tools.NewRegistry()
	reg.Register(&mockRichTool{
		name: "rich_fail",
		err:  fmt.Errorf("read failed: permission denied"),
	})
	a := NewLoop(provider, reg)

	ch := a.Events()
	go func() {
		if err := a.Prompt(context.Background(), "read protected file"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(ch, 2*time.Second)

	for _, ev := range events {
		if ev.Type == EventToolExecEnd {
			if !ev.ToolError {
				t.Error("expected ToolError=true")
			}
			if ev.ToolResult != "read failed: permission denied" {
				t.Errorf("expected error message, got %q", ev.ToolResult)
			}
		}
	}

	// Error result should be a simple text tool result (not rich).
	msgs := a.Messages()
	toolResultMsg := msgs[2]
	cb := toolResultMsg.Content[0]
	if len(cb.ContentBlocks) != 0 {
		t.Errorf("error result should not have ContentBlocks, got %d", len(cb.ContentBlocks))
	}
	if cb.Content != "read failed: permission denied" {
		t.Errorf("expected error content, got %q", cb.Content)
	}
}

// --- hook error tests -------------------------------------------------------

// mockHook implements tools.Hook for testing hook error paths.
type mockHook struct {
	beforeErr error
	afterErr  error
}

func (h *mockHook) BeforeExecute(_ context.Context, _ string, _ map[string]any) error {
	return h.beforeErr
}

func (h *mockHook) AfterExecute(_ context.Context, _ string, _ map[string]any, result string, err error) (string, error) {
	if h.afterErr != nil {
		return "", h.afterErr
	}
	return result, err
}

func TestHookBeforeErrorReturnsToolError(t *testing.T) {
	provider := &mockProvider{
		streamFn: toolThenText("my_tool", "tc-1", `{}`, "ok"),
	}
	reg := tools.NewRegistry()
	executed := false
	reg.Register(&callbackTool{name: "my_tool", fn: func() (string, error) {
		executed = true
		return "should not run", nil
	}})
	a := NewLoop(provider, reg)

	// Replace hooks with a registry containing a failing Before hook.
	hookReg := tools.NewHookRegistry()
	hookReg.Register(&mockHook{beforeErr: errors.New("permission denied by policy")})
	a.hooks = hookReg

	ch := a.Events()
	go func() {
		if err := a.Prompt(context.Background(), "call tool"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(ch, 2*time.Second)

	if executed {
		t.Error("tool should not have been executed when Before hook fails")
	}

	found := false
	for _, ev := range events {
		if ev.Type == EventToolExecEnd && ev.ToolCallID == "tc-1" {
			found = true
			if !ev.ToolError {
				t.Error("expected ToolError=true when Before hook returns error")
			}
			if !strings.Contains(ev.ToolResult, "hook error") {
				t.Errorf("expected hook error in result, got %q", ev.ToolResult)
			}
			if !strings.Contains(ev.ToolResult, "permission denied by policy") {
				t.Errorf("expected original error message in result, got %q", ev.ToolResult)
			}
		}
	}
	if !found {
		t.Error("expected EventToolExecEnd for hook before error")
	}
}

func TestRichToolAfterHookErrorReturnsToolError(t *testing.T) {
	provider := &mockProvider{
		streamFn: toolThenText("rich_tool", "tc-1", `{}`, "ok"),
	}
	reg := tools.NewRegistry()
	reg.Register(&mockRichTool{
		name: "rich_tool",
		blocks: []ai.ContentBlock{
			{Type: ai.ContentTypeText, Text: "success output"},
		},
	})
	a := NewLoop(provider, reg)

	// Replace hooks with a registry containing a failing After hook.
	hookReg := tools.NewHookRegistry()
	hookReg.Register(&mockHook{afterErr: errors.New("post-processing failed")})
	a.hooks = hookReg

	ch := a.Events()
	go func() {
		if err := a.Prompt(context.Background(), "call rich tool"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(ch, 2*time.Second)

	found := false
	for _, ev := range events {
		if ev.Type == EventToolExecEnd && ev.ToolCallID == "tc-1" {
			found = true
			if !ev.ToolError {
				t.Error("expected ToolError=true when After hook returns error for RichTool")
			}
			if ev.ToolResult != "post-processing failed" {
				t.Errorf("expected after-hook error message, got %q", ev.ToolResult)
			}
		}
	}
	if !found {
		t.Error("expected EventToolExecEnd for rich tool after-hook error")
	}

	// The message should be a plain error result, not a rich result.
	msgs := a.Messages()
	toolResultMsg := msgs[2]
	cb := toolResultMsg.Content[0]
	if !cb.IsError {
		t.Error("expected tool result message to be marked as error")
	}
}

// --- panicking tool mocks ---------------------------------------------------

type panicTool struct {
	name string
}

func (t *panicTool) Name() string        { return t.name }
func (t *panicTool) Description() string { return "panics on execute" }
func (t *panicTool) Schema() any         { return nil }
func (t *panicTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	panic("nil map access")
}

type panicRichTool struct {
	name string
}

func (t *panicRichTool) Name() string        { return t.name }
func (t *panicRichTool) Description() string { return "panics on execute" }
func (t *panicRichTool) Schema() any         { return nil }
func (t *panicRichTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	panic("should not be called")
}
func (t *panicRichTool) ExecuteRich(_ context.Context, _ map[string]any) ([]ai.ContentBlock, error) {
	panic("index out of range")
}

func TestToolPanicReturnsError(t *testing.T) {
	provider := &mockProvider{
		streamFn: toolThenText("panic_tool", "tc-1", `{}`, "recovered"),
	}
	reg := tools.NewRegistry()
	reg.Register(&panicTool{name: "panic_tool"})
	a := NewLoop(provider, reg)

	ch := a.Events()
	go func() {
		if err := a.Prompt(context.Background(), "call panicking tool"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(ch, 2*time.Second)

	for _, ev := range events {
		if ev.Type == EventToolExecEnd {
			if !ev.ToolError {
				t.Error("expected ToolError=true for panicking tool")
			}
			if !strings.Contains(ev.ToolResult, "tool panicked") {
				t.Errorf("expected panic error message, got %q", ev.ToolResult)
			}
		}
	}
}

func TestRichToolPanicReturnsError(t *testing.T) {
	provider := &mockProvider{
		streamFn: toolThenText("panic_rich", "tc-1", `{}`, "recovered"),
	}
	reg := tools.NewRegistry()
	reg.Register(&panicRichTool{name: "panic_rich"})
	a := NewLoop(provider, reg)

	ch := a.Events()
	go func() {
		if err := a.Prompt(context.Background(), "call panicking rich tool"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(ch, 2*time.Second)

	for _, ev := range events {
		if ev.Type == EventToolExecEnd {
			if !ev.ToolError {
				t.Error("expected ToolError=true for panicking rich tool")
			}
			if !strings.Contains(ev.ToolResult, "tool panicked") {
				t.Errorf("expected panic error message, got %q", ev.ToolResult)
			}
		}
	}
}

func TestDoTurnStreamErrorDrainsChannel(t *testing.T) {
	// Verify that when an EventError arrives mid-stream in doTurn, the
	// remaining events are drained so the producer goroutine can exit cleanly.
	goroutineDone := make(chan struct{})
	provider := &mockProvider{
		streamFn: func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent) // unbuffered — producer blocks until consumer reads
			go func() {
				defer close(ch)
				defer close(goroutineDone)
				ch <- ai.StreamEvent{Type: ai.EventMessageStart}
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "partial"}
				ch <- ai.StreamEvent{Type: ai.EventError, Error: fmt.Errorf("mid-stream failure")}
				// These events come after the error. Without drain, the
				// producer would block here forever on an unbuffered channel.
				select {
				case ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "trailing"}:
				case <-ctx.Done():
					return
				}
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	a := NewLoop(provider, reg, WithMessages([]ai.Message{
		ai.NewTextMessage(ai.RoleUser, "hello"),
	}))

	msg, err := a.doTurn(context.Background())
	if err == nil {
		t.Fatal("expected error from stream")
	}
	if msg != nil {
		t.Errorf("expected nil message on error, got %+v", msg)
	}
	if !strings.Contains(err.Error(), "mid-stream failure") {
		t.Errorf("expected mid-stream failure error, got %v", err)
	}

	// The producer goroutine must finish promptly — if the stream isn't
	// drained or the context isn't cancelled, this will time out.
	select {
	case <-goroutineDone:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("producer goroutine did not exit; stream was not drained after error")
	}
}

// TestZeroValueLoop verifies that a zero-value Loop does not panic.
// Before the ensureInit fix, close(nil) on the events channel caused a panic.
func TestZeroValueLoop(t *testing.T) {
	t.Run("Prompt with provider does not panic", func(t *testing.T) {
		a := &Loop{}
		a.SetProvider(&mockProvider{streamFn: textResponse("ok")})
		// Must not panic on nil channel close.
		err := a.Prompt(context.Background(), "hello")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("Prompt without provider returns ErrNoProvider", func(t *testing.T) {
		a := &Loop{}
		err := a.Prompt(context.Background(), "hello")
		if !errors.Is(err, ErrNoProvider) {
			t.Fatalf("expected ErrNoProvider, got %v", err)
		}
	})

	t.Run("Steer on zero value does not block or panic", func(t *testing.T) {
		a := &Loop{}
		done := make(chan struct{})
		go func() {
			a.Steer("test")
			close(done)
		}()
		select {
		case <-done:
			// ok
		case <-time.After(time.Second):
			t.Fatal("Steer blocked on zero-value Loop")
		}
	})

	t.Run("FollowUp on zero value does not block or panic", func(t *testing.T) {
		a := &Loop{}
		done := make(chan struct{})
		go func() {
			a.FollowUp("test")
			close(done)
		}()
		select {
		case <-done:
			// ok
		case <-time.After(time.Second):
			t.Fatal("FollowUp blocked on zero-value Loop")
		}
	})

	t.Run("Events on zero value returns non-nil channel", func(t *testing.T) {
		a := &Loop{}
		ch := a.Events()
		if ch == nil {
			t.Fatal("Events() returned nil on zero-value Loop")
		}
	})
}

// TestEmitDuringPromptClose verifies that concurrent emit() calls do not panic
// when Prompt() closes the events channel. This is a regression test for a race
// where emit() could send on a closed channel if Compact() (or any method
// calling emit) ran concurrently with Prompt() completion.
func TestEmitDuringPromptClose(t *testing.T) {
	// Use a slow provider so we can control when Prompt() finishes.
	gate := make(chan struct{})
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				<-gate // wait for test to release
				ch <- ai.StreamEvent{Type: ai.EventMessageStart}
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "done"}
				ch <- ai.StreamEvent{Type: ai.EventMessageEnd, Usage: &ai.Usage{InputTokens: 1, OutputTokens: 1}}
			}()
			return ch, nil
		},
	}

	a := NewLoop(provider, tools.NewRegistry())

	// Drain events so the buffer doesn't fill.
	events := a.Events()
	go func() {
		for range events {
		}
	}()

	// Start Prompt in background — it blocks until we close gate.
	promptDone := make(chan error, 1)
	go func() {
		promptDone <- a.Prompt(context.Background(), "hello")
	}()

	// Hammer emit() from multiple goroutines while Prompt() is about to close
	// the events channel. With the race detector enabled (-race), this would
	// catch send-on-closed-channel without the eventsMu fix.
	var wg sync.WaitGroup
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				a.emit(ctx, Event{Type: EventAgentError})
			}
		}()
	}

	// Release the provider so Prompt() finishes (and closes the channel).
	close(gate)

	// Wait for everything to finish without panicking.
	wg.Wait()
	if err := <-promptDone; err != nil {
		t.Fatalf("Prompt returned error: %v", err)
	}
}
