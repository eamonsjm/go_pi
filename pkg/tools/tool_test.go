package tools

import (
	"context"
	"testing"

	"github.com/ejm/go_pi/pkg/ai"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()

	tool := &ReadTool{}
	r.Register(tool)

	got, ok := r.Get("read")
	if !ok {
		t.Fatal("expected tool 'read' to be found")
	}
	if got.Name() != "read" {
		t.Fatalf("expected name 'read', got %q", got.Name())
	}
}

func TestRegistry_GetMissing(t *testing.T) {
	r := NewRegistry()

	_, ok := r.Get("nonexistent")
	if ok {
		t.Fatal("expected tool to not be found")
	}
}

func TestRegistry_All(t *testing.T) {
	r := NewRegistry()
	r.Register(&WriteTool{})
	r.Register(&ReadTool{})
	r.Register(&BashTool{})

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(all))
	}

	// All() should return tools sorted by name.
	expected := []string{"bash", "read", "write"}
	for i, name := range expected {
		if all[i].Name() != name {
			t.Errorf("index %d: expected %q, got %q", i, name, all[i].Name())
		}
	}
}

func TestRegistry_ToToolDefs(t *testing.T) {
	r := NewRegistry()
	r.Register(&ReadTool{})
	r.Register(&BashTool{})

	defs := r.ToToolDefs()
	if len(defs) != 2 {
		t.Fatalf("expected 2 tool defs, got %d", len(defs))
	}

	// Sorted by name: bash, read.
	if defs[0].Name != "bash" {
		t.Errorf("expected first def name 'bash', got %q", defs[0].Name)
	}
	if defs[1].Name != "read" {
		t.Errorf("expected second def name 'read', got %q", defs[1].Name)
	}

	// Each def should have a non-empty description and non-nil schema.
	for _, d := range defs {
		if d.Description == "" {
			t.Errorf("tool %q has empty description", d.Name)
		}
		if d.InputSchema == nil {
			t.Errorf("tool %q has nil InputSchema", d.Name)
		}
	}
}

func TestRegisterDefaults(t *testing.T) {
	r := NewRegistry()
	RegisterDefaults(r)

	expected := []string{"bash", "edit", "glob", "grep", "read", "write"}
	all := r.All()
	if len(all) != len(expected) {
		t.Fatalf("expected %d tools, got %d", len(expected), len(all))
	}
	for i, name := range expected {
		if all[i].Name() != name {
			t.Errorf("index %d: expected %q, got %q", i, name, all[i].Name())
		}
	}
}

func TestRegistry_RegisterOverwrites(t *testing.T) {
	r := NewRegistry()
	r.Register(&ReadTool{})
	r.Register(&ReadTool{})

	all := r.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 tool after duplicate register, got %d", len(all))
	}
}

// testRichTool is a mock RichTool for testing.
type testRichTool struct {
	name string
}

func (t *testRichTool) Name() string        { return t.name }
func (t *testRichTool) Description() string { return "test rich tool" }
func (t *testRichTool) Schema() any         { return nil }
func (t *testRichTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	return "fallback", nil
}
func (t *testRichTool) ExecuteRich(_ context.Context, _ map[string]any) ([]ai.ContentBlock, error) {
	return []ai.ContentBlock{
		{Type: ai.ContentTypeText, Text: "hello"},
	}, nil
}

