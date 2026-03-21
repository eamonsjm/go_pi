// Package sdk provides a high-level API for embedding go_pi as a library.
//
// The primary entry point is [NewSession], which creates a fully-configured
// agent session ready for use. Sessions manage the agent loop, tool registry,
// session persistence, and event streaming.
//
// Basic usage:
//
//	s, err := sdk.NewSession(sdk.WithAPIKey("anthropic", "sk-..."))
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer s.Close()
//
//	// Stream events while prompting
//	events := s.Events()
//	go s.Prompt(ctx, "Explain this codebase")
//
//	for event := range events {
//	    if event.Type == agent.EventAssistantText {
//	        fmt.Print(event.Delta)
//	    }
//	}
package sdk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/session"
	"github.com/ejm/go_pi/pkg/tools"
)

// Session is the primary SDK type. It wraps an agent loop, tool registry,
// and session manager into a single, easy-to-use interface for programmatic
// agent execution.
type Session struct {
	loop       *agent.AgentLoop
	registry   *tools.Registry
	sessionMgr *session.Manager
	config     *SessionConfig
}

// SessionConfig holds all configuration for creating a session.
type SessionConfig struct {
	// Provider is the AI provider name ("anthropic", "openai", "gemini", etc.).
	Provider string

	// APIKey is the API key for the provider.
	APIKey string

	// Model is the model ID to use (e.g. "claude-sonnet-4-20250514").
	// If empty, a default is chosen based on provider.
	Model string

	// SystemPrompt is the system prompt for the agent.
	// If empty, a minimal default is used.
	SystemPrompt string

	// MaxTokens is the maximum output tokens per LLM call. Default: 8192.
	MaxTokens int

	// ThinkingLevel controls extended thinking. Default: off.
	ThinkingLevel ai.ThinkingLevel

	// WorkingDir sets the working directory for tool execution.
	// If empty, the current directory is used.
	WorkingDir string

	// SessionDir is the directory for session persistence.
	// If empty, sessions are stored in ~/.gi/sessions/.
	SessionDir string

	// Tools is an optional custom tool registry. If nil, the default
	// tools (read, write, edit, bash, glob, grep) are registered.
	Tools *tools.Registry

	// ContextWindow is the total context window size in tokens.
	// Auto-compaction triggers when approaching this limit. Default: 200000.
	ContextWindow int

	// ReserveTokens is the token buffer for auto-compaction. Default: 16384.
	ReserveTokens int

	// Messages pre-loads conversation history (e.g. from a restored session).
	Messages []ai.Message

	// providerInstance allows passing a pre-built provider directly.
	providerInstance ai.Provider
}

// SessionOption configures a SessionConfig.
type SessionOption func(*SessionConfig)

// WithAPIKey sets the provider and API key.
func WithAPIKey(provider, apiKey string) SessionOption {
	return func(c *SessionConfig) {
		c.Provider = provider
		c.APIKey = apiKey
	}
}

// WithProvider sets a pre-built AI provider directly, bypassing API key resolution.
func WithProvider(p ai.Provider) SessionOption {
	return func(c *SessionConfig) {
		c.providerInstance = p
	}
}

// WithModel sets the model ID.
func WithModel(model string) SessionOption {
	return func(c *SessionConfig) {
		c.Model = model
	}
}

// WithSystemPrompt sets the system prompt.
func WithSystemPrompt(prompt string) SessionOption {
	return func(c *SessionConfig) {
		c.SystemPrompt = prompt
	}
}

// WithMaxTokens sets the maximum output tokens per LLM call.
func WithMaxTokens(n int) SessionOption {
	return func(c *SessionConfig) {
		c.MaxTokens = n
	}
}

// WithThinking sets the extended thinking level.
func WithThinking(level ai.ThinkingLevel) SessionOption {
	return func(c *SessionConfig) {
		c.ThinkingLevel = level
	}
}

// WithWorkingDir sets the working directory for tool execution.
func WithWorkingDir(dir string) SessionOption {
	return func(c *SessionConfig) {
		c.WorkingDir = dir
	}
}

// WithSessionDir sets the directory for session persistence.
func WithSessionDir(dir string) SessionOption {
	return func(c *SessionConfig) {
		c.SessionDir = dir
	}
}

