package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"

	"github.com/ejm/go_pi/pkg/agent"
)

// stdinProxy copies os.Stdin into an io.Pipe and cancels the context when
// stdin reaches EOF (connection drop). This allows the RPC server to detect
// disconnection even while a prompt is executing.
type stdinProxy struct {
	pr     *io.PipeReader
	pw     *io.PipeWriter
	cancel context.CancelFunc
	done   chan struct{}
}

func newStdinProxy(cancel context.CancelFunc) *stdinProxy {
	pr, pw := io.Pipe()
	sp := &stdinProxy{pr: pr, pw: pw, cancel: cancel, done: make(chan struct{})}
	go sp.copy()
	return sp
}

func (sp *stdinProxy) copy() {
	defer close(sp.done)
	_, _ = io.Copy(sp.pw, os.Stdin)
	sp.pw.Close()
	sp.cancel() // stdin closed — cancel running prompts
}

// close shuts down the proxy, unblocking any pending scanner reads.
func (sp *stdinProxy) close() {
	sp.pr.Close()
	<-sp.done
}

// JSON-RPC 2.0 types.

// Request is a JSON-RPC 2.0 request.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // string or number; nil for notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Notification is a JSON-RPC 2.0 notification (no id field).
type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// PromptParams are the parameters for the "prompt" method.
type PromptParams struct {
	Text string `json:"text"`
}

// PromptResult is the result of a completed "prompt" call.
type PromptResult struct {
	Text string `json:"text"`
}

// RunRPC runs the agent in JSON-RPC 2.0 mode. It reads requests from stdin
// and writes responses/notifications to stdout. Supported methods:
//
//   - "prompt" — send a prompt and receive events as notifications, then a response
//   - "cancel" — cancel the current prompt execution
//   - "steer" — inject a steering message
//   - "shutdown" — gracefully exit
func RunRPC(agentLoop *agent.AgentLoop) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle OS interrupt. The goroutine exits via ctx.Done when the
	// server shuts down, and signal.Stop unregisters the channel so the
	// runtime can reclaim it.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(sigCh)
	}()

	// Proxy stdin so we detect connection drops (EOF) even while a
	// prompt is executing. Without this, a disconnected client leaves
	// the Prompt goroutine running indefinitely.
	proxy := newStdinProxy(cancel)
	defer proxy.close()

	s := &rpcServer{
		agentLoop: agentLoop,
		ctx:       ctx,
		cancel:    cancel,
		writer:    os.Stdout,
	}
	s.serve(proxy.pr)
}

type rpcServer struct {
	agentLoop *agent.AgentLoop
	ctx       context.Context
	cancel    context.CancelFunc
	writer    io.Writer

	mu      sync.Mutex // protects writes to writer
	running bool       // true while a prompt is executing
}

func (s *rpcServer) serve(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		if s.ctx.Err() != nil {
			return
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			s.sendResponse(Response{
				JSONRPC: "2.0",
				Error: &RPCError{
					Code:    CodeParseError,
					Message: "Parse error",
					Data:    err.Error(),
				},
			})
			continue
		}

		if req.JSONRPC != "2.0" {
			s.sendResponse(Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &RPCError{
					Code:    CodeInvalidRequest,
					Message: "Invalid Request: jsonrpc must be \"2.0\"",
				},
			})
			continue
		}

		s.handleRequest(req)
	}

	if err := scanner.Err(); err != nil {
		log.Printf("rpc: scanner error: %v", err)
	}
}

func (s *rpcServer) handleRequest(req Request) {
	switch req.Method {
	case "prompt":
		s.handlePrompt(req)
	case "cancel":
		s.agentLoop.Cancel()
		s.sendResponse(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]string{"status": "cancelled"},
		})
	case "steer":
		var params PromptParams
		if err := json.Unmarshal(req.Params, &params); err != nil || params.Text == "" {
			s.sendResponse(Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &RPCError{
					Code:    CodeInvalidParams,
					Message: "Invalid params: expected {\"text\": \"...\"}",
				},
			})
			return
		}
		s.agentLoop.Steer(params.Text)
		s.sendResponse(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]string{"status": "steered"},
		})
	case "shutdown":
		s.sendResponse(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]string{"status": "shutting_down"},
		})
		s.cancel()
	default:
		s.sendResponse(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    CodeMethodNotFound,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			},
		})
	}
}

func (s *rpcServer) handlePrompt(req Request) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		s.sendResponse(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    CodeInternalError,
				Message: "A prompt is already running. Use \"cancel\" first.",
			},
		})
		return
	}
	s.running = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	var params PromptParams
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Text == "" {
		s.sendResponse(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    CodeInvalidParams,
				Message: "Invalid params: expected {\"text\": \"...\"}",
			},
		})
		return
	}

	events := s.agentLoop.Events()

	var promptErr error
	done := make(chan struct{})
	go func() {
		promptErr = s.agentLoop.Prompt(s.ctx, params.Text)
		close(done)
	}()

	var resultText string
	for event := range events {
		ev := EventFromAgent(event)
		s.sendNotification(Notification{
			JSONRPC: "2.0",
			Method:  "agent/event",
			Params:  ev,
		})

		// Accumulate assistant text for the final response.
		if event.Type == agent.EventAssistantText {
			resultText += event.Delta
		}
	}

	<-done

	if promptErr != nil {
		s.sendResponse(Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    CodeInternalError,
				Message: promptErr.Error(),
			},
		})
		return
	}

	s.sendResponse(Response{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  PromptResult{Text: resultText},
	})
}

func (s *rpcServer) sendResponse(resp Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("rpc: failed to marshal response: %v", err)
		return
	}
	if _, err := fmt.Fprintf(s.writer, "%s\n", data); err != nil {
		log.Printf("rpc: failed to write response: %v", err)
		s.cancel()
	}
}

func (s *rpcServer) sendNotification(n Notification) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(n)
	if err != nil {
		log.Printf("rpc: failed to marshal notification: %v", err)
		return
	}
	if _, err := fmt.Fprintf(s.writer, "%s\n", data); err != nil {
		log.Printf("rpc: failed to write notification: %v", err)
		s.cancel()
	}
}
