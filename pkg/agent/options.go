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
