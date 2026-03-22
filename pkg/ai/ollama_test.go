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

func TestNewOllamaProvider_DefaultHost(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	p, err := NewOllamaProvider("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.baseURL != "http://localhost:11434" {
		t.Errorf("expected default URL, got %q", p.baseURL)
	}
	if p.Name() != "ollama" {
		t.Errorf("expected name %q, got %q", "ollama", p.Name())
	}
}

func TestNewOllamaProvider_EnvHost(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://myhost:9999")
	p, err := NewOllamaProvider("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.baseURL != "http://myhost:9999" {
		t.Errorf("expected env URL, got %q", p.baseURL)
	}
}

func TestNewOllamaProvider_ExplicitHost(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://env:1111")
	p, err := NewOllamaProvider("http://explicit:2222")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.baseURL != "http://explicit:2222" {
		t.Errorf("expected explicit URL, got %q", p.baseURL)
	}
}

func TestOllamaBuildRequestBody_Basic(t *testing.T) {
	p := &OllamaProvider{baseURL: "http://localhost:11434"}

	req := StreamRequest{
		Model:        "llama3.2",
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

	if body["model"] != "llama3.2" {
		t.Errorf("expected model %q, got %v", "llama3.2", body["model"])
	}
	if body["stream"] != true {
		t.Errorf("expected stream=true, got %v", body["stream"])
	}

	msgs := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(msgs))
	}

	sysMsg := msgs[0].(map[string]any)
	if sysMsg["role"] != "system" {
		t.Errorf("expected system role, got %v", sysMsg["role"])
	}
	if sysMsg["content"] != "You are helpful." {
		t.Errorf("expected system content, got %v", sysMsg["content"])
	}

	opts := body["options"].(map[string]any)
	if int(opts["num_predict"].(float64)) != 2048 {
		t.Errorf("expected num_predict=2048, got %v", opts["num_predict"])
	}
}

