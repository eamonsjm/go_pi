package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/ejm/go_pi/pkg/config"
	"github.com/ejm/go_pi/pkg/mcp/transport"
	"github.com/ejm/go_pi/pkg/skill"
	"github.com/ejm/go_pi/pkg/tools"
)

// Sentinel errors for MCP server state.
var (
	ErrServerCrashed    = errors.New("MCP server crashed")
	ErrServerRestarting = errors.New("MCP server is restarting")
)

// maxPendingMessages is the cap on queued system messages. When exceeded,
// overflow messages are coalesced into a single summary.
const maxPendingMessages = 10

// Manager manages all configured MCP servers, analogous to plugin.Manager.
//
// Lock ordering (innermost last):
//  1. Manager.mu
//  2. tools.Registry.mu (via ReplaceByPrefix etc.)
//  3. skill.Registry.mu (via ReplaceByPrefix etc.)
//
// Never acquire Manager.mu while holding a registry lock.
type Manager struct {
	mu sync.Mutex

	servers    map[string]*Server
	serverList []string // ordered server names (config order)

	toolRegistry  *tools.Registry
	skillRegistry *skill.Registry

	// System messages queued for the agent loop.
	pendingSystemMessages []string

	// Configuration.
	workingDir  string
	configDir   string
	projectPath string

	// Version info for initialize handshake.
	clientName    string
	clientVersion string

	// Sampling support.
	samplingHandler SamplingHandler
	confirmSampling ConfirmSamplingFunc
}

// ManagerConfig holds the parameters for creating a Manager.
type ManagerConfig struct {
	ToolRegistry    *tools.Registry
	SkillRegistry   *skill.Registry
	WorkingDir      string
	ConfigDir       string // ~/.gi
	ProjectPath     string // project root for approval cache keying
	ClientName      string // e.g., "gi"
	ClientVersion   string
	SamplingHandler SamplingHandler
	ConfirmSampling ConfirmSamplingFunc
}

// NewManager creates a new Manager.
func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{
		servers:         make(map[string]*Server),
		toolRegistry:    cfg.ToolRegistry,
		skillRegistry:   cfg.SkillRegistry,
		workingDir:      cfg.WorkingDir,
		configDir:       cfg.ConfigDir,
		projectPath:     cfg.ProjectPath,
		clientName:      cfg.ClientName,
		clientVersion:   cfg.ClientVersion,
		samplingHandler: cfg.SamplingHandler,
		confirmSampling: cfg.ConfirmSampling,
	}
}

// StartAll initializes all MCP servers from the given config. Servers are
// started sequentially (each server's init sequence is inherently sequential).
// Project-level servers that aren't in the approval cache are skipped with a
// warning (interactive approval is handled by the caller before this).
func (m *Manager) StartAll(ctx context.Context, appCfg *config.Config) error {
	if len(appCfg.MCPServers) == 0 {
		return nil
	}

	// Build allowlist for project-level env var interpolation.
	allowlist := make(map[string]bool, len(appCfg.AllowProjectEnvVars))
	for _, v := range appCfg.AllowProjectEnvVars {
		allowlist[v] = true
	}

	for name, serverCfg := range appCfg.MCPServers {
		if serverCfg == nil {
			continue
		}
		// Project-level servers use the env var allowlist; global servers
		// get unrestricted interpolation (nil allowlist).
		var envAllowlist map[string]bool
		if serverCfg.Origin == "project" {
			envAllowlist = allowlist
		}
		expanded := expandServerConfig(serverCfg, envAllowlist)
		if err := m.startServer(ctx, name, expanded); err != nil {
			log.Printf("mcp: failed to start server %q: %v", name, err)
			continue
		}
		m.serverList = append(m.serverList, name)
	}
	return nil
}

