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

func TestNewGeminiProvider_NoKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	_, err := NewGeminiProvider("")
	if err == nil {
		t.Fatal("expected error when no API key provided")
	}
	if !strings.Contains(err.Error(), "API key not set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewGeminiProvider_ExplicitKey(t *testing.T) {
	p, err := NewGeminiProvider("test-key-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.apiKey != "test-key-123" {
		t.Errorf("expected key %q, got %q", "test-key-123", p.apiKey)
	}
	if p.Name() != "gemini" {
		t.Errorf("expected name %q, got %q", "gemini", p.Name())
	}
}

func TestNewGeminiProvider_EnvKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "env-key-456")
	p, err := NewGeminiProvider("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.apiKey != "env-key-456" {
		t.Errorf("expected key %q, got %q", "env-key-456", p.apiKey)
	}
}

func TestGeminiBuildRequestBody_Basic(t *testing.T) {
	p := &GeminiProvider{apiKey: "test"}

	req := StreamRequest{
		Model:        "gemini-2.0-flash",
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

	// System instruction.
	sysInstr, ok := body["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatal("expected systemInstruction")
	}
	parts := sysInstr["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("expected 1 system part, got %d", len(parts))
	}
	if parts[0].(map[string]any)["text"] != "You are helpful." {
		t.Errorf("unexpected system text: %v", parts[0])
	}

	// Generation config.
	genCfg, ok := body["generationConfig"].(map[string]any)
	if !ok {
		t.Fatal("expected generationConfig")
	}
	if int(genCfg["maxOutputTokens"].(float64)) != 1024 {
		t.Errorf("expected maxOutputTokens=1024, got %v", genCfg["maxOutputTokens"])
	}

	// Contents.
	contents, ok := body["contents"].([]any)
	if !ok || len(contents) != 1 {
		t.Fatalf("expected 1 content, got %v", body["contents"])
	}
	content := contents[0].(map[string]any)
	if content["role"] != "user" {
		t.Errorf("expected role 'user', got %v", content["role"])
	}
}

func TestGeminiBuildRequestBody_DefaultMaxTokens(t *testing.T) {
	p := &GeminiProvider{apiKey: "test"}

	req := StreamRequest{
		Model:    "gemini-2.0-flash",
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

	genCfg := body["generationConfig"].(map[string]any)
	if int(genCfg["maxOutputTokens"].(float64)) != GeminiDefaultMaxTokens {
		t.Errorf("expected default maxOutputTokens=%d, got %v", GeminiDefaultMaxTokens, genCfg["maxOutputTokens"])
	}
}

func TestGeminiBuildRequestBody_WithTools(t *testing.T) {
	p := &GeminiProvider{apiKey: "test"}

	req := StreamRequest{
		Model:    "gemini-2.0-flash",
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
		t.Fatalf("expected 1 tool set, got %v", body["tools"])
	}
	toolSet := tools[0].(map[string]any)
	decls := toolSet["functionDeclarations"].([]any)
	if len(decls) != 1 {
		t.Fatalf("expected 1 function declaration, got %d", len(decls))
	}
	decl := decls[0].(map[string]any)
	if decl["name"] != "search" {
		t.Errorf("expected function name 'search', got %v", decl["name"])
	}
}

func TestGeminiBuildRequestBody_Thinking(t *testing.T) {
	p := &GeminiProvider{apiKey: "test"}

	req := StreamRequest{
		Model:         "gemini-2.5-pro",
		Messages:      []Message{NewTextMessage(RoleUser, "think")},
		ThinkingLevel: ThinkingMedium,
	}

	data, err := p.buildRequestBody(req)
	if err != nil {
		t.Fatalf("buildRequestBody failed: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	genCfg := body["generationConfig"].(map[string]any)
	thinkCfg, ok := genCfg["thinkingConfig"].(map[string]any)
	if !ok {
		t.Fatal("expected thinkingConfig")
	}
	if int(thinkCfg["thinkingBudget"].(float64)) != 10000 {
		t.Errorf("expected thinkingBudget=10000, got %v", thinkCfg["thinkingBudget"])
	}
}

func TestGeminiBuildRequestBody_NoSystemPrompt(t *testing.T) {
	p := &GeminiProvider{apiKey: "test"}

	req := StreamRequest{
		Model:    "gemini-2.0-flash",
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

	if body["systemInstruction"] != nil {
		t.Error("expected no systemInstruction when SystemPrompt is empty")
	}
}

func TestGeminiBuildRequestBody_MessageRoles(t *testing.T) {
	p := &GeminiProvider{apiKey: "test"}

	req := StreamRequest{
		Model: "gemini-2.0-flash",
		Messages: []Message{
			NewTextMessage(RoleUser, "hello"),
			NewTextMessage(RoleAssistant, "hi there"),
			NewTextMessage(RoleUser, "how are you"),
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

	contents := body["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("expected 3 contents, got %d", len(contents))
	}

	roles := []string{"user", "model", "user"}
	for i, c := range contents {
		content := c.(map[string]any)
		if content["role"] != roles[i] {
			t.Errorf("content[%d]: expected role %q, got %v", i, roles[i], content["role"])
		}
	}
}

// --- SSE stream parsing tests ---

func newTestGeminiServer(t *testing.T, ssePayload string) (*httptest.Server, *GeminiProvider) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header.
		if r.Header.Get("x-goog-api-key") != "test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, ssePayload)
	}))
	p := &GeminiProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}
	return srv, p
}

func TestGeminiStream_SimpleText(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"parts":[{"text":"Hello"}],"role":"model"},"index":0}]}

