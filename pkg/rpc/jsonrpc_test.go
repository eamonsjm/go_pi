package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func newTestServer() (*rpcServer, *bytes.Buffer) {
	var out bytes.Buffer
	return &rpcServer{
		writer: &out,
	}, &out
}

func TestRPCParseError(t *testing.T) {
	s, out := newTestServer()

	s.serve(context.Background(), strings.NewReader("not json\n"))

	var resp Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != CodeParseError {
		t.Errorf("error code = %d, want %d", resp.Error.Code, CodeParseError)
	}
}

func TestRPCMethodNotFound(t *testing.T) {
	s, out := newTestServer()

	s.serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"nonexistent"}` + "\n"))

	var resp Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != CodeMethodNotFound {
		t.Errorf("code = %d, want %d", resp.Error.Code, CodeMethodNotFound)
	}
}

func TestRPCInvalidVersion(t *testing.T) {
	s, out := newTestServer()

	s.serve(context.Background(), strings.NewReader(`{"jsonrpc":"1.0","id":1,"method":"cancel"}` + "\n"))

	var resp Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error")
	}
	if resp.Error.Code != CodeInvalidRequest {
		t.Errorf("code = %d, want %d", resp.Error.Code, CodeInvalidRequest)
	}
}

func TestRPCSteerInvalidParams(t *testing.T) {
	s, out := newTestServer()

	s.serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"steer","params":{}}` + "\n"))

	var resp Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing text param")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestRPCPromptInvalidParams(t *testing.T) {
	s, out := newTestServer()

	s.serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"prompt","params":{}}` + "\n"))

	var resp Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for missing text param")
	}
	if resp.Error.Code != CodeInvalidParams {
		t.Errorf("code = %d, want %d", resp.Error.Code, CodeInvalidParams)
	}
}

func TestRPCNotification(t *testing.T) {
	s, out := newTestServer()

	s.sendNotification(Notification{
		JSONRPC: "2.0",
		Method:  "agent/event",
		Params:  Event{Type: "agent_start"},
	})

	var n Notification
	if err := json.Unmarshal(out.Bytes(), &n); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if n.Method != "agent/event" {
		t.Errorf("method = %q, want agent/event", n.Method)
	}
}

func TestRPCShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var out bytes.Buffer
	s := &rpcServer{
		cancel: cancel,
		writer: &out,
	}

	s.serve(ctx, strings.NewReader(`{"jsonrpc":"2.0","id":99,"method":"shutdown"}` + "\n"))

	var resp Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
}
