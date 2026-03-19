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

// CommandRegistry holds all registered slash commands and their aliases.
type CommandRegistry struct {
	commands map[string]*SlashCommand
	aliases  map[string]string // alias -> target command name
}

// NewCommandRegistry creates an empty CommandRegistry.
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{
		commands: make(map[string]*SlashCommand),
		aliases:  make(map[string]string),
	}
}

// Register adds a slash command to the registry. If a command with the same
// name already exists it is replaced.
func (r *CommandRegistry) Register(cmd *SlashCommand) {
	r.commands[cmd.Name] = cmd
}

// Get returns the command with the given name, resolving aliases if necessary.
// Returns (nil, false) if the command or alias target is not found.
func (r *CommandRegistry) Get(name string) (*SlashCommand, bool) {
	// Resolve alias if one exists
	if target, isAlias := r.aliases[name]; isAlias {
		name = target
	}
	cmd, ok := r.commands[name]
	return cmd, ok
}

// SetAlias creates a new command alias. The target must be a registered command.
func (r *CommandRegistry) SetAlias(alias string, target string) bool {
	// Verify target command exists
	if _, ok := r.commands[target]; !ok {
		return false
	}
	r.aliases[alias] = target
	return true
}

// GetAlias returns the target command for an alias, or empty string if not found.
func (r *CommandRegistry) GetAlias(alias string) string {
	return r.aliases[alias]
}

// RemoveAlias removes an alias.
func (r *CommandRegistry) RemoveAlias(alias string) {
	delete(r.aliases, alias)
}

// AllAliases returns all aliases as a map of alias -> target.
func (r *CommandRegistry) AllAliases() map[string]string {
	result := make(map[string]string)
	for k, v := range r.aliases {
		result[k] = v
	}
	return result
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
