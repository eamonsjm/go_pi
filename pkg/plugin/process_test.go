package plugin

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// --- In-process readLoop tests (no subprocess needed) ---

// newTestProcess creates a PluginProcess with a scanner reading from the given
// JSONL input. The process has no real subprocess — only the readLoop path is
// exercised.
func newTestProcess(name, jsonl string) *PluginProcess {
	scanner := bufio.NewScanner(strings.NewReader(jsonl))
	scanner.Buffer(make([]byte, maxScannerBuffer), maxScannerBuffer)
	return &PluginProcess{
		name:        name,
		scanner:     scanner,
		injectCh:    make(chan PluginMessage, 64),
		responseCh:  make(chan PluginMessage, 16),
		uiRequestCh: make(chan PluginMessage, 16),
		heartbeatCh: make(chan PluginMessage, 4),
		healthy:     true,
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

func TestReadLoop_ResponseDroppedWhenFull(t *testing.T) {
	// When responseCh buffer is full, readLoop drops excess messages instead
	// of blocking. This prevents goroutine leaks when callers abandon the
	// channel (e.g. after a tool timeout).
	var lines string
	for i := 0; i < 5; i++ {
		lines += fmt.Sprintf(`{"type":"tool_result","content":"msg%d"}`, i) + "\n"
	}

	scanner := bufio.NewScanner(strings.NewReader(lines))
	scanner.Buffer(make([]byte, maxScannerBuffer), maxScannerBuffer)
	p := &PluginProcess{
		name:        "test-drop-response",
		scanner:     scanner,
		injectCh:    make(chan PluginMessage, 64),
		responseCh:  make(chan PluginMessage, 2), // small buffer
		uiRequestCh: make(chan PluginMessage, 16),
		heartbeatCh: make(chan PluginMessage, 4),
		healthy:     true,
	}

	done := make(chan struct{})
	go func() {
		p.readLoop()
		close(done)
	}()

	// readLoop must complete without blocking, even though nobody is reading.
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readLoop blocked on full responseCh — goroutine would leak")
	}

	// At most 2 messages delivered (buffer size).
	var count int
	for range p.responseCh {
		count++
	}
	if count > 2 {
		t.Fatalf("expected at most 2 response messages (buffer size), got %d", count)
	}
	if count == 0 {
		t.Fatal("expected at least some response messages to be delivered")
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
		name:        "test-drop",
		scanner:     scanner,
		injectCh:    make(chan PluginMessage, 2), // small buffer
		responseCh:  make(chan PluginMessage, 16),
		uiRequestCh: make(chan PluginMessage, 16),
		heartbeatCh: make(chan PluginMessage, 4),
		healthy:     true,
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

// --- Test subprocess helper ---
//
// Tests that need a real subprocess run the test binary itself with
// -test.run=TestHelperPlugin. The helper inspects PLUGIN_MODE to decide
// which fake plugin behaviour to simulate. This is the standard Go pattern
// for testing os/exec interaction (see os/exec_test.go in the stdlib).

func TestHelperPlugin(t *testing.T) {
	if os.Getenv("GO_PLUGIN_TEST_HELPER") != "1" {
		return // Skip unless we are the subprocess.
	}

	mode := os.Getenv("PLUGIN_MODE")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	switch mode {
	case "echo_caps":
		// Wait for initialize, reply with capabilities.
		if !scanner.Scan() {
			os.Exit(1)
		}
		var msg HostMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			os.Exit(1)
		}
		resp := PluginMessage{
			Type: "capabilities",
			Tools: []ToolDef{
				{Name: "echo", Description: "echoes input", InputSchema: map[string]any{"type": "object"}},
			},
			Commands: []CommandDef{
				{Name: "greet", Description: "say hello"},
			},
		}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))

		// Serve tool_call / command / event / shutdown requests.
		for scanner.Scan() {
			var req HostMessage
			if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
				continue
			}
			switch req.Type {
			case "tool_call":
				r := PluginMessage{
					Type:    "tool_result",
					ID:      req.ID,
					Content: "echoed:" + req.Name,
				}
				data, _ := json.Marshal(r)
				fmt.Fprintln(os.Stdout, string(data))
			case "command":
				r := PluginMessage{
					Type: "command_result",
					Text: "hello:" + req.Args,
				}
				data, _ := json.Marshal(r)
				fmt.Fprintln(os.Stdout, string(data))
			case "event":
				// fire-and-forget, no response needed
			case "shutdown":
				os.Exit(0)
			}
		}

	case "inject_messages":
		// Immediately send inject_message and log messages, then wait.
		if !scanner.Scan() {
			os.Exit(1)
		}
		// Reply to initialize with capabilities.
		resp := PluginMessage{Type: "capabilities"}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))

		// Now send some inject messages.
		for _, msg := range []PluginMessage{
			{Type: "inject_message", Role: "assistant", Content: "injected text"},
			{Type: "log", Level: "info", Message: "log line"},
		} {
			data, _ := json.Marshal(msg)
			fmt.Fprintln(os.Stdout, string(data))
		}

		// Keep running until shutdown.
		for scanner.Scan() {
			var req HostMessage
			json.Unmarshal(scanner.Bytes(), &req)
			if req.Type == "shutdown" {
				os.Exit(0)
			}
		}

	case "ui_requests":
		// Send UI request messages, then wait for responses.
		if !scanner.Scan() {
			os.Exit(1)
		}
		// Reply to initialize with capabilities.
		resp := PluginMessage{Type: "capabilities"}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))

		// Send some UI requests.
		for _, msg := range []PluginMessage{
			{Type: "ui_request", ID: "req1", UIType: "input", UITitle: "Enter name:", UIDefault: "John"},
			{Type: "ui_request", ID: "req2", UIType: "confirm", UITitle: "Continue?"},
		} {
			data, _ := json.Marshal(msg)
			fmt.Fprintln(os.Stdout, string(data))
		}

		// Keep running until shutdown, handling ui_response messages.
		for scanner.Scan() {
			var req HostMessage
			json.Unmarshal(scanner.Bytes(), &req)
			if req.Type == "shutdown" {
				os.Exit(0)
			}
		}

	case "tool_error":
		// Responds to initialize, then returns tool_result with is_error.
		if !scanner.Scan() {
			os.Exit(1)
		}
		resp := PluginMessage{Type: "capabilities"}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))

		for scanner.Scan() {
			var req HostMessage
			json.Unmarshal(scanner.Bytes(), &req)
			switch req.Type {
			case "tool_call":
				r := PluginMessage{
					Type:    "tool_result",
					ID:      req.ID,
					Content: "something went wrong",
					IsError: true,
				}
				data, _ := json.Marshal(r)
				fmt.Fprintln(os.Stdout, string(data))
			case "shutdown":
				os.Exit(0)
			}
		}

	case "slow_init":
		// Never responds to initialize — used to test init timeout.
		scanner.Scan() // consume the initialize message
		time.Sleep(30 * time.Second)

	case "bad_json":
		// Sends invalid JSON on stdout, then valid capabilities.
		if !scanner.Scan() {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "NOT VALID JSON {{{")
		resp := PluginMessage{Type: "capabilities"}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))
		for scanner.Scan() {
			var req HostMessage
			json.Unmarshal(scanner.Bytes(), &req)
			if req.Type == "shutdown" {
				os.Exit(0)
			}
		}

	case "crash_on_tool":
		// Initialize normally, then crash when a tool_call arrives.
		if !scanner.Scan() {
			os.Exit(1)
		}
		resp := PluginMessage{Type: "capabilities"}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))

		for scanner.Scan() {
			var req HostMessage
			json.Unmarshal(scanner.Bytes(), &req)
			if req.Type == "tool_call" {
				os.Exit(1) // crash mid-operation
			}
			if req.Type == "shutdown" {
				os.Exit(0)
			}
		}

	case "close_stdout_early":
		// Initialize normally, then close stdout while process stays alive.
		if !scanner.Scan() {
			os.Exit(1)
		}
		resp := PluginMessage{Type: "capabilities"}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))
		os.Stdout.Close()
		// Keep reading stdin so process stays alive (but can't respond).
		for scanner.Scan() {
			var req HostMessage
			json.Unmarshal(scanner.Bytes(), &req)
			if req.Type == "shutdown" {
				os.Exit(0)
			}
		}

	case "hang_on_tool":
		// Initialize normally, then hang forever on tool_call.
		if !scanner.Scan() {
			os.Exit(1)
		}
		resp := PluginMessage{Type: "capabilities"}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))

		for scanner.Scan() {
			var req HostMessage
			json.Unmarshal(scanner.Bytes(), &req)
			if req.Type == "tool_call" {
				select {} // hang forever
			}
			if req.Type == "shutdown" {
				os.Exit(0)
			}
		}

	case "exit_immediately":
		// Exit without reading anything — simulates crash.
		os.Exit(1)

	case "crash_after_delay":
		// Initialize successfully, then crash after a short delay.
		// Used for testing auto-restart.
		if !scanner.Scan() {
			os.Exit(1)
		}
		resp := PluginMessage{
			Type: "capabilities",
			Tools: []ToolDef{
				{Name: "echo", Description: "echoes input", InputSchema: map[string]any{"type": "object"}},
			},
		}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))

		// Serve requests briefly, then crash.
		crashTimer := time.NewTimer(100 * time.Millisecond)
		for {
			select {
			case <-crashTimer.C:
				os.Exit(1)
			default:
			}
			if !scanner.Scan() {
				break
			}
			var req HostMessage
			json.Unmarshal(scanner.Bytes(), &req)
			switch req.Type {
			case "tool_call":
				r := PluginMessage{
					Type:    "tool_result",
					ID:      req.ID,
					Content: "echoed:" + req.Name,
				}
				d, _ := json.Marshal(r)
				fmt.Fprintln(os.Stdout, string(d))
			case "shutdown":
				os.Exit(0)
			}
		}

	case "slow_tool":
		// Initialize normally, then respond to tool_call after a delay.
		// Used to test goroutine leak on tool timeout.
		if !scanner.Scan() {
			os.Exit(1)
		}
		resp := PluginMessage{Type: "capabilities"}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))

		for scanner.Scan() {
			var req HostMessage
			json.Unmarshal(scanner.Bytes(), &req)
			switch req.Type {
			case "tool_call":
				time.Sleep(200 * time.Millisecond)
				r := PluginMessage{
					Type:    "tool_result",
					ID:      req.ID,
					Content: "late:" + req.Name,
				}
				data, _ := json.Marshal(r)
				fmt.Fprintln(os.Stdout, string(data))
			case "shutdown":
				os.Exit(0)
			}
		}

	case "heartbeat_ack":
		// Initialize normally, then respond to heartbeat with heartbeat_ack.
		if !scanner.Scan() {
			os.Exit(1)
		}
		resp := PluginMessage{Type: "capabilities"}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))

		for scanner.Scan() {
			var req HostMessage
			json.Unmarshal(scanner.Bytes(), &req)
			switch req.Type {
			case "heartbeat":
				r := PluginMessage{
					Type: "heartbeat_ack",
					Status: &HeartbeatStatus{
						MemoryBytes: 1024000,
						Goroutines:  5,
						UptimeSecs:  42,
					},
				}
				data, _ := json.Marshal(r)
				fmt.Fprintln(os.Stdout, string(data))
			case "tool_call":
				r := PluginMessage{
					Type:    "tool_result",
					ID:      req.ID,
					Content: "echoed:" + req.Name,
				}
				data, _ := json.Marshal(r)
				fmt.Fprintln(os.Stdout, string(data))
			case "shutdown":
				os.Exit(0)
			}
		}

	case "heartbeat_slow":
		// Initialize normally, then respond to heartbeat after a delay.
		// Used to test heartbeat timeout.
		if !scanner.Scan() {
			os.Exit(1)
		}
		resp := PluginMessage{Type: "capabilities"}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))

		for scanner.Scan() {
			var req HostMessage
			json.Unmarshal(scanner.Bytes(), &req)
			switch req.Type {
			case "heartbeat":
				time.Sleep(500 * time.Millisecond)
				r := PluginMessage{
					Type: "heartbeat_ack",
					Status: &HeartbeatStatus{
						MemoryBytes: 999,
					},
				}
				data, _ := json.Marshal(r)
				fmt.Fprintln(os.Stdout, string(data))
			case "shutdown":
				os.Exit(0)
			}
		}

	case "heartbeat_ignore":
		// Initialize normally, never responds to heartbeat.
		// Simulates old plugin that doesn't understand heartbeat.
		if !scanner.Scan() {
			os.Exit(1)
		}
		resp := PluginMessage{Type: "capabilities"}
		data, _ := json.Marshal(resp)
		fmt.Fprintln(os.Stdout, string(data))

		for scanner.Scan() {
			var req HostMessage
			json.Unmarshal(scanner.Bytes(), &req)
			switch req.Type {
			case "heartbeat":
				// Silently ignore — simulates old plugin.
			case "shutdown":
				os.Exit(0)
			}
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown PLUGIN_MODE: %s\n", mode)
		os.Exit(2)
	}
}

