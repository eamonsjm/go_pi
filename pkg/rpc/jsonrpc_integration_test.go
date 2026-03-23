package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

// --- mock provider for rpc tests -------------------------------------------

type rpcMockProvider struct {
	mu       sync.Mutex
	streamFn func(ctx context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error)
}

func (m *rpcMockProvider) Name() string { return "rpc-mock" }

func (m *rpcMockProvider) Stream(ctx context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
	m.mu.Lock()
	fn := m.streamFn
	m.mu.Unlock()
	return fn(ctx, req)
}

func (m *rpcMockProvider) setStreamFn(fn func(ctx context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error)) {
	m.mu.Lock()
	m.streamFn = fn
	m.mu.Unlock()
}

// textStreamFn returns a streamFn that produces a simple text assistant reply.
func textStreamFn(text string) func(context.Context, ai.StreamRequest) (<-chan ai.StreamEvent, error) {
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

// errorStreamFn returns a streamFn that returns an error immediately.
func errorStreamFn(err error) func(context.Context, ai.StreamRequest) (<-chan ai.StreamEvent, error) {
	return func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
		return nil, err
	}
}

// blockingStreamFn returns a streamFn that blocks until the context is cancelled.
func blockingStreamFn() func(context.Context, ai.StreamRequest) (<-chan ai.StreamEvent, error) {
	return func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
		ch := make(chan ai.StreamEvent, 10)
		go func() {
			defer close(ch)
			ch <- ai.StreamEvent{Type: ai.EventMessageStart}
			<-ctx.Done()
			ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
		}()
		return ch, nil
	}
}


func newTestAgentLoop(provider ai.Provider) *agent.AgentLoop {
	return agent.NewAgentLoop(provider, tools.NewRegistry())
}

// --- helpers ----------------------------------------------------------------

// readResponses reads newline-delimited JSON from the buffer and returns
// decoded Response objects. Notifications are returned separately.
func readLines(data []byte) []json.RawMessage {
	var lines []json.RawMessage
	for _, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		lines = append(lines, json.RawMessage(line))
	}
	return lines
}

func decodeResponse(raw json.RawMessage) (Response, error) {
	var resp Response
	err := json.Unmarshal(raw, &resp)
	return resp, err
}

func decodeNotification(raw json.RawMessage) (Notification, error) {
	var n Notification
	err := json.Unmarshal(raw, &n)
	return n, err
}

// isNotification returns true if the JSON message has a "method" field (notification)
// vs an "id" field (response).
func isNotification(raw json.RawMessage) bool {
	var probe struct {
		Method string          `json:"method"`
		ID     json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	return probe.Method != "" && probe.ID == nil
}

// rpcRequest builds a JSON-RPC 2.0 request string.
func rpcRequest(id int, method string, params any) string {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		p, _ := json.Marshal(params)
		req["params"] = json.RawMessage(p)
	}
	data, _ := json.Marshal(req)
	return string(data) + "\n"
}

// --- rpcServer tests with real AgentLoop ------------------------------------

func TestRPCPromptLifecycle(t *testing.T) {
	mock := &rpcMockProvider{streamFn: textStreamFn("hello world")}
	loop := newTestAgentLoop(mock)

	var out bytes.Buffer
	s := &rpcServer{agentLoop: loop, writer: &out}

	input := rpcRequest(1, "prompt", PromptParams{Text: "say hello"})
	ctx := context.Background()

	s.serve(ctx, strings.NewReader(input), func() {})

	lines := readLines(out.Bytes())
	if len(lines) == 0 {
		t.Fatal("expected at least one response line")
	}

	// Collect notifications and final response.
	var notifications []Notification
	var finalResp *Response
	for _, line := range lines {
		if isNotification(line) {
			n, err := decodeNotification(line)
			if err != nil {
				t.Fatalf("failed to decode notification: %v", err)
			}
			notifications = append(notifications, n)
		} else {
			r, err := decodeResponse(line)
			if err != nil {
				t.Fatalf("failed to decode response: %v", err)
			}
			finalResp = &r
		}
	}

	if finalResp == nil {
		t.Fatal("expected a final response")
	}
	if finalResp.Error != nil {
		t.Fatalf("unexpected error: %v", finalResp.Error)
	}

	// The result should contain the prompt text.
	resultJSON, _ := json.Marshal(finalResp.Result)
	var result PromptResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if result.Text != "hello world" {
		t.Errorf("result.Text = %q, want %q", result.Text, "hello world")
	}

	// Should have received agent/event notifications.
	if len(notifications) == 0 {
		t.Error("expected at least one agent/event notification")
	}
	for _, n := range notifications {
		if n.Method != "agent/event" {
			t.Errorf("notification method = %q, want agent/event", n.Method)
		}
	}
}

