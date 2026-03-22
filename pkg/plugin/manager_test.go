package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/tools"
)

// makeExitExe creates a minimal executable that exits immediately.
// Used for discovery/loading tests that only need a valid executable path
// without actually starting a full plugin subprocess.
func makeExitExe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var name, content string
	if runtime.GOOS == "windows" {
		name = "exit.bat"
		content = "@exit /b 0\n"
	} else {
		name = "exit.sh"
		content = "#!/bin/sh\nexit 0\n"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewManager(t *testing.T) {
	reg := tools.NewRegistry()
	m := NewManager(reg)
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
	if m.toolRegistry != reg {
		t.Error("toolRegistry not set correctly")
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0", len(m.plugins))
	}
}

func TestDiscover_NonExistentDir(t *testing.T) {
	reg := tools.NewRegistry()
	m := NewManager(reg)

	// Non-existent directories should be silently skipped.
	err := m.Discover([]string{"/no/such/directory/exists"})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0", len(m.plugins))
	}
}

func TestDiscover_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover([]string{dir})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0 for empty dir", len(m.plugins))
	}
}

func TestDiscover_SkipsFiles(t *testing.T) {
	dir := t.TempDir()

	// Create a regular file (not a directory) — should be skipped.
	os.WriteFile(filepath.Join(dir, "not-a-dir"), []byte("hi"), 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover([]string{dir})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0", len(m.plugins))
	}
}

func TestDiscover_WithManifest(t *testing.T) {
	exe := makeExitExe(t)
	dir := t.TempDir()

	pluginDir := filepath.Join(dir, "test-plugin")
	os.MkdirAll(pluginDir, 0755)

	manifest := Manifest{
		Name:       "test-plugin",
		Executable: exe,
	}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), data, 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover([]string{dir})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	if len(m.plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(m.plugins))
	}
	if m.plugins[0].name != "test-plugin" {
		t.Errorf("plugin name = %q, want %q", m.plugins[0].name, "test-plugin")
	}

	m.Shutdown()
}

func TestDiscover_ManifestDefaultName(t *testing.T) {
	exe := makeExitExe(t)
	dir := t.TempDir()

	pluginDir := filepath.Join(dir, "my-plugin")
	os.MkdirAll(pluginDir, 0755)

	manifest := Manifest{
		// Name omitted — should default to directory name.
		Executable: exe,
	}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), data, 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)
	m.Discover([]string{dir})

	if len(m.plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(m.plugins))
	}
	if m.plugins[0].name != "my-plugin" {
		t.Errorf("name = %q, want %q", m.plugins[0].name, "my-plugin")
	}

	m.Shutdown()
}

func TestDiscover_BadManifest(t *testing.T) {
	dir := t.TempDir()

	pluginDir := filepath.Join(dir, "bad-plugin")
	os.MkdirAll(pluginDir, 0755)

	// Invalid JSON in manifest.
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte("{invalid"), 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	// Should not return an error — individual plugin failures are logged and skipped.
	err := m.Discover([]string{dir})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0", len(m.plugins))
	}
}

func TestDiscover_NoManifestNoExecutable(t *testing.T) {
	dir := t.TempDir()

	pluginDir := filepath.Join(dir, "empty-plugin")
	os.MkdirAll(pluginDir, 0755)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.Discover([]string{dir})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	// No manifest and no matching executable — plugin should fail to load.
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0", len(m.plugins))
	}
}

func TestDiscover_ExecutableNotExecutable(t *testing.T) {
	dir := t.TempDir()

	pluginDir := filepath.Join(dir, "my-plugin")
	os.MkdirAll(pluginDir, 0755)

	// Create a file with matching name but no execute permission.
	execPath := filepath.Join(pluginDir, "my-plugin")
	os.WriteFile(execPath, []byte("#!/bin/sh\n"), 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)
	m.Discover([]string{dir})

	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0 (not executable)", len(m.plugins))
	}
}

func TestLoadPlugin_DirectExecutable(t *testing.T) {
	exe := makeExitExe(t)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.LoadPlugin(exe)
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if len(m.plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(m.plugins))
	}

	m.Shutdown()
}

