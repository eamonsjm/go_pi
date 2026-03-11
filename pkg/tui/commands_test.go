package tui

import "testing"

func TestCommandRegistry_RegisterAndGet(t *testing.T) {
	reg := NewCommandRegistry()

	cmd := &SlashCommand{Name: "test", Description: "a test command"}
	reg.Register(cmd)

	got, ok := reg.Get("test")
	if !ok {
		t.Fatal("expected command to be found")
	}
	if got.Name != "test" {
		t.Errorf("expected name 'test', got %q", got.Name)
	}

	_, ok = reg.Get("nonexistent")
	if ok {
		t.Error("expected false for nonexistent command")
	}
}

func TestCommandRegistry_RegisterOverwrite(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "cmd", Description: "first"})
	reg.Register(&SlashCommand{Name: "cmd", Description: "second"})

	got, ok := reg.Get("cmd")
	if !ok {
		t.Fatal("expected command to be found")
	}
	if got.Description != "second" {
		t.Errorf("expected overwritten description 'second', got %q", got.Description)
	}
}

func TestCommandRegistry_All(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "zebra"})
	reg.Register(&SlashCommand{Name: "alpha"})
	reg.Register(&SlashCommand{Name: "mid"})

	all := reg.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(all))
	}
	if all[0].Name != "alpha" || all[1].Name != "mid" || all[2].Name != "zebra" {
		t.Errorf("expected alphabetical order, got %v, %v, %v", all[0].Name, all[1].Name, all[2].Name)
	}
}

func TestCommandRegistry_AllEmpty(t *testing.T) {
	reg := NewCommandRegistry()
	all := reg.All()
	if len(all) != 0 {
		t.Errorf("expected empty list, got %d", len(all))
	}
}

func TestCommandRegistry_Match(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "model"})
	reg.Register(&SlashCommand{Name: "module"})
	reg.Register(&SlashCommand{Name: "compact"})

	tests := []struct {
		prefix string
		want   int
		names  []string
	}{
		{"mod", 2, []string{"model", "module"}},
		{"model", 1, []string{"model"}},
		{"comp", 1, []string{"compact"}},
		{"xyz", 0, nil},
		{"", 3, []string{"compact", "model", "module"}},
	}

	for _, tt := range tests {
		matches := reg.Match(tt.prefix)
		if len(matches) != tt.want {
			t.Errorf("Match(%q): expected %d matches, got %d", tt.prefix, tt.want, len(matches))
			continue
		}
		for i, name := range tt.names {
			if matches[i].Name != name {
				t.Errorf("Match(%q)[%d]: expected %q, got %q", tt.prefix, i, name, matches[i].Name)
			}
		}
	}
}