func TestOllamaBuildRequestBody_WithTools(t *testing.T) {
	p := &OllamaProvider{baseURL: "http://localhost:11434"}

	req := StreamRequest{
		Model: "llama3.2",
		Messages: []Message{
			NewTextMessage(RoleUser, "What's the weather?"),
		},
		Tools: []ToolDef{
			{
				Name:        "get_weather",
				Description: "Get weather for a location",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string"},
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

	tools := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("expected type 'function', got %v", tool["type"])
	}

	fn := tool["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("expected tool name 'get_weather', got %v", fn["name"])
	}
}

func TestOllamaStream_TextResponse(t *testing.T) {
	// Simulate Ollama's newline-delimited JSON streaming.
	chunks := []string{
		`{"model":"llama3.2","created_at":"2024-01-01T00:00:00Z","message":{"role":"assistant","content":"Hello"},"done":false}`,
		`{"model":"llama3.2","created_at":"2024-01-01T00:00:01Z","message":{"role":"assistant","content":" world"},"done":false}`,
		`{"model":"llama3.2","created_at":"2024-01-01T00:00:02Z","message":{"role":"assistant","content":""},"done":true,"prompt_eval_count":10,"eval_count":5}`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("expected path /api/chat, got %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, chunk := range chunks {
			fmt.Fprintln(w, chunk)
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	p := &OllamaProvider{
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "llama3.2",
		Messages: []Message{NewTextMessage(RoleUser, "Hi")},
	})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	// Expected: MessageStart, TextDelta("Hello"), TextDelta(" world"), MessageEnd
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d: %+v", len(events), events)
	}

	if events[0].Type != EventMessageStart {
		t.Errorf("event[0]: expected MessageStart, got %v", events[0].Type)
	}
	if events[1].Type != EventTextDelta || events[1].Delta != "Hello" {
		t.Errorf("event[1]: expected TextDelta 'Hello', got %v %q", events[1].Type, events[1].Delta)
	}
	if events[2].Type != EventTextDelta || events[2].Delta != " world" {
		t.Errorf("event[2]: expected TextDelta ' world', got %v %q", events[2].Type, events[2].Delta)
	}
	if events[3].Type != EventMessageEnd {
		t.Errorf("event[3]: expected MessageEnd, got %v", events[3].Type)
	}
	if events[3].Usage == nil {
		t.Fatal("event[3]: expected usage info")
	}
	if events[3].Usage.InputTokens != 10 {
		t.Errorf("expected input tokens 10, got %d", events[3].Usage.InputTokens)
	}
	if events[3].Usage.OutputTokens != 5 {
		t.Errorf("expected output tokens 5, got %d", events[3].Usage.OutputTokens)
	}
}

func TestOllamaStream_ToolCallResponse(t *testing.T) {
	chunks := []string{
		`{"model":"llama3.2","created_at":"2024-01-01T00:00:00Z","message":{"role":"assistant","content":"","tool_calls":[{"function":{"name":"get_weather","arguments":{"location":"London"}}}]},"done":false}`,
		`{"model":"llama3.2","created_at":"2024-01-01T00:00:01Z","message":{"role":"assistant","content":""},"done":true,"prompt_eval_count":20,"eval_count":15}`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, chunk := range chunks {
			fmt.Fprintln(w, chunk)
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	p := &OllamaProvider{
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "llama3.2",
		Messages: []Message{NewTextMessage(RoleUser, "Weather in London?")},
	})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	// Expected: MessageStart, ToolUseStart, ToolUseDelta, ToolUseEnd, MessageEnd
	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d: %+v", len(events), events)
	}

	if events[1].Type != EventToolUseStart {
		t.Errorf("event[1]: expected ToolUseStart, got %v", events[1].Type)
	}
	if events[1].ToolName != "get_weather" {
		t.Errorf("event[1]: expected tool name 'get_weather', got %q", events[1].ToolName)
	}
	if !strings.HasPrefix(events[1].ToolCallID, "ollama_call_") {
		t.Errorf("event[1]: expected ollama_call_ prefix, got %q", events[1].ToolCallID)
	}

	if events[2].Type != EventToolUseDelta {
		t.Errorf("event[2]: expected ToolUseDelta, got %v", events[2].Type)
	}
	if !strings.Contains(events[2].PartialInput, "London") {
		t.Errorf("event[2]: expected args containing 'London', got %q", events[2].PartialInput)
	}

	if events[3].Type != EventToolUseEnd {
		t.Errorf("event[3]: expected ToolUseEnd, got %v", events[3].Type)
	}
}

func TestOllamaStream_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"model 'nonexistent' not found"}`)
	}))
	defer server.Close()

	p := &OllamaProvider{
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "nonexistent",
		Messages: []Message{NewTextMessage(RoleUser, "Hi")},
	})
	if err == nil {
		t.Fatal("expected error for 404 response")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != 404 {
		t.Errorf("expected status 404, got %d", apiErr.StatusCode)
	}
	if apiErr.Provider != "ollama" {
		t.Errorf("expected provider 'ollama', got %q", apiErr.Provider)
	}
}

func TestOllamaMapMessages_ToolResult(t *testing.T) {
	msg := NewToolResultMessage("call_123", "The weather is sunny", false)
	result := mapToOllamaMessages(msg)

	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Role != "tool" {
		t.Errorf("expected role 'tool', got %q", result[0].Role)
	}
	if result[0].Content != "The weather is sunny" {
		t.Errorf("expected content, got %q", result[0].Content)
	}
}

func TestOllamaStream_ContextCancellation(t *testing.T) {
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		fmt.Fprintln(w, `{"model":"llama3.2","created_at":"2024-01-01T00:00:00Z","message":{"role":"assistant","content":"Hello"},"done":false}`)
		flusher.Flush()
		close(ready)
		<-r.Context().Done()
	}))
	defer srv.Close()

	p := &OllamaProvider{
		baseURL:    srv.URL,
		httpClient: srv.Client(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := p.Stream(ctx, StreamRequest{
		Model:    "llama3.2",
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
