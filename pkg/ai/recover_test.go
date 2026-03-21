package ai

import (
	"context"
	"io"
	"strings"
	"testing"
)

// panicReader is an io.ReadCloser that panics on Read after returning some initial data.
type panicReader struct {
	data    string
	read    bool
	msg     string
	closeFn func() error
}

func (r *panicReader) Read(p []byte) (int, error) {
	if !r.read {
		r.read = true
		n := copy(p, r.data)
		return n, nil
	}
	panic(r.msg)
}

func (r *panicReader) Close() error {
	if r.closeFn != nil {
		return r.closeFn()
	}
	return nil
}

func TestAnthropicStream_PanicRecovery(t *testing.T) {
	p := &AnthropicProvider{}
	ch := make(chan StreamEvent, 64)

	body := &panicReader{
		data: "event: message_start\ndata: ",
		msg:  "test panic in anthropic stream",
	}

	// readSSEStream runs synchronously in this call — the goroutine wrapper is in Stream().
	p.readSSEStream(context.Background(), body, ch)

	events := collectEvents(ch)
	if len(events) == 0 {
		t.Fatal("expected at least one event from panic recovery, got none")
	}
	last := events[len(events)-1]
	if last.Type != EventError {
		t.Fatalf("expected last event to be EventError, got %v", last.Type)
	}
	if !strings.Contains(last.Error.Error(), "panicked") {
		t.Errorf("expected panic error message, got %q", last.Error.Error())
	}
}

func TestOpenAIStream_PanicRecovery(t *testing.T) {
	p := &OpenAIProvider{}
	ch := make(chan StreamEvent, 64)

	body := &panicReader{
		data: "data: ",
		msg:  "test panic in openai stream",
	}

	p.readSSEStream(context.Background(), body, ch)

	events := collectEventsFromChan(ch)
	if len(events) == 0 {
		t.Fatal("expected at least one event from panic recovery, got none")
	}
	last := events[len(events)-1]
	if last.Type != EventError {
		t.Fatalf("expected last event to be EventError, got %v", last.Type)
	}
	if !strings.Contains(last.Error.Error(), "panicked") {
		t.Errorf("expected panic error message, got %q", last.Error.Error())
	}
}

func TestGeminiStream_PanicRecovery(t *testing.T) {
	p := &GeminiProvider{}
	ch := make(chan StreamEvent, 64)

	body := &panicReader{
		data: "data: ",
		msg:  "test panic in gemini stream",
	}

	p.readSSEStream(context.Background(), body, ch)

	events := collectEventsFromChan(ch)
	if len(events) == 0 {
		t.Fatal("expected at least one event from panic recovery, got none")
	}
	last := events[len(events)-1]
	if last.Type != EventError {
		t.Fatalf("expected last event to be EventError, got %v", last.Type)
	}
	if !strings.Contains(last.Error.Error(), "panicked") {
		t.Errorf("expected panic error message, got %q", last.Error.Error())
	}
}

func TestOllamaStream_PanicRecovery(t *testing.T) {
	p := &OllamaProvider{}
	ch := make(chan StreamEvent, 64)

	body := &panicReader{
		data: `{"message":{"role":"assistant","content":"hi"}}` + "\n",
		msg:  "test panic in ollama stream",
	}

	p.readStream(context.Background(), body, ch)

	events := collectEventsFromChan(ch)
	if len(events) == 0 {
		t.Fatal("expected at least one event from panic recovery, got none")
	}
	last := events[len(events)-1]
	if last.Type != EventError {
		t.Fatalf("expected last event to be EventError, got %v", last.Type)
	}
	if !strings.Contains(last.Error.Error(), "panicked") {
		t.Errorf("expected panic error message, got %q", last.Error.Error())
	}
}

// collectEventsFromChan is like collectEvents but avoids redeclaring it.
func collectEventsFromChan(ch <-chan StreamEvent) []StreamEvent {
	var events []StreamEvent
	for evt := range ch {
		events = append(events, evt)
	}
	return events
}

// Verify that a stream goroutine that does NOT panic still works correctly
// (the recover does not interfere with normal operation).
func TestAnthropicStream_RecoverDoesNotAffectNormalFlow(t *testing.T) {
	p := &AnthropicProvider{}
	ch := make(chan StreamEvent, 64)

	// Minimal valid SSE that produces a message_start and closes cleanly.
	sseData := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-3","usage":{"input_tokens":10,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	body := io.NopCloser(strings.NewReader(sseData))
	p.readSSEStream(context.Background(), body, ch)

	events := collectEventsFromChan(ch)
	var hasText, hasEnd bool
	for _, e := range events {
		if e.Type == EventError {
			t.Errorf("unexpected error event: %v", e.Error)
		}
		if e.Type == EventTextDelta && e.Delta == "hello" {
			hasText = true
		}
		if e.Type == EventMessageEnd {
			hasEnd = true
		}
	}
	if !hasText {
		t.Error("expected text delta event with 'hello'")
	}
	if !hasEnd {
		t.Error("expected message end event")
	}
}
