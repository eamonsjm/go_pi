package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

const eventBufSize = 1024

// AgentLoop orchestrates the conversation between user, LLM, and tools.
// It drives the agentic loop: send messages -> collect response -> execute
// tool calls -> repeat until the model stops calling tools.
type AgentLoop struct {
	provider     ai.Provider
	tools        *tools.Registry
	systemPrompt string
	model        string
	maxTokens    int
	thinking     ai.ThinkingLevel

	mu       sync.Mutex
	messages []ai.Message
	events   chan AgentEvent

	// cancel is called to abort the current Prompt execution.
	cancel context.CancelFunc

	// steerCh allows injecting a steering message that interrupts tool execution.
	steerCh chan string
	// followUpCh allows queuing a follow-up user message processed after the loop ends.
	followUpCh chan string
}

// NewAgentLoop creates a new agent loop wired to the given provider and tool registry.
func NewAgentLoop(provider ai.Provider, toolRegistry *tools.Registry, opts ...Option) *AgentLoop {
	a := &AgentLoop{
		provider:   provider,
		tools:      toolRegistry,
		maxTokens:  8192,
		thinking:   ai.ThinkingOff,
		events:     make(chan AgentEvent, eventBufSize),
		steerCh:    make(chan string, 1),
		followUpCh: make(chan string, 1),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Events returns the channel on which agent events are emitted.
// The caller should read from this channel to receive real-time updates.
// The channel is closed when the current Prompt call completes, so
// a consumer using range will exit automatically.
func (a *AgentLoop) Events() <-chan AgentEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.events
}

// Messages returns a copy of the current conversation history.
func (a *AgentLoop) Messages() []ai.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := make([]ai.Message, len(a.messages))
	copy(cp, a.messages)
	return cp
}

// SetMessages replaces the conversation history (e.g. when restoring a session).
func (a *AgentLoop) SetMessages(msgs []ai.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = msgs
}

// SetModel changes the model used for subsequent LLM calls.
func (a *AgentLoop) SetModel(model string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = model
}

// SetThinking changes the extended thinking level.
func (a *AgentLoop) SetThinking(level ai.ThinkingLevel) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.thinking = level
}

// SetMaxTokens changes the max output tokens for subsequent LLM calls.
func (a *AgentLoop) SetMaxTokens(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.maxTokens = n
}

// SetProvider replaces the AI provider used for subsequent LLM calls.
// This is used to wire up a provider after login when the app started
// without one.
func (a *AgentLoop) SetProvider(p ai.Provider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.provider = p
}

// SetSystemPrompt updates the system prompt for subsequent turns.
func (a *AgentLoop) SetSystemPrompt(prompt string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.systemPrompt = prompt
}

// Cancel aborts the current Prompt execution.
func (a *AgentLoop) Cancel() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
}

// Steer injects a steering message. If the agent is currently executing tools,
// remaining tool calls are skipped and the steering message is sent to the model
// as a user turn. Safe to call from any goroutine.
func (a *AgentLoop) Steer(text string) {
	// Non-blocking send — only one steering message can be buffered.
	select {
	case a.steerCh <- text:
	default:
	}
}

// FollowUp queues a follow-up user message that will be processed after the
// current agent run finishes. Safe to call from any goroutine.
func (a *AgentLoop) FollowUp(text string) {
	select {
	case a.followUpCh <- text:
	default:
	}
}

// Prompt adds a user message and runs the agent loop until the model produces
// a final response with no tool calls. Events are emitted on the Events() channel
// throughout execution. Prompt blocks until the agent run completes or the context
// is cancelled.
func (a *AgentLoop) Prompt(ctx context.Context, text string) error {
	if a.provider == nil {
		return fmt.Errorf("no AI provider configured. Set an API key (ANTHROPIC_API_KEY, OPENROUTER_API_KEY, or OPENAI_API_KEY) and restart pi")
	}
	ctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.cancel = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.cancel = nil
		a.mu.Unlock()
	}()

	a.appendMessage(ai.NewTextMessage(ai.RoleUser, text))
	err := a.run(ctx)

	// Close the events channel so consumer goroutines unblock (range
	// terminates on a closed channel), then create a fresh channel for
	// the next Prompt call.
	a.mu.Lock()
	close(a.events)
	a.events = make(chan AgentEvent, eventBufSize)
	a.mu.Unlock()

	return err
}

