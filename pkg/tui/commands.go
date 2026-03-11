package tui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// SlashCommand defines a command invoked by typing /name in the editor.
type SlashCommand struct {
	Name        string
	Description string
	Execute     func(args string) tea.Cmd
}

// CommandRegistry holds all registered slash commands.
type CommandRegistry struct {
	commands map[string]*SlashCommand
}

// NewCommandRegistry creates an empty CommandRegistry.
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{
		commands: make(map[string]*SlashCommand),
	}
}

// Register adds a slash command to the registry. If a command with the same
// name already exists it is replaced.
func (r *CommandRegistry) Register(cmd *SlashCommand) {
	r.commands[cmd.Name] = cmd
}

// Get returns the command with the given name, or (nil, false) if not found.
func (r *CommandRegistry) Get(name string) (*SlashCommand, bool) {
	cmd, ok := r.commands[name]
	return cmd, ok
}

// All returns every registered command sorted alphabetically by name.
func (r *CommandRegistry) All() []*SlashCommand {
	cmds := make([]*SlashCommand, 0, len(r.commands))
	for _, cmd := range r.commands {
		cmds = append(cmds, cmd)
	}
	sort.Slice(cmds, func(i, j int) bool {
		return cmds[i].Name < cmds[j].Name
	})
	return cmds
}

// Match returns all commands whose name starts with the given prefix, sorted
// alphabetically. This powers the autocomplete hint.
func (r *CommandRegistry) Match(prefix string) []*SlashCommand {
	var matches []*SlashCommand
	for _, cmd := range r.commands {
		if strings.HasPrefix(cmd.Name, prefix) {
			matches = append(matches, cmd)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Name < matches[j].Name
	})
	return matches
}
