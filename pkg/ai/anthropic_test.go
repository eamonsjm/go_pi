package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAnthropicProvider_NoKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := NewAnthropicProvider("")
	if err == nil {
		t.Fatal("expected error when no API key provided")
	}
	if !strings.Contains(err.Error(), "API key not set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewAnthropicProvider_ExplicitKey(t *testing.T) {
	p, err := NewAnthropicProvider("sk-test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.apiKey != "sk-test-key" {
		t.Errorf("expected key %q, got %q", "sk-test-key", p.apiKey)
	}
	if p.Name() != "anthropic" {
		t.Errorf("expected name %q, got %q", "anthropic", p.Name())
	}
}

func TestNewAnthropicProvider_EnvKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-env-key")
	p, err := NewAnthropicProvider("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.apiKey != "sk-env-key" {
		t.Errorf("expected key %q, got %q", "sk-env-key", p.apiKey)
	}
}

func TestBuildRequestBody_Basic(t *testing.T) {
	p := &AnthropicProvider{apiKey: "test"}

	req := StreamRequest{
		Model:        "claude-sonnet-4-20250514",
		SystemPrompt: "You are helpful.",
		Messages: []Message{
			NewTextMessage(RoleUser, "Hello"),
		},
		MaxTokens: 1024,
	}

	data, err := p.buildRequestBody(req)
	if err != nil {
		t.Fatalf("buildRequestBody failed: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if body["model"] != "claude-sonnet-4-20250514" {
		t.Errorf("expected model %q, got %v", "claude-sonnet-4-20250514", body["model"])
	}
	if body["stream"] != true {
		t.Errorf("expected stream=true, got %v", body["stream"])
	}
	if int(body["max_tokens"].(float64)) != 1024 {
		t.Errorf("expected max_tokens=1024, got %v", body["max_tokens"])
	}

	// System prompt with cache control.
	system, ok := body["system"].([]any)
	if !ok || len(system) != 1 {
		t.Fatalf("expected 1 system block, got %v", body["system"])
	}
	sysBlock := system[0].(map[string]any)
	if sysBlock["text"] != "You are helpful." {
		t.Errorf("expected system text %q, got %v", "You are helpful.", sysBlock["text"])
	}
	if sysBlock["cache_control"] == nil {
		t.Error("expected cache_control on system block")
	}

	// Messages.
	msgs, ok := body["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %v", body["messages"])
	}
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("expected role 'user', got %v", msg["role"])
	}
}

