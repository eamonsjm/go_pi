package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

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

// ---------------------------------------------------------------------------
// Command registry edge cases
// ---------------------------------------------------------------------------

func TestCommandRegistry_EmptyName(t *testing.T) {
	reg := NewCommandRegistry()
	cmd := &SlashCommand{Name: "", Description: "empty name command"}
	reg.Register(cmd)

	got, ok := reg.Get("")
	if !ok {
		t.Fatal("expected empty-name command to be retrievable")
	}
	if got.Description != "empty name command" {
		t.Errorf("expected description 'empty name command', got %q", got.Description)
	}
}

func TestCommandRegistry_DuplicateRegisterPreservesLatest(t *testing.T) {
	reg := NewCommandRegistry()
	var callOrder []string
	cmd1 := &SlashCommand{
		Name: "dup",
		Execute: func(args string) tea.Cmd {
			callOrder = append(callOrder, "first")
			return nil
		},
	}
	cmd2 := &SlashCommand{
		Name: "dup",
		Execute: func(args string) tea.Cmd {
			callOrder = append(callOrder, "second")
			return nil
		},
	}
	reg.Register(cmd1)
	reg.Register(cmd2)

	got, ok := reg.Get("dup")
	if !ok {
		t.Fatal("expected command to be found")
	}
	got.Execute("")
	if len(callOrder) != 1 || callOrder[0] != "second" {
		t.Errorf("expected second registration's Execute to be called, got %v", callOrder)
	}

	// All should return only one entry for the duplicate.
	all := reg.All()
	count := 0
	for _, c := range all {
		if c.Name == "dup" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 'dup' command in All(), got %d", count)
	}
}

func TestCommandRegistry_MatchEmptyRegistry(t *testing.T) {
	reg := NewCommandRegistry()
	matches := reg.Match("")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for empty registry, got %d", len(matches))
	}
	matches = reg.Match("anything")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for empty registry, got %d", len(matches))
	}
}

func TestCommandRegistry_GetNonexistentFromPopulated(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "alpha"})
	reg.Register(&SlashCommand{Name: "beta"})
	reg.Register(&SlashCommand{Name: "gamma"})

	_, ok := reg.Get("delta")
	if ok {
		t.Error("expected false for nonexistent command in populated registry")
	}
}

func TestCommandRegistry_MatchSortOrder(t *testing.T) {
	reg := NewCommandRegistry()
	// Register in reverse alphabetical order.
	reg.Register(&SlashCommand{Name: "zoo"})
	reg.Register(&SlashCommand{Name: "zap"})
	reg.Register(&SlashCommand{Name: "zeal"})

	matches := reg.Match("z")
	if len(matches) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(matches))
	}
	// Should be sorted alphabetically.
	if matches[0].Name != "zap" || matches[1].Name != "zeal" || matches[2].Name != "zoo" {
		t.Errorf("expected alphabetical order [zap, zeal, zoo], got [%s, %s, %s]",
			matches[0].Name, matches[1].Name, matches[2].Name)
	}
}

// ---------------------------------------------------------------------------
// Alias management methods: SetAlias, GetAlias, RemoveAlias, AllAliases
// ---------------------------------------------------------------------------

func TestCommandRegistry_SetAlias(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "help"})

	// Set alias for existing command should succeed.
	if !reg.SetAlias("h", "help") {
		t.Error("expected SetAlias to succeed for existing target")
	}

	// Set alias for nonexistent command should fail.
	if reg.SetAlias("x", "nonexistent") {
		t.Error("expected SetAlias to fail for nonexistent target")
	}
}

func TestCommandRegistry_GetAlias(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "help"})
	reg.SetAlias("h", "help")

	if target := reg.GetAlias("h"); target != "help" {
		t.Errorf("GetAlias('h') = %q, want 'help'", target)
	}

	if target := reg.GetAlias("nonexistent"); target != "" {
		t.Errorf("GetAlias('nonexistent') = %q, want empty string", target)
	}
}

func TestCommandRegistry_RemoveAlias(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "help"})
	reg.SetAlias("h", "help")

	reg.RemoveAlias("h")
	if target := reg.GetAlias("h"); target != "" {
		t.Errorf("expected alias to be removed, got %q", target)
	}

	// Removing nonexistent alias should not panic.
	reg.RemoveAlias("nonexistent")
}

func TestCommandRegistry_AllAliases(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "help"})
	reg.Register(&SlashCommand{Name: "model"})

	// Empty initially.
	aliases := reg.AllAliases()
	if len(aliases) != 0 {
		t.Errorf("expected 0 aliases, got %d", len(aliases))
	}

	reg.SetAlias("h", "help")
	reg.SetAlias("m", "model")

	aliases = reg.AllAliases()
	if len(aliases) != 2 {
		t.Fatalf("expected 2 aliases, got %d", len(aliases))
	}
	if aliases["h"] != "help" {
		t.Errorf("aliases['h'] = %q, want 'help'", aliases["h"])
	}
	if aliases["m"] != "model" {
		t.Errorf("aliases['m'] = %q, want 'model'", aliases["m"])
	}
}

func TestCommandRegistry_AllAliases_ReturnsCopy(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "help"})
	reg.SetAlias("h", "help")

	aliases := reg.AllAliases()
	// Modifying the returned map should not affect the registry.
	aliases["injected"] = "evil"

	if reg.GetAlias("injected") != "" {
		t.Error("AllAliases should return a copy, not a reference to the internal map")
	}
}

func TestCommandRegistry_GetResolvesAlias(t *testing.T) {
	reg := NewCommandRegistry()
	cmd := &SlashCommand{Name: "help", Description: "help command"}
	reg.Register(cmd)
	reg.SetAlias("h", "help")

	got, ok := reg.Get("h")
	if !ok {
		t.Fatal("expected Get to resolve alias 'h'")
	}
	if got.Name != "help" {
		t.Errorf("expected resolved command 'help', got %q", got.Name)
	}
}

func TestCommandRegistry_SetAlias_Overwrite(t *testing.T) {
	reg := NewCommandRegistry()
	reg.Register(&SlashCommand{Name: "help"})
	reg.Register(&SlashCommand{Name: "model"})

	reg.SetAlias("x", "help")
	reg.SetAlias("x", "model")

	if target := reg.GetAlias("x"); target != "model" {
		t.Errorf("expected overwritten alias target 'model', got %q", target)
	}
}

func TestCommandRegistry_ManyCommands(t *testing.T) {
	reg := NewCommandRegistry()
	for i := 0; i < 100; i++ {
		name := "cmd" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		reg.Register(&SlashCommand{Name: name})
	}

	all := reg.All()
	// Map deduplication: 100 registrations, but some names may collide.
	if len(all) == 0 {
		t.Error("expected non-empty All()")
	}

	// Verify sorted.
	for i := 1; i < len(all); i++ {
		if all[i].Name < all[i-1].Name {
			t.Errorf("All() not sorted: %q before %q", all[i-1].Name, all[i].Name)
			break
		}
	}
}
