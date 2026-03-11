package plugin

import (
	"context"
	"strings"
	"testing"
)

func TestPluginTool_NameDescriptionSchema(t *testing.T) {
	pt := &PluginTool{
		def: ToolDef{
			Name:        "my_tool",
			Description: "does stuff",
			InputSchema: map[string]any{"type": "object"},
		},
	}

	if pt.Name() != "my_tool" {
		t.Errorf("Name() = %q, want %q", pt.Name(), "my_tool")
	}
	if pt.Description() != "does stuff" {
		t.Errorf("Description() = %q, want %q", pt.Description(), "does stuff")
	}
	if pt.Schema() == nil {
		t.Error("Schema() = nil, want non-nil")
	}
}

func TestPluginTool_Execute_Success(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	pt := &PluginTool{
		def:     ToolDef{Name: "echo", Description: "echoes"},
		process: p,
	}

	result, err := pt.Execute(context.Background(), map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result != "echoed:echo" {
		t.Errorf("result = %q, want %q", result, "echoed:echo")
	}
}

func TestPluginTool_Execute_PluginError(t *testing.T) {
	p := startTestPlugin(t, "tool_error")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	pt := &PluginTool{
		def:     ToolDef{Name: "fail", Description: "fails"},
		process: p,
	}

	_, err := pt.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error from plugin tool error")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("error = %q, want containing %q", err.Error(), "something went wrong")
	}
}

func TestPluginTool_Execute_DeadProcess(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(PluginConfig{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	p.Stop()

	pt := &PluginTool{
		def:     ToolDef{Name: "echo"},
		process: p,
	}

	_, err := pt.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error from dead process")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("error = %q, want containing %q", err.Error(), "not running")
	}
}

func TestRandomID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := randomID()
		if !strings.HasPrefix(id, "plugin_") {
			t.Fatalf("id %q does not have plugin_ prefix", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id: %s", id)
		}
		seen[id] = true
	}
}

func TestPluginCommand_Fields(t *testing.T) {
	pc := PluginCommand{
		Def:     CommandDef{Name: "test", Description: "testing"},
		Process: &PluginProcess{name: "owner"},
	}
	if pc.Def.Name != "test" {
		t.Errorf("Def.Name = %q, want %q", pc.Def.Name, "test")
	}
	if pc.Process.Name() != "owner" {
		t.Errorf("Process.Name() = %q, want %q", pc.Process.Name(), "owner")
	}
}
