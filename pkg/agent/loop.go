package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

// ErrNoProvider is returned when Prompt is called without a configured AI provider.
var ErrNoProvider = errors.New("no AI provider configured")

const eventBufSize = 1024

// AgentLoop orchestrates the conversation between user, LLM, and tools.
// It drives the agentic loop: send messages -> collect response -> execute
// tool calls -> repeat until the model stops calling tools.
type AgentLoop struct {
	provider     ai.Provider
	tools        *tools.Registry
	hooks        *tools.HookRegistry
	metrics      *tools.Metrics
	systemPrompt string
	model        string
	maxTokens    int
	thinking     ai.ThinkingLevel

	// Auto-compaction settings.
	contextWindow    int // total context window in tokens (0 = disabled)
	reserveTokens    int // reserve buffer in tokens
	keepRecentTokens int // approx tokens to preserve during compaction

	// logger is used for operational log messages (dropped messages,
	// compaction events, etc.). Defaults to log.Default().
	logger *log.Logger

	// workingDir is passed to tools via context so they can resolve relative
	// paths without relying on process-global os.Chdir.
	workingDir string

	// lastInputTokens stores the input_tokens from the most recent usage event.
	lastInputTokens int

	mu       sync.Mutex
	messages []ai.Message
	events   chan AgentEvent

	// eventsMu serializes emit() sends and Prompt() channel close to
	// prevent send-on-closed-channel panics. emit() holds RLock during
	// the channel send; Prompt() holds a full Lock when closing and
	// recreating the channel. Lock ordering: eventsMu before mu.
	eventsMu sync.RWMutex

	// cancel is called to abort the current Prompt execution.
	cancel context.CancelFunc

	// steerCh allows injecting a steering message that interrupts tool execution.
	steerCh chan string
	// followUpCh allows queuing a follow-up user message processed after the loop ends.
	followUpCh chan string
}

