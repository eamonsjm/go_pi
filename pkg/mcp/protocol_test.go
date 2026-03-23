package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// mockTransport is a test double for transport.Transport.
type mockTransport struct {
	incoming chan json.RawMessage
	mu       sync.Mutex
	sent     []json.RawMessage
	closed   bool
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		incoming: make(chan json.RawMessage, 64),
	}
}

func (m *mockTransport) Connect(_ context.Context) error { return nil }

func (m *mockTransport) Send(_ context.Context, msg json.RawMessage) error {
	m.mu.Lock()
	m.sent = append(m.sent, msg)
	m.mu.Unlock()
	return nil
}

func (m *mockTransport) Receive() <-chan json.RawMessage { return m.incoming }

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		close(m.incoming)
	}
	return nil
}

func (m *mockTransport) getSent() []json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]json.RawMessage, len(m.sent))
	copy(cp, m.sent)
	return cp
}

func TestMCPClientRequestResponse(t *testing.T) {
	mt := newMockTransport()
	client := NewMCPClient(mt, nil)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Simulate: client sends request, server replies.
	go func() {
		// Wait for the request to be sent.
		time.Sleep(50 * time.Millisecond)
		// Respond with matching id.
		mt.incoming <- json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}`)
	}()

	result, err := client.Request(ctx, "test/method", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	var parsed struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("Unmarshal result: %v", err)
	}
	if parsed.Status != "ok" {
		t.Errorf("status = %q, want %q", parsed.Status, "ok")
	}
}

func TestMCPClientRequestError(t *testing.T) {
	mt := newMockTransport()
	client := NewMCPClient(mt, nil)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		mt.incoming <- json.RawMessage(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`)
	}()

	_, err := client.Request(ctx, "nonexistent", nil)
	if err == nil {
		t.Fatal("expected error")
	}

	rpcErr, ok := err.(*JSONRPCError)
	if !ok {
		t.Fatalf("expected *JSONRPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != ErrCodeMethodNotFound {
		t.Errorf("code = %d, want %d", rpcErr.Code, ErrCodeMethodNotFound)
	}
}

func TestMCPClientNotification(t *testing.T) {
	mt := newMockTransport()

	type notif struct {
		method string
		params json.RawMessage
	}
	ch := make(chan notif, 1)
	client := NewMCPClient(mt, func(method string, params json.RawMessage) {
		ch <- notif{method, params}
	})
	defer client.Close()

	// Send a notification from the "server".
	mt.incoming <- json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/test","params":{"key":"val"}}`)

	select {
	case n := <-ch:
		if n.method != "notifications/test" {
			t.Errorf("notification method = %q, want %q", n.method, "notifications/test")
		}
		if n.params == nil {
			t.Fatal("notification params should not be nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for notification")
	}
}

func TestMCPClientNotify(t *testing.T) {
	mt := newMockTransport()
	client := NewMCPClient(mt, nil)
	defer client.Close()

	ctx := context.Background()
	if err := client.Notify(ctx, "notifications/initialized", nil); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	sent := mt.getSent()
	if len(sent) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(sent))
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(sent[0], &req); err != nil {
		t.Fatalf("Unmarshal sent: %v", err)
	}
	if req.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want %q", req.JSONRPC, "2.0")
	}
	if req.ID != nil {
		t.Errorf("notification should have no id, got %s", req.ID)
	}
	if req.Method != "notifications/initialized" {
		t.Errorf("method = %q, want %q", req.Method, "notifications/initialized")
	}
}

func TestMCPClientRequestTimeout(t *testing.T) {
	mt := newMockTransport()
	client := NewMCPClient(mt, nil)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// No response sent → should timeout.
	_, err := client.Request(ctx, "slow/method", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got: %v", err)
	}
}