// helperPluginCmd returns an exec.Cmd that runs the test binary as a fake
// plugin subprocess with the given mode.
func helperPluginCmd(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperPlugin")
	cmd.Env = append(os.Environ(),
		"GO_PLUGIN_TEST_HELPER=1",
		"PLUGIN_MODE="+mode,
	)
	return cmd
}

// startTestPlugin starts a PluginProcess backed by the test helper subprocess.
func startTestPlugin(t *testing.T, mode string) *PluginProcess {
	t.Helper()

	cmd := helperPluginCmd(mode)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("creating stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("creating stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting helper: %v", err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, maxScannerBuffer), maxScannerBuffer)

	p := &PluginProcess{
		name:        "test-plugin",
		path:        os.Args[0],
		spawnCmd:    func() *exec.Cmd { return helperPluginCmd(mode) },
		cmd:         cmd,
		stdin:       stdinPipe,
		stdout:      stdoutPipe,
		scanner:     scanner,
		injectCh:    make(chan PluginMessage, 64),
		responseCh:  make(chan PluginMessage, 16),
		uiRequestCh: make(chan PluginMessage, 16),
		heartbeatCh: make(chan PluginMessage, 4),
		healthy:     true,
		timeouts:    DefaultTimeoutConfig(),
	}

	go p.readLoop()

	t.Cleanup(func() {
		p.Stop()
	})

	return p
}

// --- Subprocess-based process tests ---

func TestInitialize_Success(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")

	err := p.Initialize(PluginConfig{
		Cwd:       "/tmp",
		Model:     "test-model",
		Provider:  "test",
		GiVersion: "0.0.1",
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if len(p.tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(p.tools))
	}
	if p.tools[0].Name != "echo" {
		t.Errorf("tool name = %q, want %q", p.tools[0].Name, "echo")
	}

	if len(p.commands) != 1 {
		t.Fatalf("commands count = %d, want 1", len(p.commands))
	}
	if p.commands[0].Name != "greet" {
		t.Errorf("command name = %q, want %q", p.commands[0].Name, "greet")
	}
}

func TestExecuteTool_RoundTrip(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	content, isError, err := p.ExecuteTool("call-1", "echo", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if content != "echoed:echo" {
		t.Errorf("content = %q, want %q", content, "echoed:echo")
	}
}

func TestExecuteTool_ErrorResult(t *testing.T) {
	p := startTestPlugin(t, "tool_error")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	content, isError, err := p.ExecuteTool("call-err", "anything", nil)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true")
	}
	if content != "something went wrong" {
		t.Errorf("content = %q, want %q", content, "something went wrong")
	}
}

func TestExecuteCommand_RoundTrip(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	text, isError, err := p.ExecuteCommand("greet", "world")
	if err != nil {
		t.Fatalf("ExecuteCommand: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if text != "hello:world" {
		t.Errorf("text = %q, want %q", text, "hello:world")
	}
}

func TestSendEvent_FireAndForget(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	err := p.SendEvent(EventPayload{Type: "tool_exec_start", ToolName: "bash"})
	if err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	// No response expected. Just verify no panic and the process is still alive.
	if !p.Alive() {
		t.Error("process not alive after SendEvent")
	}
}

func TestInjectMessages_Routing(t *testing.T) {
	p := startTestPlugin(t, "inject_messages")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Collect inject messages sent by the helper.
	var msgs []PluginMessage
	timeout := time.After(5 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case msg, ok := <-p.InjectMessages():
			if !ok {
				t.Fatal("injectCh closed prematurely")
			}
			msgs = append(msgs, msg)
		case <-timeout:
			t.Fatalf("timed out waiting for inject messages, got %d", len(msgs))
		}
	}

	if msgs[0].Type != "inject_message" {
		t.Errorf("msg[0].Type = %q, want %q", msgs[0].Type, "inject_message")
	}
	if msgs[0].Content != "injected text" {
		t.Errorf("msg[0].Content = %q, want %q", msgs[0].Content, "injected text")
	}
	if msgs[1].Type != "log" {
		t.Errorf("msg[1].Type = %q, want %q", msgs[1].Type, "log")
	}
	if msgs[1].Message != "log line" {
		t.Errorf("msg[1].Message = %q, want %q", msgs[1].Message, "log line")
	}
}

func TestStop_GracefulShutdown(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	err := p.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if p.Alive() {
		t.Error("process still alive after Stop")
	}
}

func TestStop_Idempotent(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if err := p.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestSend_AfterClose(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	p.Stop()

	err := p.Send(HostMessage{Type: "test"})
	if err == nil {
		t.Fatal("expected error sending to closed process")
	}
	if !strings.Contains(err.Error(), "process closed") {
		t.Errorf("error = %q, want containing %q", err.Error(), "process closed")
	}
}

func TestInitialize_ProcessExitedDuringInit(t *testing.T) {
	p := startTestPlugin(t, "exit_immediately")

	err := p.Initialize(PluginConfig{})
	if err == nil {
		t.Fatal("expected error from crashed process")
	}
}

func TestReadLoop_LogsMalformedJSON(t *testing.T) {
	input := "NOT VALID JSON {{{\n" +
		`{"type":"tool_result","content":"ok"}` + "\n"

	p := newTestProcess("test-malformed", input)

	// Capture log output.
	var buf strings.Builder
	log.SetOutput(&buf)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
	})

	p.readLoop()

	logOutput := buf.String()
	if !strings.Contains(logOutput, "test-malformed") {
		t.Errorf("log output missing plugin name, got: %q", logOutput)
	}
	if !strings.Contains(logOutput, "malformed JSON") {
		t.Errorf("log output missing 'malformed JSON', got: %q", logOutput)
	}

	// Valid message after the bad line should still be delivered.
	msg, ok := <-p.responseCh
	if !ok {
		t.Fatal("responseCh closed before delivering valid message")
	}
	if msg.Type != "tool_result" || msg.Content != "ok" {
		t.Errorf("got message %+v, want tool_result with content 'ok'", msg)
	}
}

func TestReadLoop_SkipsBadJSON(t *testing.T) {
	p := startTestPlugin(t, "bad_json")

	// Initialize should succeed because the helper sends bad JSON first
	// (which readLoop skips), then valid capabilities.
	err := p.Initialize(PluginConfig{})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
}

func TestName(t *testing.T) {
	p := &PluginProcess{name: "foo"}
	if p.Name() != "foo" {
		t.Errorf("Name() = %q, want %q", p.Name(), "foo")
	}
}

func TestCommands(t *testing.T) {
	cmds := []CommandDef{{Name: "a"}, {Name: "b"}}
	p := &PluginProcess{commands: cmds}
	got := p.Commands()
	if len(got) != 2 {
		t.Fatalf("Commands() len = %d, want 2", len(got))
	}
	if got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("Commands() = %v, want %v", got, cmds)
	}
}

