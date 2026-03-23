// Package mcp implements the MCP (Model Context Protocol) client.
//
// This file provides the JSON-RPC 2.0 client layer that sits on top of a
// transport.Transport. It handles request/response correlation, notification
// dispatch, error codes, and the initialize handshake with version negotiation.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/ejm/go_pi/pkg/mcp/transport"
)

// Supported MCP protocol versions, newest first.
var supportedVersions = []string{
	"2025-11-25",
	"2025-03-26",
	"2024-11-05",
}

// Standard JSON-RPC 2.0 error codes.
const (
	ErrCodeParseError     = -32700 // Invalid JSON
	ErrCodeInvalidRequest = -32600 // Not a valid JSON-RPC request
	ErrCodeMethodNotFound = -32601 // Method does not exist
	ErrCodeInvalidParams  = -32602 // Invalid method parameters
	ErrCodeInternalError  = -32603 // Internal JSON-RPC error
)

// MCP / implementation-defined error codes.
// The JSON-RPC spec reserves -32000 to -32099 for implementation-defined server errors.
const (
	ErrCodeServerMin = -32099
	ErrCodeServerMax = -32000
)

// JSONRPCError represents a JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// IsServerError returns true if the error code is in the implementation-defined
// range (-32099 to -32000).
func (e *JSONRPCError) IsServerError() bool {
	return e.Code >= ErrCodeServerMin && e.Code <= ErrCodeServerMax
}

// JSONRPCRequest is a JSON-RPC 2.0 request or notification.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`  // nil for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse is a JSON-RPC 2.0 response.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// pendingRequest tracks an in-flight request waiting for its response.
type pendingRequest struct {
	ch chan JSONRPCResponse
}

// NotificationHandler is called when the server sends a notification (a message
// with no id). The handler receives the method name and raw params.
type NotificationHandler func(method string, params json.RawMessage)

// RequestHandler is called when the server sends a request (a message with both
// id and method). The handler receives the method name, request id, and raw params.
// Used for server-initiated requests like sampling/createMessage and roots/list.
type RequestHandler func(method string, id json.RawMessage, params json.RawMessage)

// InitializeResult holds the parsed fields from the initialize response.
type InitializeResult struct {
	ProtocolVersion string                `json:"protocolVersion"`
	Capabilities    ServerCapabilities    `json:"capabilities"`
	ServerInfo      ImplementationInfo    `json:"serverInfo"`
	Instructions    string                `json:"instructions,omitempty"`
}

// ServerCapabilities describes what the server supports.
type ServerCapabilities struct {
	Tools     *ToolCapability     `json:"tools,omitempty"`
	Resources *ResourceCapability `json:"resources,omitempty"`
	Prompts   *PromptCapability   `json:"prompts,omitempty"`
	Logging   *LoggingCapability  `json:"logging,omitempty"`
}

// ToolCapability describes the server's tool support.
type ToolCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourceCapability describes the server's resource support.
type ResourceCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

// PromptCapability describes the server's prompt support.
type PromptCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// LoggingCapability describes the server's logging support.
type LoggingCapability struct{}

// ImplementationInfo identifies a client or server implementation.
type ImplementationInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ClientCapabilities describes what the client supports.
type ClientCapabilities struct {
	Roots    *RootsCapability    `json:"roots,omitempty"`
	Sampling *SamplingCapability `json:"sampling,omitempty"`
}

// RootsCapability describes the client's roots support.
type RootsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// SamplingCapability describes the client's sampling support.
type SamplingCapability struct{}

// MCPClient is a JSON-RPC 2.0 client for the MCP protocol. It correlates
// requests with responses using a pending map and a demux goroutine.
type MCPClient struct {
	transport transport.Transport
	nextID    atomic.Int64

	pendingMu sync.Mutex
	pending   map[string]*pendingRequest

	onNotification NotificationHandler
	onRequest      RequestHandler

	// Set after successful initialize handshake.
	negotiatedVersion string
	serverCaps        ServerCapabilities
	serverInfo        ImplementationInfo
	instructions      string

	done chan struct{} // closed when demux goroutine exits
}

// NewMCPClient creates a new MCP client using the given transport.
// The transport must already be connected (Connect called).
// onNotification is called for server-sent notifications; may be nil.
func NewMCPClient(t transport.Transport, onNotification NotificationHandler) *MCPClient {
	c := &MCPClient{
		transport:      t,
		pending:        make(map[string]*pendingRequest),
		onNotification: onNotification,
		done:           make(chan struct{}),
	}
	go c.demux()
	return c
}

