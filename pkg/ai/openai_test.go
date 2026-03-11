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

func TestNewOpenAIProvider_NoKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, err := NewOpenAIProvider("")
	if err == nil {
		t.Fatal("expected error when no API key provided")
	}
	if !strings.Contains(err.Error(), "API key not set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewOpenAIProvider_ExplicitKey(t *testing.T) {
	p, err := NewOpenAIProvider("sk-test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.apiKey != "sk-test-key" {
		t.Errorf("expected key %q, got %q", "sk-test-key", p.apiKey)
	}
	if p.Name() != "openai" {
		t.Errorf("expected name %q, got %q", "openai", p.Name())
	}
}

func TestNewOpenAIProvider_EnvKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env-key")
	p, err := NewOpenAIProvider("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.apiKey != "sk-env-key" {
		t.Errorf("expected key %q, got %q", "sk-env-key", p.apiKey)
	}
}

func TestOpenAIBuildRequestBody_Basic(t *testing.T) {
	p := &OpenAIProvider{apiKey: "test"}

	req := StreamRequest{
		Model:        "gpt-4o",
		SystemPrompt: "You are helpful.",
		Messages: []Message{
			NewTextMessage(RoleUser, "Hello"),
		},
		MaxTokens: 2048,
	}

	data, err := p.buildRequestBody(req)
	if err != nil {
		t.Fatalf("buildRequestBody failed: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if body["model"] != "gpt-4o" {
		t.Errorf("expected model %q, got %v", "gpt-4o", body["model"])
	}
	if body["stream"] != true {
		t.Errorf("expected stream=true, got %v", body["stream"])
	}
	if int(body["max_tokens"].(float64)) != 2048 {
		t.Errorf("expected max_tokens=2048, got %v", body["max_tokens"])
	}

	// stream_options should include usage.
	streamOpts, ok := body["stream_options"].(map[string]any)
	if !ok {
		t.Fatal("expected stream_options")
	}
	if streamOpts["include_usage"] != true {
		t.Errorf("expected include_usage=true, got %v", streamOpts["include_usage"])
	}

	// Messages: system + user = 2.
	msgs, ok := body["messages"].([]any)
	if !ok {
		t.Fatal("expected messages array")
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(msgs))
	}

	sysMsg := msgs[0].(map[string]any)
	if sysMsg["role"] != "system" {
		t.Errorf("expected system message first, got role %v", sysMsg["role"])
	}
	if sysMsg["content"] != "You are helpful." {
		t.Errorf("expected system content %q, got %v", "You are helpful.", sysMsg["content"])
	}

	userMsg := msgs[1].(map[string]any)
	if userMsg["role"] != "user" {
		t.Errorf("expected user message, got role %v", userMsg["role"])
	}
	if userMsg["content"] != "Hello" {
		t.Errorf("expected user content %q, got %v", "Hello", userMsg["content"])
	}
}

func TestOpenAIBuildRequestBody_DefaultMaxTokens(t *testing.T) {
	p := &OpenAIProvider{apiKey: "test"}

	req := StreamRequest{
		Model:    "gpt-4o",
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

	if int(body["max_tokens"].(float64)) != openaiDefaultMaxToks {
		t.Errorf("expected default max_tokens=%d, got %v", openaiDefaultMaxToks, body["max_tokens"])
	}
}

func TestOpenAIBuildRequestBody_WithTools(t *testing.T) {
	p := &OpenAIProvider{apiKey: "test"}

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
		},
	}
	req := StreamRequest{
		Model:    "gpt-4o",
		Messages: []Message{NewTextMessage(RoleUser, "search")},
		Tools: []ToolDef{
			{Name: "search", Description: "Search the web", InputSchema: schema},
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
	if tool["type"] != "function" {
		t.Errorf("expected tool type 'function', got %v", tool["type"])
	}
	fn := tool["function"].(map[string]any)
	if fn["name"] != "search" {
		t.Errorf("expected function name 'search', got %v", fn["name"])
	}
	if fn["description"] != "Search the web" {
		t.Errorf("expected description %q, got %v", "Search the web", fn["description"])
	}
}

func TestOpenAIBuildRequestBody_ToolResultMapping(t *testing.T) {
	p := &OpenAIProvider{apiKey: "test"}

	req := StreamRequest{
		Model: "gpt-4o",
		Messages: []Message{
			NewToolResultMessage("call_abc", "result data", false),
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
	// No system prompt, so just 1 message.
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}

	msg := msgs[0].(map[string]any)
	if msg["role"] != "tool" {
		t.Errorf("expected role 'tool', got %v", msg["role"])
	}
	if msg["tool_call_id"] != "call_abc" {
		t.Errorf("expected tool_call_id 'call_abc', got %v", msg["tool_call_id"])
	}
	if msg["content"] != "result data" {
		t.Errorf("expected content 'result data', got %v", msg["content"])
	}
}

func TestOpenAIBuildRequestBody_AssistantWithToolCalls(t *testing.T) {
	p := &OpenAIProvider{apiKey: "test"}

	req := StreamRequest{
		Model: "gpt-4o",
		Messages: []Message{
			{
				Role: RoleAssistant,
				Content: []ContentBlock{
					{Type: ContentTypeText, Text: "Let me search."},
					{Type: ContentTypeToolUse, ToolUseID: "call_1", ToolName: "search", Input: map[string]any{"q": "test"}},
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

	msgs := body["messages"].([]any)
	msg := msgs[0].(map[string]any)

	if msg["role"] != "assistant" {
		t.Errorf("expected role 'assistant', got %v", msg["role"])
	}
	if msg["content"] != "Let me search." {
		t.Errorf("expected content %q, got %v", "Let me search.", msg["content"])
	}

	toolCalls, ok := msg["tool_calls"].([]any)
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %v", msg["tool_calls"])
	}
	tc := toolCalls[0].(map[string]any)
	if tc["id"] != "call_1" {
		t.Errorf("expected tool call id 'call_1', got %v", tc["id"])
	}
	if tc["type"] != "function" {
		t.Errorf("expected type 'function', got %v", tc["type"])
	}
}

// --- SSE stream parsing tests ---

func newTestOpenAIServer(t *testing.T, ssePayload string) (*httptest.Server, *OpenAIProvider) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, ssePayload)
	}))
	p := &OpenAIProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}
	return srv, p
}