func TestBuildRequestBody_DefaultMaxTokens(t *testing.T) {
	p := &AnthropicProvider{apiKey: "test"}

	req := StreamRequest{
		Model:    "test-model",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	}

	data, err := p.buildRequestBody(req)
	if err != nil {
		t.Fatalf("buildRequestBody failed: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if int(body["max_tokens"].(float64)) != defaultMaxTokens {
		t.Errorf("expected default max_tokens=%d, got %v", defaultMaxTokens, body["max_tokens"])
	}
}

func TestBuildRequestBody_WithTools(t *testing.T) {
	p := &AnthropicProvider{apiKey: "test"}

	req := StreamRequest{
		Model:    "test-model",
		Messages: []Message{NewTextMessage(RoleUser, "search for Go")},
		Tools: []ToolDef{
			{
				Name:        "search",
				Description: "Search the web",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	data, err := p.buildRequestBody(req)
	if err != nil {
		t.Fatalf("buildRequestBody failed: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %v", body["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "search" {
		t.Errorf("expected tool name 'search', got %v", tool["name"])
	}
	if tool["description"] != "Search the web" {
		t.Errorf("expected tool description %q, got %v", "Search the web", tool["description"])
	}
}

func TestBuildRequestBody_ThinkingBudgets(t *testing.T) {
	p := &AnthropicProvider{apiKey: "test"}

	tests := []struct {
		level  ThinkingLevel
		budget int
	}{
		{ThinkingLow, 5000},
		{ThinkingMedium, 10000},
		{ThinkingHigh, 32000},
	}

	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			temp := 0.5
			req := StreamRequest{
				Model:         "test-model",
				Messages:      []Message{NewTextMessage(RoleUser, "think")},
				ThinkingLevel: tt.level,
				Temperature:   &temp,
			}

			data, err := p.buildRequestBody(req)
			if err != nil {
				t.Fatalf("buildRequestBody failed: %v", err)
			}

			var body map[string]any
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("failed to parse JSON: %v", err)
			}

			thinking, ok := body["thinking"].(map[string]any)
			if !ok {
				t.Fatal("expected thinking block")
			}
			if thinking["type"] != "enabled" {
				t.Errorf("expected thinking type 'enabled', got %v", thinking["type"])
			}
			if int(thinking["budget_tokens"].(float64)) != tt.budget {
				t.Errorf("expected budget %d, got %v", tt.budget, thinking["budget_tokens"])
			}

			// Temperature must be nil when thinking is enabled.
			if body["temperature"] != nil {
				t.Error("expected temperature to be nil when thinking is enabled")
			}
		})
	}
}

func TestBuildRequestBody_ThinkingBudgetExceedsMaxTokens(t *testing.T) {
	p := &AnthropicProvider{apiKey: "test"}

	// With default max_tokens (8192), ThinkingHigh budget (32000) exceeds it.
	// The API requires budget_tokens < max_tokens, so max_tokens must be bumped.
	req := StreamRequest{
		Model:         "test-model",
		Messages:      []Message{NewTextMessage(RoleUser, "think hard")},
		ThinkingLevel: ThinkingHigh,
	}

	data, err := p.buildRequestBody(req)
	if err != nil {
		t.Fatalf("buildRequestBody failed: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	maxTokens := int(body["max_tokens"].(float64))
	thinking := body["thinking"].(map[string]any)
	budget := int(thinking["budget_tokens"].(float64))

	if budget >= maxTokens {
		t.Errorf("budget_tokens (%d) must be < max_tokens (%d)", budget, maxTokens)
	}
	if maxTokens != budget+1 {
		t.Errorf("expected max_tokens=%d, got %d", budget+1, maxTokens)
	}

	// When caller provides sufficient max_tokens, it should not be changed.
	req2 := StreamRequest{
		Model:         "test-model",
		Messages:      []Message{NewTextMessage(RoleUser, "think hard")},
		ThinkingLevel: ThinkingHigh,
		MaxTokens:     64000,
	}

	data2, err := p.buildRequestBody(req2)
	if err != nil {
		t.Fatalf("buildRequestBody failed: %v", err)
	}

	var body2 map[string]any
	if err := json.Unmarshal(data2, &body2); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if int(body2["max_tokens"].(float64)) != 64000 {
		t.Errorf("expected max_tokens=64000 (caller-specified), got %v", body2["max_tokens"])
	}
}

func TestBuildRequestBody_NoThinking(t *testing.T) {
	p := &AnthropicProvider{apiKey: "test"}
	temp := 0.7

	req := StreamRequest{
		Model:         "test-model",
		Messages:      []Message{NewTextMessage(RoleUser, "hi")},
		ThinkingLevel: ThinkingOff,
		Temperature:   &temp,
	}

	data, err := p.buildRequestBody(req)
	if err != nil {
		t.Fatalf("buildRequestBody failed: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if body["thinking"] != nil {
		t.Error("expected no thinking block when ThinkingOff")
	}
	if body["temperature"] == nil {
		t.Error("expected temperature to be set when thinking is off")
	}
}

func TestBuildRequestBody_NoSystemPrompt(t *testing.T) {
	p := &AnthropicProvider{apiKey: "test"}

	req := StreamRequest{
		Model:    "test-model",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	}

	data, err := p.buildRequestBody(req)
	if err != nil {
		t.Fatalf("buildRequestBody failed: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if body["system"] != nil {
		t.Error("expected no system block when SystemPrompt is empty")
	}
}

// --- SSE stream parsing tests ---

func newTestAnthropicServer(t *testing.T, ssePayload string) (*httptest.Server, *AnthropicProvider) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, ssePayload)
	}))
	p := &AnthropicProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}
	return srv, p
}

func collectEvents(ch <-chan StreamEvent) []StreamEvent {
	var events []StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}
	return events
}

