package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestStreamableHTTPJSONResponse(t *testing.T) {
	resp := `{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "test-session-123")
		fmt.Fprint(w, resp)
	}))
	defer server.Close()

	tr := NewStreamableHTTP(server.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msg := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	if err := tr.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-tr.Receive():
		if string(got) != resp {
			t.Errorf("got %s, want %s", got, resp)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for response")
	}

	// Verify session ID was stored.
	tr.mu.RLock()
	sid := tr.sessionID
	tr.mu.RUnlock()
	if sid != "test-session-123" {
		t.Errorf("sessionID = %q, want %q", sid, "test-session-123")
	}
}

func TestStreamableHTTPSSEResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		fmt.Fprint(w, "id: evt-1\n")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"status\":\"ok\"}}\n")
		fmt.Fprint(w, "\n")
		flusher.Flush()

		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n")
		fmt.Fprint(w, "\n")
		flusher.Flush()
	}))
	defer server.Close()

	tr := NewStreamableHTTP(server.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msg := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	if err := tr.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Should receive two messages from the SSE stream.
	for i := 0; i < 2; i++ {
		select {
		case got := <-tr.Receive():
			if !json.Valid(got) {
				t.Errorf("message %d: invalid JSON: %s", i, got)
			}
		case <-ctx.Done():
			t.Fatalf("timeout waiting for message %d", i)
		}
	}

	// Verify event ID was tracked.
	tr.lastEventMu.Lock()
	lastID := tr.lastEventIDs["post"]
	tr.lastEventMu.Unlock()
	if lastID != "evt-1" {
		t.Errorf("lastEventID = %q, want %q", lastID, "evt-1")
	}
}

func TestStreamableHTTPSSEMultiLineData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("expected http.Flusher")
		}

		// Send a multi-line SSE data field (per SSE spec, multiple data:
		// lines in one event should be joined with newlines).
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\n")
		fmt.Fprint(w, "data: \"result\":{\"multi\":true}}\n")
		fmt.Fprint(w, "\n")
		flusher.Flush()
	}))
	defer server.Close()

	tr := NewStreamableHTTP(server.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	msg := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	if err := tr.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-tr.Receive():
		// The two data lines should be joined with a newline.
		want := "{\"jsonrpc\":\"2.0\",\"id\":1,\n\"result\":{\"multi\":true}}"
		if string(got) != want {
			t.Errorf("got %q, want %q", string(got), want)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for multi-line SSE message")
	}
}

func TestStreamableHTTPBatchRejected(t *testing.T) {
	tr := NewStreamableHTTP("http://unused", nil)
	err := tr.Send(context.Background(), json.RawMessage(`[{"jsonrpc":"2.0"}]`))
	if err == nil {
		t.Error("expected error for batch request")
	}
	if !strings.Contains(err.Error(), "batch") {
		t.Errorf("error should mention batch: %v", err)
	}
}

func TestStreamableHTTPHeaders(t *testing.T) {
	var gotHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer server.Close()

	tr := NewStreamableHTTP(server.URL, map[string]string{
		"Authorization": "Bearer test-token",
	})
	tr.mu.Lock()
	tr.sessionID = "sid-abc"
	tr.negotiatedVersion = "2025-11-25"
	tr.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"test"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Drain response.
	<-tr.Receive()

	if got := gotHeaders.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
	}
	if got := gotHeaders.Get("Mcp-Session-Id"); got != "sid-abc" {
		t.Errorf("Mcp-Session-Id = %q, want %q", got, "sid-abc")
	}
	if got := gotHeaders.Get("MCP-Protocol-Version"); got != "2025-11-25" {
		t.Errorf("MCP-Protocol-Version = %q, want %q", got, "2025-11-25")
	}
}

func TestStreamableHTTPCloseWithSession(t *testing.T) {
	var gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tr := NewStreamableHTTP(server.URL, nil)
	tr.mu.Lock()
	tr.sessionID = "sess-to-close"
	tr.mu.Unlock()

	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
}

func TestStreamableHTTPCloseNoSession(t *testing.T) {
	tr := NewStreamableHTTP("http://unused", nil)
	// No session ID → Close should be a no-op (no HTTP call).
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestStreamableHTTPCloseClosesIncoming(t *testing.T) {
	tr := NewStreamableHTTP("http://unused", nil)

	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Receive channel must be closed after Close(), so range or receive
	// terminates instead of blocking forever (the original bug).
	select {
	case _, ok := <-tr.Receive():
		if ok {
			t.Error("expected incoming channel to be closed, but received a message")
		}
	case <-time.After(time.Second):
		t.Fatal("Receive() blocked after Close() — incoming channel not closed")
	}
}

func TestStreamableHTTPCloseWithActiveSSEStream(t *testing.T) {
	// Server holds an SSE stream open; Close() must still close the incoming
	// channel so consumers unblock.
	streamReady := make(chan struct{})
	holdConn := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n")
		flusher.Flush()
		close(streamReady)
		// Hold connection open until test signals.
		<-holdConn
	}))
	defer server.Close()
	defer close(holdConn) // Unblock handler before server.Close() waits for it

	tr := NewStreamableHTTP(server.URL, nil)

	ctx := context.Background()
	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"test"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Wait for the SSE stream to deliver one message.
	<-streamReady
	select {
	case <-tr.Receive():
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SSE message")
	}

	// Now close — incoming must close even though SSE stream is still open.
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case _, ok := <-tr.Receive():
		if ok {
			t.Error("expected incoming channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("Receive() blocked after Close() with active SSE stream")
	}
}

func TestStreamableHTTPCloseStopsSSEGoroutines(t *testing.T) {
	// Verify that Close() actually terminates parseSSEStream goroutines by
	// closing response bodies, not just signaling done. Without body closure,
	// goroutines block on scanner.Scan() until the server drops the connection.
	holdConn := make(chan struct{})
	streamReady := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n\n")
		flusher.Flush()
		close(streamReady)
		<-holdConn
	}))
	defer server.Close()
	defer close(holdConn)

	tr := NewStreamableHTTP(server.URL, nil)

	ctx := context.Background()
	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"test"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	<-streamReady
	select {
	case <-tr.Receive():
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SSE message")
	}

	// Snapshot goroutine count before close.
	before := runtime.NumGoroutine()

	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The parseSSEStream goroutine should exit promptly after Close()
	// closes its response body.
	deadline := time.After(2 * time.Second)
	for {
		after := runtime.NumGoroutine()
		if after < before {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("goroutine leak: %d goroutines before Close, %d after (expected decrease)",
				before, runtime.NumGoroutine())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func TestStreamableHTTPClose405Accepted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	tr := NewStreamableHTTP(server.URL, nil)
	tr.mu.Lock()
	tr.sessionID = "sess-405"
	tr.mu.Unlock()

	// 405 is acceptable (server disallows client-initiated termination).
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: unexpected error: %v", err)
	}
}

func TestStreamableHTTPInvalidSessionIDRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "bad session\nid")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer server.Close()

	tr := NewStreamableHTTP(server.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tr.Send(ctx, json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"test"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	<-tr.Receive()

	// Session ID with spaces/newlines should be rejected.
	tr.mu.RLock()
	sid := tr.sessionID
	tr.mu.RUnlock()
	if sid != "" {
		t.Errorf("invalid session ID should not have been stored, got %q", sid)
	}
}

func TestStreamableHTTPOpenServerStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/test\"}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	tr := NewStreamableHTTP(server.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tr.OpenServerStream(ctx); err != nil {
		t.Fatalf("OpenServerStream: %v", err)
	}

	select {
	case got := <-tr.Receive():
		if !json.Valid(got) {
			t.Errorf("invalid JSON from server stream: %s", got)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for server-initiated message")
	}
}

func TestStreamableHTTPOpenServerStreamReconnect(t *testing.T) {
	var getCount int
	var lastEventIDHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
			return
		}
		getCount++
		lastEventIDHeader = r.Header.Get("Last-Event-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// Send one event with an ID, then close the stream (simulates disconnect).
		fmt.Fprintf(w, "id: evt-%d\n", getCount)
		fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notify\",\"params\":{\"n\":%d}}\n\n", getCount)
		flusher.Flush()
		// Handler returns → server closes the response body → stream ends.
	}))
	defer server.Close()

	tr := NewStreamableHTTP(server.URL, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// First connection.
	if err := tr.OpenServerStream(ctx); err != nil {
		t.Fatalf("OpenServerStream (1st): %v", err)
	}
	select {
	case got := <-tr.Receive():
		if !strings.Contains(string(got), `"n":1`) {
			t.Errorf("1st message: got %s, want n:1", got)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for 1st message")
	}

	// Wait for parseSSEStream to exit and clear getStreamActive.
	// The server handler returned, so the body is closed quickly.
	deadline := time.After(2 * time.Second)
	for {
		tr.getStreamMu.Lock()
		active := tr.getStreamActive
		tr.getStreamMu.Unlock()
		if !active {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timeout waiting for getStreamActive to clear")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// Second connection (reconnect).
	if err := tr.OpenServerStream(ctx); err != nil {
		t.Fatalf("OpenServerStream (2nd): %v", err)
	}
	select {
	case got := <-tr.Receive():
		if !strings.Contains(string(got), `"n":2`) {
			t.Errorf("2nd message: got %s, want n:2", got)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for 2nd message")
	}

	if getCount != 2 {
		t.Errorf("expected 2 GET requests, got %d", getCount)
	}

	// Verify Last-Event-ID was sent on reconnection.
	if lastEventIDHeader != "evt-1" {
		t.Errorf("Last-Event-ID on reconnect = %q, want %q", lastEventIDHeader, "evt-1")
	}
}