func TestAlive_DefaultTrue(t *testing.T) {
	p := &PluginProcess{}
	if !p.Alive() {
		t.Error("Alive() = false for new process, want true")
	}
}

func TestAlive_FalseAfterClose(t *testing.T) {
	p := &PluginProcess{closed: true}
	if p.Alive() {
		t.Error("Alive() = true for closed process, want false")
	}
}

func TestWaitResponse_Timeout(t *testing.T) {
	p := &PluginProcess{
		name:       "timeout-test",
		responseCh: make(chan PluginMessage), // unbuffered, nothing will send
	}

	start := time.Now()
	_, err := p.waitResponse(50 * time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want containing %q", err.Error(), "timed out")
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("returned too quickly (%v), expected ~50ms timeout", elapsed)
	}
}

func TestWaitResponse_ChannelClosed(t *testing.T) {
	responseCh := make(chan PluginMessage)
	close(responseCh)

	p := &PluginProcess{
		name:       "closed-test",
		responseCh: responseCh,
	}

	_, err := p.waitResponse(5 * time.Second)
	if err == nil {
		t.Fatal("expected error from closed channel")
	}
	if !strings.Contains(err.Error(), "process exited") {
		t.Errorf("error = %q, want containing %q", err.Error(), "process exited")
	}
}