func TestAnthropicStream_SimpleText(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_01","role":"assistant","usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`
	srv, p := newTestAnthropicServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	// Find text deltas.
	var textParts []string
	for _, e := range events {
		if e.Type == EventTextDelta {
			textParts = append(textParts, e.Delta)
		}
	}
	fullText := strings.Join(textParts, "")
	if fullText != "Hello world" {
		t.Errorf("expected text %q, got %q", "Hello world", fullText)
	}

	// Check message_start.
	if events[0].Type != EventMessageStart {
		t.Errorf("expected first event to be message_start, got %v", events[0].Type)
	}
	if events[0].Usage == nil || events[0].Usage.InputTokens != 10 {
		t.Errorf("expected input_tokens=10 in message_start, got %+v", events[0].Usage)
	}

	// Check message_end with usage.
	last := events[len(events)-1]
	if last.Type != EventMessageEnd {
		t.Errorf("expected last event to be message_end, got %v", last.Type)
	}
	if last.Usage == nil {
		t.Fatal("expected usage in message_end")
	}
	if last.Usage.InputTokens != 10 {
		t.Errorf("expected input_tokens=10, got %d", last.Usage.InputTokens)
	}
	// output_tokens comes solely from message_delta.
	if last.Usage.OutputTokens != 5 {
		t.Errorf("expected output_tokens=5, got %d", last.Usage.OutputTokens)
	}
}

func TestAnthropicStream_ToolUse(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_02","role":"assistant","usage":{"input_tokens":20,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01","name":"search"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"q\":"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"hello\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":15}}

event: message_stop
data: {"type":"message_stop"}

`
	srv, p := newTestAnthropicServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "search for hello")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	// Verify tool_use_start.
	var toolStartCount, toolDeltaCount, toolEndCount int
	var accumulatedInput string
	for _, e := range events {
		switch e.Type {
		case EventToolUseStart:
			toolStartCount++
			if e.ToolCallID != "toolu_01" {
				t.Errorf("expected tool ID %q, got %q", "toolu_01", e.ToolCallID)
			}
			if e.ToolName != "search" {
				t.Errorf("expected tool name %q, got %q", "search", e.ToolName)
			}
		case EventToolUseDelta:
			toolDeltaCount++
			accumulatedInput += e.PartialInput
			if e.ToolCallID != "toolu_01" {
				t.Errorf("expected tool ID in delta, got %q", e.ToolCallID)
			}
		case EventToolUseEnd:
			toolEndCount++
			if e.ToolCallID != "toolu_01" {
				t.Errorf("expected tool ID in end, got %q", e.ToolCallID)
			}
		}
	}

	if toolStartCount != 1 {
		t.Errorf("expected 1 tool_use_start, got %d", toolStartCount)
	}
	if toolDeltaCount != 2 {
		t.Errorf("expected 2 tool_use_delta, got %d", toolDeltaCount)
	}
	if toolEndCount != 1 {
		t.Errorf("expected 1 tool_use_end, got %d", toolEndCount)
	}
	if accumulatedInput != `{"q":"hello"}` {
		t.Errorf("expected accumulated input %q, got %q", `{"q":"hello"}`, accumulatedInput)
	}
}

func TestAnthropicStream_Thinking(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_03","role":"assistant","usage":{"input_tokens":5,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think..."}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" Done."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Result"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":10}}

event: message_stop
data: {"type":"message_stop"}

`
	srv, p := newTestAnthropicServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "think about it")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	var thinkingParts []string
	var textParts []string
	for _, e := range events {
		switch e.Type {
		case EventThinkingDelta:
			thinkingParts = append(thinkingParts, e.Delta)
		case EventTextDelta:
			textParts = append(textParts, e.Delta)
		}
	}

	thinking := strings.Join(thinkingParts, "")
	if thinking != "Let me think... Done." {
		t.Errorf("expected thinking %q, got %q", "Let me think... Done.", thinking)
	}

	text := strings.Join(textParts, "")
	if text != "Result" {
		t.Errorf("expected text %q, got %q", "Result", text)
	}
}

func TestAnthropicStream_Error(t *testing.T) {
	sse := `event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"API is overloaded"}}