// run is the core loop. It sends messages to the model, processes tool calls,
// handles steering, and loops until the model is done.
func (a *AgentLoop) run(ctx context.Context) error {
	a.emit(AgentEvent{Type: EventAgentStart})
	defer a.emit(AgentEvent{Type: EventAgentEnd})

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		assistantMsg, err := a.doTurn(ctx)
		if err != nil {
			a.emit(AgentEvent{Type: EventAgentError, Error: err})
			return err
		}

		toolCalls := assistantMsg.GetToolCalls()
		if len(toolCalls) == 0 {
			// No tool calls — the model is done.
			// Check for a follow-up message before fully stopping.
			select {
			case followUp := <-a.followUpCh:
				a.appendMessage(ai.NewTextMessage(ai.RoleUser, followUp))
				continue
			default:
			}
			break
		}

		// Execute tool calls sequentially. A steering message can interrupt.
		steered := false
		for i, tc := range toolCalls {
			// Check for steering interrupt before each tool.
			select {
			case steerMsg := <-a.steerCh:
				a.addSteeringSkipResults(toolCalls, tc.ToolUseID)
				a.appendMessage(ai.NewTextMessage(ai.RoleUser, steerMsg))
				steered = true
			default:
			}
			if steered {
				break
			}

			result, toolErr := a.executeTool(ctx, tc)
			if ctx.Err() != nil {
				return ctx.Err()
			}

			a.appendMessage(ai.NewToolResultMessage(tc.ToolUseID, result, toolErr))

			// Check for steering after execution too.
			select {
			case steerMsg := <-a.steerCh:
				// Current tool already has a result; skip remaining tools.
				for _, remaining := range toolCalls[i+1:] {
					a.appendMessage(ai.NewToolResultMessage(remaining.ToolUseID, "tool execution skipped: user sent a new message", true))
				}
				a.appendMessage(ai.NewTextMessage(ai.RoleUser, steerMsg))
				steered = true
			default:
			}
			if steered {
				break
			}
		}
		// Loop back to send tool results (or steering message) to the model.
	}

	return nil
}

// doTurn sends the current messages to the model and collects the full
// streamed assistant response. It emits text/thinking delta events as
// they arrive and returns the completed assistant message.
func (a *AgentLoop) doTurn(ctx context.Context) (*ai.Message, error) {
	a.emit(AgentEvent{Type: EventTurnStart})

	a.mu.Lock()
	req := ai.StreamRequest{
		Model:         a.model,
		SystemPrompt:  a.systemPrompt,
		Messages:      cloneMessages(a.messages),
		Tools:         a.tools.ToToolDefs(),
		MaxTokens:     a.maxTokens,
		ThinkingLevel: a.thinking,
	}
	a.mu.Unlock()

	stream, err := a.provider.Stream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("provider stream: %w", err)
	}

	assistantMsg := ai.Message{Role: ai.RoleAssistant}

	// State for accumulating the current tool use input JSON.
	type toolAccum struct {
		id        string
		name      string
		inputJSON string
	}
	var currentTool *toolAccum

	for event := range stream {
		switch event.Type {
		case ai.EventMessageStart:
			// Nothing to do; we already initialized assistantMsg.

		case ai.EventTextDelta:
			a.emit(AgentEvent{Type: EventAssistantText, Delta: event.Delta})
			appendTextDelta(&assistantMsg, event.Delta)

		case ai.EventThinkingDelta:
			a.emit(AgentEvent{Type: EventAssistantThinking, Delta: event.Delta})
			appendThinkingDelta(&assistantMsg, event.Delta)

		case ai.EventToolUseStart:
			currentTool = &toolAccum{
				id:   event.ToolCallID,
				name: event.ToolName,
			}

		case ai.EventToolUseDelta:
			if currentTool != nil {
				currentTool.inputJSON += event.PartialInput
			}

		case ai.EventToolUseEnd:
			if currentTool != nil {
				var input any
				if currentTool.inputJSON != "" {
					if err := json.Unmarshal([]byte(currentTool.inputJSON), &input); err != nil {
						// Keep raw string as input if it doesn't parse.
						input = currentTool.inputJSON
					}
				}
				assistantMsg.Content = append(assistantMsg.Content, ai.ContentBlock{
					Type:      ai.ContentTypeToolUse,
					ToolUseID: currentTool.id,
					ToolName:  currentTool.name,
					Input:     input,
				})
				currentTool = nil
			}

		case ai.EventMessageEnd:
			if event.Usage != nil {
				a.emit(AgentEvent{Type: EventUsageUpdate, Usage: event.Usage})
			}

		case ai.EventError:
			return nil, fmt.Errorf("stream error: %w", event.Error)
		}
	}

	a.appendMessage(assistantMsg)
	a.emit(AgentEvent{Type: EventTurnEnd, Message: &assistantMsg})
	return &assistantMsg, nil
}