func TestLoadPlugin_NonExistent(t *testing.T) {
	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.LoadPlugin("/does/not/exist")
	if err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestLoadPlugin_DirectoryPath(t *testing.T) {
	exe := makeExitExe(t)
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "test-plugin")
	os.MkdirAll(pluginDir, 0755)

	manifest := Manifest{
		Name:       "dir-loaded",
		Executable: exe,
	}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(pluginDir, "plugin.json"), data, 0644)

	reg := tools.NewRegistry()
	m := NewManager(reg)

	err := m.LoadPlugin(pluginDir)
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	if len(m.plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(m.plugins))
	}
	if m.plugins[0].name != "dir-loaded" {
		t.Errorf("name = %q, want %q", m.plugins[0].name, "dir-loaded")
	}

	m.Shutdown()
}

func TestInitialize_RegistersTools(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")

	reg := tools.NewRegistry()
	m := NewManager(reg)
	m.plugins = []*PluginProcess{p}

	err := m.Initialize(context.Background(), PluginConfig{})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// The "echo" tool from the helper should be registered.
	tool, exists := reg.Get("echo")
	if !exists {
		t.Fatal("tool 'echo' not registered")
	}
	if tool.Name() != "echo" {
		t.Errorf("tool.Name() = %q, want %q", tool.Name(), "echo")
	}
}

func TestInitialize_SkipsDuplicateTools(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")

	reg := tools.NewRegistry()
	// Pre-register a tool with the same name.
	reg.Register(&dummyTool{name: "echo"})

	m := NewManager(reg)
	m.plugins = []*PluginProcess{p}

	m.Initialize(context.Background(), PluginConfig{})

	// The existing tool should remain (not be replaced).
	tool, _ := reg.Get("echo")
	if _, ok := tool.(*dummyTool); !ok {
		t.Error("existing tool was replaced by plugin tool")
	}
}

func TestInitialize_RemovesFailedPlugins(t *testing.T) {
	good := startTestPlugin(t, "echo_caps")
	bad := startTestPlugin(t, "exit_immediately")

	reg := tools.NewRegistry()
	m := NewManager(reg)
	m.plugins = []*PluginProcess{bad, good}

	m.Initialize(context.Background(), PluginConfig{})

	if len(m.plugins) != 1 {
		t.Fatalf("plugins = %d, want 1 (failed plugin should be removed)", len(m.plugins))
	}
	if m.plugins[0].name != good.name {
		t.Error("wrong plugin survived")
	}
}

func TestShutdown(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(context.Background(), PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	reg := tools.NewRegistry()
	m := NewManager(reg)
	m.plugins = []*PluginProcess{p}

	err := m.Shutdown()
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0 after shutdown", len(m.plugins))
	}
}