`
	srv, p := newTestAnthropicServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventError {
		t.Errorf("expected error event, got %v", events[0].Type)
	}
	if events[0].Error == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(events[0].Error.Error(), "overloaded_error") {
		t.Errorf("expected overloaded_error in message, got %q", events[0].Error.Error())
	}
	if !strings.Contains(events[0].Error.Error(), "API is overloaded") {
		t.Errorf("expected 'API is overloaded' in message, got %q", events[0].Error.Error())
	}
}

func TestAnthropicStream_UsageTracking(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_04","role":"assistant","usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":50,"cache_creation_input_tokens":25}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}

event: message_stop
data: {"type":"message_stop"}

`
	srv, p := newTestAnthropicServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	// message_start should report initial usage.
	if events[0].Type != EventMessageStart {
		t.Fatalf("expected message_start first, got %v", events[0].Type)
	}
	startUsage := events[0].Usage
	if startUsage.InputTokens != 100 {
		t.Errorf("expected input_tokens=100, got %d", startUsage.InputTokens)
	}
	if startUsage.CacheRead != 50 {
		t.Errorf("expected cache_read=50, got %d", startUsage.CacheRead)
	}
	if startUsage.CacheWrite != 25 {
		t.Errorf("expected cache_write=25, got %d", startUsage.CacheWrite)
	}

	// message_end should have accumulated usage.
	last := events[len(events)-1]
	if last.Type != EventMessageEnd {
		t.Fatalf("expected message_end last, got %v", last.Type)
	}
	endUsage := last.Usage
	if endUsage.InputTokens != 100 {
		t.Errorf("expected accumulated input_tokens=100, got %d", endUsage.InputTokens)
	}
	// Output tokens come solely from message_delta.
	if endUsage.OutputTokens != 20 {
		t.Errorf("expected accumulated output_tokens=20, got %d", endUsage.OutputTokens)
	}
	if endUsage.CacheRead != 50 {
		t.Errorf("expected cache_read=50, got %d", endUsage.CacheRead)
	}
	if endUsage.CacheWrite != 25 {
		t.Errorf("expected cache_write=25, got %d", endUsage.CacheWrite)
	}
}

func TestAnthropicStream_MalformedContentBlockStop(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_05","role":"assistant","usage":{"input_tokens":5,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_99","name":"search"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"hi\"}"}}

event: content_block_stop
data: {not valid json!!!

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`
	srv, p := newTestAnthropicServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	// Stream must terminate with an error event for the malformed JSON.
	var errorEvents []StreamEvent
	var toolEndCount int
	for _, e := range events {
		if e.Type == EventError {
			errorEvents = append(errorEvents, e)
		}
		if e.Type == EventToolUseEnd {
			toolEndCount++
		}
	}

	if len(errorEvents) != 1 {
		t.Fatalf("expected 1 error event, got %d", len(errorEvents))
	}
	if !strings.Contains(errorEvents[0].Error.Error(), "content_block_stop") {
		t.Errorf("expected error about content_block_stop, got %q", errorEvents[0].Error.Error())
	}

	// Must NOT emit a tool_use_end with stale data from the block.
	if toolEndCount != 0 {
		t.Errorf("expected 0 tool_use_end events (block state must not be modified on parse failure), got %d", toolEndCount)
	}

	// Stream must stop — no message_end event after the error.
	last := events[len(events)-1]
	if last.Type != EventError {
		t.Errorf("expected last event to be error (stream should stop), got %v", last.Type)
	}
}

func TestAnthropicStream_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":"rate_limited"}`)
	}))
	defer srv.Close()

	p := &AnthropicProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error for HTTP 429")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("expected status 429 in error, got %q", err.Error())
	}
}

func TestAnthropicStream_MalformedJSON(t *testing.T) {
	tests := []struct {
		name    string
		sse     string
		errPart string
	}{
		{
			name:    "message_start",
			sse:     "event: message_start\ndata: {not valid\n\n",
			errPart: "message_start",
		},
		{
			name: "content_block_start",
			sse: "event: message_start\n" +
				"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"role\":\"assistant\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
				"event: content_block_start\ndata: !!!broken!!!\n\n",
			errPart: "content_block_start",
		},
		{
			name: "content_block_delta",
			sse: "event: message_start\n" +
				"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"role\":\"assistant\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
				"event: content_block_start\n" +
				"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
				"event: content_block_delta\ndata: NOTJSON\n\n",
			errPart: "content_block_delta",
		},
		{
			name: "message_delta",
			sse: "event: message_start\n" +
				"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"role\":\"assistant\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n" +
				"event: content_block_start\n" +
				"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
				"event: content_block_stop\n" +
				"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
				"event: message_delta\ndata: {garbage\n\n",
			errPart: "message_delta",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, p := newTestAnthropicServer(t, tt.sse)
			defer srv.Close()

			ch, err := p.Stream(context.Background(), StreamRequest{
				Model:    "test",
				Messages: []Message{NewTextMessage(RoleUser, "hi")},
			})
			if err != nil {
				t.Fatalf("Stream failed: %v", err)
			}

			events := collectEvents(ch)
			last := events[len(events)-1]
			if last.Type != EventError {
				t.Fatalf("expected last event to be error, got %v", last.Type)
			}
			if !strings.Contains(last.Error.Error(), tt.errPart) {
				t.Errorf("expected error containing %q, got %q", tt.errPart, last.Error.Error())
			}
		})
	}
}