func TestRegistry_RichToolRegistration(t *testing.T) {
	r := NewRegistry()
	rt := &testRichTool{name: "rich"}
	r.Register(rt)

	got, ok := r.Get("rich")
	if !ok {
		t.Fatal("expected tool 'rich' to be found")
	}
	if got.Name() != "rich" {
		t.Fatalf("expected name 'rich', got %q", got.Name())
	}

	// Should be assertable to RichTool.
	richTool, ok := got.(RichTool)
	if !ok {
		t.Fatal("expected tool to implement RichTool interface")
	}

	blocks, err := richTool.ExecuteRich(context.Background(), nil)
	if err != nil {
		t.Fatalf("ExecuteRich returned error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Text != "hello" {
		t.Errorf("expected text 'hello', got %q", blocks[0].Text)
	}
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	r.Register(&ReadTool{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			r.Register(&testRichTool{name: "concurrent"})
		}
	}()

	for i := 0; i < 1000; i++ {
		r.Get("read")
		r.All()
		r.ToToolDefs()
	}
	<-done
}

func TestRegistry_NonRichToolNotAssertable(t *testing.T) {
	r := NewRegistry()
	r.Register(&ReadTool{})

	got, _ := r.Get("read")
	if _, ok := got.(RichTool); ok {
		t.Error("ReadTool should not implement RichTool")
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	r.Register(&ReadTool{})
	r.Register(&WriteTool{})

	r.Unregister("read")

	if _, ok := r.Get("read"); ok {
		t.Error("expected 'read' to be removed")
	}
	if _, ok := r.Get("write"); !ok {
		t.Error("expected 'write' to still exist")
	}
}

func TestRegistry_UnregisterMissing(t *testing.T) {
	r := NewRegistry()
	// Should not panic.
	r.Unregister("nonexistent")
}

func TestRegistry_ReplaceByPrefix(t *testing.T) {
	r := NewRegistry()
	r.Register(&ReadTool{})
	r.Register(&testRichTool{name: "mcp__fs__read"})
	r.Register(&testRichTool{name: "mcp__fs__write"})
	r.Register(&testRichTool{name: "mcp__db__query"})

	// Replace all mcp__fs__ tools.
	r.ReplaceByPrefix("mcp__fs__", []Tool{
		&testRichTool{name: "mcp__fs__list"},
	})

	// mcp__fs__read and mcp__fs__write should be gone.
	if _, ok := r.Get("mcp__fs__read"); ok {
		t.Error("expected mcp__fs__read to be removed")
	}
	if _, ok := r.Get("mcp__fs__write"); ok {
		t.Error("expected mcp__fs__write to be removed")
	}
	// mcp__fs__list should be added.
	if _, ok := r.Get("mcp__fs__list"); !ok {
		t.Error("expected mcp__fs__list to be registered")
	}
	// built-in 'read' should be untouched.
	if _, ok := r.Get("read"); !ok {
		t.Error("expected built-in 'read' to remain")
	}
	// mcp__db__query should be untouched.
	if _, ok := r.Get("mcp__db__query"); !ok {
		t.Error("expected mcp__db__query to remain")
	}
}

func TestRegistry_ReplaceByPrefixEmpty(t *testing.T) {
	r := NewRegistry()
	r.Register(&testRichTool{name: "mcp__fs__read"})

	// Replace with empty set = remove all.
	r.ReplaceByPrefix("mcp__fs__", nil)

	if _, ok := r.Get("mcp__fs__read"); ok {
		t.Error("expected mcp__fs__read to be removed")
	}
	all := r.All()
	if len(all) != 0 {
		t.Errorf("expected 0 tools, got %d", len(all))
	}
}

func TestRegistry_AllWithPrefix(t *testing.T) {
	r := NewRegistry()
	r.Register(&ReadTool{})
	r.Register(&testRichTool{name: "mcp__fs__read"})
	r.Register(&testRichTool{name: "mcp__fs__write"})
	r.Register(&testRichTool{name: "mcp__db__query"})

	fsTools := r.AllWithPrefix("mcp__fs__")
	if len(fsTools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(fsTools))
	}

	dbTools := r.AllWithPrefix("mcp__db__")
	if len(dbTools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(dbTools))
	}

	noTools := r.AllWithPrefix("mcp__nonexist__")
	if len(noTools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(noTools))
	}
}

func TestRichToolError(t *testing.T) {
	err := &RichToolError{
		Blocks: []ai.ContentBlock{
			{Type: ai.ContentTypeText, Text: "error: "},
			{Type: ai.ContentTypeImage, MediaType: "image/png", ImageData: "data"},
			{Type: ai.ContentTypeText, Text: "file not found"},
		},
	}
	// Error() should only concatenate text blocks.
	if err.Error() != "error: file not found" {
		t.Errorf("Error() = %q, want %q", err.Error(), "error: file not found")
	}
}

func TestRichToolErrorEmpty(t *testing.T) {
	err := &RichToolError{}
	if err.Error() != "" {
		t.Errorf("Error() = %q, want empty", err.Error())
	}
}

func TestRegistry_ConcurrentReplaceAndRead(t *testing.T) {
	r := NewRegistry()
	r.Register(&ReadTool{})
	r.Register(&testRichTool{name: "mcp__fs__read"})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			r.ReplaceByPrefix("mcp__fs__", []Tool{
				&testRichTool{name: "mcp__fs__v2"},
			})
		}
	}()

	for i := 0; i < 500; i++ {
		r.AllWithPrefix("mcp__fs__")
		r.Get("read")
		r.All()
	}
	<-done
}
