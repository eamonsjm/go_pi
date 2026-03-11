package tools

import (
	"testing"
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