data: {"candidates":[{"content":{"parts":[{"text":" world"}],"role":"model"},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}

`
	srv, p := newTestGeminiServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.0-flash",
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

func TestGeminiStream_ToolUse(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"search","args":{"query":"hello"}}}],"role":"model"},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":15,"totalTokenCount":35}}

`
	srv, p := newTestGeminiServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewTextMessage(RoleUser, "search for hello")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	var toolStartCount, toolDeltaCount, toolEndCount int
	var accumulatedInput string
	var toolName string
	for _, e := range events {
		switch e.Type {
		case EventToolUseStart:
			toolStartCount++
			toolName = e.ToolName
		case EventToolUseDelta:
			toolDeltaCount++
			accumulatedInput += e.PartialInput
		case EventToolUseEnd:
			toolEndCount++
		}
	}

	if toolStartCount != 1 {
		t.Errorf("expected 1 tool_use_start, got %d", toolStartCount)
	}
	if toolName != "search" {
		t.Errorf("expected tool name %q, got %q", "search", toolName)
	}
	if toolDeltaCount != 1 {
		t.Errorf("expected 1 tool_use_delta, got %d", toolDeltaCount)
	}
	if toolEndCount != 1 {
		t.Errorf("expected 1 tool_use_end, got %d", toolEndCount)
	}

	// Verify the accumulated input is valid JSON with the expected query.
	var args map[string]any
	if err := json.Unmarshal([]byte(accumulatedInput), &args); err != nil {
		t.Fatalf("accumulated input is not valid JSON: %v", err)
	}
	if args["query"] != "hello" {
		t.Errorf("expected query %q, got %v", "hello", args["query"])
	}
}

func TestGeminiStream_Thinking(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"parts":[{"text":"Let me think...","thought":true}],"role":"model"},"index":0}]}

data: {"candidates":[{"content":{"parts":[{"text":"Here is the answer"}],"role":"model"},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":10,"totalTokenCount":15}}

`
	srv, p := newTestGeminiServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.5-pro",
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
	if thinking != "Let me think..." {
		t.Errorf("expected thinking %q, got %q", "Let me think...", thinking)
	}

	text := strings.Join(textParts, "")
	if text != "Here is the answer" {
		t.Errorf("expected text %q, got %q", "Here is the answer", text)
	}
}

func TestGeminiStream_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"Invalid request"}}`)
	}))
	defer srv.Close()

	p := &GeminiProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err == nil {
		t.Fatal("expected error for HTTP 400")
	}
}

