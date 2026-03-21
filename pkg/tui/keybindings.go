package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Action identifies a bindable TUI action.
type Action string

const (
	// App-level actions.
	ActionToggleThinking     Action = "toggle_thinking"
	ActionToggleToolResult   Action = "toggle_tool_result"
	ActionCycleThinking      Action = "cycle_thinking"
	ActionCycleModelForward  Action = "cycle_model_forward"
	ActionCycleModelBackward Action = "cycle_model_backward"
	ActionSuspend            Action = "suspend"
	ActionToggleMouse        Action = "toggle_mouse"
	ActionHistorySearch      Action = "history_search"
)

// actionDescriptions provides human-readable descriptions for /hotkeys display.
var actionDescriptions = map[Action]string{
	ActionToggleThinking:     "Toggle thinking block expand/collapse",
	ActionToggleToolResult:   "Toggle tool result expand/collapse",
	ActionCycleThinking:      "Cycle thinking level (off/low/medium/high)",
	ActionCycleModelForward:  "Cycle to next model",
	ActionCycleModelBackward: "Cycle to previous model",
	ActionSuspend:            "Suspend (Ctrl+Z)",
	ActionToggleMouse:        "Toggle mouse capture (for scroll vs text selection)",
	ActionHistorySearch:      "Reverse-search prompt history",
}

// Binding represents a key-to-action mapping.
type Binding struct {
	Key    string `json:"key"`
	Action Action `json:"action"`
}

// KeybindingConfig holds the resolved keybinding map.
type KeybindingConfig struct {
	actionToKey map[Action]string
	keyToAction map[string]Action
}

// defaultBindings are the built-in keybindings for app-level actions.
var defaultBindings = []Binding{
	{Key: "ctrl+t", Action: ActionToggleThinking},
	{Key: "alt+r", Action: ActionToggleToolResult},
	{Key: "ctrl+r", Action: ActionHistorySearch},
	{Key: "shift+tab", Action: ActionCycleThinking},
	{Key: "ctrl+p", Action: ActionCycleModelForward},
	{Key: "alt+p", Action: ActionCycleModelBackward},
	{Key: "ctrl+z", Action: ActionSuspend},
	{Key: "alt+m", Action: ActionToggleMouse},
}

// LoadKeybindings loads keybinding configuration from ~/.gi/keybindings.json,
// merging user overrides onto the defaults. Missing file is not an error.
func LoadKeybindings() *KeybindingConfig {
	kc := &KeybindingConfig{
		actionToKey: make(map[Action]string),
		keyToAction: make(map[string]Action),
	}

	// Apply defaults.
	for _, b := range defaultBindings {
		kc.actionToKey[b.Action] = b.Key
	}

	// Load user overrides.
	home, err := os.UserHomeDir()
	if err == nil {
		path := filepath.Join(home, ".gi", "keybindings.json")
		if data, err := os.ReadFile(path); err == nil {
			var overrides []Binding
			if json.Unmarshal(data, &overrides) == nil {
				for _, b := range overrides {
					if b.Action != "" && b.Key != "" {
						kc.actionToKey[b.Action] = b.Key
					}
				}
			}
		}
	}

	// Build reverse map.
	for action, key := range kc.actionToKey {
		kc.keyToAction[key] = action
	}

	return kc
}

// ActionFor returns the action bound to the given key string, if any.
func (kc *KeybindingConfig) ActionFor(key string) (Action, bool) {
	action, ok := kc.keyToAction[key]
	return action, ok
}

// KeyFor returns the key string bound to the given action.
func (kc *KeybindingConfig) KeyFor(action Action) string {
	return kc.actionToKey[action]
}

// AllBindings returns all bindings sorted by action name.
func (kc *KeybindingConfig) AllBindings() []Binding {
	bindings := make([]Binding, 0, len(kc.actionToKey))
	for action, key := range kc.actionToKey {
		bindings = append(bindings, Binding{Key: key, Action: action})
	}
	sort.Slice(bindings, func(i, j int) bool {
		return bindings[i].Action < bindings[j].Action
	})
	return bindings
}
