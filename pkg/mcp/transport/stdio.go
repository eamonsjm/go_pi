package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
)

const (
	// maxScannerBuffer is the maximum size of a single JSON-RPC message (1 MB),
	// matching the limit in pkg/plugin/process.go.
	maxScannerBuffer = 1024 * 1024

	// incomingBufferSize is the capacity of the incoming message channel.
	incomingBufferSize = 64
)

// Compile-time interface check.
var _ Transport = (*Stdio)(nil)

// Stdio implements Transport for local MCP servers via subprocess stdin/stdout.
// JSON-RPC 2.0 messages are newline-delimited per the MCP spec.
type Stdio struct {
	command string
	args    []string
	env     []string // additional environment variables ("KEY=VALUE")

	mu        sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	incoming  chan json.RawMessage
	connected bool
	done      chan struct{} // closed on Close() to unblock readLoop sends
	closed    bool
}

// NewStdio creates a new Stdio transport. The subprocess is not started until
// Connect is called. env is a list of additional environment variables in
// "KEY=VALUE" format appended to the parent process environment.
func NewStdio(command string, args []string, env []string) *Stdio {
	return &Stdio{
		command:  command,
		args:     args,
		env:      env,
		incoming: make(chan json.RawMessage, incomingBufferSize),
		done:     make(chan struct{}),
	}
}

// Connect spawns the subprocess and starts the read loop.
func (t *Stdio) Connect(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return fmt.Errorf("transport is closed")
	}
	if t.connected {
		return fmt.Errorf("transport already connected")
	}

	// Use exec.Command (not exec.CommandContext) so the subprocess lifetime
	// is not tied to the Connect context. The process lives until Close()
	// is called. CommandContext would kill the subprocess if ctx expires.
	cmd := exec.Command(t.command, t.args...)
	if len(t.env) > 0 {
		cmd.Env = append(cmd.Environ(), t.env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("creating stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("creating stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("starting MCP server %q: %w", t.command, err)
	}

	t.cmd = cmd
	t.stdin = stdin
	t.stdout = stdout
	t.connected = true

	go t.readLoop()
	return nil
}

// Send writes a JSON-RPC message to the server's stdin, followed by a newline.
func (t *Stdio) Send(_ context.Context, msg json.RawMessage) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return fmt.Errorf("transport is closed")
	}
	if t.stdin == nil {
		return fmt.Errorf("transport not connected")
	}

	// Write message + newline delimiter atomically under the lock.
	if _, err := t.stdin.Write(msg); err != nil {
		return fmt.Errorf("writing to MCP server stdin: %w", err)
	}
	if _, err := t.stdin.Write([]byte("\n")); err != nil {
		return fmt.Errorf("writing newline to MCP server stdin: %w", err)
	}
	return nil
}

// Receive returns the channel of incoming messages from the server.
func (t *Stdio) Receive() <-chan json.RawMessage {
	return t.incoming
}

// Close shuts down the subprocess and closes the transport.
func (t *Stdio) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true
	close(t.done)

	if t.stdin != nil {
		if err := t.stdin.Close(); err != nil {
			log.Printf("mcp stdio: cleanup: failed to close stdin: %v", err)
		}
	}
	if t.stdout != nil {
		if err := t.stdout.Close(); err != nil {
			log.Printf("mcp stdio: cleanup: failed to close stdout: %v", err)
		}
	}
	if t.cmd != nil && t.cmd.Process != nil {
		// Best-effort kill; the process may have already exited.
		_ = t.cmd.Process.Kill()
		_ = t.cmd.Wait()
	}

	return nil
}

// readLoop reads newline-delimited JSON messages from stdout and sends them
// to the incoming channel. It closes the channel when the reader returns.
func (t *Stdio) readLoop() {
	defer close(t.incoming)

	scanner := bufio.NewScanner(t.stdout)
	buf := make([]byte, 0, maxScannerBuffer)
	scanner.Buffer(buf, maxScannerBuffer)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Validate that it's valid JSON before forwarding.
		if !json.Valid(line) {
			log.Printf("mcp stdio: skipping malformed JSON: %s", line)
			continue
		}
		// Copy the bytes since scanner reuses its buffer.
		msg := make(json.RawMessage, len(line))
		copy(msg, line)
		select {
		case t.incoming <- msg:
		case <-t.done:
			return
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("mcp stdio: read error: %v", err)
	}
}