// --- Plugin process failure scenario tests ---

func TestCrashDuringActiveSend(t *testing.T) {
	p := startTestPlugin(t, "crash_on_tool")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Send a tool call — the plugin crashes upon receiving it.
	_, _, err := p.ExecuteTool("call-crash", "anything", nil)
	if err == nil {
		t.Fatal("expected error from crashed process during tool call")
	}

	// Verify cleanup works on a crashed process.
	p.Stop()
	if p.Alive() {
		t.Error("process still alive after crash and stop")
	}
}

func TestPrematureStdoutClose(t *testing.T) {
	p := startTestPlugin(t, "close_stdout_early")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Wait for readLoop to detect stdout EOF and close channels.
	select {
	case _, ok := <-p.InjectMessages():
		if ok {
			t.Fatal("unexpected message on injectCh")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("readLoop did not finish after stdout close")
	}

	// Plugin closed stdout; responseCh is now closed.
	// ExecuteTool should fail with "process exited".
	_, _, err := p.ExecuteTool("call-closed", "anything", nil)
	if err == nil {
		t.Fatal("expected error when plugin closed stdout")
	}
	if !strings.Contains(err.Error(), "process exited") {
		t.Errorf("error = %q, want containing %q", err.Error(), "process exited")
	}
}

func TestHangingPluginTimeout(t *testing.T) {
	p := startTestPlugin(t, "hang_on_tool")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Send a tool call — the plugin will hang and never respond.
	if err := p.Send(HostMessage{
		Type: "tool_call",
		ID:   "call-hang",
		Name: "anything",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Use a short timeout to verify timeout enforcement without waiting 30s.
	_, err := p.waitResponse(100 * time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error from hanging plugin")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want containing %q", err.Error(), "timed out")
	}
}

func TestExecuteToolOnClosedProcess(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	p.Stop()

	_, _, err := p.ExecuteTool("call-after-close", "echo", nil)
	if err == nil {
		t.Fatal("expected error from ExecuteTool on closed process")
	}
	if !strings.Contains(err.Error(), "process closed") {
		t.Errorf("error = %q, want containing %q", err.Error(), "process closed")
	}
}

func TestExecuteCommandOnClosedProcess(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	p.Stop()

	_, _, err := p.ExecuteCommand("greet", "world")
	if err == nil {
		t.Fatal("expected error from ExecuteCommand on closed process")
	}
	if !strings.Contains(err.Error(), "process closed") {
		t.Errorf("error = %q, want containing %q", err.Error(), "process closed")
	}
}

// --- Auto-restart tests ---

func TestAutoRestart_RecoverFromCrash(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Verify the plugin works before crash.
	content, _, err := p.ExecuteTool("pre-crash", "echo", nil)
	if err != nil {
		t.Fatalf("ExecuteTool before crash: %v", err)
	}
	if content != "echoed:echo" {
		t.Errorf("content = %q, want %q", content, "echoed:echo")
	}

	// Enable auto-restart with fast backoff for testing.
	p.EnableAutoRestart(RestartConfig{
		MaxAttempts:    3,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     200 * time.Millisecond,
	})

	// Kill the process to simulate a crash.
	p.cmd.Process.Kill()

	// Wait for the supervisor to detect the crash and complete a restart.
	// RestartCount >= 1 ensures the supervisor attempted restart.
	// Alive() ensures the restart completed and the process is running.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for plugin to restart (restartCount=%d, alive=%v, restarting=%v)",
				p.RestartCount(), p.Alive(), p.Restarting())
		default:
		}
		if p.RestartCount() >= 1 && p.Alive() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Verify plugin works after restart.
	content, _, err = p.ExecuteTool("post-crash", "echo", nil)
	if err != nil {
		t.Fatalf("ExecuteTool after restart: %v", err)
	}
	if content != "echoed:echo" {
		t.Errorf("content = %q, want %q", content, "echoed:echo")
	}

	if p.RestartCount() < 1 {
		t.Errorf("RestartCount() = %d, want >= 1", p.RestartCount())
	}
}