func TestAnthropicStream_PartialToolUse(t *testing.T) {
	// Stream cuts off mid-tool-call: no content_block_stop or message_stop.
	sse := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"role\":\"assistant\",\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_partial\",\"name\":\"search\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"q\\\":\"}}\n\n"

	srv, p := newTestAnthropicServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	var hasStart, hasDelta, hasEnd, hasMessageEnd bool
	for _, e := range events {
		switch e.Type {
		case EventToolUseStart:
			hasStart = true
		case EventToolUseDelta:
			hasDelta = true
		case EventToolUseEnd:
			hasEnd = true
		case EventMessageEnd:
			hasMessageEnd = true
		}
	}

	if !hasStart {
		t.Error("expected tool_use_start event")
	}
	if !hasDelta {
		t.Error("expected tool_use_delta event")
	}
	if hasEnd {
		t.Error("should NOT have tool_use_end (stream cut off)")
	}
	if hasMessageEnd {
		t.Error("should NOT have message_end (stream cut off)")
	}
}

func TestAnthropicStream_ContextCancellation(t *testing.T) {
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"role\":\"assistant\",\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n")
		flusher.Flush()
		close(ready)
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := &AnthropicProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := p.Stream(ctx, StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	<-ready

	evt := <-ch
	if evt.Type != EventMessageStart {
		t.Fatalf("expected message_start, got %v", evt.Type)
	}

	cancel()

	var gotError bool
	for e := range ch {
		if e.Type == EventError {
			gotError = true
		}
	}
	if !gotError {
		t.Error("expected error event from context cancellation")
	}
}

func TestAnthropicStream_OutOfOrderContentBlocks(t *testing.T) {
	// Content blocks arrive with interleaved indices: block 0 (thinking) starts,
	// then block 1 (text) starts, then deltas for block 1 arrive before block 0's
	// delta. The parser uses a map keyed by index, so both should be tracked.
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_ooo","role":"assistant","usage":{"input_tokens":5,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Answer"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Hmm..."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}

event: message_stop
data: {"type":"message_stop"}

`
	srv, p := newTestAnthropicServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "think then answer")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	var thinkingText, textContent string
	var hasError bool
	for _, e := range events {
		switch e.Type {
		case EventThinkingDelta:
			thinkingText += e.Delta
		case EventTextDelta:
			textContent += e.Delta
		case EventError:
			hasError = true
		}
	}

	if hasError {
		t.Error("unexpected error event for out-of-order content blocks")
	}
	if thinkingText != "Hmm..." {
		t.Errorf("expected thinking %q, got %q", "Hmm...", thinkingText)
	}
	if textContent != "Answer" {
		t.Errorf("expected text %q, got %q", "Answer", textContent)
	}

	// Verify stream completed normally with message_end.
	last := events[len(events)-1]
	if last.Type != EventMessageEnd {
		t.Errorf("expected message_end, got %v", last.Type)
	}
}

func TestAnthropicStream_DuplicateBlockIndices(t *testing.T) {
	// Two content_block_start events arrive with the same index.
	// The second overwrites the first in the blocks map. The parser should
	// not panic and should use the latest block state for subsequent deltas.
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_dup","role":"assistant","usage":{"input_tokens":5,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_first","name":"search"}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_second","name":"lookup"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"test\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":10}}

event: message_stop
data: {"type":"message_stop"}

`
	srv, p := newTestAnthropicServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	// Should see two tool_use_start events (one for each block_start).
	var toolStarts []StreamEvent
	var toolDeltas []StreamEvent
	var toolEnds []StreamEvent
	for _, e := range events {
		switch e.Type {
		case EventToolUseStart:
			toolStarts = append(toolStarts, e)
		case EventToolUseDelta:
			toolDeltas = append(toolDeltas, e)
		case EventToolUseEnd:
			toolEnds = append(toolEnds, e)
		}
	}

	if len(toolStarts) != 2 {
		t.Fatalf("expected 2 tool_use_start events, got %d", len(toolStarts))
	}
	if toolStarts[0].ToolCallID != "toolu_first" {
		t.Errorf("expected first start ID %q, got %q", "toolu_first", toolStarts[0].ToolCallID)
	}
	if toolStarts[1].ToolCallID != "toolu_second" {
		t.Errorf("expected second start ID %q, got %q", "toolu_second", toolStarts[1].ToolCallID)
	}

	// Delta should use the second (overwritten) block's tool info.
	if len(toolDeltas) != 1 {
		t.Fatalf("expected 1 tool_use_delta, got %d", len(toolDeltas))
	}
	if toolDeltas[0].ToolCallID != "toolu_second" {
		t.Errorf("expected delta tool ID %q, got %q", "toolu_second", toolDeltas[0].ToolCallID)
	}
	if toolDeltas[0].ToolName != "lookup" {
		t.Errorf("expected delta tool name %q, got %q", "lookup", toolDeltas[0].ToolName)
	}

	// End should also use the second block's tool info.
	if len(toolEnds) != 1 {
		t.Fatalf("expected 1 tool_use_end, got %d", len(toolEnds))
	}
	if toolEnds[0].ToolCallID != "toolu_second" {
		t.Errorf("expected end tool ID %q, got %q", "toolu_second", toolEnds[0].ToolCallID)
	}
}