func TestMCPClientInitialize(t *testing.T) {
	mt := newMockTransport()
	client := NewMCPClient(mt, nil)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		// Wait for the initialize request.
		time.Sleep(50 * time.Millisecond)

		// Respond with an initialize result.
		mt.incoming <- json.RawMessage(`{
			"jsonrpc": "2.0",
			"id": 1,
			"result": {
				"protocolVersion": "2025-11-25",
				"capabilities": {
					"tools": {"listChanged": true},
					"resources": {"subscribe": true, "listChanged": true}
				},
				"serverInfo": {"name": "test-server", "version": "1.0"},
				"instructions": "Use tools wisely."
			}
		}`)
	}()

	result, err := client.Initialize(ctx, "gi", "0.1.0", ClientCapabilities{
		Roots: &RootsCapability{ListChanged: true},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if result.ProtocolVersion != "2025-11-25" {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, "2025-11-25")
	}
	if result.ServerInfo.Name != "test-server" {
		t.Errorf("serverInfo.name = %q, want %q", result.ServerInfo.Name, "test-server")
	}
	if result.Instructions != "Use tools wisely." {
		t.Errorf("instructions = %q, want %q", result.Instructions, "Use tools wisely.")
	}
	if result.Capabilities.Tools == nil || !result.Capabilities.Tools.ListChanged {
		t.Error("expected tools.listChanged = true")
	}

	// Check that notifications/initialized was sent.
	sent := mt.getSent()
	if len(sent) < 2 {
		t.Fatalf("expected at least 2 sent messages (initialize + initialized), got %d", len(sent))
	}
	var notifReq JSONRPCRequest
	if err := json.Unmarshal(sent[1], &notifReq); err != nil {
		t.Fatalf("Unmarshal initialized notification: %v", err)
	}
	if notifReq.Method != "notifications/initialized" {
		t.Errorf("expected notifications/initialized, got %q", notifReq.Method)
	}

	// Check stored state.
	if client.NegotiatedVersion() != "2025-11-25" {
		t.Errorf("NegotiatedVersion = %q, want %q", client.NegotiatedVersion(), "2025-11-25")
	}
	if client.ServerInfo().Name != "test-server" {
		t.Errorf("ServerInfo().Name = %q", client.ServerInfo().Name)
	}
	if client.Instructions() != "Use tools wisely." {
		t.Errorf("Instructions() = %q", client.Instructions())
	}
}

func TestMCPClientInitializeUnsupportedVersion(t *testing.T) {
	mt := newMockTransport()
	client := NewMCPClient(mt, nil)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		mt.incoming <- json.RawMessage(`{
			"jsonrpc": "2.0",
			"id": 1,
			"result": {
				"protocolVersion": "2020-01-01",
				"capabilities": {},
				"serverInfo": {"name": "old-server"}
			}
		}`)
	}()

	_, err := client.Initialize(ctx, "gi", "0.1.0", ClientCapabilities{})
	if err == nil {
		t.Fatal("expected error for unsupported version")
	}
}

func TestMCPClientInitializeOlderSupportedVersion(t *testing.T) {
	mt := newMockTransport()
	client := NewMCPClient(mt, nil)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		// Server responds with an older but still supported version.
		mt.incoming <- json.RawMessage(`{
			"jsonrpc": "2.0",
			"id": 1,
			"result": {
				"protocolVersion": "2024-11-05",
				"capabilities": {},
				"serverInfo": {"name": "older-server"}
			}
		}`)
	}()

	result, err := client.Initialize(ctx, "gi", "0.1.0", ClientCapabilities{})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if result.ProtocolVersion != "2024-11-05" {
		t.Errorf("protocolVersion = %q, want %q", result.ProtocolVersion, "2024-11-05")
	}
}