func TestAutoRestart_NoRestartOnCleanShutdown(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	p.EnableAutoRestart(RestartConfig{
		MaxAttempts:    3,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     200 * time.Millisecond,
	})

	// Clean shutdown should not trigger restart.
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if p.Alive() {
		t.Error("process still alive after clean shutdown")
	}
	if p.RestartCount() != 0 {
		t.Errorf("RestartCount() = %d, want 0 after clean shutdown", p.RestartCount())
	}
}

func TestAutoRestart_MaxAttemptsExhausted(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	p.EnableAutoRestart(RestartConfig{
		MaxAttempts:    2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     20 * time.Millisecond,
	})

	// Switch the spawn command to exit_immediately so restarts fail.
	p.mu.Lock()
	p.spawnCmd = func() *exec.Cmd { return helperPluginCmd("exit_immediately") }
	p.mu.Unlock()

	// Kill current process to trigger restart.
	p.cmd.Process.Kill()

	// Wait for the supervisor to exhaust max attempts.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for supervisor to give up")
		default:
		}

		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if closed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if p.Alive() {
		t.Error("process still alive after max attempts exhausted")
	}
}

func TestAutoRestart_RestartingState(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	p.EnableAutoRestart(RestartConfig{
		MaxAttempts:    3,
		InitialBackoff: 200 * time.Millisecond, // Longer backoff so we can observe restarting state
		MaxBackoff:     1 * time.Second,
	})

	// Kill the process.
	p.cmd.Process.Kill()

	// Should enter restarting state.
	deadline := time.After(2 * time.Second)
	for {
		if p.Restarting() {
			// During restart, Alive should be false.
			if p.Alive() {
				t.Error("Alive() = true while restarting, want false")
			}
			break
		}

		select {
		case <-deadline:
			t.Fatal("never saw restarting state")
		default:
		}

		time.Sleep(10 * time.Millisecond)
	}

	// Wait for restart to complete.
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for restart to complete")
		default:
		}
		if p.Alive() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if p.Restarting() {
		t.Error("still restarting after plugin came back alive")
	}
}

func TestAutoRestart_StopDuringRestart(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	p.EnableAutoRestart(RestartConfig{
		MaxAttempts:    5,
		InitialBackoff: 500 * time.Millisecond, // Long backoff so we can stop during it
		MaxBackoff:     2 * time.Second,
	})

	// Kill the process to trigger restart.
	p.cmd.Process.Kill()

	// Wait until restarting state is entered.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for restarting state")
		default:
		}
		if p.Restarting() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Stop during the backoff wait — should cancel promptly.
	done := make(chan error, 1)
	go func() {
		done <- p.Stop()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop during restart: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not return during restart backoff")
	}

	if p.Alive() {
		t.Error("process still alive after stop during restart")
	}
}

