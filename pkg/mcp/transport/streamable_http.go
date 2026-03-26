package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Compile-time interface check.
var _ Transport = (*StreamableHTTP)(nil)

// StreamableHTTP implements Transport for remote MCP servers over
// Streamable HTTP per the MCP 2025-11-25 spec.
//
// A single HTTP endpoint handles both directions:
//   - Client → Server: HTTP POST with JSON-RPC body. Server responds with
//     either a JSON body or an SSE stream.
//   - Server → Client: Server may keep an SSE stream open on GET or POST
//     responses for notifications and streaming results.
type StreamableHTTP struct {
	endpoint string            // e.g., "https://mcp.example.com/mcp"
	headers  map[string]string // auth headers (interpolated at config load time)

	mu                sync.RWMutex
	sessionID         string // from Mcp-Session-Id response header
	negotiatedVersion string // from initialize handshake

	httpClient *http.Client

	// GET stream for server-initiated messages.
	// Uses mutex+bool instead of sync.Once so parseSSEStream can clear the
	// flag on exit, allowing reconnection after disconnect.
	getStreamMu     sync.Mutex
	getStreamActive bool

	// SSE event ID tracking for reconnection.
	lastEventIDs map[string]string
	lastEventMu  sync.Mutex

	// Unified channel for all incoming messages (from POST response streams
	// and GET streams).
	incoming chan json.RawMessage

	// Active SSE response bodies tracked for cleanup in Close().
	// parseSSEStream registers its body on entry; Close() closes all
	// tracked bodies to unblock goroutines blocked on scanner.Scan().
	activeBodyMu sync.Mutex
	activeBodies []io.ReadCloser

	closeMu sync.Mutex
	closed  bool
	done    chan struct{} // closed on Close() to signal writer goroutines
}

// NewStreamableHTTP creates a new StreamableHTTP transport.
// The connection is established lazily on the first Send call.
func NewStreamableHTTP(endpoint string, headers map[string]string) *StreamableHTTP {
	return &StreamableHTTP{
		endpoint:     endpoint,
		headers:      headers,
		httpClient:   &http.Client{},
		lastEventIDs: make(map[string]string),
		incoming:     make(chan json.RawMessage, incomingBufferSize),
		done:         make(chan struct{}),
	}
}

// Connect is a no-op for StreamableHTTP — connections are made on demand.
func (t *StreamableHTTP) Connect(_ context.Context) error {
	return nil
}

// Send transmits a JSON-RPC message via HTTP POST and handles the response.
// The server may respond with a direct JSON body or open an SSE stream.
func (t *StreamableHTTP) Send(ctx context.Context, msg json.RawMessage) error {
	// The 2025-11-25 spec forbids JSON-RPC batching in POST request bodies.
	if len(msg) > 0 && msg[0] == '[' {
		return fmt.Errorf("JSON-RPC batch requests are not allowed per MCP spec; send individual messages")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", t.endpoint, bytes.NewReader(msg))
	if err != nil {
		return fmt.Errorf("creating POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	t.mu.RLock()
	negotiatedVersion := t.negotiatedVersion
	sessionID := t.sessionID
	t.mu.RUnlock()

	// MCP-Protocol-Version header is REQUIRED on all HTTP requests after initialization.
	if negotiatedVersion != "" {
		req.Header.Set("MCP-Protocol-Version", negotiatedVersion)
	}
	// Mcp-Session-Id per 2025-11-25 spec canonical casing.
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}

	for k, v := range t.headers {
		req.Header.Set(k, v)
	}

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", t.endpoint, err)
	}

	// Reject non-2xx responses before Content-Type dispatch. Without this
	// check, error responses (400/401/429/500) that happen to carry a JSON
	// Content-Type are silently forwarded to the incoming channel as if they
	// were valid JSON-RPC messages.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return fmt.Errorf("HTTP %d from MCP server: %s", resp.StatusCode, body)
	}

	// Content-Type dispatch: handle both response formats.
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "text/event-stream"):
		go t.parseSSEStream(resp.Body, "post")
	case strings.HasPrefix(ct, "application/json"):
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("reading JSON response: %w", readErr)
		}
		t.trySend(body)
	default:
		// Robustness: attempt JSON parse as fallback for unknown Content-Type.
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if len(body) > 0 {
			log.Printf("mcp http: unexpected Content-Type %q from MCP server, attempting JSON parse", ct)
			t.trySend(body)
		}
	}

	// Store Mcp-Session-Id from response header.
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		if err := validateSessionID(sid); err != nil {
			log.Printf("mcp http: invalid session ID from MCP server: %v", err)
		} else {
			t.mu.Lock()
			t.sessionID = sid
			t.mu.Unlock()
		}
	}
	return nil
}

// SetNegotiatedVersion stores the protocol version from the initialize
// handshake. Called by MCPServer after processing the initialize response.
func (t *StreamableHTTP) SetNegotiatedVersion(version string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.negotiatedVersion = version
}