// demux reads messages from the transport and dispatches them: responses go
// to their pending request channel, notifications go to the handler.
func (c *MCPClient) demux() {
	defer close(c.done)

	for msg := range c.transport.Receive() {
		// Peek at the message to determine if it's a response or notification.
		var peek struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Error  *JSONRPCError   `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(msg, &peek); err != nil {
			log.Printf("mcp: skipping malformed JSON-RPC message: %v", err)
			continue
		}

		if peek.Method != "" && peek.ID == nil {
			// Notification (has method, no id).
			if c.onNotification != nil {
				var req JSONRPCRequest
				if err := json.Unmarshal(msg, &req); err != nil {
					log.Printf("mcp: failed to parse notification: %v", err)
					continue
				}
				c.onNotification(req.Method, req.Params)
			}
			continue
		}

		if peek.Method != "" && peek.ID != nil {
			// Server-initiated request (has both method and id).
			// Examples: sampling/createMessage, roots/list
			if c.onRequest != nil {
				var req JSONRPCRequest
				if err := json.Unmarshal(msg, &req); err != nil {
					log.Printf("mcp: failed to parse server request: %v", err)
					continue
				}
				c.onRequest(req.Method, req.ID, req.Params)
			} else {
				log.Printf("mcp: no handler for server request %q (id=%s)", peek.Method, string(peek.ID))
			}
			continue
		}

		if peek.ID != nil {
			// Response (has id, no method).
			idStr := string(peek.ID)
			c.pendingMu.Lock()
			p, ok := c.pending[idStr]
			if ok {
				delete(c.pending, idStr)
			}
			c.pendingMu.Unlock()

			if ok {
				var resp JSONRPCResponse
				if err := json.Unmarshal(msg, &resp); err != nil {
					log.Printf("mcp: failed to parse response for id %s: %v", idStr, err)
					continue
				}
				p.ch <- resp
			} else {
				log.Printf("mcp: received response for unknown request id %s", idStr)
			}
			continue
		}

		log.Printf("mcp: dropping unrecognized message (no id, no method)")
	}
}

// newRequestID returns a unique monotonically increasing request ID.
func (c *MCPClient) newRequestID() int64 {
	return c.nextID.Add(1)
}

// Request sends a JSON-RPC request and waits for the response. It returns the
// raw result on success or a *JSONRPCError if the server returned an error.
func (c *MCPClient) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.newRequestID()
	idJSON, _ := json.Marshal(id)

	var paramsJSON json.RawMessage
	if params != nil {
		var err error
		paramsJSON, err = json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshaling params: %w", err)
		}
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      idJSON,
		Method:  method,
		Params:  paramsJSON,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	// Register pending request before sending to avoid race.
	idStr := strconv.FormatInt(id, 10)
	p := &pendingRequest{ch: make(chan JSONRPCResponse, 1)}
	c.pendingMu.Lock()
	c.pending[idStr] = p
	c.pendingMu.Unlock()

	if err := c.transport.Send(ctx, reqBytes); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, idStr)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("sending request: %w", err)
	}

	select {
	case resp := <-p.ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, idStr)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("transport closed while waiting for response")
	}
}

// Notify sends a JSON-RPC notification (no id, no response expected).
func (c *MCPClient) Notify(ctx context.Context, method string, params any) error {
	var paramsJSON json.RawMessage
	if params != nil {
		var err error
		paramsJSON, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshaling params: %w", err)
		}
	}

	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling notification: %w", err)
	}
	return c.transport.Send(ctx, reqBytes)
}

// initializeParams is the params object for the initialize request.
type initializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      ImplementationInfo `json:"clientInfo"`
}

// Initialize performs the MCP initialize handshake with version negotiation.
// It sends initialize, validates the server's protocol version, sends
// notifications/initialized, and stores the negotiated state.
//
// clientName and clientVersion identify this client to the server.
// caps describes the client's capabilities (roots, sampling).
func (c *MCPClient) Initialize(ctx context.Context, clientName, clientVersion string, caps ClientCapabilities) (*InitializeResult, error) {
	params := initializeParams{
		ProtocolVersion: supportedVersions[0], // offer latest
		Capabilities:    caps,
		ClientInfo: ImplementationInfo{
			Name:    clientName,
			Version: clientVersion,
		},
	}

	result, err := c.Request(ctx, "initialize", params)
	if err != nil {
		return nil, fmt.Errorf("initialize request: %w", err)
	}

	var initResult InitializeResult
	if err := json.Unmarshal(result, &initResult); err != nil {
		return nil, fmt.Errorf("parsing initialize response: %w", err)
	}

	// Version negotiation: check if the server's version is in our supported set.
	if !isVersionSupported(initResult.ProtocolVersion) {
		return nil, fmt.Errorf("unsupported server protocol version: %q (supported: %v)",
			initResult.ProtocolVersion, supportedVersions)
	}

	// Send notifications/initialized as required by the spec.
	if err := c.Notify(ctx, "notifications/initialized", nil); err != nil {
		return nil, fmt.Errorf("sending initialized notification: %w", err)
	}

	// Store negotiated state.
	c.negotiatedVersion = initResult.ProtocolVersion
	c.serverCaps = initResult.Capabilities
	c.serverInfo = initResult.ServerInfo
	c.instructions = initResult.Instructions

	return &initResult, nil
}

// NegotiatedVersion returns the protocol version agreed upon during initialization.
func (c *MCPClient) NegotiatedVersion() string {
	return c.negotiatedVersion
}

// ServerCapabilities returns the server's capabilities from the initialize response.
func (c *MCPClient) ServerCapabilities() ServerCapabilities {
	return c.serverCaps
}

// ServerInfo returns the server's implementation info from the initialize response.
func (c *MCPClient) ServerInfo() ImplementationInfo {
	return c.serverInfo
}

// Instructions returns the server's instructions string from the initialize response.
func (c *MCPClient) Instructions() string {
	return c.instructions
}

// Close shuts down the demux goroutine and the underlying transport.
func (c *MCPClient) Close() error {
	err := c.transport.Close()
	// Wait for demux to finish (it exits when the transport's Receive channel closes).
	<-c.done
	return err
}

// isVersionSupported checks if a version string is in the supported set.
func isVersionSupported(version string) bool {
	for _, v := range supportedVersions {
		if v == version {
			return true
		}
	}
	return false
}
