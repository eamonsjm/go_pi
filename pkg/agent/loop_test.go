package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

func (t *mockTool) Name() string                                                  { return t.name }
func (t *mockTool) Description() string                                           { return "mock tool" }
func (t *mockTool) Schema() any                                                   { return nil }
func (t *mockTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	if t.err != nil {
		return "", t.err
	}
	return t.result, nil
}

// --- helpers ----------------------------------------------------------------

// drainEvents reads all events from the agent until EventAgentEnd or timeout.
func drainEvents(a *AgentLoop, timeout time.Duration) []AgentEvent {
	var events []AgentEvent
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev := <-a.Events():
			events = append(events, ev)
			if ev.Type == EventAgentEnd || ev.Type == EventAgentError {
				return events
			}
		case <-timer.C:
			return events
		}
	}
}

func hasEventType(events []AgentEvent, t AgentEventType) bool {
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
	a := NewAgentLoop(provider, reg)

	go func() {
		if err := a.Prompt(context.Background(), "Hi"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(a, 2*time.Second)

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
	a := NewAgentLoop(provider, reg)

	go func() {
		if err := a.Prompt(context.Background(), "use echo"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(a, 2*time.Second)

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
	a := NewAgentLoop(provider, reg)

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
	a := NewAgentLoop(provider, reg)

	go func() {
		// Send steer right away — it will be picked up before the second tool executes.
		time.Sleep(20 * time.Millisecond)
		a.Steer("stop and do this instead")
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "do stuff")
	}()

	events := drainEvents(a, 2*time.Second)

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

	var agent *AgentLoop
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

	agent = NewAgentLoop(provider, reg)

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Prompt(context.Background(), "run tools")
	}()

	events := drainEvents(agent, 2*time.Second)

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
func (t *callbackTool) Description() string  { return "callback tool" }
func (t *callbackTool) Schema() any          { return nil }
func (t *callbackTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	return t.fn()
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
	a := NewAgentLoop(provider, reg)

	// Queue a follow-up before starting.
	a.FollowUp("follow up question")

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "first question")
	}()

	events := drainEvents(a, 2*time.Second)

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
	a := NewAgentLoop(provider, reg)

	go func() {
		if err := a.Prompt(context.Background(), "call unknown tool"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(a, 2*time.Second)

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

func TestAppendTextDelta(t *testing.T) {
	msg := &ai.Message{Role: ai.RoleAssistant}

	// First delta should create a new text block.
	appendTextDelta(msg, "Hello")
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	if msg.Content[0].Text != "Hello" {
		t.Errorf("expected 'Hello', got %q", msg.Content[0].Text)
	}

	// Second delta should append to existing block.
	appendTextDelta(msg, " World")
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	if msg.Content[0].Text != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", msg.Content[0].Text)
	}
}

func TestAppendThinkingDelta(t *testing.T) {
	msg := &ai.Message{Role: ai.RoleAssistant}

	// First delta creates a thinking block.
	appendThinkingDelta(msg, "Let me think")
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	if msg.Content[0].Type != ai.ContentTypeThinking {
		t.Errorf("expected thinking type, got %s", msg.Content[0].Type)
	}
	if msg.Content[0].Thinking != "Let me think" {
		t.Errorf("expected 'Let me think', got %q", msg.Content[0].Thinking)
	}

	// Second delta appends.
	appendThinkingDelta(msg, " more")
	if msg.Content[0].Thinking != "Let me think more" {
		t.Errorf("expected 'Let me think more', got %q", msg.Content[0].Thinking)
	}
}

func TestAppendTextDeltaAfterToolUse(t *testing.T) {
	// appendTextDelta scans backwards and finds the existing text block,
	// appending to it even though a tool_use block sits in between.
	msg := &ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeText, Text: "first"},
			{Type: ai.ContentTypeToolUse, ToolUseID: "tc-1", ToolName: "test"},
		},
	}

	appendTextDelta(msg, " continued")
	if len(msg.Content) != 2 {
		t.Fatalf("expected 2 content blocks, got %d", len(msg.Content))
	}
	if msg.Content[0].Text != "first continued" {
		t.Errorf("expected 'first continued', got %q", msg.Content[0].Text)
	}
}

func TestToolExecWithError(t *testing.T) {
	provider := &mockProvider{
		streamFn: toolThenText("fail_tool", "tc-1", `{}`, "handled"),
	}
	reg := tools.NewRegistry()
	reg.Register(&mockTool{name: "fail_tool", err: fmt.Errorf("something broke")})
	a := NewAgentLoop(provider, reg)

	go func() {
		if err := a.Prompt(context.Background(), "call failing tool"); err != nil {
			t.Errorf("Prompt returned error: %v", err)
		}
	}()

	events := drainEvents(a, 2*time.Second)

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

func TestOptions(t *testing.T) {
	provider := &mockProvider{}
	reg := tools.NewRegistry()

	msgs := []ai.Message{ai.NewTextMessage(ai.RoleUser, "restored")}

	a := NewAgentLoop(provider, reg,
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

func TestSettersAndMessages(t *testing.T) {
	provider := &mockProvider{}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg)

	a.SetModel("new-model")
	if a.model != "new-model" {
		t.Errorf("SetModel failed")
	}

	a.SetThinking(ai.ThinkingMedium)
	if a.thinking != ai.ThinkingMedium {
		t.Errorf("SetThinking failed")
	}

	a.SetSystemPrompt("new prompt")
	if a.systemPrompt != "new prompt" {
		t.Errorf("SetSystemPrompt failed")
	}

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