// executeTool runs a single tool call and emits the corresponding events.
func (a *AgentLoop) executeTool(ctx context.Context, tc ai.ContentBlock) (string, bool) {
	params := toParamsMap(tc.Input)

	a.emit(AgentEvent{
		Type:       EventToolExecStart,
		ToolCallID: tc.ToolUseID,
		ToolName:   tc.ToolName,
		ToolArgs:   params,
	})

	tool, ok := a.tools.Get(tc.ToolName)
	if !ok {
		errMsg := fmt.Sprintf("unknown tool: %s", tc.ToolName)
		a.emit(AgentEvent{
			Type:       EventToolExecEnd,
			ToolCallID: tc.ToolUseID,
			ToolName:   tc.ToolName,
			ToolResult: errMsg,
			ToolError:  true,
		})
		return errMsg, true
	}

	result, err := tool.Execute(ctx, params)
	isError := err != nil
	if isError {
		result = err.Error()
	}

	a.emit(AgentEvent{
		Type:       EventToolExecEnd,
		ToolCallID: tc.ToolUseID,
		ToolName:   tc.ToolName,
		ToolResult: result,
		ToolError:  isError,
	})
	return result, isError
}

// addSteeringSkipResults adds error tool results for any tool calls that were
// skipped due to a steering interrupt. The skipAfterID parameter indicates the
// tool call ID after which to start adding skip results. If empty, no skip
// results are added (all tools already have results).
func (a *AgentLoop) addSteeringSkipResults(toolCalls []ai.ContentBlock, skipAfterID string) {
	skipping := false
	if skipAfterID == "" {
		return
	}
	for _, tc := range toolCalls {
		if tc.ToolUseID == skipAfterID {
			skipping = true
			// This tool call itself is being skipped — add a result for it.
			a.appendMessage(ai.NewToolResultMessage(tc.ToolUseID, "tool execution skipped: user sent a new message", true))
			continue
		}
		if skipping {
			a.appendMessage(ai.NewToolResultMessage(tc.ToolUseID, "tool execution skipped: user sent a new message", true))
		}
	}
}

// appendMessage safely appends a message to the conversation history.
func (a *AgentLoop) appendMessage(msg ai.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append(a.messages, msg)
}

// emit sends an event to the events channel. It never blocks — if the channel
// is full the event is dropped and a warning is logged. The mutex is held
// briefly to ensure the channel reference is not replaced concurrently.
func (a *AgentLoop) emit(event AgentEvent) {
	a.mu.Lock()
	ch := a.events
	a.mu.Unlock()
	select {
	case ch <- event:
	default:
		log.Printf("agent: event dropped (type=%s), events channel buffer full", event.Type)
	}
}

// --- helpers ----------------------------------------------------------------

// cloneMessages returns a shallow copy of the messages slice.
func cloneMessages(msgs []ai.Message) []ai.Message {
	cp := make([]ai.Message, len(msgs))
	copy(cp, msgs)
	return cp
}

// appendTextDelta appends text to the last text content block of an assistant
// message, or creates a new text block if there isn't one.
func appendTextDelta(msg *ai.Message, delta string) {
	for i := len(msg.Content) - 1; i >= 0; i-- {
		if msg.Content[i].Type == ai.ContentTypeText {
			msg.Content[i].Text += delta
			return
		}
	}
	msg.Content = append(msg.Content, ai.ContentBlock{
		Type: ai.ContentTypeText,
		Text: delta,
	})
}

// appendThinkingDelta appends thinking text to the last thinking content block,
// or creates a new one.
func appendThinkingDelta(msg *ai.Message, delta string) {
	for i := len(msg.Content) - 1; i >= 0; i-- {
		if msg.Content[i].Type == ai.ContentTypeThinking {
			msg.Content[i].Thinking += delta
			return
		}
	}
	msg.Content = append(msg.Content, ai.ContentBlock{
		Type:     ai.ContentTypeThinking,
		Thinking: delta,
	})
}

// toParamsMap converts a tool input (which may be a map or raw JSON) into
// a map[string]any suitable for passing to Tool.Execute.
func toParamsMap(input any) map[string]any {
	switch v := input.(type) {
	case map[string]any:
		return v
	case string:
		var m map[string]any
		if err := json.Unmarshal([]byte(v), &m); err == nil {
			return m
		}
	}
	return nil
}
