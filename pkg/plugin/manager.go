package plugin

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/tools"
)

// Manager handles discovery, loading, initialization, and lifecycle management
// of all plugins. It bridges plugin-provided tools and commands into the host's
// registries.
type Manager struct {
	plugins      []*PluginProcess
	toolRegistry *tools.Registry
	restartCfg   *RestartConfig // if set, enables auto-restart for plugins
}

// NewManager creates a new plugin manager. The tool registry is used to register
// plugin-provided tools alongside built-in tools.
func NewManager(toolRegistry *tools.Registry) *Manager {
	cfg := DefaultRestartConfig()
	return &Manager{
		toolRegistry: toolRegistry,
		restartCfg:   &cfg,
	}
}

// SetRestartConfig sets the restart configuration for plugins. If cfg is nil,
// auto-restart is disabled.
func (m *Manager) SetRestartConfig(cfg *RestartConfig) {
	m.restartCfg = cfg
}

// Discover scans the given directories for plugins. Each subdirectory is
// examined for either a plugin.json manifest or a same-named executable.
// Errors for individual plugins are logged but do not prevent other plugins
// from loading.
func (m *Manager) Discover(dirs []string) error {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// Directory doesn't exist or isn't readable -- skip silently.
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			pluginDir := filepath.Join(dir, entry.Name())
			if err := m.loadFromDir(pluginDir, entry.Name()); err != nil {
				log.Printf("plugin: failed to load %s: %v", entry.Name(), err)
			}
		}
	}
	return nil
}

// LoadPlugin loads a single plugin from the given path. The path can point to
// either a directory (containing a manifest or executable) or directly to an
// executable file.
func (m *Manager) LoadPlugin(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("plugin path %s: %w", path, err)
	}

	if info.IsDir() {
		return m.loadFromDir(path, filepath.Base(path))
	}

	// Direct executable path.
	name := filepath.Base(path)
	return m.startAndRegister(name, path)
}

// loadFromDir loads a plugin from a directory, checking for a manifest first.
func (m *Manager) loadFromDir(dir, defaultName string) error {
	manifestPath := filepath.Join(dir, "plugin.json")
	data, err := os.ReadFile(manifestPath)
	if err == nil {
		// Manifest found.
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return fmt.Errorf("parsing manifest: %w", err)
		}

		name := manifest.Name
		if name == "" {
			name = defaultName
		}

		execPath := manifest.Executable
		if !filepath.IsAbs(execPath) {
			execPath = filepath.Join(dir, execPath)
		}

		return m.startAndRegister(name, execPath)
	}

	// No manifest -- look for executable matching directory name.
	execPath := filepath.Join(dir, defaultName)
	info, err := os.Stat(execPath)
	if err != nil {
		return fmt.Errorf("no manifest or executable found in %s", dir)
	}
	if info.Mode()&0111 == 0 {
		return fmt.Errorf("%s is not executable", execPath)
	}

	return m.startAndRegister(defaultName, execPath)
}

// startAndRegister spawns the plugin process and stores it for later initialization.
func (m *Manager) startAndRegister(name, execPath string) error {
	// Verify the executable exists and is executable.
	info, err := os.Stat(execPath)
	if err != nil {
		return fmt.Errorf("plugin executable %s: %w", execPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("plugin executable %s is a directory", execPath)
	}

	proc, err := startPlugin(name, execPath)
	if err != nil {
		return err
	}

	m.plugins = append(m.plugins, proc)
	return nil
}

// Initialize sends the initialize message to all loaded plugins and registers
// their tools and commands. Plugins that fail initialization or registration
// are stopped and removed.
func (m *Manager) Initialize(cfg PluginConfig) error {
	var alive []*PluginProcess

	for _, p := range m.plugins {
		if err := m.initializePlugin(p, cfg); err != nil {
			log.Printf("plugin: %s initialization failed: %v", p.name, err)
			continue
		}

		if m.restartCfg != nil {
			p.EnableAutoRestart(*m.restartCfg)
		}

		alive = append(alive, p)
		log.Printf("plugin: %s loaded (%d tools, %d commands)", p.name, len(p.tools), len(p.commands))
	}

	m.plugins = alive
	return nil
}

// initializePlugin initializes a single plugin and registers its tools.
// If any step after successful Initialize() fails (including panics during
// tool registration), the plugin process is stopped to prevent leaks.
func (m *Manager) initializePlugin(p *PluginProcess, cfg PluginConfig) (retErr error) {
	if err := p.Initialize(cfg); err != nil {
		_ = p.Stop()
		return err
	}

	// Ensure the process is stopped if anything after Initialize() fails.
	defer func() {
		if r := recover(); r != nil {
			_ = p.Stop()
			retErr = fmt.Errorf("plugin %s: registration panicked: %v", p.name, r)
		} else if retErr != nil {
			_ = p.Stop()
		}
	}()

	// Register plugin tools.
	for _, td := range p.tools {
		if _, exists := m.toolRegistry.Get(td.Name); exists {
			log.Printf("plugin: %s tool %q conflicts with existing tool, skipping", p.name, td.Name)
			continue
		}
		m.toolRegistry.Register(&PluginTool{
			def:     td,
			process: p,
		})
	}

	return nil
}

// Shutdown sends a shutdown message to all plugins and waits for them to exit.
func (m *Manager) Shutdown() error {
	var firstErr error
	for _, p := range m.plugins {
		if err := p.Stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	m.plugins = nil
	return firstErr
}

// ForwardEvent forwards an agent lifecycle event to all plugins that are alive.
func (m *Manager) ForwardEvent(event agent.AgentEvent) {
	payload := agentEventToPayload(event)
	for _, p := range m.plugins {
		if p.Alive() {
			_ = p.SendEvent(payload)
		}
	}
}

// Plugins returns the list of active plugin processes.
func (m *Manager) Plugins() []*PluginProcess {
	return m.plugins
}

// PluginTools returns all tools provided by all loaded plugins.
func (m *Manager) PluginTools() []ToolDef {
	var result []ToolDef
	for _, p := range m.plugins {
		result = append(result, p.tools...)
	}
	return result
}

// PluginCommands returns all commands provided by all loaded plugins.
func (m *Manager) PluginCommands() []CommandDef {
	var result []CommandDef
	for _, p := range m.plugins {
		result = append(result, p.commands...)
	}
	return result
}

// agentEventToPayload converts an agent.AgentEvent to the plugin EventPayload format.
func agentEventToPayload(event agent.AgentEvent) EventPayload {
	payload := EventPayload{
		Type: string(event.Type),
	}

	switch event.Type {
	case agent.EventToolExecStart, agent.EventToolExecEnd:
		payload.ToolName = event.ToolName
		payload.ToolCallID = event.ToolCallID
		payload.ToolArgs = event.ToolArgs
		payload.ToolResult = event.ToolResult
		payload.ToolError = event.ToolError
	case agent.EventAgentError:
		if event.Error != nil {
			payload.Error = event.Error.Error()
		}
	}

	return payload
}
