package plugin

import (
	"bufio"
	"strings"
	"testing"
	"time"
)

// newTestProcess creates a PluginProcess with a scanner reading from the given
// JSONL input. The process has no real subprocess — only the readLoop path is
// exercised.
func newTestProcess(name, jsonl string) *PluginProcess {
	scanner := bufio.NewScanner(strings.NewReader(jsonl))
	scanner.Buffer(make([]byte, maxScannerBuffer), maxScannerBuffer)
	return &PluginProcess{
		name:       name,
		scanner:    scanner,
		injectCh:   make(chan PluginMessage, 64),
		responseCh: make(chan PluginMessage, 16),
	}
}

func TestReadLoop_CriticalMessagesDelivered(t *testing.T) {
	input := `{"type":"capabilities","tools":[]}` + "\n" +
		`{"type":"tool_result","content":"ok"}` + "\n" +
		`{"type":"command_result","text":"done"}` + "\n"

	p := newTestProcess("test-critical", input)
	p.readLoop() // blocks until scanner exhausted, then closes channels

	var got []string
	for msg := range p.responseCh {
		got = append(got, msg.Type)
	}

	want := []string{"capabilities", "tool_result", "command_result"}
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("message %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReadLoop_InjectMessagesDelivered(t *testing.T) {
	input := `{"type":"inject_message","content":"hello"}` + "\n" +
		`{"type":"log","level":"info","message":"something"}` + "\n"

	p := newTestProcess("test-inject", input)
	p.readLoop()

	var got []string
	for msg := range p.injectCh {
		got = append(got, msg.Type)
	}

	want := []string{"inject_message", "log"}
	if len(got) != len(want) {
		t.Fatalf("got %d messages, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("message %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestReadLoop_CriticalMessageBlocksUntilConsumed(t *testing.T) {
	// Use a responseCh with buffer size 1 so second message must block.
	input := `{"type":"tool_result","content":"first"}` + "\n" +
		`{"type":"tool_result","content":"second"}` + "\n"

	scanner := bufio.NewScanner(strings.NewReader(input))
	scanner.Buffer(make([]byte, maxScannerBuffer), maxScannerBuffer)
	p := &PluginProcess{
		name:       "test-blocking",
		scanner:    scanner,
		injectCh:   make(chan PluginMessage, 64),
		responseCh: make(chan PluginMessage, 1), // only 1 slot
	}

	done := make(chan struct{})
	go func() {
		p.readLoop()
		close(done)
	}()

	// First message should arrive quickly.
	select {
	case msg := <-p.responseCh:
		if msg.Content != "first" {
			t.Fatalf("expected first, got %q", msg.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first message")
	}

	// Second message should also arrive (readLoop blocks until channel has room).
	select {
	case msg := <-p.responseCh:
		if msg.Content != "second" {
			t.Fatalf("expected second, got %q", msg.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second message — readLoop may have dropped it")
	}

	// readLoop should finish after scanner exhausted.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop did not exit")
	}
}

func TestReadLoop_InjectDroppedWhenFull(t *testing.T) {
	// Build enough messages to overflow a size-2 injectCh.
	var lines string
	for i := 0; i < 5; i++ {
		lines += `{"type":"inject_message","content":"msg"}` + "\n"
	}

	scanner := bufio.NewScanner(strings.NewReader(lines))
	scanner.Buffer(make([]byte, maxScannerBuffer), maxScannerBuffer)
	p := &PluginProcess{
		name:       "test-drop",
		scanner:    scanner,
		injectCh:   make(chan PluginMessage, 2), // small buffer
		responseCh: make(chan PluginMessage, 16),
	}

	p.readLoop()

	var count int
	for range p.injectCh {
		count++
	}

	if count > 2 {
		t.Fatalf("expected at most 2 inject messages (buffer size), got %d", count)
	}
	if count == 0 {
		t.Fatal("expected at least some inject messages to be delivered")
	}
}