// startServer initializes a single MCP server connection.
func (m *Manager) startServer(ctx context.Context, name string, cfg *config.MCPServerConfig) error {
	// Create transport.
	var t transport.Transport
	if cfg.URL != "" {
		t = transport.NewStreamableHTTP(cfg.URL, cfg.Headers)
	} else if cfg.Command != "" {
		var envSlice []string
		for k, v := range cfg.Env {
			envSlice = append(envSlice, k+"="+v)
		}
		t = transport.NewStdio(cfg.Command, cfg.Args, envSlice)
	} else {
		return fmt.Errorf("server %q has neither command nor url", name)
	}

	// Connect transport.
	if err := t.Connect(ctx); err != nil {
		return fmt.Errorf("connecting transport for %q: %w", name, err)
	}

	// Create server.
	server := newServer(name, cfg, t, m)

	// Initialize handshake.
	if err := server.initialize(ctx); err != nil {
		if closeErr := t.Close(); closeErr != nil {
			log.Printf("mcp: cleanup: failed to close transport for %q: %v", name, closeErr)
		}
		return fmt.Errorf("initializing %q: %w", name, err)
	}

	// Discover and register tools.
	if err := server.discoverAndRegisterTools(ctx); err != nil {
		log.Printf("mcp: failed to discover tools for %q: %v", name, err)
		// Non-fatal: server is connected, tools may appear later via list_changed.
	}

	// Discover and register resources (if server advertises resource capability).
	caps := server.client.ServerCapabilities()
	if caps.Resources != nil {
		if caps.Resources.Subscribe {
			server.subscriptions = newSubscriptionManager(server.client)
		}
		if err := server.discoverAndRegisterResources(ctx); err != nil {
			log.Printf("mcp: failed to discover resources for %q: %v", name, err)
			// Non-fatal: resources may appear later via list_changed.
		}
	}

	// Discover and register prompts (if server supports them).
	if err := server.discoverAndRegisterPrompts(ctx); err != nil {
		log.Printf("mcp: failed to discover prompts for %q: %v", name, err)
		// Non-fatal: prompts may appear later via list_changed.
	}

	m.mu.Lock()
	m.servers[name] = server
	m.mu.Unlock()

	return nil
}

// Shutdown gracefully stops all MCP servers.
func (m *Manager) Shutdown(ctx context.Context) {
	m.mu.Lock()
	servers := make(map[string]*Server, len(m.servers))
	for k, v := range m.servers {
		servers[k] = v
	}
	m.mu.Unlock()

	for name, server := range servers {
		if err := server.close(); err != nil {
			log.Printf("mcp: error shutting down server %q: %v", name, err)
		}
	}

	m.mu.Lock()
	m.servers = make(map[string]*Server)
	m.serverList = nil
	m.pendingSystemMessages = nil
	m.mu.Unlock()
}

// injectSystemMessage queues a system message for the next agent loop turn.
func (m *Manager) injectSystemMessage(msg string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pendingSystemMessages) >= maxPendingMessages {
		// Coalesce overflow into the last slot.
		if len(m.pendingSystemMessages) == maxPendingMessages {
			m.pendingSystemMessages = append(m.pendingSystemMessages,
				"[additional MCP notifications coalesced]")
		}
		return
	}
	m.pendingSystemMessages = append(m.pendingSystemMessages, msg)
}

// DrainSystemMessages returns and clears pending system messages.
// Called by the agent loop at the start of each turn.
func (m *Manager) DrainSystemMessages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := m.pendingSystemMessages
	m.pendingSystemMessages = nil
	return msgs
}

// ServerInstructions returns sanitized server instructions for LLM system
// prompt injection. Instructions are wrapped in sandbox tags, length-capped,
// and have angle brackets escaped to prevent prompt injection.
func (m *Manager) ServerInstructions() string {
	m.mu.Lock()
	servers := make([]*Server, 0, len(m.serverList))
	for _, name := range m.serverList {
		if s, ok := m.servers[name]; ok {
			servers = append(servers, s)
		}
	}
	m.mu.Unlock()

	var b strings.Builder
	for _, s := range servers {
		if s.instructions == "" {
			continue
		}
		if s.config.Instructions == "ignore" {
			continue
		}

		// Sanitize: length cap, tag stripping, sandbox wrapping.
		instr := s.instructions
		if len(instr) > 2000 {
			instr = instr[:2000] + " [truncated]"
		}
		// Strip angle brackets to prevent closing system tags.
		instr = strings.ReplaceAll(instr, "<", "&lt;")
		instr = strings.ReplaceAll(instr, ">", "&gt;")

		fmt.Fprintf(&b, "\n<mcp-server-instructions server=%q>\n%s\n</mcp-server-instructions>\n",
			s.name, instr)
	}
	return b.String()
}