func TestAnthropicStream_MissingBlockStartBeforeDelta(t *testing.T) {
	// A content_block_delta arrives for an index that never had a
	// content_block_start. The parser should gracefully skip it (bs == nil).
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_miss","role":"assistant","usage":{"input_tokens":5,"output_tokens":0}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"orphan text"}}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"valid text"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}

event: message_stop
data: {"type":"message_stop"}

`
	srv, p := newTestAnthropicServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	// The orphan delta (index 0 with no block_start) should be silently dropped.
	// Only the valid text from index 1 should appear.
	var textParts []string
	var hasError bool
	for _, e := range events {
		switch e.Type {
		case EventTextDelta:
			textParts = append(textParts, e.Delta)
		case EventError:
			hasError = true
		}
	}

	if hasError {
		t.Error("unexpected error event — orphan deltas should be silently skipped")
	}
	fullText := strings.Join(textParts, "")
	if fullText != "valid text" {
		t.Errorf("expected only %q, got %q (orphan delta should be dropped)", "valid text", fullText)
	}

	// Verify stream completed normally.
	last := events[len(events)-1]
	if last.Type != EventMessageEnd {
		t.Errorf("expected message_end, got %v", last.Type)
	}
}

func TestAnthropicStream_LargeToolInputPayload(t *testing.T) {
	// A tool input payload that is large but within the 1MB buffer limit.
	// The parser should correctly accumulate and emit all chunks.
	// We use a large but simple value to keep things within the scanner buffer.
	largeValue := strings.Repeat("a", 100_000)
	chunk1 := `{"data":"` + largeValue[:50_000]
	chunk2 := largeValue[50_000:] + `"}`

	// Build SSE lines with proper JSON encoding using json.Marshal for delta payloads.
	delta1, _ := json.Marshal(map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": chunk1,
		},
	})
	delta2, _ := json.Marshal(map[string]any{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]any{
			"type":         "input_json_delta",
			"partial_json": chunk2,
		},
	})

	sse := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_large\",\"role\":\"assistant\",\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_big\",\"name\":\"ingest\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: " + string(delta1) + "\n\n" +
		"event: content_block_delta\n" +
		"data: " + string(delta2) + "\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":100}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	srv, p := newTestAnthropicServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "ingest data")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	var accumulatedInput string
	var hasError bool
	var hasToolEnd bool
	for _, e := range events {
		switch e.Type {
		case EventToolUseDelta:
			accumulatedInput += e.PartialInput
			if e.ToolCallID != "toolu_big" {
				t.Errorf("expected tool ID %q, got %q", "toolu_big", e.ToolCallID)
			}
		case EventToolUseEnd:
			hasToolEnd = true
		case EventError:
			hasError = true
			t.Errorf("unexpected error: %v", e.Error)
		}
	}

	if hasError {
		t.Fatal("large tool input should not cause errors")
	}
	if !hasToolEnd {
		t.Error("expected tool_use_end event")
	}

	expectedInput := chunk1 + chunk2
	if accumulatedInput != expectedInput {
		t.Errorf("accumulated input length: got %d, want %d", len(accumulatedInput), len(expectedInput))
	}

	// Verify the accumulated input is valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(accumulatedInput), &parsed); err != nil {
		t.Errorf("accumulated input is not valid JSON: %v", err)
	}
	if data, ok := parsed["data"].(string); !ok || len(data) != 100_000 {
		t.Errorf("expected data field of length 100000, got length %d", len(data))
	}

	// Stream should complete normally.
	last := events[len(events)-1]
	if last.Type != EventMessageEnd {
		t.Errorf("expected message_end, got %v", last.Type)
	}
}