func TestGeminiStream_MalformedJSON(t *testing.T) {
	sse := "data: {not valid json}\n\n"

	srv, p := newTestGeminiServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.0-flash",
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
	if !strings.Contains(events[0].Error.Error(), "failed to parse") {
		t.Errorf("expected parse error, got %q", events[0].Error.Error())
	}
}

func TestGeminiStream_UsageTracking(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"totalTokenCount":120}}

`
	srv, p := newTestGeminiServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	last := events[len(events)-1]
	if last.Type != EventMessageEnd {
		t.Fatalf("expected message_end last, got %v", last.Type)
	}
	if last.Usage == nil {
		t.Fatal("expected usage in message_end")
	}
	if last.Usage.InputTokens != 100 {
		t.Errorf("expected input_tokens=100, got %d", last.Usage.InputTokens)
	}
	if last.Usage.OutputTokens != 20 {
		t.Errorf("expected output_tokens=20, got %d", last.Usage.OutputTokens)
	}
}

func TestGeminiStream_EmptyCandidates(t *testing.T) {
	// A chunk with no candidates should be skipped gracefully.
	sse := `data: {"candidates":[],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":0,"totalTokenCount":5}}

data: {"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7}}

`
	srv, p := newTestGeminiServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	var textParts []string
	var hasError bool
	for _, e := range events {
		if e.Type == EventTextDelta {
			textParts = append(textParts, e.Delta)
		}
		if e.Type == EventError {
			hasError = true
		}
	}

	if hasError {
		t.Error("unexpected error event")
	}
	fullText := strings.Join(textParts, "")
	if fullText != "ok" {
		t.Errorf("expected text %q, got %q", "ok", fullText)
	}
}

func TestGeminiStream_MultipleToolCalls(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"search","args":{"q":"go"}}},{"functionCall":{"name":"read","args":{"path":"main.go"}}}],"role":"model"},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"totalTokenCount":30}}

`
	srv, p := newTestGeminiServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewTextMessage(RoleUser, "search and read")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	var toolStarts []StreamEvent
	var toolEnds []StreamEvent
	for _, e := range events {
		switch e.Type {
		case EventToolUseStart:
			toolStarts = append(toolStarts, e)
		case EventToolUseEnd:
			toolEnds = append(toolEnds, e)
		}
	}

	if len(toolStarts) != 2 {
		t.Fatalf("expected 2 tool_use_start events, got %d", len(toolStarts))
	}
	if toolStarts[0].ToolName != "search" {
		t.Errorf("expected first tool name %q, got %q", "search", toolStarts[0].ToolName)
	}
	if toolStarts[1].ToolName != "read" {
		t.Errorf("expected second tool name %q, got %q", "read", toolStarts[1].ToolName)
	}
	if len(toolEnds) != 2 {
		t.Fatalf("expected 2 tool_use_end events, got %d", len(toolEnds))
	}
}

func TestGeminiStream_StreamCutoff(t *testing.T) {
	// Stream ends without a finishReason — should still produce message_end.
	sse := `data: {"candidates":[{"content":{"parts":[{"text":"partial"}],"role":"model"},"index":0}]}

`
	srv, p := newTestGeminiServer(t, sse)
	defer srv.Close()

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}

	events := collectEvents(ch)

	var hasText, hasMessageEnd bool
	for _, e := range events {
		if e.Type == EventTextDelta {
			hasText = true
		}
		if e.Type == EventMessageEnd {
			hasMessageEnd = true
		}
	}

	if !hasText {
		t.Error("expected text event")
	}
	if !hasMessageEnd {
		t.Error("expected message_end event even without finishReason")
	}
}