func TestAutoRestart_NoGoroutineLeakOnFailedRespawn(t *testing.T) {
	// Baseline goroutine count before starting the plugin.
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	goroutinesBefore := runtime.NumGoroutine()

	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Short init timeout so respawn's Initialize() fails quickly.
	p.SetTimeouts(TimeoutConfig{InitTimeout: 200 * time.Millisecond})

	p.EnableAutoRestart(RestartConfig{
		MaxAttempts:    2,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
	})

	// Switch spawn to slow_init — the respawned process won't respond to
	// Initialize(), causing it to time out and trigger the cleanup path.
	p.mu.Lock()
	p.spawnCmd = func() *exec.Cmd { return helperPluginCmd("slow_init") }
	p.mu.Unlock()

	// Kill process to trigger restart cycle.
	p.cmd.Process.Kill()

	// Wait for the supervisor to exhaust attempts and set closed.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for supervisor to give up")
		default:
		}
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if closed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Allow goroutines to wind down.
	time.Sleep(500 * time.Millisecond)
	runtime.GC()

	goroutinesAfter := runtime.NumGoroutine()
	// Allow margin of 3 for test infrastructure goroutines.
	if goroutinesAfter > goroutinesBefore+3 {
		t.Errorf("goroutine leak: before=%d, after=%d (diff=%d)",
			goroutinesBefore, goroutinesAfter, goroutinesAfter-goroutinesBefore)
	}
}

func TestDefaultRestartConfig(t *testing.T) {
	cfg := DefaultRestartConfig()
	if cfg.MaxAttempts != 5 {
		t.Errorf("MaxAttempts = %d, want 5", cfg.MaxAttempts)
	}
	if cfg.InitialBackoff != 1*time.Second {
		t.Errorf("InitialBackoff = %v, want 1s", cfg.InitialBackoff)
	}
	if cfg.MaxBackoff != 30*time.Second {
		t.Errorf("MaxBackoff = %v, want 30s", cfg.MaxBackoff)
	}
}

// --- Heartbeat tests ---

func TestHeartbeat_Success(t *testing.T) {
	p := startTestPlugin(t, "heartbeat_ack")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	status, err := p.Heartbeat(5 * time.Second)
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}

	if status == nil {
		t.Fatal("status is nil")
	}
	if status.MemoryBytes != 1024000 {
		t.Errorf("MemoryBytes = %d, want 1024000", status.MemoryBytes)
	}
	if status.Goroutines != 5 {
		t.Errorf("Goroutines = %d, want 5", status.Goroutines)
	}
	if status.UptimeSecs != 42 {
		t.Errorf("UptimeSecs = %d, want 42", status.UptimeSecs)
	}

	if !p.Healthy() {
		t.Error("Healthy() = false after successful heartbeat")
	}

	lastStatus := p.LastHeartbeatStatus()
	if lastStatus == nil {
		t.Fatal("LastHeartbeatStatus() is nil")
	}
	if lastStatus.MemoryBytes != 1024000 {
		t.Errorf("LastHeartbeatStatus().MemoryBytes = %d, want 1024000", lastStatus.MemoryBytes)
	}
}

func TestHeartbeat_Timeout(t *testing.T) {
	p := startTestPlugin(t, "heartbeat_slow")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// The helper takes 500ms to respond; use a 50ms timeout.
	_, err := p.Heartbeat(50 * time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "heartbeat timeout") {
		t.Errorf("error = %q, want containing %q", err.Error(), "heartbeat timeout")
	}

	if p.Healthy() {
		t.Error("Healthy() = true after heartbeat timeout")
	}
}

func TestHeartbeat_OldPluginIgnores(t *testing.T) {
	p := startTestPlugin(t, "heartbeat_ignore")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Old plugin ignores heartbeat — should timeout.
	_, err := p.Heartbeat(100 * time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error from old plugin")
	}

	// Before any heartbeat, plugin was healthy. After missed heartbeat, unhealthy.
	if p.Healthy() {
		t.Error("Healthy() = true after missed heartbeat")
	}
}

func TestHeartbeat_ClosedProcess(t *testing.T) {
	p := startTestPlugin(t, "heartbeat_ack")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	p.Stop()

	_, err := p.Heartbeat(1 * time.Second)
	if err == nil {
		t.Fatal("expected error from heartbeat on closed process")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("error = %q, want containing %q", err.Error(), "not running")
	}
}

func TestHeartbeat_DoesNotInterfereWithTools(t *testing.T) {
	p := startTestPlugin(t, "heartbeat_ack")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Heartbeat first.
	status, err := p.Heartbeat(5 * time.Second)
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if status == nil {
		t.Fatal("status is nil")
	}

	// Tool call should still work.
	content, isError, err := p.ExecuteTool("call-1", "echo", nil)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if isError {
		t.Error("isError = true")
	}
	if content != "echoed:echo" {
		t.Errorf("content = %q, want %q", content, "echoed:echo")
	}
}

func TestHealthy_DefaultTrue(t *testing.T) {
	p := &PluginProcess{healthy: true}
	if !p.Healthy() {
		t.Error("Healthy() = false for new process, want true")
	}
}

func TestDefaultHeartbeatConfig(t *testing.T) {
	cfg := DefaultHeartbeatConfig()
	if cfg.Interval != 10*time.Second {
		t.Errorf("Interval = %v, want 10s", cfg.Interval)
	}
	if cfg.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", cfg.Timeout)
	}
}

