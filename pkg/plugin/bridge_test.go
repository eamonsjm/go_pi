package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// discardWriteCloser is an io.WriteCloser that discards all writes.
type discardWriteCloser struct{}

func (discardWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardWriteCloser) Close() error                { return nil }

func TestTool_NameDescriptionSchema(t *testing.T) {
	pt := &Tool{
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
	schema, ok := pt.Schema().(map[string]any)
	if !ok {
		t.Fatalf("Schema() type = %T, want map[string]any", pt.Schema())
	}
	if schema["type"] != "object" {
		t.Errorf("Schema()[\"type\"] = %v, want %q", schema["type"], "object")
	}
}

func TestTool_Execute_Success(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(context.Background(), Config{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	pt := &Tool{
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

func TestTool_Execute_Error(t *testing.T) {
	p := startTestPlugin(t, "tool_error")
	if err := p.Initialize(context.Background(), Config{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	pt := &Tool{
		def:     ToolDef{Name: "fail", Description: "fails"},
		process: p,
	}

	_, err := pt.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error from plugin tool error")
	}
	var pluginErr *Error
	if !errors.As(err, &pluginErr) {
		t.Fatalf("error type = %T, want *Error", err)
	}
	if !strings.Contains(pluginErr.Content, "something went wrong") {
		t.Errorf("Error.Content = %q, want containing %q", pluginErr.Content, "something went wrong")
	}
}

func TestTool_Execute_DeadProcess(t *testing.T) {
	p := startTestPlugin(t, "echo_caps")
	if err := p.Initialize(context.Background(), Config{}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	p.Stop()

	pt := &Tool{
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

func TestTool_Execute_CommunicationError(t *testing.T) {
	// Simulate a process that passes the Alive() check but whose response
	// channel is already closed — e.g., the plugin process exited while a
	// tool call was in flight.
	responseCh := make(chan Message)
	close(responseCh)

	p := &Process{
		name:       "crashed",
		stdin:      discardWriteCloser{},
		injectCh:   make(chan Message, 64),
		responseCh: responseCh,
		timeouts:   DefaultTimeoutConfig(),
	}

	pt := &Tool{
		def:     ToolDef{Name: "my_tool"},
		process: p,
	}

	_, err := pt.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error from communication failure")
	}
	if !strings.Contains(err.Error(), "plugin tool my_tool") {
		t.Errorf("error = %q, want containing %q", err.Error(), "plugin tool my_tool")
	}
	if !strings.Contains(err.Error(), "process exited") {
		t.Errorf("error = %q, want containing %q", err.Error(), "process exited")
	}
}

func TestRandomID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := randomID()
		if err != nil {
			t.Fatalf("randomID() returned error: %v", err)
		}
		if !strings.HasPrefix(id, "plugin_") {
			t.Fatalf("id %q does not have plugin_ prefix", id)
		}
		// 16 random bytes = 32 hex chars, plus "plugin_" prefix = 39
		if len(id) != 39 {
			t.Fatalf("id %q has length %d, want 39", id, len(id))
		}
		if seen[id] {
			t.Fatalf("duplicate id: %s", id)
		}
		seen[id] = true
	}
}