// handleRootsList responds to a roots/list request from a server.
func (m *Manager) handleRootsList() map[string]any {
	return map[string]any{
		"roots": []map[string]any{
			{
				"uri":  "file://" + m.workingDir,
				"name": m.workingDir,
			},
		},
	}
}

// Server returns a server by name, or nil if not found.
func (m *Manager) Server(name string) *Server {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.servers[name]
}

// ServerNames returns the ordered list of server names.
func (m *Manager) ServerNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.serverList))
	copy(out, m.serverList)
	return out
}

// --- Server ---

// Server manages a single MCP server connection.
type Server struct {
	name         string
	config       *config.MCPServerConfig
	client       *Client
	transport    transport.Transport
	manager      *Manager
	instructions string // from initialize response

	mu           sync.Mutex
	closed       bool
	resourceTool tools.Tool             // current read_resource tool, if any
	subscriptions *subscriptionManager  // resource subscription TTL manager
}

// newServer creates a new Server. The notification handler is wired up
// to dispatch to the manager's handleListChanged, and the request handler
// dispatches server-initiated requests (sampling, roots).
func newServer(name string, cfg *config.MCPServerConfig, t transport.Transport, mgr *Manager) *Server {
	s := &Server{
		name:      name,
		config:    cfg,
		transport: t,
		manager:   mgr,
	}
	client := NewClient(t, s.handleNotification)
	client.onRequest = s.handleRequest
	s.client = client
	return s
}

// ServerName implements ToolCaller.
func (s *Server) ServerName() string { return s.name }

// CallTool implements ToolCaller — sends tools/call via the JSON-RPC client.
func (s *Server) CallTool(ctx context.Context, name string, params map[string]any) (*ToolResult, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrServerCrashed
	}
	s.mu.Unlock()

	return s.client.CallToolRaw(ctx, name, params)
}

// initialize performs the MCP initialize handshake.
func (s *Server) initialize(ctx context.Context) error {
	caps := ClientCapabilities{
		Roots: &RootsCapability{ListChanged: true},
	}
	if s.config.Sampling != nil && s.config.Sampling.Enabled {
		caps.Sampling = &SamplingCapability{}
	}

	result, err := s.client.Initialize(ctx,
		s.manager.clientName,
		s.manager.clientVersion,
		caps,
	)
	if err != nil {
		return err
	}

	s.instructions = result.Instructions

	// For Streamable HTTP, store negotiated version on the transport.
	if httpT, ok := s.transport.(*transport.StreamableHTTP); ok {
		httpT.SetNegotiatedVersion(result.ProtocolVersion)
	}

	return nil
}

// discoverAndRegisterTools discovers tools and registers them in the registry.
func (s *Server) discoverAndRegisterTools(ctx context.Context) error {
	discovered, err := DiscoverTools(ctx, s, func(ctx context.Context, cursor string) (*ToolsListPage, error) {
		return s.client.ListTools(ctx, cursor)
	})
	if err != nil {
		return err
	}

	prefix := "mcp__" + s.name + "__"
	s.manager.toolRegistry.ReplaceByPrefix(prefix, discovered)

	log.Printf("mcp: server %q: registered %d tools", s.name, len(discovered))
	return nil
}

// handleNotification dispatches notifications from the MCP server.
func (s *Server) handleNotification(method string, params json.RawMessage) {
	switch method {
	case "notifications/tools/list_changed":
		s.handleToolsListChanged()
	case "notifications/resources/list_changed":
		s.handleResourcesListChanged()
	case "notifications/resources/updated":
		s.handleResourcesUpdated(params)
	case "notifications/prompts/list_changed":
		s.handlePromptsListChanged()
	case "notifications/message":
		s.handleLogMessage(params)
	case "notifications/progress":
		log.Printf("mcp: server %q: progress notification (deferred)", s.name)
	default:
		log.Printf("mcp: server %q: unknown notification %q", s.name, method)
	}
}

// handleRequest dispatches server-initiated requests (method + id).
func (s *Server) handleRequest(method string, id json.RawMessage, params json.RawMessage) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch method {
	case "sampling/createMessage":
		s.handleSamplingRequest(ctx, id, params)
	case "roots/list":
		s.respondResult(ctx, id, s.manager.handleRootsList())
	default:
		s.respondError(ctx, id, ErrCodeMethodNotFound,
			fmt.Sprintf("unsupported server request: %q", method))
	}
}