func TestOpenAIStream_SimpleText(t *testing.T) {
	sse := `data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: {"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}

data: [DONE]

`
	srv, p := newTestOpenAIServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4o",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	// Check message_start emitted.
	if events[0].Type != EventMessageStart {
		t.Errorf("expected first event to be message_start, got %v", events[0].Type)
	}

	// Collect text deltas.
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
	if last.Usage.OutputTokens != 5 {
		t.Errorf("expected output_tokens=5, got %d", last.Usage.OutputTokens)
	}
}

func TestOpenAIStream_ToolCall(t *testing.T) {
	sse := `data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"role":"assistant","content":null},"finish_reason":null}]}

data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"search","arguments":""}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"test\"}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-2","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"id":"chatcmpl-2","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":10,"total_tokens":30}}

data: [DONE]

`
	srv, p := newTestOpenAIServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4o",
		Messages: []Message{NewTextMessage(RoleUser, "search test")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	var toolStartCount, toolDeltaCount, toolEndCount int
	var accumulatedArgs string
	for _, e := range events {
		switch e.Type {
		case EventToolUseStart:
			toolStartCount++
			if e.ToolCallID != "call_abc" {
				t.Errorf("expected tool call ID %q, got %q", "call_abc", e.ToolCallID)
			}
			if e.ToolName != "search" {
				t.Errorf("expected tool name %q, got %q", "search", e.ToolName)
			}
		case EventToolUseDelta:
			toolDeltaCount++
			accumulatedArgs += e.PartialInput
			if e.ToolCallID != "call_abc" {
				t.Errorf("expected tool call ID in delta, got %q", e.ToolCallID)
			}
		case EventToolUseEnd:
			toolEndCount++
			if e.ToolCallID != "call_abc" {
				t.Errorf("expected tool call ID in end, got %q", e.ToolCallID)
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
	if accumulatedArgs != `{"q":"test"}` {
		t.Errorf("expected accumulated args %q, got %q", `{"q":"test"}`, accumulatedArgs)
	}
}

func TestOpenAIStream_ToolCallDeltaBeforeID(t *testing.T) {
	// Simulate out-of-order: argument delta arrives before the tool call ID chunk.
	// This must not panic (nil dereference) and should gracefully skip the orphan delta.
	sse := `data: {"id":"chatcmpl-ooo","choices":[{"index":0,"delta":{"role":"assistant","content":null},"finish_reason":null}]}

data: {"id":"chatcmpl-ooo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\":"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-ooo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_late","type":"function","function":{"name":"search","arguments":""}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-ooo","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"hello\"}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-ooo","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"id":"chatcmpl-ooo","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}

data: [DONE]

`
	srv, p := newTestOpenAIServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4o",
		Messages: []Message{NewTextMessage(RoleUser, "search hello")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	// The orphan delta (before ID) should be dropped; no panic should occur.
	var toolStartCount, toolDeltaCount, toolEndCount int
	for _, e := range events {
		switch e.Type {
		case EventToolUseStart:
			toolStartCount++
			if e.ToolCallID != "call_late" {
				t.Errorf("expected tool call ID %q, got %q", "call_late", e.ToolCallID)
			}
		case EventToolUseDelta:
			toolDeltaCount++
		case EventToolUseEnd:
			toolEndCount++
		case EventError:
			t.Fatalf("unexpected error event: %v", e.Error)
		}
	}

	if toolStartCount != 1 {
		t.Errorf("expected 1 tool_use_start, got %d", toolStartCount)
	}
	// Only the delta AFTER the ID should be emitted (the orphan is dropped).
	if toolDeltaCount != 1 {
		t.Errorf("expected 1 tool_use_delta (orphan dropped), got %d", toolDeltaCount)
	}
	if toolEndCount != 1 {
		t.Errorf("expected 1 tool_use_end, got %d", toolEndCount)
	}
}

func TestOpenAIStream_ToolCallMalformedDelta(t *testing.T) {
	// Deltas for unknown indices with no ID should never panic.
	sse := `data: {"id":"chatcmpl-mal","choices":[{"index":0,"delta":{"role":"assistant","content":null},"finish_reason":null}]}

data: {"id":"chatcmpl-mal","choices":[{"index":0,"delta":{"tool_calls":[{"index":5,"function":{"arguments":"garbage"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-mal","choices":[{"index":0,"delta":{"tool_calls":[{"index":99,"function":{"arguments":"more"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-mal","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`
	srv, p := newTestOpenAIServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4o",
		Messages: []Message{NewTextMessage(RoleUser, "test")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	// No tool events should be emitted — all deltas are for unknown indices.
	for _, e := range events {
		switch e.Type {
		case EventToolUseStart, EventToolUseDelta, EventToolUseEnd:
			t.Errorf("unexpected tool event %v for orphan delta", e.Type)
		case EventError:
			t.Fatalf("unexpected error event: %v", e.Error)
		}
	}
}

func TestOpenAIStream_DoneHandling(t *testing.T) {
	sse := `data: {"id":"chatcmpl-3","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}

data: [DONE]

`
	srv, p := newTestOpenAIServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4o",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	// Should have: message_start, text_delta, message_end.
	var hasStart, hasEnd bool
	for _, e := range events {
		if e.Type == EventMessageStart {
			hasStart = true
		}
		if e.Type == EventMessageEnd {
			hasEnd = true
		}
	}
	if !hasStart {
		t.Error("expected message_start event")
	}
	if !hasEnd {
		t.Error("expected message_end event from [DONE]")
	}
}

func TestOpenAIStream_EmptyChoices(t *testing.T) {
	// Verify that chunks with empty or missing Choices don't cause panics.
	// The usage-only chunk and the interspersed empty-choices chunk must be
	// silently skipped without a nil-pointer dereference.
	sse := `data: {"id":"chatcmpl-e","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}

data: {"id":"chatcmpl-e","choices":[],"usage":null}

data: {"id":"chatcmpl-e","choices":[{"index":0,"delta":{"content":" there"},"finish_reason":null}]}

data: {"id":"chatcmpl-e","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: {"id":"chatcmpl-e","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}

data: [DONE]

`
	srv, p := newTestOpenAIServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4o",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	// Collect text deltas — should get "hi" and " there".
	var textParts []string
	for _, e := range events {
		if e.Type == EventTextDelta {
			textParts = append(textParts, e.Delta)
		}
	}
	fullText := strings.Join(textParts, "")
	if fullText != "hi there" {
		t.Errorf("expected text %q, got %q", "hi there", fullText)
	}

	// Should end cleanly with usage.
	last := events[len(events)-1]
	if last.Type != EventMessageEnd {
		t.Errorf("expected last event to be message_end, got %v", last.Type)
	}
	if last.Usage == nil {
		t.Fatal("expected usage in message_end")
	}
	if last.Usage.InputTokens != 5 || last.Usage.OutputTokens != 2 {
		t.Errorf("expected usage 5/2, got %d/%d", last.Usage.InputTokens, last.Usage.OutputTokens)
	}

	// No error events should be present.
	for _, e := range events {
		if e.Type == EventError {
			t.Errorf("unexpected error event: %v", e.Error)
		}
	}
}

func TestOpenAIStream_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid api key"}}`)
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		apiKey:     "bad-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4o",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error for HTTP 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("expected status 401 in error, got %q", err.Error())
	}
}

func TestOpenAIStream_MalformedChunk(t *testing.T) {
	sse := "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n" +
		"data: {this is not valid json}\n\n" +
		"data: [DONE]\n\n"

	srv, p := newTestOpenAIServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4o",
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
	if !strings.Contains(last.Error.Error(), "parse chunk") {
		t.Errorf("expected error about parsing chunk, got %q", last.Error.Error())
	}
}

func TestOpenAIStream_PartialToolUse(t *testing.T) {
	// Stream cuts off mid-tool-call: no finish_reason or [DONE].
	sse := "data: {\"id\":\"chatcmpl-p\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":null},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-p\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_partial\",\"type\":\"function\",\"function\":{\"name\":\"search\",\"arguments\":\"\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"id\":\"chatcmpl-p\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"q\\\":\"}}]},\"finish_reason\":null}]}\n\n"

	srv, p := newTestOpenAIServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4o",
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

func TestOpenAIStream_ContextCancellation(t *testing.T) {
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-c\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"},\"finish_reason\":null}]}\n\n")
		flusher.Flush()
		close(ready)
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := &OpenAIProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := p.Stream(ctx, StreamRequest{
		Model:    "gpt-4o",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	<-ready

	// Read the initial message_start event.
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

func TestOpenAIStream_ScannerBufferOverflow(t *testing.T) {
	// Create a data line that exceeds the 1MB scanner buffer limit.
	longPayload := strings.Repeat("x", 1024*1024)
	sse := "data: " + longPayload + "\n\n"

	srv, p := newTestOpenAIServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4o",
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
