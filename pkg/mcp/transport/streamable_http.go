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
	getStreamOnce sync.Once

	// SSE event ID tracking for reconnection.
	lastEventIDs map[string]string
	lastEventMu  sync.Mutex

	// Unified channel for all incoming messages (from POST response streams
	// and GET streams).
	incoming chan json.RawMessage

	closeMu sync.Mutex
	closed  bool
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
		return err
	}

	// Content-Type dispatch: handle both response formats.
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "text/event-stream"):
		go t.parseSSEStream(resp.Body, t.incoming, "post")
	case strings.HasPrefix(ct, "application/json"):
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return fmt.Errorf("reading JSON response: %w", readErr)
		}
		t.incoming <- body
	default:
		// Robustness: attempt JSON parse as fallback for unknown Content-Type.
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if len(body) > 0 {
			log.Printf("mcp http: unexpected Content-Type %q from MCP server, attempting JSON parse", ct)
			t.incoming <- body
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
// Call once after initialization; the stream feeds into the same incoming
// channel as POST responses. Safe to call multiple times (only the first
// call establishes the stream).
func (t *StreamableHTTP) OpenServerStream(ctx context.Context) error {
	var streamErr error
	t.getStreamOnce.Do(func() {
		req, err := http.NewRequestWithContext(ctx, "GET", t.endpoint, nil)
		if err != nil {
			streamErr = fmt.Errorf("creating GET request: %w", err)
			return
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
			streamErr = err
			return
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			streamErr = fmt.Errorf("GET stream returned HTTP %d", resp.StatusCode)
			return
		}
		go t.parseSSEStream(resp.Body, t.incoming, "get")
	})
	return streamErr
}

// Receive returns the unified channel for all incoming messages.
func (t *StreamableHTTP) Receive() <-chan json.RawMessage {
	return t.incoming
}

// Close terminates the HTTP session via DELETE and releases resources.
func (t *StreamableHTTP) Close() error {
	t.closeMu.Lock()
	defer t.closeMu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true

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
		return err
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
	resp.Body.Close()

	// 405 Method Not Allowed is valid (server disallows client-initiated termination).
	if resp.StatusCode == http.StatusMethodNotAllowed {
		return nil
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("session termination returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// parseSSEStream reads SSE events from body and sends parsed JSON messages to ch.
// streamName identifies the stream for event ID tracking ("get" or "post").
func (t *StreamableHTTP) parseSSEStream(body io.ReadCloser, ch chan<- json.RawMessage, streamName string) {
	defer body.Close()

	scanner := bufio.NewScanner(body)
	var data strings.Builder
	var eventID string

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data: "):
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
							ch <- m
						}
					} else {
						ch <- msg
					}
				} else {
					ch <- msg
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