func TestRPCPromptEmptyText(t *testing.T) {
	mock := &rpcMockProvider{streamFn: textStreamFn("unused")}
	loop := newTestAgentLoop(mock)

	var out bytes.Buffer
	s := &rpcServer{agentLoop: loop, writer: &out}

	input := rpcRequest(1, "prompt", PromptParams{Text: ""})
	s.serve(context.Background(), strings.NewReader(input), func() {})

	lines := readLines(out.Bytes())
	if len(lines) == 0 {
		t.Fatal("expected a response")
	}
	resp, err := decodeResponse(lines[len(lines)-1])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for empty text")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestRPCPromptAlreadyRunning(t *testing.T) {
	// serve() is synchronous, so we test the "already running" guard
	// by calling handleRequest directly from two goroutines.
	mock := &rpcMockProvider{streamFn: blockingStreamFn()}
	loop := newTestAgentLoop(mock)

	var out bytes.Buffer
	s := &rpcServer{agentLoop: loop, writer: &out}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	origFn := mock.streamFn
	mock.setStreamFn(func(ctx context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
		close(started) // signal that prompt is running
		return origFn(ctx, req)
	})

	// Start first prompt in background.
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		req := Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "prompt", Params: json.RawMessage(`{"text":"first"}`)}
		s.handleRequest(ctx, cancel, req)
	}()

	// Wait for the first prompt to actually start running.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first prompt did not start")
	}

	// Send second prompt while first is still running.
	req2 := Request{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "prompt", Params: json.RawMessage(`{"text":"second"}`)}
	s.handleRequest(ctx, cancel, req2)

	// Cancel to unblock the first prompt.
	cancel()
	<-done1

	lines := readLines(out.Bytes())
	var foundAlreadyRunning bool
	for _, line := range lines {
		if isNotification(line) {
			continue
		}
		resp, err := decodeResponse(line)
		if err != nil {
			continue
		}
		if resp.Error != nil && resp.Error.Code == CodeInternalError &&
			strings.Contains(resp.Error.Message, "already running") {
			foundAlreadyRunning = true
		}
	}
	if !foundAlreadyRunning {
		t.Error("expected 'already running' error for second concurrent prompt")
	}
}

func TestRPCCancelMethod(t *testing.T) {
	// Test that the cancel RPC method calls agentLoop.Cancel and returns success.
	mock := &rpcMockProvider{streamFn: textStreamFn("unused")}
	loop := newTestAgentLoop(mock)

	var out bytes.Buffer
	s := &rpcServer{agentLoop: loop, writer: &out}

	input := rpcRequest(1, "cancel", nil)
	s.serve(context.Background(), strings.NewReader(input), func() {})

	lines := readLines(out.Bytes())
	if len(lines) == 0 {
		t.Fatal("expected a response")
	}
	resp, err := decodeResponse(lines[0])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	resultJSON, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(resultJSON), "cancelled") {
		t.Errorf("expected cancelled status, got %s", resultJSON)
	}
}

func TestRPCCancelDuringPrompt(t *testing.T) {
	// Test that calling Cancel on the agent loop unblocks a running prompt.
	started := make(chan struct{})
	mock := &rpcMockProvider{streamFn: func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
		ch := make(chan ai.StreamEvent, 10)
		go func() {
			defer close(ch)
			ch <- ai.StreamEvent{Type: ai.EventMessageStart}
			close(started)
			<-ctx.Done()
			ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
		}()
		return ch, nil
	}}
	loop := newTestAgentLoop(mock)

	var out bytes.Buffer
	s := &rpcServer{agentLoop: loop, writer: &out}

	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		defer close(done)
		req := Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "prompt", Params: json.RawMessage(`{"text":"blocking"}`)}
		s.handleRequest(ctx, func() {}, req)
	}()

	// Wait for prompt to start.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not start")
	}

	// Cancel the agent loop (simulates cancel RPC).
	loop.Cancel()

	// Prompt should complete.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("prompt did not exit after Cancel")
	}
}

func TestRPCSteerValid(t *testing.T) {
	mock := &rpcMockProvider{streamFn: textStreamFn("ok")}
	loop := newTestAgentLoop(mock)

	var out bytes.Buffer
	s := &rpcServer{agentLoop: loop, writer: &out}

	input := rpcRequest(1, "steer", PromptParams{Text: "change direction"})
	s.serve(context.Background(), strings.NewReader(input), func() {})

	lines := readLines(out.Bytes())
	if len(lines) == 0 {
		t.Fatal("expected a response")
	}
	resp, err := decodeResponse(lines[0])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	resultJSON, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(resultJSON), "steered") {
		t.Errorf("expected steered status, got %s", resultJSON)
	}
}