// OpenServerStream issues a GET to receive server-initiated messages.
// Call after initialization; the stream feeds into the same incoming
// channel as POST responses. Safe to call multiple times — only starts
// a new stream if no active GET stream exists. After a disconnect,
// parseSSEStream clears the active flag, allowing the next call to
// re-establish the stream with Last-Event-ID for resumption.
func (t *StreamableHTTP) OpenServerStream(ctx context.Context) error {
	t.getStreamMu.Lock()
	if t.getStreamActive {
		t.getStreamMu.Unlock()
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", t.endpoint, nil)
	if err != nil {
		t.getStreamMu.Unlock()
		return fmt.Errorf("creating GET request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	t.mu.RLock()
	if t.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	if t.negotiatedVersion != "" {
		req.Header.Set("MCP-Protocol-Version", t.negotiatedVersion)
	}
	t.mu.RUnlock()

	for k, v := range t.headers {
		req.Header.Set(k, v)
	}

	// Track last event ID for reconnection.
	t.lastEventMu.Lock()
	if lastID, ok := t.lastEventIDs["get"]; ok {
		req.Header.Set("Last-Event-ID", lastID)
	}
	t.lastEventMu.Unlock()

	resp, err := t.httpClient.Do(req)
	if err != nil {
		t.getStreamMu.Unlock()
		return fmt.Errorf("GET stream %s: %w", t.endpoint, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.getStreamMu.Unlock()
		return fmt.Errorf("GET stream returned HTTP %d", resp.StatusCode)
	}

	t.getStreamActive = true
	t.getStreamMu.Unlock()

	go t.parseSSEStream(resp.Body, "get")
	return nil
}

// Receive returns the unified channel for all incoming messages.
func (t *StreamableHTTP) Receive() <-chan json.RawMessage {
	return t.incoming
}

// Close terminates the HTTP session via DELETE and releases resources.
// It closes the incoming channel so consumers (e.g. MCPClient.demux) exit
// their range loops cleanly.
func (t *StreamableHTTP) Close() error {
	t.closeMu.Lock()
	if t.closed {
		t.closeMu.Unlock()
		return nil
	}
	t.closed = true
	close(t.done)
	t.closeMu.Unlock()

	// Close active SSE response bodies to unblock parseSSEStream goroutines
	// that are blocked on scanner.Scan(). Without this, those goroutines
	// leak until the remote server closes the connection.
	t.activeBodyMu.Lock()
	for _, body := range t.activeBodies {
		_ = body.Close()
	}
	t.activeBodies = nil
	t.activeBodyMu.Unlock()

	// Close incoming so consumers unblock. Writer goroutines are signaled
	// via done and guard sends with trySend.
	close(t.incoming)

	t.mu.RLock()
	sessionID := t.sessionID
	negotiatedVersion := t.negotiatedVersion
	t.mu.RUnlock()

	if sessionID == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "DELETE", t.endpoint, nil)
	if err != nil {
		return fmt.Errorf("creating session termination request: %w", err)
	}
	req.Header.Set("Mcp-Session-Id", sessionID)
	if negotiatedVersion != "" {
		req.Header.Set("MCP-Protocol-Version", negotiatedVersion)
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("session termination DELETE failed: %w", err)
	}
	_ = resp.Body.Close()

	// 405 Method Not Allowed is valid (server disallows client-initiated termination).
	if resp.StatusCode == http.StatusMethodNotAllowed {
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("session termination returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// trySend sends msg to the incoming channel, returning false if the transport
// is shutting down. The recover handles the narrow race where Close() closes
// the channel between the select choosing the send case and the send executing.
func (t *StreamableHTTP) trySend(msg json.RawMessage) (sent bool) {
	defer func() {
		if recover() != nil {
			sent = false
		}
	}()
	select {
	case t.incoming <- msg:
		return true
	case <-t.done:
		return false
	}
}

// parseSSEStream reads SSE events from body and sends parsed JSON messages
// to the incoming channel via trySend.
// streamName identifies the stream for event ID tracking ("get" or "post").
// For "get" streams, clears getStreamActive on exit to allow reconnection.
func (t *StreamableHTTP) parseSSEStream(body io.ReadCloser, streamName string) {
	// Track this body so Close() can interrupt scanner.Scan() by closing it.
	t.activeBodyMu.Lock()
	t.activeBodies = append(t.activeBodies, body)
	t.activeBodyMu.Unlock()

	defer func() { _ = body.Close() }()
	if streamName == "get" {
		defer func() {
			t.getStreamMu.Lock()
			t.getStreamActive = false
			t.getStreamMu.Unlock()
		}()
	}

	scanner := bufio.NewScanner(body)
	var data strings.Builder
	var eventID string

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data: "):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(line, "data: "))
		case strings.HasPrefix(line, "id: "):
			eventID = strings.TrimPrefix(line, "id: ")
		case line == "":
			if data.Len() > 0 {
				msg := json.RawMessage(data.String())

				if eventID != "" {
					t.lastEventMu.Lock()
					t.lastEventIDs[streamName] = eventID
					t.lastEventMu.Unlock()
				}

				// Defensive: demux batch arrays. The spec doesn't define batch
				// support, but handle it per the robustness principle.
				if len(msg) > 0 && msg[0] == '[' {
					var batch []json.RawMessage
					if json.Unmarshal(msg, &batch) == nil {
						for _, m := range batch {
							if !t.trySend(m) {
								return
							}
						}
					} else {
						if !t.trySend(msg) {
							return
						}
					}
				} else {
					if !t.trySend(msg) {
						return
					}
				}
				data.Reset()
				eventID = ""
			}
		}
	}
}

// validateSessionID checks that a session ID contains only visible ASCII
// characters (0x21-0x7E) per the MCP spec and is not unreasonably long.
func validateSessionID(id string) error {
	if len(id) == 0 {
		return fmt.Errorf("empty session ID")
	}
	if len(id) > 1024 {
		return fmt.Errorf("session ID too long: %d bytes", len(id))
	}
	for _, c := range id {
		if c < 0x21 || c > 0x7E {
			return fmt.Errorf("invalid session ID character: %U", c)
		}
	}
	return nil
}
