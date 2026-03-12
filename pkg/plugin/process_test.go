package plugin

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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
		name:       "test-plugin",
		path:       os.Args[0],
		cmd:        cmd,
		stdin:      stdinPipe,
		scanner:    scanner,
		injectCh:   make(chan PluginMessage, 64),
		responseCh: make(chan PluginMessage, 16),
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