func TestGeminiStream_AuthHeader(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-goog-api-key")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `data: {"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`+"\n\n")
	}))
	defer srv.Close()

	p := &GeminiProvider{
		apiKey:     "my-secret-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.0-flash",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	collectEvents(ch)

	if gotHeader != "my-secret-key" {
		t.Errorf("expected x-goog-api-key header %q, got %q", "my-secret-key", gotHeader)
	}
}

func TestGeminiStream_URLContainsModel(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `data: {"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"index":0,"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`+"\n\n")
	}))
	defer srv.Close()

	p := &GeminiProvider{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gemini-2.5-pro",
		Messages: []Message{NewTextMessage(RoleUser, "hi")},
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	collectEvents(ch)

	if !strings.Contains(gotURL, "gemini-2.5-pro") {
		t.Errorf("expected URL to contain model name, got %q", gotURL)
	}
	if !strings.Contains(gotURL, "streamGenerateContent") {
		t.Errorf("expected URL to contain streamGenerateContent, got %q", gotURL)
	}
	if !strings.Contains(gotURL, "alt=sse") {
		t.Errorf("expected URL to contain alt=sse, got %q", gotURL)
	}
}

func TestGeminiBuildRequestBody_ImageContent(t *testing.T) {
	p := &GeminiProvider{apiKey: "test"}

	req := StreamRequest{
		Model: "gemini-2.0-flash",
		Messages: []Message{
			{
				Role: RoleUser,
				Content: []ContentBlock{
					{Type: ContentTypeText, Text: "What is in this image?"},
					{Type: ContentTypeImage, MediaType: "image/png", ImageData: "iVBOR"},
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

	contents := body["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}

	content := contents[0].(map[string]any)
	parts := content["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(parts))
	}

	// First part: text.
	textPart := parts[0].(map[string]any)
	if textPart["text"] != "What is in this image?" {
		t.Errorf("expected text content, got %v", textPart["text"])
	}

	// Second part: inlineData.
	imgPart := parts[1].(map[string]any)
	inlineData, ok := imgPart["inlineData"].(map[string]any)
	if !ok {
		t.Fatalf("expected inlineData in second part, got %v", imgPart)
	}
	if inlineData["mimeType"] != "image/png" {
		t.Errorf("expected mimeType 'image/png', got %v", inlineData["mimeType"])
	}
	if inlineData["data"] != "iVBOR" {
		t.Errorf("expected data 'iVBOR', got %v", inlineData["data"])
	}
}

func TestGeminiBuildRequestBody_RichToolResult(t *testing.T) {
	p := &GeminiProvider{apiKey: "test"}

	req := StreamRequest{
		Model: "gemini-2.0-flash",
		Messages: []Message{
			NewRichToolResultMessage("read_file", []ContentBlock{
				{Type: ContentTypeText, Text: "File contents:"},
				{Type: ContentTypeImage, MediaType: "image/jpeg", ImageData: "/9j/4"},
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

	contents := body["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}

	content := contents[0].(map[string]any)
	if content["role"] != "user" {
		t.Errorf("expected role 'user', got %v", content["role"])
	}

	parts := content["parts"].([]any)
	// Should have: functionResponse, text, inlineData = 3 parts.
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parts))
	}

	// First part: functionResponse.
	frPart := parts[0].(map[string]any)
	funcResp, ok := frPart["functionResponse"].(map[string]any)
	if !ok {
		t.Fatalf("expected functionResponse in first part, got %v", frPart)
	}
	if funcResp["name"] != "read_file" {
		t.Errorf("expected name 'read_file', got %v", funcResp["name"])
	}

	// Second part: text.
	textPart := parts[1].(map[string]any)
	if textPart["text"] != "File contents:" {
		t.Errorf("expected text 'File contents:', got %v", textPart["text"])
	}

	// Third part: inlineData.
	imgPart := parts[2].(map[string]any)
	inlineData, ok := imgPart["inlineData"].(map[string]any)
	if !ok {
		t.Fatalf("expected inlineData in third part, got %v", imgPart)
	}
	if inlineData["mimeType"] != "image/jpeg" {
		t.Errorf("expected mimeType 'image/jpeg', got %v", inlineData["mimeType"])
	}
	if inlineData["data"] != "/9j/4" {
		t.Errorf("expected data '/9j/4', got %v", inlineData["data"])
	}
}
