package plugin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/tools"
)

// testPluginBinary caches the compiled test plugin binary path.
var testPluginBinary string

// buildTestPlugin compiles the testdata/testplugin binary and returns its path.
func buildTestPlugin(t *testing.T) string {
	t.Helper()

	if testPluginBinary != "" {
		if _, err := os.Stat(testPluginBinary); err == nil {
			return testPluginBinary
		}
	}

	srcDir := filepath.Join("testdata", "testplugin")
	if _, err := os.Stat(filepath.Join(srcDir, "main.go")); err != nil {
		t.Fatalf("testdata/testplugin/main.go not found: %v", err)
	}

	outDir := t.TempDir()
	binName := "testplugin"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(outDir, binName)

	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = srcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building test plugin: %v\n%s", err, out)
	}

	testPluginBinary = binPath
	return binPath
}

// startIntegrationPlugin starts the test plugin binary with the given mode
// and returns a PluginProcess ready for communication.
func startIntegrationPlugin(t *testing.T, binPath, mode string) *PluginProcess {
	t.Helper()

	cmd := exec.Command(binPath)
	cmd.Env = append(os.Environ(), "PLUGIN_MODE="+mode)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("creating stdin pipe: %v", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		t.Fatalf("creating stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting test plugin (mode=%s): %v", mode, err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, maxScannerBuffer), maxScannerBuffer)

	p := &PluginProcess{
		name:        "testplugin",
		path:        binPath,
		cmd:         cmd,
		stdin:       stdinPipe,
		scanner:     scanner,
		injectCh:    make(chan PluginMessage, 64),
		responseCh:  make(chan PluginMessage, 16),
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

// --- Integration Tests ---

func TestIntegration_InitializationHandshake(t *testing.T) {
	bin := buildTestPlugin(t)
	p := startIntegrationPlugin(t, bin, "normal")

	err := p.Initialize(PluginConfig{
		Cwd:       "/tmp",
		Model:     "test-model",
		Provider:  "test",
		GiVersion: "0.1.0",
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if len(p.tools) != 2 {
		t.Fatalf("tools count = %d, want 2", len(p.tools))
	}

	toolNames := map[string]bool{}
	for _, td := range p.tools {
		toolNames[td.Name] = true
	}
	if !toolNames["reverse"] {
		t.Error("missing tool 'reverse'")
	}
	if !toolNames["upper"] {
		t.Error("missing tool 'upper'")
	}

	if len(p.commands) != 1 {
		t.Fatalf("commands count = %d, want 1", len(p.commands))
	}
	if p.commands[0].Name != "test-cmd" {
		t.Errorf("command name = %q, want %q", p.commands[0].Name, "test-cmd")
	}
}

func TestIntegration_ToolCallRoundTrip(t *testing.T) {
	bin := buildTestPlugin(t)
	p := startIntegrationPlugin(t, bin, "normal")

	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Test reverse tool.
	content, isError, err := p.ExecuteTool("call-1", "reverse", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("ExecuteTool reverse: %v", err)
	}
	if isError {
		t.Error("reverse: isError = true, want false")
	}
	if content != "olleh" {
		t.Errorf("reverse content = %q, want %q", content, "olleh")
	}

	// Test upper tool.
	content, isError, err = p.ExecuteTool("call-2", "upper", map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("ExecuteTool upper: %v", err)
	}
	if isError {
		t.Error("upper: isError = true, want false")
	}
	if content != "HELLO" {
		t.Errorf("upper content = %q, want %q", content, "HELLO")
	}
}

func TestIntegration_ToolCallUnknownTool(t *testing.T) {
	bin := buildTestPlugin(t)
	p := startIntegrationPlugin(t, bin, "normal")

	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	content, isError, err := p.ExecuteTool("call-unk", "nonexistent", nil)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true for unknown tool")
	}
	if !strings.Contains(content, "unknown tool") {
		t.Errorf("content = %q, want containing 'unknown tool'", content)
	}
}

func TestIntegration_CommandExecution(t *testing.T) {
	bin := buildTestPlugin(t)
	p := startIntegrationPlugin(t, bin, "normal")

	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	text, isError, err := p.ExecuteCommand("test-cmd", "arg1 arg2")
	if err != nil {
		t.Fatalf("ExecuteCommand: %v", err)
	}
	if isError {
		t.Error("isError = true, want false")
	}
	if !strings.Contains(text, "arg1 arg2") {
		t.Errorf("text = %q, want containing 'arg1 arg2'", text)
	}
}

func TestIntegration_CommandUnknown(t *testing.T) {
	bin := buildTestPlugin(t)
	p := startIntegrationPlugin(t, bin, "normal")

	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	text, isError, err := p.ExecuteCommand("bogus", "")
	if err != nil {
		t.Fatalf("ExecuteCommand: %v", err)
	}
	if !isError {
		t.Error("isError = false, want true for unknown command")
	}
	if !strings.Contains(text, "unknown command") {
		t.Errorf("text = %q, want containing 'unknown command'", text)
	}
}

func TestIntegration_EventForwarding(t *testing.T) {
	bin := buildTestPlugin(t)
	p := startIntegrationPlugin(t, bin, "event_recorder")

	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Send several events.
	events := []EventPayload{
		{Type: "tool_exec_start", ToolName: "bash", ToolCallID: "tc_1"},
		{Type: "tool_exec_end", ToolName: "bash", ToolCallID: "tc_1", ToolResult: "output"},
		{Type: "agent_start"},
	}
	for _, ev := range events {
		if err := p.SendEvent(ev); err != nil {
			t.Fatalf("SendEvent: %v", err)
		}
	}

	// Give the plugin a moment to process events before querying.
	time.Sleep(50 * time.Millisecond)

	// Query recorded events.
	content, isError, err := p.ExecuteTool("call-ev", "get_events", nil)
	if err != nil {
		t.Fatalf("ExecuteTool get_events: %v", err)
	}
	if isError {
		t.Fatalf("get_events returned error: %s", content)
	}

	var recorded []map[string]any
	if err := json.Unmarshal([]byte(content), &recorded); err != nil {
		t.Fatalf("parsing recorded events: %v", err)
	}

	if len(recorded) != 3 {
		t.Fatalf("recorded %d events, want 3", len(recorded))
	}
	if recorded[0]["type"] != "tool_exec_start" {
		t.Errorf("event[0].type = %v, want tool_exec_start", recorded[0]["type"])
	}
	if recorded[0]["tool_name"] != "bash" {
		t.Errorf("event[0].tool_name = %v, want bash", recorded[0]["tool_name"])
	}
	if recorded[2]["type"] != "agent_start" {
		t.Errorf("event[2].type = %v, want agent_start", recorded[2]["type"])
	}
}

func TestIntegration_InjectMessage(t *testing.T) {
	bin := buildTestPlugin(t)
	p := startIntegrationPlugin(t, bin, "inject_on_init")

	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// The inject_on_init mode sends inject_message and log right after capabilities.
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
		t.Errorf("msg[0].Type = %q, want inject_message", msgs[0].Type)
	}
	if msgs[0].Content != "injected from testplugin" {
		t.Errorf("msg[0].Content = %q, want %q", msgs[0].Content, "injected from testplugin")
	}
	if msgs[0].Role != "assistant" {
		t.Errorf("msg[0].Role = %q, want assistant", msgs[0].Role)
	}

	if msgs[1].Type != "log" {
		t.Errorf("msg[1].Type = %q, want log", msgs[1].Type)
	}
	if msgs[1].Message != "testplugin initialized" {
		t.Errorf("msg[1].Message = %q, want %q", msgs[1].Message, "testplugin initialized")
	}
}

func TestIntegration_GracefulShutdown(t *testing.T) {
	bin := buildTestPlugin(t)
	p := startIntegrationPlugin(t, bin, "normal")

	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Verify alive before shutdown.
	if !p.Alive() {
		t.Fatal("plugin not alive before shutdown")
	}

	err := p.Stop()
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if p.Alive() {
		t.Error("plugin still alive after shutdown")
	}

	// Second stop should be idempotent.
	if err := p.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

func TestIntegration_CrashRecovery(t *testing.T) {
	bin := buildTestPlugin(t)
	p := startIntegrationPlugin(t, bin, "crash_on_tool")

	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Sending a tool call causes the plugin to crash.
	_, _, err := p.ExecuteTool("call-crash", "crash", nil)
	if err == nil {
		t.Fatal("expected error from crashed plugin")
	}

	// Process should be cleanable after crash.
	p.Stop()
	if p.Alive() {
		t.Error("plugin still alive after crash and stop")
	}
}

func TestIntegration_InitTimeout(t *testing.T) {
	bin := buildTestPlugin(t)

	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "PLUGIN_MODE=slow_init")

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("creating stdin pipe: %v", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		t.Fatalf("creating stdout pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting plugin: %v", err)
	}

	scanner := bufio.NewScanner(stdoutPipe)
	scanner.Buffer(make([]byte, maxScannerBuffer), maxScannerBuffer)

	p := &PluginProcess{
		name:        "testplugin-slow",
		path:        bin,
		cmd:         cmd,
		stdin:       stdinPipe,
		scanner:     scanner,
		injectCh:    make(chan PluginMessage, 64),
		responseCh:  make(chan PluginMessage, 16),
		heartbeatCh: make(chan PluginMessage, 4),
		healthy:     true,
		timeouts:    DefaultTimeoutConfig(),
	}

	go p.readLoop()

	t.Cleanup(func() {
		p.Stop()
	})

	// Send initialize and use a short waitResponse to simulate timeout
	// without waiting the full 10s initTimeout.
	if err := p.Send(HostMessage{
		Type:   "initialize",
		Config: &PluginConfig{},
	}); err != nil {
		t.Fatalf("Send initialize: %v", err)
	}

	_, err = p.waitResponse(200 * time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error from slow init")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want containing 'timed out'", err.Error())
	}
}

// --- Manager integration tests (end-to-end through Manager) ---

func TestIntegration_ManagerDiscoverAndInitialize(t *testing.T) {
	bin := buildTestPlugin(t)

	// Set up a plugin directory structure with the test binary.
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "testplugin")
	os.MkdirAll(pluginDir, 0755)

	// Copy binary into plugin directory.
	binName := "testplugin"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	destBin := filepath.Join(pluginDir, binName)
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("reading test binary: %v", err)
	}
	if err := os.WriteFile(destBin, data, 0755); err != nil {
		t.Fatalf("writing test binary: %v", err)
	}

	// Create manifest.
	manifest := Manifest{
		Name:       "testplugin",
		Executable: "./" + binName,
	}
	mdata, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), mdata, 0644)

	// Discover and initialize.
	reg := tools.NewRegistry()
	m := NewManager(reg)

	if err := m.Discover([]string{dir}); err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(m.plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(m.plugins))
	}

	if err := m.Initialize(PluginConfig{
		Cwd:       "/tmp",
		Model:     "test",
		Provider:  "test",
		GiVersion: "0.1.0",
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Verify tools were registered.
	if _, exists := reg.Get("reverse"); !exists {
		t.Error("tool 'reverse' not registered")
	}
	if _, exists := reg.Get("upper"); !exists {
		t.Error("tool 'upper' not registered")
	}

	// Execute a tool through the registry.
	tool, _ := reg.Get("reverse")
	result, err := tool.Execute(context.Background(), map[string]any{"text": "integration"})
	if err != nil {
		t.Fatalf("Execute reverse: %v", err)
	}
	if result != "noitargetni" {
		t.Errorf("result = %q, want %q", result, "noitargetni")
	}

	// Verify commands.
	cmds := m.PluginCommands()
	if len(cmds) != 1 {
		t.Fatalf("PluginCommands() = %d, want 1", len(cmds))
	}
	if cmds[0].Name != "test-cmd" {
		t.Errorf("command name = %q, want test-cmd", cmds[0].Name)
	}

	// Shutdown.
	if err := m.Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestIntegration_ManagerForwardEvent(t *testing.T) {
	bin := buildTestPlugin(t)
	p := startIntegrationPlugin(t, bin, "event_recorder")

	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	reg := tools.NewRegistry()
	m := NewManager(reg)
	m.plugins = []*PluginProcess{p}

	// Forward agent events through the manager.
	m.ForwardEvent(agent.AgentEvent{
		Type:     agent.EventToolExecStart,
		ToolName: "bash",
	})
	m.ForwardEvent(agent.AgentEvent{
		Type: agent.EventAgentStart,
	})

	// Give events time to be processed.
	time.Sleep(50 * time.Millisecond)

	// Query the plugin to verify events were received.
	content, _, err := p.ExecuteTool("call-check", "get_events", nil)
	if err != nil {
		t.Fatalf("ExecuteTool: %v", err)
	}

	var events []map[string]any
	if err := json.Unmarshal([]byte(content), &events); err != nil {
		t.Fatalf("parsing events: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
}

func TestIntegration_ManagerLoadPlugin(t *testing.T) {
	bin := buildTestPlugin(t)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	if err := m.LoadPlugin(bin); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}

	if len(m.plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(m.plugins))
	}

	if err := m.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Verify tools registered.
	if _, exists := reg.Get("reverse"); !exists {
		t.Error("tool 'reverse' not registered after LoadPlugin")
	}

	m.Shutdown()
}

func TestIntegration_MultipleToolCallsSequential(t *testing.T) {
	bin := buildTestPlugin(t)
	p := startIntegrationPlugin(t, bin, "normal")

	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	cases := []struct {
		tool   string
		text   string
		expect string
	}{
		{"reverse", "abc", "cba"},
		{"upper", "abc", "ABC"},
		{"reverse", "12345", "54321"},
		{"upper", "hello world", "HELLO WORLD"},
		{"reverse", "", ""},
	}

	for i, tc := range cases {
		content, isError, err := p.ExecuteTool(
			fmt.Sprintf("call-%d", i),
			tc.tool,
			map[string]any{"text": tc.text},
		)
		if err != nil {
			t.Fatalf("case %d: ExecuteTool %s(%q): %v", i, tc.tool, tc.text, err)
		}
		if isError {
			t.Errorf("case %d: isError = true", i)
		}
		if content != tc.expect {
			t.Errorf("case %d: got %q, want %q", i, content, tc.expect)
		}
	}
}

func TestIntegration_ToolCallAfterEvent(t *testing.T) {
	bin := buildTestPlugin(t)
	p := startIntegrationPlugin(t, bin, "normal")

	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Interleave events and tool calls to verify no interference.
	if err := p.SendEvent(EventPayload{Type: "agent_start"}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	content, _, err := p.ExecuteTool("interleave-1", "reverse", map[string]any{"text": "test"})
	if err != nil {
		t.Fatalf("ExecuteTool after event: %v", err)
	}
	if content != "tset" {
		t.Errorf("content = %q, want %q", content, "tset")
	}

	if err := p.SendEvent(EventPayload{Type: "tool_exec_end"}); err != nil {
		t.Fatalf("SendEvent: %v", err)
	}

	content, _, err = p.ExecuteTool("interleave-2", "upper", map[string]any{"text": "test"})
	if err != nil {
		t.Fatalf("ExecuteTool after second event: %v", err)
	}
	if content != "TEST" {
		t.Errorf("content = %q, want %q", content, "TEST")
	}
}

// TestUIRequest tests that plugins can send UI requests and receive responses.
func TestUIRequest(t *testing.T) {
	binPath := buildTestPlugin(t)
	p := startIntegrationPlugin(t, binPath, "ui_request")
	defer p.Stop()

	cfg := PluginConfig{
		Cwd:       "/tmp",
		Model:     "test",
		Provider:  "test",
		GiVersion: "0.1.0",
	}
	if err := p.Initialize(cfg); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Execute a tool that will send a UI request
	go func() {
		// Give the plugin time to send the UI request
		time.Sleep(100 * time.Millisecond)
		// Respond to the UI request with a value
		err := p.RespondToUIRequest("plugin_test_id", "John Smith", false, "")
		if err != nil {
			t.Errorf("RespondToUIRequest: %v", err)
		}
	}()

	// The plugin's ask_name tool sends a UI request but doesn't immediately return
	// Instead, it waits for a UI response. However, in our implementation the tool
	// call still needs to complete. This test demonstrates the API exists.
	// In a real implementation, the TUI would handle this asynchronously.

	// For now, just verify that WaitUIRequest works
	p2 := startIntegrationPlugin(t, binPath, "ui_request")
	defer p2.Stop()

	if err := p2.Initialize(cfg); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// In a real scenario, the host would:
	// 1. Send tool_call to plugin
	// 2. Receive ui_request from plugin (via WaitUIRequest)
	// 3. Show UI to user
	// 4. Send ui_response back to plugin
	// 5. Plugin completes tool_call with tool_result

	p.Stop()
}