func TestRPCSteerEmptyText(t *testing.T) {
	mock := &rpcMockProvider{streamFn: textStreamFn("ok")}
	loop := newTestAgentLoop(mock)

	var out bytes.Buffer
	s := &rpcServer{agentLoop: loop, writer: &out}

	input := rpcRequest(1, "steer", PromptParams{Text: ""})
	s.serve(context.Background(), strings.NewReader(input), func() {})

	lines := readLines(out.Bytes())
	if len(lines) == 0 {
		t.Fatal("expected a response")
	}
	resp, err := decodeResponse(lines[0])
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for empty steer text")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestRPCShutdownCancelsContext(t *testing.T) {
	mock := &rpcMockProvider{streamFn: textStreamFn("ok")}
	loop := newTestAgentLoop(mock)

	var out bytes.Buffer
	s := &rpcServer{agentLoop: loop, writer: &out}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// After shutdown, serve should return because context is cancelled.
	// Additional requests after shutdown should not be processed.
	input := rpcRequest(1, "shutdown", nil) + rpcRequest(2, "cancel", nil)
	s.serve(ctx, strings.NewReader(input), cancel)

	lines := readLines(out.Bytes())
	// Should have exactly one response (shutdown). The cancel request
	// should not be processed because context was cancelled.
	var responses []Response
	for _, line := range lines {
		if !isNotification(line) {
			r, err := decodeResponse(line)
			if err == nil {
				responses = append(responses, r)
			}
		}
	}
	if len(responses) != 1 {
		t.Errorf("expected 1 response (shutdown only), got %d", len(responses))
	}
	if responses[0].Error != nil {
		t.Fatalf("unexpected shutdown error: %v", responses[0].Error)
	}
	resultJSON, _ := json.Marshal(responses[0].Result)
	if !strings.Contains(string(resultJSON), "shutting_down") {
		t.Errorf("expected shutting_down status, got %s", resultJSON)
	}
}

func TestRPCPromptProviderError(t *testing.T) {
	mock := &rpcMockProvider{streamFn: errorStreamFn(fmt.Errorf("provider unavailable"))}
	loop := newTestAgentLoop(mock)

	var out bytes.Buffer
	s := &rpcServer{agentLoop: loop, writer: &out}

	input := rpcRequest(1, "prompt", PromptParams{Text: "hello"})
	s.serve(context.Background(), strings.NewReader(input), func() {})

	lines := readLines(out.Bytes())
	// Find the final response (not notifications).
	var finalResp *Response
	for _, line := range lines {
		if !isNotification(line) {
			r, err := decodeResponse(line)
			if err == nil {
				finalResp = &r
			}
		}
	}
	if finalResp == nil {
		t.Fatal("expected a response")
	}
	if finalResp.Error == nil {
		t.Fatal("expected error response for provider error")
	}
	if finalResp.Error.Code != CodeInternalError {
		t.Errorf("code = %d, want %d", finalResp.Error.Code, CodeInternalError)
	}
}

func TestRPCMultipleSequentialPrompts(t *testing.T) {
	callCount := 0
	mock := &rpcMockProvider{streamFn: func(ctx context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
		callCount++
		n := callCount
		ch := make(chan ai.StreamEvent, 10)
		go func() {
			defer close(ch)
			ch <- ai.StreamEvent{Type: ai.EventMessageStart}
			ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: fmt.Sprintf("reply%d", n)}
			ch <- ai.StreamEvent{Type: ai.EventMessageEnd, Usage: &ai.Usage{InputTokens: 10, OutputTokens: 5}}
		}()
		return ch, nil
	}}
	loop := newTestAgentLoop(mock)

	var out bytes.Buffer
	s := &rpcServer{agentLoop: loop, writer: &out}

	// Send two prompts sequentially.
	input := rpcRequest(1, "prompt", PromptParams{Text: "first"}) +
		rpcRequest(2, "prompt", PromptParams{Text: "second"})
	s.serve(context.Background(), strings.NewReader(input), func() {})

	lines := readLines(out.Bytes())
	var responses []Response
	for _, line := range lines {
		if !isNotification(line) {
			r, err := decodeResponse(line)
			if err == nil && r.Error == nil {
				responses = append(responses, r)
			}
		}
	}
	if len(responses) < 2 {
		t.Fatalf("expected at least 2 successful responses, got %d", len(responses))
	}
}

func TestRPCEmptyLineSkipped(t *testing.T) {
	s, out := newTestServer()

	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Empty lines should be silently skipped.
	input := "\n\n" + `{"jsonrpc":"2.0","id":1,"method":"shutdown"}` + "\n\n"
	s.serve(context.Background(), strings.NewReader(input), cancel)

	lines := readLines(out.Bytes())
	if len(lines) != 1 {
		t.Errorf("expected 1 response, got %d", len(lines))
	}
}

