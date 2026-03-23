package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/ejm/go_pi/pkg/config"
	"github.com/ejm/go_pi/pkg/tools"
)

func TestNewMCPManager(t *testing.T) {
	reg := tools.NewRegistry()
	mgr := NewMCPManager(MCPManagerConfig{
		ToolRegistry:  reg,
		WorkingDir:    "/home/user/project",
		ConfigDir:     "/home/user/.gi",
		ProjectPath:   "/home/user/project",
		ClientName:    "gi",
		ClientVersion: "0.1.0",
	})

	if mgr == nil {
		t.Fatal("NewMCPManager returned nil")
	}
	if mgr.workingDir != "/home/user/project" {
		t.Errorf("workingDir = %q, want %q", mgr.workingDir, "/home/user/project")
	}
	if mgr.clientName != "gi" {
		t.Errorf("clientName = %q, want %q", mgr.clientName, "gi")
	}
}

func TestInjectSystemMessage(t *testing.T) {
	reg := tools.NewRegistry()
	mgr := NewMCPManager(MCPManagerConfig{ToolRegistry: reg})

	// Inject a message.
	mgr.injectSystemMessage("test message 1")
	mgr.injectSystemMessage("test message 2")

	msgs := mgr.DrainSystemMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0] != "test message 1" {
		t.Errorf("msgs[0] = %q, want %q", msgs[0], "test message 1")
	}

	// After drain, should be empty.
	msgs = mgr.DrainSystemMessages()
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages after drain, got %d", len(msgs))
	}
}

func TestInjectSystemMessageOverflow(t *testing.T) {
	reg := tools.NewRegistry()
	mgr := NewMCPManager(MCPManagerConfig{ToolRegistry: reg})

	// Inject maxPendingMessages + 5 messages.
	for i := 0; i < maxPendingMessages+5; i++ {
		mgr.injectSystemMessage("msg")
	}

	msgs := mgr.DrainSystemMessages()
	// Should be capped at maxPendingMessages + 1 (the coalesced overflow msg).
	if len(msgs) != maxPendingMessages+1 {
		t.Fatalf("expected %d messages, got %d", maxPendingMessages+1, len(msgs))
	}
	last := msgs[len(msgs)-1]
	if !strings.Contains(last, "coalesced") {
		t.Errorf("last message should mention coalesced, got %q", last)
	}
}

func TestServerInstructions(t *testing.T) {
	reg := tools.NewRegistry()
	mgr := NewMCPManager(MCPManagerConfig{ToolRegistry: reg})

	// Add mock servers directly.
	mgr.mu.Lock()
	mgr.servers["server1"] = &MCPServer{
		name:         "server1",
		config:       &config.MCPServerConfig{},
		instructions: "Use this server for file operations.",
	}
	mgr.servers["server2"] = &MCPServer{
		name:         "server2",
		config:       &config.MCPServerConfig{Instructions: "ignore"},
		instructions: "Ignored instructions.",
	}
	mgr.servers["server3"] = &MCPServer{
		name:         "server3",
		config:       &config.MCPServerConfig{},
		instructions: "<script>alert('xss')</script>",
	}
	mgr.serverList = []string{"server1", "server2", "server3"}
	mgr.mu.Unlock()

	result := mgr.ServerInstructions()

	// server1 should be present.
	if !strings.Contains(result, "file operations") {
		t.Error("expected server1 instructions in output")
	}
	// server2 should be ignored.
	if strings.Contains(result, "Ignored instructions") {
		t.Error("server2 instructions should be ignored")
	}
	// server3 should have escaped angle brackets.
	if strings.Contains(result, "<script>") {
		t.Error("angle brackets should be escaped")
	}
	if !strings.Contains(result, "&lt;script&gt;") {
		t.Error("expected escaped angle brackets")
	}
	// All should be in mcp-server-instructions tags.
	if !strings.Contains(result, "<mcp-server-instructions") {
		t.Error("expected mcp-server-instructions tags")
	}
}

func TestServerInstructionsLengthCap(t *testing.T) {
	reg := tools.NewRegistry()
	mgr := NewMCPManager(MCPManagerConfig{ToolRegistry: reg})

	longInstr := strings.Repeat("x", 3000)
	mgr.mu.Lock()
	mgr.servers["long"] = &MCPServer{
		name:         "long",
		config:       &config.MCPServerConfig{},
		instructions: longInstr,
	}
	mgr.serverList = []string{"long"}
	mgr.mu.Unlock()

	result := mgr.ServerInstructions()
	if !strings.Contains(result, "[truncated]") {
		t.Error("expected truncation marker for long instructions")
	}
	// The instruction content (minus tags) should be <= 2000 + len(" [truncated]").
	if strings.Count(result, "x") > 2000 {
		t.Error("instructions should be truncated to 2000 chars")
	}
}

func TestDiffToolCount(t *testing.T) {
	old := []tools.Tool{
		&mockTool{name: "a"},
		&mockTool{name: "b"},
		&mockTool{name: "c"},
	}
	newT := []tools.Tool{
		&mockTool{name: "b"},
		&mockTool{name: "c"},
		&mockTool{name: "d"},
	}

	added, removed := diffToolCount(old, newT)
	if added != 1 || removed != 1 {
		t.Errorf("diffToolCount = (added=%d, removed=%d), want (1, 1)", added, removed)
	}
}

func TestHandleRootsList(t *testing.T) {
	reg := tools.NewRegistry()
	mgr := NewMCPManager(MCPManagerConfig{
		ToolRegistry: reg,
		WorkingDir:   "/home/user/project",
	})

	result := mgr.handleRootsList()
	roots, ok := result["roots"].([]map[string]any)
	if !ok || len(roots) != 1 {
		t.Fatal("expected 1 root")
	}
	if roots[0]["uri"] != "file:///home/user/project" {
		t.Errorf("root URI = %v, want file:///home/user/project", roots[0]["uri"])
	}
}

// mockTool is a minimal tools.Tool implementation for testing.
type mockTool struct {
	name string
}

func (t *mockTool) Name() string                                             { return t.name }
func (t *mockTool) Description() string                                      { return "mock" }
func (t *mockTool) Schema() any                                              { return nil }
func (t *mockTool) Execute(_ context.Context, _ map[string]any) (string, error) { return "", nil }
