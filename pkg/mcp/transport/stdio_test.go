package transport

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"
)

func TestStdioRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows (no cat)")
	}

	// Use cat as a simple echo server: it reads from stdin and writes to stdout.
	s := NewStdio("cat", nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer s.Close()

	// Send a JSON message.
	msg := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	if err := s.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Receive the echoed message.
	select {
	case got := <-s.Receive():
		if string(got) != string(msg) {
			t.Errorf("got %s, want %s", got, msg)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for message")
	}
}

func TestStdioClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows (no cat)")
	}

	s := NewStdio("cat", nil, nil)

	ctx := context.Background()
	if err := s.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Channel should be closed eventually.
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-s.Receive():
		// May receive buffered messages before close; drain.
	case <-timer.C:
		t.Fatal("channel not closed after Close")
	}

	// Second close is a no-op.
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestStdioSendBeforeConnect(t *testing.T) {
	s := NewStdio("cat", nil, nil)
	err := s.Send(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error sending before Connect")
	}
}

func TestStdioMalformedJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows (no echo)")
	}

	// Use echo to send a non-JSON line followed by EOF.
	s := NewStdio("echo", []string{"not json"}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := s.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer s.Close()

	// The malformed line should be skipped; channel should close on EOF.
	select {
	case msg, ok := <-s.Receive():
		if ok {
			t.Errorf("expected channel close, got message: %s", msg)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for channel close")
	}
}

func TestStdioCloseUnblocksFullChannel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows (no cat)")
	}

	s := NewStdio("cat", nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Fill the incoming channel buffer so readLoop blocks on the next send.
	for i := 0; i < incomingBufferSize; i++ {
		msg := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"fill"}`)
		if err := s.Send(ctx, msg); err != nil {
			t.Fatalf("Send fill %d: %v", i, err)
		}
	}

	// Send one more message — readLoop will read it from the scanner but
	// block trying to send it to the full channel.
	extra := json.RawMessage(`{"jsonrpc":"2.0","id":99,"method":"blocked"}`)
	if err := s.Send(ctx, extra); err != nil {
		t.Fatalf("Send extra: %v", err)
	}

	// Give readLoop time to read the extra message and block on channel send.
	time.Sleep(100 * time.Millisecond)

	// Close must unblock readLoop via the done channel. Without the fix,
	// this would hang because readLoop is stuck on channel send and
	// closing stdout has no effect on it.
	done := make(chan struct{})
	go func() {
		s.Close()
		// Drain the channel to confirm it gets closed.
		for range s.Receive() {
		}
		close(done)
	}()

	select {
	case <-done:
		// Success: Close unblocked readLoop.
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not unblock readLoop stuck on full channel send")
	}
}

func TestStdioMultipleMessages(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on windows (no cat)")
	}

	s := NewStdio("cat", nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer s.Close()

	messages := []string{
		`{"jsonrpc":"2.0","id":1,"method":"a"}`,
		`{"jsonrpc":"2.0","id":2,"method":"b"}`,
		`{"jsonrpc":"2.0","id":3,"method":"c"}`,
	}

	for _, m := range messages {
		if err := s.Send(ctx, json.RawMessage(m)); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	for i, want := range messages {
		select {
		case got := <-s.Receive():
			if string(got) != want {
				t.Errorf("message %d: got %s, want %s", i, got, want)
			}
		case <-ctx.Done():
			t.Fatalf("timeout waiting for message %d", i)
		}
	}
}