func TestRPCSendResponseWriteError(t *testing.T) {
	cancelled := false
	cancel := func() { cancelled = true }

	s := &rpcServer{writer: &failWriter{}}
	s.sendResponse(Response{JSONRPC: "2.0", ID: json.RawMessage(`1`)}, cancel)

	if !cancelled {
		t.Error("expected cancel to be called on write error")
	}
}

func TestRPCSendNotificationWriteError(t *testing.T) {
	cancelled := false
	cancel := func() { cancelled = true }

	s := &rpcServer{writer: &failWriter{}}
	s.sendNotification(Notification{JSONRPC: "2.0", Method: "test"}, cancel)

	if !cancelled {
		t.Error("expected cancel to be called on write error")
	}
}

func TestRPCSendResponseNilCancel(t *testing.T) {
	// Ensure nil cancel doesn't panic on write error.
	s := &rpcServer{writer: &failWriter{}}
	s.sendResponse(Response{JSONRPC: "2.0", ID: json.RawMessage(`1`)}, nil)
	// No panic = pass.
}

func TestRPCSendNotificationNilCancel(t *testing.T) {
	s := &rpcServer{writer: &failWriter{}}
	s.sendNotification(Notification{JSONRPC: "2.0", Method: "test"}, nil)
	// No panic = pass.
}

// --- stdinProxy tests -------------------------------------------------------

func TestStdinProxyCopy(t *testing.T) {
	// We can't test with real os.Stdin, but we can test the proxy's
	// cancellation behavior by creating one and closing the pipe.
	cancelled := false
	cancel := func() { cancelled = true }

	pr, pw := io.Pipe()
	sp := &stdinProxy{pr: pr, pw: pw, cancel: cancel, done: make(chan struct{})}

	// Simulate the copy goroutine reading from the write end.
	go func() {
		defer close(sp.done)
		_, _ = io.Copy(sp.pw, strings.NewReader("test data"))
		_ = sp.pw.Close()
		sp.cancel()
	}()

	// Read from the proxy's read end.
	data, err := io.ReadAll(sp.pr)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "test data" {
		t.Errorf("data = %q, want %q", string(data), "test data")
	}

	// Wait for copy to finish.
	<-sp.done

	if !cancelled {
		t.Error("expected cancel to be called after copy completes")
	}
}

func TestStdinProxyClose(t *testing.T) {
	cancelled := false
	cancel := func() { cancelled = true }

	pr, pw := io.Pipe()
	sp := &stdinProxy{pr: pr, pw: pw, cancel: cancel, done: make(chan struct{})}

	// Start the copy goroutine (reading from pw which will be closed by sp.close).
	go func() {
		defer close(sp.done)
		_, _ = io.Copy(sp.pw, pr) // pr closing will cause this to end
		_ = sp.pw.Close()
		sp.cancel()
	}()

	// Close the proxy — should unblock the copy goroutine.
	sp.close()

	if !cancelled {
		t.Error("expected cancel after close")
	}
}

// --- failWriter for testing write errors ------------------------------------

type failWriter struct{}

func (f *failWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("write failed")
}

// --- context cancellation during serve --------------------------------------

func TestRPCServeContextCancellation(t *testing.T) {
	var out bytes.Buffer
	s := &rpcServer{writer: &out}

	ctx, cancel := context.WithCancel(context.Background())

	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.serve(ctx, pr, cancel)
	}()

	// Cancel context before sending any requests.
	cancel()

	// Send a request — serve should exit because context is cancelled.
	_, _ = pw.Write([]byte(rpcRequest(1, "shutdown", nil)))
	_ = pw.Close()

	select {
	case <-done:
		// serve returned, success.
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not exit after context cancellation")
	}
}

func TestRPCScannerError(t *testing.T) {
	var out bytes.Buffer
	s := &rpcServer{writer: &out}

	// Create a reader that returns an error after some data.
	r := &errorReader{data: []byte(`{"jsonrpc":"2.0","id":1,"method":"shutdown"}` + "\n"), err: fmt.Errorf("read error")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.serve(ctx, r, cancel)

	// Should have processed the first valid request before the error.
	lines := readLines(out.Bytes())
	if len(lines) == 0 {
		t.Error("expected at least one response before scanner error")
	}
}

type errorReader struct {
	data []byte
	err  error
	read bool
}

func (r *errorReader) Read(p []byte) (int, error) {
	if !r.read && len(r.data) > 0 {
		r.read = true
		n := copy(p, r.data)
		return n, nil
	}
	return 0, r.err
}