func TestReadLoop_HeartbeatAckRouted(t *testing.T) {
	input := `{"type":"heartbeat_ack","status":{"memory_bytes":42}}` + "\n"

	p := newTestProcess("test-hb", input)
	p.heartbeatCh = make(chan PluginMessage, 4)
	p.readLoop()

	msg, ok := <-p.heartbeatCh
	if !ok {
		t.Fatal("heartbeatCh closed before delivering message")
	}
	if msg.Type != "heartbeat_ack" {
		t.Errorf("Type = %q, want %q", msg.Type, "heartbeat_ack")
	}
	if msg.Status == nil || msg.Status.MemoryBytes != 42 {
		t.Errorf("Status = %+v, want MemoryBytes=42", msg.Status)
	}
}

func TestToolTimeout_NoGoroutineLeak(t *testing.T) {
	// Verify that tool timeouts don't leak goroutines. We send tool calls
	// with a short timeout, let late responses accumulate in the buffer,
	// then stop the plugin and verify goroutine count returns to baseline.
	goroutinesBefore := runtime.NumGoroutine()

	p := startTestPlugin(t, "slow_tool")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Send tool calls and use a short timeout. The slow_tool helper takes
	// 200ms to respond, so a 10ms timeout will fire first. Late responses
	// accumulate in the responseCh buffer (nobody reads them).
	timeouts := 0
	for i := 0; i < 3; i++ {
		_ = p.Send(HostMessage{
			Type: "tool_call",
			ID:   fmt.Sprintf("timeout-%d", i),
			Name: "slow",
		})
		_, err := p.waitResponse(10 * time.Millisecond)
		if err != nil {
			timeouts++
		}
		// Wait for the slow response to arrive and fill the buffer.
		time.Sleep(250 * time.Millisecond)
	}

	if timeouts == 0 {
		t.Fatal("expected at least one timeout")
	}

	// Stop the plugin — this kills the process, causing readLoop's scanner
	// to return false. Without the fix, readLoop would be blocked on a full
	// responseCh send and never reach scanner.Scan(), leaking the goroutine.
	p.Stop()

	// Allow goroutines to wind down.
	time.Sleep(200 * time.Millisecond)
	runtime.GC()

	goroutinesAfter := runtime.NumGoroutine()
	// Allow margin of 3 for test infrastructure goroutines.
	if goroutinesAfter > goroutinesBefore+3 {
		t.Errorf("goroutine leak: before=%d, after=%d (diff=%d)",
			goroutinesBefore, goroutinesAfter, goroutinesAfter-goroutinesBefore)
	}
}

// --- Per-plugin timeout and kill tests ---

func TestSetTimeouts_Override(t *testing.T) {
	p := &PluginProcess{timeouts: DefaultTimeoutConfig()}

	// Override only tool timeout.
	p.SetTimeouts(TimeoutConfig{ToolTimeout: 5 * time.Second})
	if p.timeouts.ToolTimeout != 5*time.Second {
		t.Errorf("ToolTimeout = %v, want 5s", p.timeouts.ToolTimeout)
	}
	if p.timeouts.InitTimeout != initTimeout {
		t.Errorf("InitTimeout changed to %v, want default %v", p.timeouts.InitTimeout, initTimeout)
	}
	if p.timeouts.CommandTimeout != commandTimeout {
		t.Errorf("CommandTimeout changed to %v, want default %v", p.timeouts.CommandTimeout, commandTimeout)
	}
}

func TestSetTimeouts_ZeroKeepsCurrent(t *testing.T) {
	p := &PluginProcess{timeouts: DefaultTimeoutConfig()}

	// Zero values should keep existing.
	p.SetTimeouts(TimeoutConfig{})
	if p.timeouts.InitTimeout != initTimeout {
		t.Errorf("InitTimeout = %v, want %v", p.timeouts.InitTimeout, initTimeout)
	}
	if p.timeouts.ToolTimeout != toolTimeout {
		t.Errorf("ToolTimeout = %v, want %v", p.timeouts.ToolTimeout, toolTimeout)
	}
	if p.timeouts.CommandTimeout != commandTimeout {
		t.Errorf("CommandTimeout = %v, want %v", p.timeouts.CommandTimeout, commandTimeout)
	}
}

func TestDefaultTimeoutConfig(t *testing.T) {
	cfg := DefaultTimeoutConfig()
	if cfg.InitTimeout != 10*time.Second {
		t.Errorf("InitTimeout = %v, want 10s", cfg.InitTimeout)
	}
	if cfg.ToolTimeout != 30*time.Second {
		t.Errorf("ToolTimeout = %v, want 30s", cfg.ToolTimeout)
	}
	if cfg.CommandTimeout != 10*time.Second {
		t.Errorf("CommandTimeout = %v, want 10s", cfg.CommandTimeout)
	}
}

func TestTimeoutConfigFromManifest(t *testing.T) {
	m := Manifest{
		InitTimeoutSecs:    5,
		ToolTimeoutSecs:    15,
		CommandTimeoutSecs: 3,
	}
	cfg := TimeoutConfigFromManifest(m)
	if cfg.InitTimeout != 5*time.Second {
		t.Errorf("InitTimeout = %v, want 5s", cfg.InitTimeout)
	}
	if cfg.ToolTimeout != 15*time.Second {
		t.Errorf("ToolTimeout = %v, want 15s", cfg.ToolTimeout)
	}
	if cfg.CommandTimeout != 3*time.Second {
		t.Errorf("CommandTimeout = %v, want 3s", cfg.CommandTimeout)
	}
}