// NewAgentLoop creates a new agent loop wired to the given provider and tool registry.
func NewAgentLoop(provider ai.Provider, toolRegistry *tools.Registry, opts ...Option) *AgentLoop {
	metrics := tools.NewMetrics()
	compressionConfig := tools.NewCompressionConfig()
	hooks := tools.NewHookRegistry()
	// Register RTK command translator first (before compression)
	hooks.Register(tools.NewRtkCommandTranslator())
	// Register language-specific compression filters and generic compressor
	tools.RegisterDefaultHooks(hooks, compressionConfig, metrics)

	a := &AgentLoop{
		provider:         provider,
		tools:            toolRegistry,
		hooks:            hooks,
		metrics:          metrics,
		logger:           log.Default(),
		maxTokens:        8192,
		thinking:         ai.ThinkingOff,
		contextWindow:    200000,
		reserveTokens:    16384,
		keepRecentTokens: 4096,
		events:           make(chan AgentEvent, eventBufSize),
		steerCh:          make(chan string, 2),
		followUpCh:       make(chan string, 2),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// ensureInit lazily initializes channels that are nil on a zero-value
// AgentLoop. This makes the zero value safe: callers who construct
// &AgentLoop{} instead of using NewAgentLoop won't hit nil-channel panics.
// The caller must NOT hold a.mu.
func (a *AgentLoop) ensureInit() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.events == nil {
		a.events = make(chan AgentEvent, eventBufSize)
	}
	if a.steerCh == nil {
		a.steerCh = make(chan string, 2)
	}
	if a.followUpCh == nil {
		a.followUpCh = make(chan string, 2)
	}
	if a.tools == nil {
		a.tools = tools.NewRegistry()
	}
	if a.hooks == nil {
		a.hooks = tools.NewHookRegistry()
	}
	if a.metrics == nil {
		a.metrics = tools.NewMetrics()
	}
	if a.logger == nil {
		a.logger = log.Default()
	}
}

// Events returns the channel on which agent events are emitted.
// The caller should read from this channel to receive real-time updates.
// The channel is closed when the current Prompt call completes, so
// a consumer using range will exit automatically.
func (a *AgentLoop) Events() <-chan AgentEvent {
	a.ensureInit()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.events
}

// Metrics returns the metrics collector for this agent loop.
func (a *AgentLoop) Metrics() *tools.Metrics {
	return a.metrics
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
// The input slice is copied to prevent the caller from mutating agent state.
func (a *AgentLoop) SetMessages(msgs []ai.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := make([]ai.Message, len(msgs))
	copy(cp, msgs)
	a.messages = cp
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

// ProviderName returns the name of the current AI provider, or empty if none.
func (a *AgentLoop) ProviderName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.provider != nil {
		return a.provider.Name()
	}
	return ""
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
	a.ensureInit()
	select {
	case a.steerCh <- text:
	default:
		a.logger.Printf("agent: steer message dropped (channel full): %s", text)
	}
}

// FollowUp queues a follow-up user message that will be processed after the
// current agent run finishes. Safe to call from any goroutine.
func (a *AgentLoop) FollowUp(text string) {
	a.ensureInit()
	select {
	case a.followUpCh <- text:
	default:
		a.logger.Printf("agent: follow-up message dropped (channel full): %s", text)
	}
}

// Prompt adds a user message and runs the agent loop until the model produces
// a final response with no tool calls. Events are emitted on the Events() channel
// throughout execution. Prompt blocks until the agent run completes or the context
// is cancelled.
func (a *AgentLoop) Prompt(ctx context.Context, text string) error {
	a.ensureInit()
	a.mu.Lock()
	provider := a.provider
	a.mu.Unlock()
	if provider == nil {
		return ErrNoProvider
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
	//
	// eventsMu.Lock waits for any in-flight emit() sends to complete
	// before closing the channel, preventing send-on-closed-channel panics.
	a.eventsMu.Lock()
	a.mu.Lock()
	close(a.events)
	a.events = make(chan AgentEvent, eventBufSize)
	a.mu.Unlock()
	a.eventsMu.Unlock()

	if err != nil {
		return fmt.Errorf("prompt: %w", err)
	}
	return nil
}

// run is the core loop. It sends messages to the model, processes tool calls,
// handles steering, and loops until the model is done.
func (a *AgentLoop) run(ctx context.Context) error {
	// Drain stale steering messages left over from a previous Prompt() call.
	// steerCh is a buffered channel that persists across calls; unconsumed
	// messages would cause the next tool-execution phase to skip tools
	// unexpectedly.
	for {
		select {
		case <-a.steerCh:
		default:
			goto steerDrained
		}
	}
steerDrained:

	a.emit(ctx, AgentEvent{Type: EventAgentStart})
	defer a.emit(ctx, AgentEvent{Type: EventAgentEnd})

	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("agent run: %w", err)
		}

		// Check if auto-compaction is needed before the next LLM call.
		if err := a.maybeAutoCompact(ctx); err != nil {
			a.logger.Printf("agent: auto-compaction failed: %v", err)
			// Non-fatal: continue with the existing context.
		}

		assistantMsg, err := a.doTurn(ctx)
		if err != nil {
			a.emit(ctx, AgentEvent{Type: EventAgentError, Error: err})
			return fmt.Errorf("agent turn: %w", err)
		}

		toolCalls := assistantMsg.GetToolCalls()
		if len(toolCalls) == 0 {
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
				a.addSteeringSkipResults(ctx, toolCalls, tc.ToolUseID)
				a.appendMessage(ai.NewTextMessage(ai.RoleUser, steerMsg))
				steered = true
			default:
			}
			if steered {
				break
			}

			toolResult := a.executeTool(ctx, tc)
			if ctx.Err() != nil {
				return fmt.Errorf("tool execution interrupted: %w", ctx.Err())
			}

			a.appendMessage(toolResult)
			a.emit(ctx, AgentEvent{Type: EventToolResult, Message: &toolResult})

			// Check for steering after execution too.
			select {
			case steerMsg := <-a.steerCh:
				// Current tool already has a result; skip remaining tools.
				for _, remaining := range toolCalls[i+1:] {
					skipResult := ai.NewToolResultMessage(remaining.ToolUseID, "tool execution skipped: user sent a new message", true)
					a.appendMessage(skipResult)
					a.emit(ctx, AgentEvent{Type: EventToolResult, Message: &skipResult})
				}
				a.appendMessage(ai.NewTextMessage(ai.RoleUser, steerMsg))
				steered = true
			default:
			}
			if steered {
				break
			}
		}
	}

	return nil
}