// handleToolsListChanged re-discovers tools and updates the registry.
func (s *Server) handleToolsListChanged() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prefix := "mcp__" + s.name + "__"
	oldTools := s.manager.toolRegistry.AllWithPrefix(prefix)

	newTools, err := DiscoverTools(ctx, s, func(ctx context.Context, cursor string) (*ToolsListPage, error) {
		return s.client.ListTools(ctx, cursor)
	})
	if err != nil {
		log.Printf("mcp: failed to re-discover tools for %q: %v", s.name, err)
		return
	}

	s.manager.toolRegistry.ReplaceByPrefix(prefix, newTools)

	// Re-register resource tool (ReplaceByPrefix removed it since it shares the prefix).
	s.mu.Lock()
	rt := s.resourceTool
	s.mu.Unlock()
	if rt != nil {
		s.manager.toolRegistry.Register(rt)
	}

	added, removed := diffToolCount(oldTools, newTools)
	s.manager.injectSystemMessage(fmt.Sprintf(
		"[MCP server %q tools updated — %d added, %d removed, %d total]",
		s.name, added, removed, len(newTools)))
}

// handlePromptsListChanged re-discovers prompts and updates the skill registry.
func (s *Server) handlePromptsListChanged() {
	if s.manager.skillRegistry == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prefix := "mcp__" + s.name + "__"
	oldSkills := s.manager.skillRegistry.AllWithPrefix(prefix)

	newSkills, err := DiscoverPrompts(ctx, s.name, func(ctx context.Context, cursor string) (*PromptsListPage, error) {
		return s.client.ListPrompts(ctx, cursor)
	}, s)
	if err != nil {
		log.Printf("mcp: failed to re-discover prompts for %q: %v", s.name, err)
		return
	}

	s.manager.skillRegistry.ReplaceByPrefix(prefix, newSkills)

	added, removed := diffSkillCount(oldSkills, newSkills)
	s.manager.injectSystemMessage(fmt.Sprintf(
		"[MCP server %q prompts updated — %d added, %d removed]",
		s.name, added, removed))
}

// handleLogMessage forwards a server log notification to the Go logger.
func (s *Server) handleLogMessage(params json.RawMessage) {
	var msg struct {
		Level  string `json:"level"`
		Logger string `json:"logger"`
		Data   any    `json:"data"`
	}
	if err := json.Unmarshal(params, &msg); err != nil {
		log.Printf("mcp: server %q: malformed log message", s.name)
		return
	}
	log.Printf("[mcp:%s] %s: %v", s.name, msg.Level, msg.Data)
}

// close shuts down the server connection.
func (s *Server) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	// Close resource subscriptions (sends unsubscribe RPCs before client shuts down).
	if s.subscriptions != nil {
		s.subscriptions.close()
	}

	// Remove tools from registry (including resource tool).
	prefix := "mcp__" + s.name + "__"
	s.manager.toolRegistry.ReplaceByPrefix(prefix, nil)

	// Remove prompts from skill registry.
	if s.manager.skillRegistry != nil {
		s.manager.skillRegistry.ReplaceByPrefix(prefix, nil)
	}

	return s.client.Close()
}

// GetAnnotations returns tool annotations for a given server+tool name.
// Used by the permission hook to check readOnlyHint etc.
func (m *Manager) GetAnnotations(serverName, toolName string) *ToolAnnotations {
	fullName := buildToolName(serverName, toolName)
	t, ok := m.toolRegistry.Get(fullName)
	if !ok {
		return nil
	}
	if mcpTool, ok := t.(*Tool); ok {
		return mcpTool.Annotations()
	}
	return nil
}

// --- Helpers ---

// diffToolCount returns the number of tools added and removed between old and
// new tool slices.
func diffToolCount(old, cur []tools.Tool) (added, removed int) {
	oldNames := make(map[string]struct{}, len(old))
	for _, t := range old {
		oldNames[t.Name()] = struct{}{}
	}
	newNames := make(map[string]struct{}, len(cur))
	for _, t := range cur {
		newNames[t.Name()] = struct{}{}
	}
	for n := range newNames {
		if _, ok := oldNames[n]; !ok {
			added++
		}
	}
	for n := range oldNames {
		if _, ok := newNames[n]; !ok {
			removed++
		}
	}
	return
}