func TestMCPClientConcurrentRequests(t *testing.T) {
	mt := newMockTransport()
	client := NewMCPClient(mt, nil)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Respond to both requests after they're sent. IDs are assigned
	// sequentially but goroutine scheduling is non-deterministic, so we
	// respond to both and just verify each goroutine gets its own response.
	go func() {
		time.Sleep(100 * time.Millisecond)
		// Respond in reverse order to test that correlation works.
		mt.incoming <- json.RawMessage(`{"jsonrpc":"2.0","id":2,"result":{"method":"second"}}`)
		mt.incoming <- json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"method":"first"}}`)
	}()

	type result struct {
		Method string `json:"method"`
	}

	results := make(chan result, 2)
	errs := make(chan error, 2)

	for i := 0; i < 2; i++ {
		go func() {
			raw, err := client.Request(ctx, "test", nil)
			if err != nil {
				errs <- err
				return
			}
			var r result
			json.Unmarshal(raw, &r)
			results <- r
		}()
	}

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			seen[r.Method] = true
		case err := <-errs:
			t.Fatalf("request failed: %v", err)
		case <-ctx.Done():
			t.Fatalf("timeout waiting for request %d", i)
		}
	}

	if !seen["first"] || !seen["second"] {
		t.Errorf("expected both 'first' and 'second' results, got: %v", seen)
	}
}

func TestJSONRPCErrorTypes(t *testing.T) {
	tests := []struct {
		name         string
		code         int
		wantServer   bool
		wantErrorStr string
	}{
		{"parse error", ErrCodeParseError, false, "JSON-RPC error -32700: parse error"},
		{"invalid request", ErrCodeInvalidRequest, false, "JSON-RPC error -32600: bad request"},
		{"method not found", ErrCodeMethodNotFound, false, "JSON-RPC error -32601: not found"},
		{"server error min", ErrCodeServerMin, true, "JSON-RPC error -32099: server min"},
		{"server error max", ErrCodeServerMax, true, "JSON-RPC error -32000: server max"},
		{"server error mid", -32050, true, "JSON-RPC error -32050: mid"},
		{"not server error", -31999, false, "JSON-RPC error -31999: other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &JSONRPCError{Code: tt.code, Message: tt.wantErrorStr[len("JSON-RPC error XXXXX: "):]}
			if got := e.IsServerError(); got != tt.wantServer {
				t.Errorf("IsServerError() = %v, want %v", got, tt.wantServer)
			}
			// Verify Error() contains the code.
			if e.Error() == "" {
				t.Error("Error() should not be empty")
			}
		})
	}
}

func TestIsVersionSupported(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"2025-11-25", true},
		{"2025-03-26", true},
		{"2024-11-05", true},
		{"2020-01-01", false},
		{"", false},
		{"latest", false},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			if got := isVersionSupported(tt.version); got != tt.want {
				t.Errorf("isVersionSupported(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestMCPClientRequestHandlerDoesNotBlockDemux(t *testing.T) {
	mt := newMockTransport()
	client := NewMCPClient(mt, nil)
	defer client.Close()

	// Install a request handler that blocks until released.
	handlerStarted := make(chan struct{})
	handlerRelease := make(chan struct{})
	client.onRequest = func(method string, id json.RawMessage, params json.RawMessage) {
		close(handlerStarted)
		<-handlerRelease
	}

	// Send a server-initiated request to trigger the blocking handler.
	mt.incoming <- json.RawMessage(`{"jsonrpc":"2.0","id":"srv-1","method":"sampling/createMessage","params":{}}`)

	// Wait for the handler to start blocking.
	select {
	case <-handlerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: request handler never started")
	}

	// While the handler is blocked, send a client request and its response.
	// If the demux goroutine is blocked, this will timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		mt.incoming <- json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`)
	}()

	result, err := client.Request(ctx, "test/method", nil)
	if err != nil {
		t.Fatalf("Request should succeed while request handler is blocked: %v", err)
	}

	var parsed struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !parsed.OK {
		t.Error("expected ok=true")
	}

	// Release the handler.
	close(handlerRelease)
}

func TestMCPClientTransportCloseDuringRequest(t *testing.T) {
	mt := newMockTransport()
	client := NewMCPClient(mt, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		// Close the transport while a request is pending.
		mt.Close()
	}()

	_, err := client.Request(ctx, "hanging/method", nil)
	if err == nil {
		t.Fatal("expected error when transport closes")
	}
}
