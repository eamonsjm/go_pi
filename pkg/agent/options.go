package agent

import "github.com/ejm/go_pi/pkg/ai"

// Option configures an AgentLoop.
type Option func(*AgentLoop)

// WithModel sets the model name.
func WithModel(model string) Option {
	return func(a *AgentLoop) {
		a.model = model
	}
}

// WithMaxTokens sets the maximum output tokens per LLM call.
func WithMaxTokens(n int) Option {
	return func(a *AgentLoop) {
		a.maxTokens = n
	}
}

// WithThinking sets the extended thinking level.
func WithThinking(level ai.ThinkingLevel) Option {
	return func(a *AgentLoop) {
		a.thinking = level
	}
}

// WithSystemPrompt sets the system prompt.
func WithSystemPrompt(prompt string) Option {
	return func(a *AgentLoop) {
		a.systemPrompt = prompt
	}
}

// WithMessages pre-loads conversation history (e.g. from a restored session).
func WithMessages(msgs []ai.Message) Option {
	return func(a *AgentLoop) {
		a.messages = msgs
	}
}

// WithContextWindow sets the total context window size in tokens. Auto-compaction
// triggers when input tokens exceed (contextWindow - reserveTokens). A zero value
// disables auto-compaction. Default: 200000.
func WithContextWindow(n int) Option {
	return func(a *AgentLoop) {
		a.contextWindow = n
	}
}

// WithReserveTokens sets the reserve token buffer. Auto-compaction triggers when
// input tokens exceed (contextWindow - reserveTokens). Default: 16384.
func WithReserveTokens(n int) Option {
	return func(a *AgentLoop) {
		a.reserveTokens = n
	}
}

// WithKeepRecentTokens sets the approximate number of recent tokens to preserve
// during auto-compaction. Older messages are summarized, recent ones are kept intact.
// Default: 4096.
func WithKeepRecentTokens(n int) Option {
	return func(a *AgentLoop) {
		a.keepRecentTokens = n
	}
}