// WithTools sets a custom tool registry. This replaces the default tools entirely.
func WithTools(registry *tools.Registry) SessionOption {
	return func(c *SessionConfig) {
		c.Tools = registry
	}
}

// WithContextWindow sets the context window size for auto-compaction.
func WithContextWindow(tokens int) SessionOption {
	return func(c *SessionConfig) {
		c.ContextWindow = tokens
	}
}

// WithMessages pre-loads conversation history.
func WithMessages(msgs []ai.Message) SessionOption {
	return func(c *SessionConfig) {
		c.Messages = msgs
	}
}

// NewSession creates a new agent session with the given options.
// At minimum, either WithAPIKey or WithProvider must be specified.
func NewSession(opts ...SessionOption) (*Session, error) {
	cfg := &SessionConfig{
		MaxTokens:     8192,
		ThinkingLevel: ai.ThinkingOff,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Resolve provider.
	provider, err := resolveProvider(cfg)
	if err != nil {
		return nil, fmt.Errorf("sdk: %w", err)
	}

	// Set working directory.
	if cfg.WorkingDir != "" {
		if err := os.Chdir(cfg.WorkingDir); err != nil {
			return nil, fmt.Errorf("sdk: change working directory: %w", err)
		}
	}

	// Set up tool registry.
	registry := cfg.Tools
	if registry == nil {
		registry = tools.NewRegistry()
		tools.RegisterDefaults(registry)
	}

	// Build agent options.
	agentOpts := []agent.Option{
		agent.WithModel(cfg.Model),
		agent.WithMaxTokens(cfg.MaxTokens),
		agent.WithThinking(cfg.ThinkingLevel),
	}
	if cfg.SystemPrompt != "" {
		agentOpts = append(agentOpts, agent.WithSystemPrompt(cfg.SystemPrompt))
	}
	if cfg.ContextWindow > 0 {
		agentOpts = append(agentOpts, agent.WithContextWindow(cfg.ContextWindow))
	}
	if cfg.ReserveTokens > 0 {
		agentOpts = append(agentOpts, agent.WithReserveTokens(cfg.ReserveTokens))
	}
	if len(cfg.Messages) > 0 {
		agentOpts = append(agentOpts, agent.WithMessages(cfg.Messages))
	}

	loop := agent.NewAgentLoop(provider, registry, agentOpts...)

	// Set up session manager.
	sessionDir := cfg.SessionDir
	if sessionDir == "" {
		home, _ := os.UserHomeDir()
		sessionDir = filepath.Join(home, ".gi", "sessions")
	}
	sessionMgr := session.NewManager(sessionDir)
	sessionMgr.NewSession()

	return &Session{
		loop:       loop,
		registry:   registry,
		sessionMgr: sessionMgr,
		config:     cfg,
	}, nil
}

// Prompt sends a user message and runs the agent loop until completion.
// Events are emitted on the Events() channel throughout execution.
// Prompt blocks until the agent finishes or the context is cancelled.
// All messages (user, assistant, tool results) are persisted to the session.
func (s *Session) Prompt(ctx context.Context, text string) error {
	// Record message count before the prompt so we can persist new messages.
	beforeCount := len(s.loop.Messages())

	// Persist user message.
	if err := s.sessionMgr.SaveMessage(ai.NewTextMessage(ai.RoleUser, text)); err != nil {
		return fmt.Errorf("sdk prompt: save user message: %w", err)
	}

	err := s.loop.Prompt(ctx, text)

	// Persist all new messages generated during this prompt (assistant turns,
	// tool results, etc.). The user message was already saved above, so skip
	// it by starting from beforeCount+1.
	allMsgs := s.loop.Messages()
	for i := beforeCount + 1; i < len(allMsgs); i++ {
		if err := s.sessionMgr.SaveMessage(allMsgs[i]); err != nil {
			return fmt.Errorf("sdk prompt: save generated message: %w", err)
		}
	}

	if err != nil {
		return fmt.Errorf("sdk prompt: agent loop: %w", err)
	}
	return nil
}

// Events returns the channel on which agent events are emitted.
// Read from this channel to receive streaming text, tool execution updates,
// usage information, and other lifecycle events.
//
// The channel is closed when the current Prompt call completes.
// A new channel is available for the next Prompt call.
func (s *Session) Events() <-chan agent.AgentEvent {
	return s.loop.Events()
}

// Cancel aborts the currently running Prompt.
func (s *Session) Cancel() {
	s.loop.Cancel()
}

// Steer injects a steering message during agent execution.
// If the agent is executing tools, remaining tool calls are skipped
// and the steering message is sent to the model.
func (s *Session) Steer(text string) {
	s.loop.Steer(text)
}

// FollowUp queues a follow-up message that will be processed after
// the current agent run finishes.
func (s *Session) FollowUp(text string) {
	s.loop.FollowUp(text)
}

// Compact summarizes the conversation history to reduce context size.
// The optional instructions parameter guides what to focus on.
func (s *Session) Compact(ctx context.Context, instructions string) error {
	return s.loop.Compact(ctx, instructions)
}

// Messages returns a copy of the current conversation history.
func (s *Session) Messages() []ai.Message {
	return s.loop.Messages()
}

// SetModel changes the model used for subsequent LLM calls.
func (s *Session) SetModel(model string) {
	s.loop.SetModel(model)
}

// SetThinking changes the extended thinking level.
func (s *Session) SetThinking(level ai.ThinkingLevel) {
	s.loop.SetThinking(level)
}

// SetSystemPrompt updates the system prompt for subsequent turns.
func (s *Session) SetSystemPrompt(prompt string) {
	s.loop.SetSystemPrompt(prompt)
}

// SetMaxTokens changes the max output tokens.
func (s *Session) SetMaxTokens(n int) {
	s.loop.SetMaxTokens(n)
}

// SessionID returns the current session ID.
func (s *Session) SessionID() string {
	return s.sessionMgr.CurrentID()
}

// AgentLoop returns the underlying agent loop for advanced usage.
func (s *Session) AgentLoop() *agent.AgentLoop {
	return s.loop
}

// Registry returns the tool registry for registering custom tools.
func (s *Session) Registry() *tools.Registry {
	return s.registry
}

// Close performs any cleanup needed when the session is done.
// Currently a no-op but reserved for future use (e.g. plugin shutdown).
func (s *Session) Close() error {
	return nil
}

// Resume loads a previous session and restores its conversation history.
func (s *Session) Resume(sessionID string) error {
	if err := s.sessionMgr.LoadSession(sessionID); err != nil {
		return fmt.Errorf("sdk: resume session: %w", err)
	}
	msgs := s.sessionMgr.GetMessages()
	s.loop.SetMessages(msgs)
	return nil
}

// resolveProvider creates an AI provider from the session config.
func resolveProvider(cfg *SessionConfig) (ai.Provider, error) {
	if cfg.providerInstance != nil {
		return cfg.providerInstance, nil
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("no API key or provider configured; use WithAPIKey or WithProvider")
	}

	// Set default model if not specified.
	if cfg.Model == "" {
		switch cfg.Provider {
		case "anthropic":
			cfg.Model = "claude-sonnet-4-20250514"
		case "openrouter":
			cfg.Model = "anthropic/claude-sonnet-4-20250514"
		case "openai":
			cfg.Model = "gpt-4o"
		case "gemini":
			cfg.Model = "gemini-2.0-flash"
		case "azure":
			cfg.Model = "gpt-4o"
		case "bedrock":
			cfg.Model = "anthropic.claude-3-5-sonnet-20241022-v2:0"
		default:
			cfg.Model = "claude-sonnet-4-20250514"
		}
	}

	switch cfg.Provider {
	case "anthropic":
		return ai.NewAnthropicProvider(cfg.APIKey)
	case "openrouter":
		return ai.NewOpenRouterProvider(cfg.APIKey)
	case "openai":
		return ai.NewOpenAIProvider(cfg.APIKey)
	case "gemini":
		return ai.NewGeminiProvider(cfg.APIKey)
	case "azure":
		return ai.NewAzureOpenAIProvider(cfg.APIKey, "", "")
	case "bedrock":
		return ai.NewBedrockProvider(cfg.APIKey)
	default:
		return nil, fmt.Errorf("unknown provider: %q", cfg.Provider)
	}
}