// doTurn sends the current messages to the model and collects the full
// streamed assistant response. It emits text/thinking delta events as
// they arrive and returns the completed assistant message.
func (a *AgentLoop) doTurn(ctx context.Context) (*ai.Message, error) {
	a.emit(ctx, AgentEvent{Type: EventTurnStart})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	a.mu.Lock()
	provider := a.provider
	req := ai.StreamRequest{
		Model:         a.model,
		SystemPrompt:  a.systemPrompt,
		Messages:      cloneMessages(a.messages),
		Tools:         a.tools.ToToolDefs(),
		MaxTokens:     a.maxTokens,
		ThinkingLevel: a.thinking,
	}
	a.mu.Unlock()

	stream, err := provider.Stream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("provider stream: %w", err)
	}

	assistantMsg := ai.Message{Role: ai.RoleAssistant, Content: []ai.ContentBlock{}}

	// Accumulate streaming deltas with strings.Builder (O(n) amortized)
	// instead of repeated string concatenation (O(n²)).
	var textBuilder strings.Builder
	textIdx := -1 // index of current text block in assistantMsg.Content
	var thinkingBuilder strings.Builder
	thinkingIdx := -1

	// State for accumulating the current tool use input JSON.
	type toolAccum struct {
		id    string
		name  string
		input strings.Builder
	}
	var currentTool *toolAccum

	for event := range stream {
		switch event.Type {
		case ai.EventMessageStart:

		case ai.EventTextDelta:
			a.emit(ctx, AgentEvent{Type: EventAssistantText, Delta: event.Delta})
			if textIdx < 0 {
				textIdx = len(assistantMsg.Content)
				assistantMsg.Content = append(assistantMsg.Content, ai.ContentBlock{
					Type: ai.ContentTypeText,
				})
			}
			textBuilder.WriteString(event.Delta)

		case ai.EventThinkingDelta:
			a.emit(ctx, AgentEvent{Type: EventAssistantThinking, Delta: event.Delta})
			if thinkingIdx < 0 {
				thinkingIdx = len(assistantMsg.Content)
				assistantMsg.Content = append(assistantMsg.Content, ai.ContentBlock{
					Type: ai.ContentTypeThinking,
				})
			}
			thinkingBuilder.WriteString(event.Delta)

		case ai.EventToolUseStart:
			// Flush text/thinking builders before the tool use block.
			if textIdx >= 0 {
				assistantMsg.Content[textIdx].Text = textBuilder.String()
				textBuilder.Reset()
				textIdx = -1
			}
			if thinkingIdx >= 0 {
				assistantMsg.Content[thinkingIdx].Thinking = thinkingBuilder.String()
				thinkingBuilder.Reset()
				thinkingIdx = -1
			}
			currentTool = &toolAccum{
				id:   event.ToolCallID,
				name: event.ToolName,
			}

		case ai.EventToolUseDelta:
			if currentTool != nil {
				currentTool.input.WriteString(event.PartialInput)
			}

		case ai.EventToolUseEnd:
			if currentTool != nil {
				inputJSON := currentTool.input.String()
				var input any
				if inputJSON != "" {
					if err := json.Unmarshal([]byte(inputJSON), &input); err != nil {
						a.logger.Printf("agent: invalid tool input JSON for %s (id=%s): %v", currentTool.name, currentTool.id, err)
						input = inputJSON
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
				a.mu.Lock()
				a.lastInputTokens = event.Usage.InputTokens
				a.mu.Unlock()
				a.emit(ctx, AgentEvent{Type: EventUsageUpdate, Usage: event.Usage})
			}

		case ai.EventError:
			cancel()
			for range stream {
			}
			return nil, fmt.Errorf("stream error: %w", event.Error)
		}
	}

	// Flush any remaining accumulated text/thinking.
	if textIdx >= 0 {
		assistantMsg.Content[textIdx].Text = textBuilder.String()
	}
	if thinkingIdx >= 0 {
		assistantMsg.Content[thinkingIdx].Thinking = thinkingBuilder.String()
	}

	a.appendMessage(assistantMsg)
	a.emit(ctx, AgentEvent{Type: EventTurnEnd, Message: &assistantMsg})
	return &assistantMsg, nil
}

// executeTool runs a single tool call and emits the corresponding events.
// It returns a tool_result message ready to be appended to the conversation.
func (a *AgentLoop) executeTool(ctx context.Context, tc ai.ContentBlock) ai.Message {
	// Snapshot mutable fields under lock to avoid races with concurrent setters.
	a.mu.Lock()
	workingDir := a.workingDir
	toolsReg := a.tools
	hooks := a.hooks
	metrics := a.metrics
	a.mu.Unlock()

	// Inject working directory into context so tools can resolve relative paths.
	if workingDir != "" {
		ctx = tools.ContextWithWorkingDir(ctx, workingDir)
	}

	params := toParamsMap(tc.Input)

	a.emit(ctx, AgentEvent{
		Type:       EventToolExecStart,
		ToolCallID: tc.ToolUseID,
		ToolName:   tc.ToolName,
		ToolArgs:   params,
	})

	// If the model sent non-nil input but it couldn't be parsed as a JSON
	// object, toParamsMap returns nil. Return an error result so the model
	// can retry with valid input instead of executing with nil params.
	if params == nil && tc.Input != nil {
		errMsg := fmt.Sprintf("invalid tool input for %s: could not parse as JSON object", tc.ToolName)
		a.emit(ctx, AgentEvent{
			Type:       EventToolExecEnd,
			ToolCallID: tc.ToolUseID,
			ToolName:   tc.ToolName,
			ToolResult: errMsg,
			ToolError:  true,
		})
		return ai.NewToolResultMessage(tc.ToolUseID, errMsg, true)
	}

	tool, ok := toolsReg.Get(tc.ToolName)
	if !ok {
		errMsg := fmt.Sprintf("unknown tool: %s", tc.ToolName)
		a.emit(ctx, AgentEvent{
			Type:       EventToolExecEnd,
			ToolCallID: tc.ToolUseID,
			ToolName:   tc.ToolName,
			ToolResult: errMsg,
			ToolError:  true,
		})
		return ai.NewToolResultMessage(tc.ToolUseID, errMsg, true)
	}

	// Fire before-execution hooks
	if err := hooks.Before(ctx, tc.ToolName, params); err != nil {
		errMsg := fmt.Sprintf("hook error: %v", err)
		a.emit(ctx, AgentEvent{
			Type:       EventToolExecEnd,
			ToolCallID: tc.ToolUseID,
			ToolName:   tc.ToolName,
			ToolResult: errMsg,
			ToolError:  true,
		})
		return ai.NewToolResultMessage(tc.ToolUseID, errMsg, true)
	}

	// Check if tool supports rich (multi-block) results.
	if richTool, ok := tool.(tools.RichTool); ok {
		blocks, err := safeExecuteRich(ctx, richTool, params)
		isError := err != nil
		var resultText string
		if isError {
			resultText = err.Error()
		} else {
			var sb strings.Builder
			for _, b := range blocks {
				if b.Type == ai.ContentTypeText {
					sb.WriteString(b.Text)
				}
			}
			resultText = sb.String()
		}

		// Fire after-execution hooks
		var hookErr error
		resultText, hookErr = hooks.After(ctx, tc.ToolName, params, resultText, nil)
		if hookErr != nil {
			isError = true
			resultText = hookErr.Error()
		}

		a.emit(ctx, AgentEvent{
			Type:       EventToolExecEnd,
			ToolCallID: tc.ToolUseID,
			ToolName:   tc.ToolName,
			ToolResult: resultText,
			ToolError:  isError,
		})
		if isError {
			return ai.NewToolResultMessage(tc.ToolUseID, resultText, true)
		}
		return ai.NewRichToolResultMessage(tc.ToolUseID, blocks, false)
	}

	result, err := safeExecute(ctx, tool, params)
	isError := err != nil
	if isError {
		result = err.Error()
	}

	// Track original size before compression hooks
	originalSize := len(result)

	// Fire after-execution hooks
	var hookErr error
	result, hookErr = hooks.After(ctx, tc.ToolName, params, result, nil)
	if hookErr != nil {
		isError = true
		result = hookErr.Error()
	}

	// Record compression metrics for bash commands
	if tc.ToolName == "bash" && !isError {
		compressedSize := len(result)
		cmd, _ := params["command"].(string)
		category := tools.DetectCategory(cmd)
		metrics.Record(category, originalSize, compressedSize, 0)
	}

	a.emit(ctx, AgentEvent{
		Type:       EventToolExecEnd,
		ToolCallID: tc.ToolUseID,
		ToolName:   tc.ToolName,
		ToolResult: result,
		ToolError:  isError,
	})
	return ai.NewToolResultMessage(tc.ToolUseID, result, isError)
}

// safeExecute calls tool.Execute inside a deferred recover so that a panicking
// tool returns an error instead of crashing the agent process.
func safeExecute(ctx context.Context, tool tools.Tool, params map[string]any) (result string, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = ""
			err = fmt.Errorf("tool panicked: %v", r)
		}
	}()
	return tool.Execute(ctx, params)
}

