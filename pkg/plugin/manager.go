package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/tools"
)

// Manager handles discovery, loading, initialization, and lifecycle management
// of all plugins. It bridges plugin-provided tools and commands into the host's
// registries.
type Manager struct {
	mu           sync.RWMutex
	plugins      []*PluginProcess
	toolRegistry *tools.Registry
	restartCfg   *RestartConfig   // if set, enables auto-restart for plugins
	heartbeatCfg *HeartbeatConfig // if set, enables periodic heartbeats

	heartbeatCancel context.CancelFunc // cancels the heartbeat goroutine
	heartbeatDone   chan struct{}      // closed when heartbeat goroutine exits
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
	return m.startAndRegisterWithManifest(name, path, nil)
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

		return m.startAndRegisterWithManifest(name, execPath, &manifest)
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

	return m.startAndRegisterWithManifest(defaultName, execPath, nil)
}

// startAndRegisterWithManifest spawns the plugin process, applies manifest
// configuration (timeouts, memory limits), and stores it for initialization.
func (m *Manager) startAndRegisterWithManifest(name, execPath string, manifest *Manifest) error {
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

	if manifest != nil {
		proc.SetTimeouts(TimeoutConfigFromManifest(*manifest))
		if manifest.MemoryLimitMB > 0 {
			proc.SetMemoryLimit(manifest.MemoryLimitMB)
			proc.applyMemoryLimit()
		}
	}

	m.mu.Lock()
	m.plugins = append(m.plugins, proc)
	m.mu.Unlock()
	return nil
}

// Initialize sends the initialize message to all loaded plugins and registers
// their tools and commands. Plugins that fail initialization or registration
// are stopped and removed.
func (m *Manager) Initialize(cfg PluginConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

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
		if stopErr := p.Stop(); stopErr != nil {
			log.Printf("plugin %s: cleanup: failed to stop after init failure: %v", p.name, stopErr)
		}
		return err
	}

	// Ensure the process is stopped if anything after Initialize() fails.
	defer func() {
		if r := recover(); r != nil {
			if stopErr := p.Stop(); stopErr != nil {
				log.Printf("plugin %s: cleanup: failed to stop after panic: %v", p.name, stopErr)
			}
			retErr = fmt.Errorf("plugin %s: registration panicked: %v", p.name, r)
		} else if retErr != nil {
			if stopErr := p.Stop(); stopErr != nil {
				log.Printf("plugin %s: cleanup: failed to stop after error: %v", p.name, stopErr)
			}
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

// SetHeartbeatConfig sets the heartbeat configuration for the manager.
// If cfg is nil, heartbeats are disabled.
func (m *Manager) SetHeartbeatConfig(cfg *HeartbeatConfig) {
	m.heartbeatCfg = cfg
}

// StartHeartbeats begins periodic heartbeat checks on all alive plugins.
// Call this after Initialize. The goroutine runs until ctx is cancelled
// or Shutdown is called.
func (m *Manager) StartHeartbeats(ctx context.Context) {
	if m.heartbeatCfg == nil {
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	m.heartbeatCancel = cancel
	m.heartbeatDone = make(chan struct{})

	cfg := *m.heartbeatCfg

	go func() {
		defer close(m.heartbeatDone)

		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.heartbeatAll(cfg.Timeout)
			}
		}
	}()
}

// heartbeatAll sends a heartbeat to each alive plugin and logs unhealthy ones.
func (m *Manager) heartbeatAll(timeout time.Duration) {
	m.mu.RLock()
	plugins := make([]*PluginProcess, len(m.plugins))
	copy(plugins, m.plugins)
	m.mu.RUnlock()

	for _, p := range plugins {
		if !p.Alive() {
			continue
		}

		status, err := p.Heartbeat(timeout)
		if err != nil {
			log.Printf("plugin %s: unhealthy: %v", p.name, err)
			continue
		}

		if status != nil {
			log.Printf("plugin %s: healthy (mem=%d goroutines=%d uptime=%ds)",
				p.name, status.MemoryBytes, status.Goroutines, status.UptimeSecs)
		}
	}
}

// PluginHealthy returns true if the named plugin's last heartbeat succeeded.
// Returns false if the plugin is not found.
func (m *Manager) PluginHealthy(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, p := range m.plugins {
		if p.name == name {
			return p.Healthy()
		}
	}
	return false
}

// Shutdown sends a shutdown message to all plugins and waits for them to exit.
// If heartbeats are running, they are stopped first.
func (m *Manager) Shutdown() error {
	// Stop heartbeat goroutine if running.
	if m.heartbeatCancel != nil {
		m.heartbeatCancel()
		<-m.heartbeatDone
		m.heartbeatCancel = nil
	}

	m.mu.Lock()
	plugins := m.plugins
	m.plugins = nil
	m.mu.Unlock()

	var firstErr error
	for _, p := range plugins {
		if err := p.Stop(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ForwardEvent forwards an agent lifecycle event to all plugins that are alive.
func (m *Manager) ForwardEvent(event agent.AgentEvent) {
	m.mu.RLock()
	plugins := make([]*PluginProcess, len(m.plugins))
	copy(plugins, m.plugins)
	m.mu.RUnlock()

	payload := agentEventToPayload(event)
	for _, p := range plugins {
		if p.Alive() {
			if err := p.SendEvent(payload); err != nil {
				log.Printf("plugin %s: cleanup: failed to forward event: %v", p.name, err)
			}
		}
	}
}

// Plugins returns the list of active plugin processes.
func (m *Manager) Plugins() []*PluginProcess {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*PluginProcess, len(m.plugins))
	copy(result, m.plugins)
	return result
}

// PluginTools returns all tools provided by all loaded plugins.
func (m *Manager) PluginTools() []ToolDef {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []ToolDef
	for _, p := range m.plugins {
		result = append(result, p.tools...)
	}
	return result
}

// PluginCommands returns all commands provided by all loaded plugins.
func (m *Manager) PluginCommands() []CommandDef {
	m.mu.RLock()
	defer m.mu.RUnlock()

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
