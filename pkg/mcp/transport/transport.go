// Package transport provides MCP transport implementations.
//
// Two transports are supported:
//   - Stdio: spawns a subprocess and communicates over stdin/stdout with
//     newline-delimited JSON-RPC 2.0 messages.
//   - StreamableHTTP: communicates over HTTP POST/GET with optional SSE
//     streaming, per the MCP 2025-11-25 spec.
package transport

import (
	"context"
	"encoding/json"
)

// Transport abstracts the communication channel between the MCP client and
// an MCP server. Implementations must be safe for concurrent use after
// Connect returns.
type Transport interface {
	// Connect establishes the transport connection. For Stdio this spawns
	// the subprocess; for StreamableHTTP this is a no-op.
	Connect(ctx context.Context) error

	// Send transmits a single JSON-RPC 2.0 message to the server.
	// msg must be a single JSON object, not a batch array.
	Send(ctx context.Context, msg json.RawMessage) error

	// Receive returns a channel that delivers incoming JSON-RPC messages
	// from the server. The channel is closed when the transport is closed
	// or the connection drops.
	Receive() <-chan json.RawMessage

	// Close shuts down the transport, releasing all resources.
	Close() error
}