func TestTimeoutConfigFromManifest_ZeroUsesDefaults(t *testing.T) {
	m := Manifest{} // All zeros.
	cfg := TimeoutConfigFromManifest(m)
	if cfg.InitTimeout != initTimeout {
		t.Errorf("InitTimeout = %v, want default %v", cfg.InitTimeout, initTimeout)
	}
	if cfg.ToolTimeout != toolTimeout {
		t.Errorf("ToolTimeout = %v, want default %v", cfg.ToolTimeout, toolTimeout)
	}
	if cfg.CommandTimeout != commandTimeout {
		t.Errorf("CommandTimeout = %v, want default %v", cfg.CommandTimeout, commandTimeout)
	}
}

func TestExecuteTool_CustomTimeout(t *testing.T) {
	p := startTestPlugin(t, "hang_on_tool")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Set a very short tool timeout.
	p.SetTimeouts(TimeoutConfig{ToolTimeout: 50 * time.Millisecond})

	start := time.Now()
	_, _, err := p.ExecuteTool("call-short", "anything", nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want containing 'timed out'", err.Error())
	}
	// Should timeout in ~50ms, not the default 30s.
	if elapsed > 1*time.Second {
		t.Errorf("timeout took %v, expected ~50ms (custom timeout not applied)", elapsed)
	}
}

func TestExecuteCommand_CustomTimeout(t *testing.T) {
	// The echo_caps helper responds to commands, but we'll use hang_on_tool
	// which hangs on tool_call. For commands, it just never responds (no
	// command handler), so it will also timeout.
	p := startTestPlugin(t, "hang_on_tool")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Set a very short command timeout.
	p.SetTimeouts(TimeoutConfig{CommandTimeout: 50 * time.Millisecond})

	start := time.Now()
	_, _, err := p.ExecuteCommand("anything", "args")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want containing 'timed out'", err.Error())
	}
	if elapsed > 1*time.Second {
		t.Errorf("timeout took %v, expected ~50ms (custom timeout not applied)", elapsed)
	}
}

func TestExecuteTool_KillsProcessOnTimeout(t *testing.T) {
	p := startTestPlugin(t, "hang_on_tool")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Set a very short tool timeout.
	p.SetTimeouts(TimeoutConfig{ToolTimeout: 50 * time.Millisecond})

	_, _, err := p.ExecuteTool("call-kill", "anything", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}

	// After timeout, the process should be killed. Wait a moment for the
	// kill to take effect.
	time.Sleep(100 * time.Millisecond)

	// The process was killed, so cmd.Wait should have returned.
	// A subsequent Send should fail.
	sendErr := p.Send(HostMessage{Type: "test"})
	if sendErr == nil {
		// The process might still be in the process of dying. Try once more.
		time.Sleep(100 * time.Millisecond)
		_ = p.Send(HostMessage{Type: "test"})
	}
	// We expect either a write error (broken pipe) or a "process closed" error.
	// Both are acceptable — the point is the process was killed.
}

func TestIsTimeout(t *testing.T) {
	te := &timeoutError{plugin: "test"}
	if !isTimeout(te) {
		t.Error("isTimeout(timeoutError) = false, want true")
	}

	other := fmt.Errorf("some other error")
	if isTimeout(other) {
		t.Error("isTimeout(other) = true, want false")
	}
}

func TestHandleTimeout_NilCmd(t *testing.T) {
	// handleTimeout should not panic when cmd is nil.
	p := &PluginProcess{
		name:     "nil-cmd",
		timeouts: DefaultTimeoutConfig(),
	}
	// Should not panic.
	p.handleTimeout("tool_call", "test", 1*time.Second)
}

func TestUIRequests_Routing(t *testing.T) {
	p := startTestPlugin(t, "ui_requests")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Collect UI requests sent by the helper.
	var msgs []PluginMessage
	timeout := time.After(5 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case msg, ok := <-p.UIRequests():
			if !ok {
				t.Fatal("uiRequestCh closed prematurely")
			}
			msgs = append(msgs, msg)
		case <-timeout:
			t.Fatalf("timed out waiting for UI requests, got %d", len(msgs))
		}
	}

	if msgs[0].Type != "ui_request" {
		t.Errorf("msg[0].Type = %q, want %q", msgs[0].Type, "ui_request")
	}
	if msgs[0].ID != "req1" {
		t.Errorf("msg[0].ID = %q, want %q", msgs[0].ID, "req1")
	}
	if msgs[0].UIType != "input" {
		t.Errorf("msg[0].UIType = %q, want %q", msgs[0].UIType, "input")
	}

	if msgs[1].Type != "ui_request" {
		t.Errorf("msg[1].Type = %q, want %q", msgs[1].Type, "ui_request")
	}
	if msgs[1].ID != "req2" {
		t.Errorf("msg[1].ID = %q, want %q", msgs[1].ID, "req2")
	}
	if msgs[1].UIType != "confirm" {
		t.Errorf("msg[1].UIType = %q, want %q", msgs[1].UIType, "confirm")
	}

	p.Stop()
}

func TestRespondToUIRequest(t *testing.T) {
	p := startTestPlugin(t, "ui_requests")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Consume the UI requests
	timeout := time.After(5 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-p.UIRequests():
		case <-timeout:
			t.Fatalf("timed out waiting for UI request %d", i)
		}
	}

	// Send a response
	err := p.RespondToUIRequest("req1", "Alice", false, "")
	if err != nil {
		t.Fatalf("RespondToUIRequest: %v", err)
	}

	p.Stop()
}