func TestBuildRequestBody_RichToolResult(t *testing.T) {
	p := &AnthropicProvider{apiKey: "test"}

	req := StreamRequest{
		Model: "test-model",
		Messages: []Message{
			NewTextMessage(RoleUser, "read the image"),
			{
				Role: RoleAssistant,
				Content: []ContentBlock{
					{Type: ContentTypeToolUse, ToolUseID: "tc-1", ToolName: "read", Input: map[string]any{"path": "/img.png"}},
				},
			},
			NewRichToolResultMessage("tc-1", []ContentBlock{
				{Type: ContentTypeText, Text: "File contents:"},
				{Type: ContentTypeImage, MediaType: "image/png", ImageData: "iVBOR"},
			}, false),
		},
	}

	data, err := p.buildRequestBody(req)
	if err != nil {
		t.Fatalf("buildRequestBody failed: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	msgs := body["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}

	// Third message is the rich tool result.
	toolResultMsg := msgs[2].(map[string]any)
	content := toolResultMsg["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("expected 1 content block in tool result message, got %d", len(content))
	}

	toolResult := content[0].(map[string]any)
	if toolResult["type"] != "tool_result" {
		t.Errorf("expected type 'tool_result', got %v", toolResult["type"])
	}
	if toolResult["tool_use_id"] != "tc-1" {
		t.Errorf("expected tool_use_id 'tc-1', got %v", toolResult["tool_use_id"])
	}

	// The content field should be an array of sub-blocks.
	subContent, ok := toolResult["content"].([]any)
	if !ok {
		t.Fatalf("expected content to be array, got %T: %v", toolResult["content"], toolResult["content"])
	}
	if len(subContent) != 2 {
		t.Fatalf("expected 2 sub-blocks, got %d", len(subContent))
	}

	// First sub-block: text.
	textBlock := subContent[0].(map[string]any)
	if textBlock["type"] != "text" {
		t.Errorf("expected first sub-block type 'text', got %v", textBlock["type"])
	}
	if textBlock["text"] != "File contents:" {
		t.Errorf("expected text 'File contents:', got %v", textBlock["text"])
	}

	// Second sub-block: image.
	imgBlock := subContent[1].(map[string]any)
	if imgBlock["type"] != "image" {
		t.Errorf("expected second sub-block type 'image', got %v", imgBlock["type"])
	}
	source := imgBlock["source"].(map[string]any)
	if source["type"] != "base64" {
		t.Errorf("expected source type 'base64', got %v", source["type"])
	}
	if source["media_type"] != "image/png" {
		t.Errorf("expected media_type 'image/png', got %v", source["media_type"])
	}
	if source["data"] != "iVBOR" {
		t.Errorf("expected data 'iVBOR', got %v", source["data"])
	}
}

func TestAnthropicStream_ScannerBufferOverflow(t *testing.T) {
	// Create a data line that exceeds the 1MB scanner buffer limit.
	longPayload := strings.Repeat("x", 1024*1024)
	sse := "event: message_start\ndata: " + longPayload + "\n\n"

	srv, p := newTestAnthropicServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	var gotError bool
	for _, e := range events {
		if e.Type == EventError {
			gotError = true
			if !strings.Contains(e.Error.Error(), "token too long") {
				t.Errorf("expected 'token too long' error, got %q", e.Error.Error())
			}
		}
	}
	if !gotError {
		t.Error("expected error event from scanner buffer overflow")
	}
}