// safeExecuteRich is the panic-safe wrapper for RichTool.ExecuteRich.
func safeExecuteRich(ctx context.Context, tool tools.RichTool, params map[string]any) (blocks []ai.ContentBlock, err error) {
	defer func() {
		if r := recover(); r != nil {
			blocks = nil
			err = fmt.Errorf("tool panicked: %v", r)
		}
	}()
	return tool.ExecuteRich(ctx, params)
}

// addSteeringSkipResults adds error tool results for any tool calls that were
// skipped due to a steering interrupt. The skipAfterID parameter indicates the
// tool call ID after which to start adding skip results. If empty, no skip
// results are added (all tools already have results).
func (a *AgentLoop) addSteeringSkipResults(ctx context.Context, toolCalls []ai.ContentBlock, skipAfterID string) {
	skipping := false
	if skipAfterID == "" {
		return
	}
	for _, tc := range toolCalls {
		if tc.ToolUseID == skipAfterID {
			skipping = true
			// This tool call itself is being skipped — add a result for it.
			skipResult := ai.NewToolResultMessage(tc.ToolUseID, "tool execution skipped: user sent a new message", true)
			a.appendMessage(skipResult)
			a.emit(ctx, AgentEvent{Type: EventToolResult, Message: &skipResult})
			continue
		}
		if skipping {
			skipResult := ai.NewToolResultMessage(tc.ToolUseID, "tool execution skipped: user sent a new message", true)
			a.appendMessage(skipResult)
			a.emit(ctx, AgentEvent{Type: EventToolResult, Message: &skipResult})
		}
	}
}

// appendMessage safely appends a message to the conversation history.
func (a *AgentLoop) appendMessage(msg ai.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append(a.messages, msg)
}

// emit sends an event to the events channel. If the channel buffer is full,
// it blocks until space is available or the context is cancelled — providing
// back-pressure to the agent loop so events are never silently dropped.
//
// eventsMu.RLock is held for the duration of the send so that Prompt() cannot
// close the channel while a send is in flight. Multiple goroutines may emit
// concurrently (RLock allows shared access).
func (a *AgentLoop) emit(ctx context.Context, event AgentEvent) {
	a.eventsMu.RLock()
	defer a.eventsMu.RUnlock()

	a.mu.Lock()
	ch := a.events
	a.mu.Unlock()

	select {
	case ch <- event:
	case <-ctx.Done():
	}
}

// --- helpers ----------------------------------------------------------------

// cloneMessages returns a shallow copy of the messages slice.
func cloneMessages(msgs []ai.Message) []ai.Message {
	cp := make([]ai.Message, len(msgs))
	copy(cp, msgs)
	return cp
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