func TestPluginTools(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(context.Background(), PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	reg := tools.NewRegistry()
	m := NewManager(reg)
	m.plugins = []*PluginProcess{p}

	td := m.PluginTools()
	if len(td) != 1 {
		t.Fatalf("PluginTools() = %d, want 1", len(td))
	}
	if td[0].Name != "echo" {
		t.Errorf("tool name = %q, want %q", td[0].Name, "echo")
	}
}

func TestPluginCommands(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(context.Background(), PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	reg := tools.NewRegistry()
	m := NewManager(reg)
	m.plugins = []*PluginProcess{p}

	cmds := m.PluginCommands()
	if len(cmds) != 1 {
		t.Fatalf("PluginCommands() = %d, want 1", len(cmds))
	}
	if cmds[0].Name != "greet" {
		t.Errorf("command name = %q, want %q", cmds[0].Name, "greet")
	}
}

func TestForwardEvent(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(context.Background(), PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	reg := tools.NewRegistry()
	m := NewManager(reg)
	m.plugins = []*PluginProcess{p}

	// Should not panic or error.
	m.ForwardEvent(agent.AgentEvent{
		Type:     agent.EventToolExecStart,
		ToolName: "bash",
	})

	if !p.Alive() {
		t.Error("plugin died after forwarding event")
	}
}

func TestForwardEvent_SkipsDeadPlugins(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(context.Background(), PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	p.Stop()

	reg := tools.NewRegistry()
	m := NewManager(reg)
	m.plugins = []*PluginProcess{p}

	// Should not panic even with dead plugin.
	m.ForwardEvent(agent.AgentEvent{Type: agent.EventAgentStart})
}

func TestAgentEventToPayload_ToolExec(t *testing.T) {
	event := agent.AgentEvent{
		Type:       agent.EventToolExecStart,
		ToolName:   "bash",
		ToolCallID: "tc_123",
		ToolArgs:   map[string]any{"command": "ls"},
		ToolResult: "file1\nfile2",
		ToolError:  true,
	}

	payload := agentEventToPayload(event)

	if payload.Type != "tool_exec_start" {
		t.Errorf("Type = %q, want %q", payload.Type, "tool_exec_start")
	}
	if payload.ToolName != "bash" {
		t.Errorf("ToolName = %q, want %q", payload.ToolName, "bash")
	}
	if payload.ToolCallID != "tc_123" {
		t.Errorf("ToolCallID = %q, want %q", payload.ToolCallID, "tc_123")
	}
	if payload.ToolArgs["command"] != "ls" {
		t.Errorf("ToolArgs[command] = %v, want %q", payload.ToolArgs["command"], "ls")
	}
	if payload.ToolResult != "file1\nfile2" {
		t.Errorf("ToolResult = %q, want %q", payload.ToolResult, "file1\nfile2")
	}
	if !payload.ToolError {
		t.Error("ToolError = false, want true")
	}
}

func TestAgentEventToPayload_ToolExecEnd(t *testing.T) {
	event := agent.AgentEvent{
		Type:       agent.EventToolExecEnd,
		ToolName:   "read",
		ToolResult: "contents",
	}

	payload := agentEventToPayload(event)
	if payload.Type != "tool_exec_end" {
		t.Errorf("Type = %q, want %q", payload.Type, "tool_exec_end")
	}
	if payload.ToolName != "read" {
		t.Errorf("ToolName = %q, want %q", payload.ToolName, "read")
	}
}

func TestAgentEventToPayload_AgentError(t *testing.T) {
	event := agent.AgentEvent{
		Type:  agent.EventAgentError,
		Error: errors.New("something broke"),
	}

	payload := agentEventToPayload(event)
	if payload.Type != "agent_error" {
		t.Errorf("Type = %q, want %q", payload.Type, "agent_error")
	}
	if payload.Error != "something broke" {
		t.Errorf("Error = %q, want %q", payload.Error, "something broke")
	}
}

func TestAgentEventToPayload_AgentErrorNil(t *testing.T) {
	event := agent.AgentEvent{
		Type:  agent.EventAgentError,
		Error: nil,
	}

	payload := agentEventToPayload(event)
	if payload.Error != "" {
		t.Errorf("Error = %q, want empty", payload.Error)
	}
}

func TestAgentEventToPayload_OtherEvent(t *testing.T) {
	event := agent.AgentEvent{
		Type:     agent.EventAgentStart,
		ToolName: "should-be-ignored",
	}

	payload := agentEventToPayload(event)
	if payload.Type != "agent_start" {
		t.Errorf("Type = %q, want %q", payload.Type, "agent_start")
	}
	// Non-tool events should not have tool fields populated.
	if payload.ToolName != "" {
		t.Errorf("ToolName = %q, want empty for non-tool event", payload.ToolName)
	}
}

func TestInitialize_StopsPluginOnRegistrationPanic(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")

	// A nil toolRegistry causes a panic during tool registration, after
	// the plugin process has already been successfully initialized.
	m := &Manager{
		plugins:      []*PluginProcess{p},
		toolRegistry: nil,
	}

	err := m.Initialize(context.Background(), PluginConfig{})
	if err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}

	// The plugin should have been removed from the alive list.
	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0 (panicked plugin should be removed)", len(m.plugins))
	}

	// The plugin process should have been stopped (not leaked).
	if p.Alive() {
		t.Error("plugin process still alive after registration panic")
	}
}

func TestStartHeartbeats_SendsPeriodicHeartbeats(t *testing.T) {
	p := startTestPlugin(t, "heartbeat_ack")
	if err := p.Initialize(context.Background(), PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	reg := tools.NewRegistry()
	m := NewManager(reg)
	m.plugins = []*PluginProcess{p}

	cfg := HeartbeatConfig{
		Interval: 100 * time.Millisecond,
		Timeout:  2 * time.Second,
	}
	m.SetHeartbeatConfig(&cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m.StartHeartbeats(ctx)

	// Wait for at least one heartbeat to complete.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for heartbeat")
		default:
		}
		if p.LastHeartbeatStatus() != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !p.Healthy() {
		t.Error("plugin not healthy after heartbeat")
	}

	status := p.LastHeartbeatStatus()
	if status.MemoryBytes != 1024000 {
		t.Errorf("MemoryBytes = %d, want 1024000", status.MemoryBytes)
	}
}

func TestPluginHealthy_UnknownPlugin(t *testing.T) {
	reg := tools.NewRegistry()
	m := NewManager(reg)

	if m.PluginHealthy("nonexistent") {
		t.Error("PluginHealthy returned true for unknown plugin")
	}
}

func TestShutdown_StopsHeartbeats(t *testing.T) {
	p := startTestPlugin(t, "heartbeat_ack")
	if err := p.Initialize(context.Background(), PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	reg := tools.NewRegistry()
	m := NewManager(reg)
	m.plugins = []*PluginProcess{p}

	cfg := HeartbeatConfig{
		Interval: 100 * time.Millisecond,
		Timeout:  2 * time.Second,
	}
	m.SetHeartbeatConfig(&cfg)

	m.StartHeartbeats(context.Background())

	// Shutdown should stop heartbeats and plugins.
	err := m.Shutdown()
	if err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if len(m.plugins) != 0 {
		t.Errorf("plugins = %d, want 0", len(m.plugins))
	}
}

func TestSetHeartbeatConfig_Nil(t *testing.T) {
	reg := tools.NewRegistry()
	m := NewManager(reg)

	m.SetHeartbeatConfig(nil)

	// StartHeartbeats should be a no-op when config is nil.
	m.StartHeartbeats(context.Background())

	if m.heartbeatDone != nil {
		t.Error("heartbeat goroutine started with nil config")
	}
}

// dummyTool is a minimal tools.Tool implementation for testing.
type dummyTool struct {
	name string
}

func (d *dummyTool) Name() string                                                { return d.name }
func (d *dummyTool) Description() string                                         { return "dummy" }
func (d *dummyTool) Schema() any                                                 { return nil }
func (d *dummyTool) Execute(_ context.Context, _ map[string]any) (string, error) { return "", nil }

// TestManagerConcurrentAccess exercises concurrent read/write on m.plugins
// to verify the RWMutex protects against data races. Run with -race.
func TestManagerConcurrentAccess(t *testing.T) {
	reg := tools.NewRegistry()
	m := NewManager(reg)

	// Seed a plugin marked as closed so readers iterate the slice
	// without attempting I/O on uninitialized process internals.
	seed := &PluginProcess{name: "seed"}
	seed.closed = true
	m.plugins = append(m.plugins, seed)

	done := make(chan struct{})

	// Reader: ForwardEvent (iterates m.plugins, skips closed)
	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < 100; i++ {
			m.ForwardEvent(agent.AgentEvent{Type: agent.EventAgentStart})
		}
	}()

	// Reader: Plugins
	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < 100; i++ {
			_ = m.Plugins()
		}
	}()

	// Reader: PluginHealthy
	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < 100; i++ {
			_ = m.PluginHealthy("seed")
		}
	}()

	// Reader: PluginTools + PluginCommands
	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < 100; i++ {
			_ = m.PluginTools()
			_ = m.PluginCommands()
		}
	}()

	// Writer: Shutdown (sets plugins to nil)
	go func() {
		defer func() { done <- struct{}{} }()
		_ = m.Shutdown()
	}()

	for i := 0; i < 5; i++ {
		<-done
	}
}
