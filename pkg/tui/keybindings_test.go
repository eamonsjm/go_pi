package tui

import (
	"testing"
)

func TestKeybindingConfig_ActionFor(t *testing.T) {
	kc := LoadKeybindings()

	// Default binding: ctrl+t -> toggle_thinking.
	action, ok := kc.ActionFor("ctrl+t")
	if !ok {
		t.Fatal("expected ctrl+t to be bound")
	}
	if action != ActionToggleThinking {
		t.Errorf("expected ActionToggleThinking, got %q", action)
	}
}

func TestKeybindingConfig_ActionFor_Unbound(t *testing.T) {
	kc := LoadKeybindings()

	_, ok := kc.ActionFor("ctrl+shift+alt+f99")
	if ok {
		t.Error("expected unbound key to return false")
	}
}

func TestKeybindingConfig_KeyFor(t *testing.T) {
	kc := LoadKeybindings()

	key := kc.KeyFor(ActionToggleThinking)
	if key != "ctrl+t" {
		t.Errorf("expected 'ctrl+t', got %q", key)
	}
}

func TestKeybindingConfig_KeyFor_UnknownAction(t *testing.T) {
	kc := LoadKeybindings()

	key := kc.KeyFor(Action("nonexistent_action"))
	if key != "" {
		t.Errorf("expected empty string for unknown action, got %q", key)
	}
}

func TestKeybindingConfig_AllBindings(t *testing.T) {
	kc := LoadKeybindings()
	bindings := kc.AllBindings()

	// Should have at least as many bindings as default bindings.
	defaults := getDefaultBindings()
	if len(bindings) < len(defaults) {
		t.Errorf("expected at least %d bindings, got %d", len(defaults), len(bindings))
	}

	// Verify sorted by action name.
	for i := 1; i < len(bindings); i++ {
		if bindings[i].Action < bindings[i-1].Action {
			t.Errorf("bindings not sorted: %q < %q at index %d", bindings[i].Action, bindings[i-1].Action, i)
		}
	}

	// Verify all default actions are present.
	actionSet := make(map[Action]bool)
	for _, b := range bindings {
		actionSet[b.Action] = true
	}
	for _, d := range defaults {
		if !actionSet[d.Action] {
			t.Errorf("default action %q missing from AllBindings", d.Action)
		}
	}
}

func TestKeybindingConfig_DefaultBindings_Coverage(t *testing.T) {
	kc := LoadKeybindings()

	// Verify all default bindings are resolvable in both directions.
	for _, b := range getDefaultBindings() {
		action, ok := kc.ActionFor(b.Key)
		if !ok {
			t.Errorf("default key %q not found in keyToAction", b.Key)
			continue
		}
		if action != b.Action {
			t.Errorf("key %q maps to %q, expected %q", b.Key, action, b.Action)
		}

		key := kc.KeyFor(b.Action)
		if key != b.Key {
			t.Errorf("action %q maps to key %q, expected %q", b.Action, key, b.Key)
		}
	}
}

func TestActionDescription(t *testing.T) {
	cases := []struct {
		action Action
		empty  bool
	}{
		{ActionToggleThinking, false},
		{ActionToggleToolResult, false},
		{ActionCycleThinking, false},
		{ActionCycleModelForward, false},
		{ActionCycleModelBackward, false},
		{ActionSuspend, false},
		{ActionToggleMouse, false},
		{ActionHistorySearch, false},
		{Action("unknown"), true},
	}
	for _, tc := range cases {
		desc := actionDescription(tc.action)
		if tc.empty && desc != "" {
			t.Errorf("actionDescription(%q) = %q, want empty", tc.action, desc)
		}
		if !tc.empty && desc == "" {
			t.Errorf("actionDescription(%q) returned empty, want non-empty", tc.action)
		}
	}
}

func TestActionDescription_ContentCheck(t *testing.T) {
	// Spot-check a few descriptions for sensible content.
	desc := actionDescription(ActionCycleThinking)
	if desc == "" {
		t.Fatal("expected non-empty description")
	}
	// It should mention thinking.
	found := false
	for _, word := range []string{"thinking", "Thinking"} {
		if containsWord(desc, word) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected description to mention thinking, got %q", desc)
	}
}

func containsWord(s, word string) bool {
	for i := 0; i <= len(s)-len(word); i++ {
		if s[i:i+len(word)] == word {
			return true
		}
	}
	return false
}
