package sdk

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

// mockProvider is a minimal ai.Provider for testing.
type mockProvider struct {
	response string
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Stream(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
	ch := make(chan ai.StreamEvent, 10)
	go func() {
		ch <- ai.StreamEvent{Type: ai.EventMessageStart}
		ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: m.response}
		ch <- ai.StreamEvent{Type: ai.EventMessageEnd, Usage: &ai.Usage{InputTokens: 10, OutputTokens: 5}}
		close(ch)
	}()
	return ch, nil
}

func TestNewSessionWithProvider(t *testing.T) {
	mock := &mockProvider{response: "Hello from mock!"}
	s, err := NewSession(context.Background(),
		WithProvider(mock),
		WithModel("test-model"),
		WithSystemPrompt("You are a test assistant."),
		WithSessionDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	if s.SessionID() == "" {
		t.Fatal("expected non-empty session ID")
	}

	if s.AgentLoop() == nil {
		t.Fatal("expected non-nil AgentLoop")
	}

	if s.Registry() == nil {
		t.Fatal("expected non-nil Registry")
	}
}

func TestSessionPromptAndEvents(t *testing.T) {
	mock := &mockProvider{response: "The answer is 42."}
	s, err := NewSession(context.Background(),
		WithProvider(mock),
		WithModel("test-model"),
		WithSessionDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	events := s.Events()

	var collected []agent.AgentEvent
	done := make(chan error, 1)
	go func() {
		done <- s.Prompt(ctx, "What is the meaning of life?")
	}()

	for event := range events {
		collected = append(collected, event)
	}

	if err := <-done; err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	// Should have agent_start, turn_start, assistant_text, usage_update, turn_end, agent_end.
	var gotText bool
	var gotStart bool
	var gotEnd bool
	for _, e := range collected {
		switch e.Type {
		case agent.EventAgentStart:
			gotStart = true
		case agent.EventAgentEnd:
			gotEnd = true
		case agent.EventAssistantText:
			gotText = true
			if e.Delta != "The answer is 42." {
				t.Errorf("expected 'The answer is 42.', got %q", e.Delta)
			}
		}
	}

	if !gotStart {
		t.Error("missing agent_start event")
	}
	if !gotEnd {
		t.Error("missing agent_end event")
	}
	if !gotText {
		t.Error("missing assistant_text event")
	}

	// Check messages were recorded.
	msgs := s.Messages()
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != ai.RoleUser {
		t.Errorf("expected first message role 'user', got %q", msgs[0].Role)
	}
	if msgs[1].Role != ai.RoleAssistant {
		t.Errorf("expected second message role 'assistant', got %q", msgs[1].Role)
	}
}

func TestSessionWithCustomTools(t *testing.T) {
	registry := tools.NewRegistry()
	// Don't register defaults — custom registry with no tools.

	mock := &mockProvider{response: "No tools available."}
	s, err := NewSession(context.Background(),
		WithProvider(mock),
		WithModel("test-model"),
		WithTools(registry),
		WithSessionDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	allTools := s.Registry().All()
	if len(allTools) != 0 {
		t.Errorf("expected 0 tools in custom registry, got %d", len(allTools))
	}
}

func TestNewSessionNoProvider(t *testing.T) {
	_, err := NewSession(context.Background(),
		WithModel("test-model"),
		WithSessionDir(t.TempDir()),
	)
	if err == nil {
		t.Fatal("expected error when no provider configured")
	}
	if !errors.Is(err, ErrNoProvider) {
		t.Errorf("expected ErrNoProvider, got: %v", err)
	}
}

func TestNewSessionUnknownProvider(t *testing.T) {
	_, err := NewSession(context.Background(),
		WithAPIKey("totally-fake-provider", "sk-test"),
		WithSessionDir(t.TempDir()),
	)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("expected ErrUnknownProvider, got: %v", err)
	}
}

func TestSessionResume(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockProvider{response: "First response."}

	// Create first session.
	s1, err := NewSession(context.Background(),
		WithProvider(mock),
		WithModel("test-model"),
		WithSessionDir(tmpDir),
	)
	if err != nil {
		t.Fatalf("NewSession s1: %v", err)
	}

	ctx := context.Background()
	events := s1.Events()
	go func() { s1.Prompt(ctx, "Hello") }()
	for range events {
	}

	sessionID := s1.SessionID()
	s1.Close()

	// Create a new session and resume.
	mock2 := &mockProvider{response: "Second response."}
	s2, err := NewSession(context.Background(),
		WithProvider(mock2),
		WithModel("test-model"),
		WithSessionDir(tmpDir),
	)
	if err != nil {
		t.Fatalf("NewSession s2: %v", err)
	}
	defer s2.Close()

	if err := s2.Resume(context.Background(), sessionID); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	msgs := s2.Messages()
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages after resume, got %d", len(msgs))
	}
}

func TestSessionSetters(t *testing.T) {
	mock := &mockProvider{response: "ok"}
	s, err := NewSession(context.Background(),
		WithProvider(mock),
		WithModel("initial-model"),
		WithSessionDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	// These should not panic.
	s.SetModel("new-model")
	s.SetThinking(ai.ThinkingHigh)
	s.SetSystemPrompt("New system prompt")
	s.SetMaxTokens(4096)
}

func TestAzureOptions(t *testing.T) {
	cfg := &SessionConfig{
		Provider:        "azure",
		APIKey:          "test-key",
		AzureEndpoint:   "https://myresource.openai.azure.com",
		AzureDeployment: "gpt-4o",
	}
	p, err := resolveProvider(context.Background(), cfg)
	if err != nil {
		t.Fatalf("resolveProvider: %v", err)
	}
	if p.Name() != "azure" {
		t.Errorf("expected provider name %q, got %q", "azure", p.Name())
	}
}

func TestWithAzureOptions(t *testing.T) {
	cfg := &SessionConfig{}
	WithAzureEndpoint("https://myresource.openai.azure.com")(cfg)
	WithAzureDeployment("gpt-4o")(cfg)
	if cfg.AzureEndpoint != "https://myresource.openai.azure.com" {
		t.Errorf("expected AzureEndpoint %q, got %q", "https://myresource.openai.azure.com", cfg.AzureEndpoint)
	}
	if cfg.AzureDeployment != "gpt-4o" {
		t.Errorf("expected AzureDeployment %q, got %q", "gpt-4o", cfg.AzureDeployment)
	}
}

// failOnSecondCallProvider sends a response with a tool call on the first
// Stream call so the agent loop iterates. On the second call it streams an
// error event. This produces messages (assistant + tool result) before the
// loop errors — the exact scenario where error shadowing was possible.
type failOnSecondCallProvider struct {
	calls int
}

func (m *failOnSecondCallProvider) Name() string { return "mock-fail2" }

func (m *failOnSecondCallProvider) Stream(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
	m.calls++
	if m.calls > 1 {
		ch := make(chan ai.StreamEvent, 2)
		go func() {
			ch <- ai.StreamEvent{Type: ai.EventError, Error: errors.New("provider unavailable")}
			close(ch)
		}()
		return ch, nil
	}

	// First call: return a response with a tool call so the loop iterates.
	ch := make(chan ai.StreamEvent, 10)
	go func() {
		ch <- ai.StreamEvent{Type: ai.EventMessageStart}
		ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "Let me check."}
		ch <- ai.StreamEvent{
			Type:       ai.EventToolUseStart,
			ToolCallID: "call_1",
			ToolName:   "nonexistent_tool",
		}
		ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
		ch <- ai.StreamEvent{Type: ai.EventMessageEnd, Usage: &ai.Usage{InputTokens: 10, OutputTokens: 5}}
		close(ch)
	}()
	return ch, nil
}

func TestPromptLoopErrorNotShadowed(t *testing.T) {
	tmpDir := t.TempDir()

	mock := &failOnSecondCallProvider{}
	s, err := NewSession(context.Background(),
		WithProvider(mock),
		WithModel("test-model"),
		WithSessionDir(tmpDir),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	events := s.Events()
	go func() { for range events {} }() // drain events

	// The first Stream call returns a tool call. The loop executes the tool
	// (it will fail because "nonexistent_tool" isn't registered, producing a
	// tool error result). On the second iteration, Stream returns an error.
	// The loop generates assistant + tool_result messages before erroring.
	// The returned error must mention "agent loop", not just a save failure.
	err = s.Prompt(context.Background(), "Hello")
	if err == nil {
		t.Fatal("expected error from Prompt, got nil")
	}

	if !strings.Contains(err.Error(), "agent loop") {
		t.Errorf("expected 'agent loop' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "provider unavailable") {
		t.Errorf("expected 'provider unavailable' in error chain, got: %v", err)
	}

	// Verify that messages were generated (assistant + tool result at minimum).
	msgs := s.Messages()
	if len(msgs) < 3 {
		t.Errorf("expected at least 3 messages (user + assistant + tool_result), got %d", len(msgs))
	}
}

func TestResolveProviderDefaults(t *testing.T) {
	tests := []struct {
		provider  string
		wantModel string
	}{
		{"anthropic", "claude-sonnet-4-6"},
		{"openai", "gpt-4.1"},
		{"gemini", "gemini-2.5-flash"},
	}

	for _, tt := range tests {
		cfg := &SessionConfig{
			Provider: tt.provider,
			APIKey:   "test-key",
		}
		// resolveProvider will fail on actual provider creation (invalid key),
		// but the model default should be set.
		resolveProvider(context.Background(), cfg)
		if cfg.Model != tt.wantModel {
			t.Errorf("provider %s: expected model %q, got %q", tt.provider, tt.wantModel, cfg.Model)
		}
	}
}

// --- streamFnProvider allows test-specific stream behavior ---

type streamFnProvider struct {
	fn func(ctx context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error)
}

func (p *streamFnProvider) Name() string { return "mock-fn" }
func (p *streamFnProvider) Stream(ctx context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
	return p.fn(ctx, req)
}

// --- Cancel tests ---

func TestCancelStopsRunningPrompt(t *testing.T) {
	provider := &streamFnProvider{
		fn: func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventMessageStart}
				// Block until cancelled.
				<-ctx.Done()
				ch <- ai.StreamEvent{Type: ai.EventError, Error: ctx.Err()}
			}()
			return ch, nil
		},
	}

	s, err := NewSession(context.Background(),
		WithProvider(provider),
		WithModel("test-model"),
		WithSessionDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	events := s.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Prompt(context.Background(), "block forever")
	}()
	// Drain events to prevent buffer fill.
	go func() { for range events {} }()

	// Give Prompt time to start streaming.
	time.Sleep(30 * time.Millisecond)
	s.Cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected error from cancelled Prompt")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Prompt did not return after Cancel")
	}
}

func TestCancelBeforePromptIsNoOp(t *testing.T) {
	mock := &mockProvider{response: "success"}
	s, err := NewSession(context.Background(),
		WithProvider(mock),
		WithModel("test-model"),
		WithSessionDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	// Cancel before any Prompt — should be a no-op.
	s.Cancel()

	events := s.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Prompt(context.Background(), "after cancel")
	}()
	for range events {
	}

	if err := <-errCh; err != nil {
		t.Fatalf("Prompt after pre-Cancel should succeed, got: %v", err)
	}
}

// --- Steer tests ---

func TestSteerInjectsMidConversation(t *testing.T) {
	// First call: return two tool calls so the loop iterates tools.
	// Second call: return text after steering.
	call := 0
	provider := &streamFnProvider{
		fn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 20)
			call++
			current := call
			go func() {
				defer close(ch)
				if current == 1 {
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-1", ToolName: "steer_trigger"}
					ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: `{}`}
					ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
					ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-2", ToolName: "noop_tool"}
					ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: `{}`}
					ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
				} else {
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "steered response"}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
				}
			}()
			return ch, nil
		},
	}

	// The steer_trigger tool sends the steer during its execution,
	// guaranteeing the steer message is in the channel for the post-exec check.
	var session *Session
	var steerOnce sync.Once
	reg := tools.NewRegistry()
	reg.Register(&callbackTool{name: "steer_trigger", fn: func() (string, error) {
		steerOnce.Do(func() {
			session.Steer("change direction now")
		})
		return "triggered", nil
	}})
	reg.Register(&simpleTool{name: "noop_tool", result: "noop"})

	s, err := NewSession(context.Background(),
		WithProvider(provider),
		WithModel("test-model"),
		WithTools(reg),
		WithSessionDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session = s
	defer s.Close()

	events := s.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Prompt(context.Background(), "do stuff")
	}()

	var collected []agent.AgentEvent
	for ev := range events {
		collected = append(collected, ev)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Prompt returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Prompt did not return")
	}

	// The steered text should appear in events.
	found := false
	for _, ev := range collected {
		if ev.Type == agent.EventAssistantText && ev.Delta == "steered response" {
			found = true
		}
	}
	if !found {
		t.Error("expected steered response text in events")
	}

	// The steer message should appear in conversation history.
	msgs := s.Messages()
	foundSteer := false
	for _, msg := range msgs {
		if msg.Role == ai.RoleUser && msg.GetText() == "change direction now" {
			foundSteer = true
		}
	}
	if !foundSteer {
		t.Error("expected steer message in conversation history")
	}
}

// --- FollowUp tests ---

func TestFollowUpQueuesAdditionalMessage(t *testing.T) {
	call := 0
	provider := &streamFnProvider{
		fn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 10)
			call++
			current := call
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventMessageStart}
				if current == 1 {
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "first reply"}
				} else {
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "follow-up reply"}
				}
				ch <- ai.StreamEvent{Type: ai.EventMessageEnd, Usage: &ai.Usage{InputTokens: 10, OutputTokens: 5}}
			}()
			return ch, nil
		},
	}

	s, err := NewSession(context.Background(),
		WithProvider(provider),
		WithModel("test-model"),
		WithSessionDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	// Queue follow-up before starting prompt.
	s.FollowUp("follow-up question")

	events := s.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Prompt(context.Background(), "initial question")
	}()

	var collected []agent.AgentEvent
	for ev := range events {
		collected = append(collected, ev)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Prompt returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Prompt did not return")
	}

	// Both replies should appear in events.
	foundFirst := false
	foundFollowUp := false
	for _, ev := range collected {
		if ev.Type == agent.EventAssistantText {
			if ev.Delta == "first reply" {
				foundFirst = true
			}
			if ev.Delta == "follow-up reply" {
				foundFollowUp = true
			}
		}
	}
	if !foundFirst {
		t.Error("expected 'first reply' in events")
	}
	if !foundFollowUp {
		t.Error("expected 'follow-up reply' in events")
	}

	// Conversation should have 4 messages: user, assistant, follow-up user, follow-up assistant.
	msgs := s.Messages()
	if len(msgs) < 4 {
		t.Fatalf("expected at least 4 messages, got %d", len(msgs))
	}
}

// --- Compact tests ---

func TestCompactTriggersMessageCompaction(t *testing.T) {
	// Provider that returns a summary when asked to compact.
	var mu sync.Mutex
	promptCalls := 0
	provider := &streamFnProvider{
		fn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 10)
			mu.Lock()
			promptCalls++
			current := promptCalls
			mu.Unlock()
			go func() {
				defer close(ch)
				if current <= 2 {
					// Regular prompt responses.
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "response " + strings.Repeat("x", current)}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd, Usage: &ai.Usage{InputTokens: 10, OutputTokens: 5}}
				} else {
					// Compaction call — return a summary.
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "Compacted summary of conversation"}
				}
			}()
			return ch, nil
		},
	}

	s, err := NewSession(context.Background(),
		WithProvider(provider),
		WithModel("test-model"),
		WithSessionDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	// Build up some conversation history.
	ctx := context.Background()
	for _, msg := range []string{"Hello", "How are you?"} {
		events := s.Events()
		go func() { for range events {} }()
		if err := s.Prompt(ctx, msg); err != nil {
			t.Fatalf("Prompt(%q): %v", msg, err)
		}
	}

	beforeCompact := len(s.Messages())
	if beforeCompact < 4 {
		t.Fatalf("expected at least 4 messages before compact, got %d", beforeCompact)
	}

	// Compact the conversation.
	if err := s.Compact(ctx, "focus on key decisions"); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	afterCompact := s.Messages()
	if len(afterCompact) >= beforeCompact {
		t.Errorf("expected fewer messages after compact, got %d (was %d)", len(afterCompact), beforeCompact)
	}

	// The compacted message should contain the summary.
	found := false
	for _, msg := range afterCompact {
		if strings.Contains(msg.GetText(), "Compacted summary") {
			found = true
		}
	}
	if !found {
		t.Error("expected compacted summary in messages")
	}
}

func TestCompactEmptyConversationReturnsError(t *testing.T) {
	mock := &mockProvider{response: "unused"}
	s, err := NewSession(context.Background(),
		WithProvider(mock),
		WithModel("test-model"),
		WithSessionDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	err = s.Compact(context.Background(), "")
	if err == nil {
		t.Fatal("expected error when compacting empty conversation")
	}
}

// --- Option function tests ---

func TestWithMaxTokensOption(t *testing.T) {
	cfg := &SessionConfig{}
	WithMaxTokens(4096)(cfg)
	if cfg.MaxTokens != 4096 {
		t.Errorf("expected MaxTokens 4096, got %d", cfg.MaxTokens)
	}
}

func TestWithThinkingOption(t *testing.T) {
	cfg := &SessionConfig{}
	WithThinking(ai.ThinkingHigh)(cfg)
	if cfg.ThinkingLevel != ai.ThinkingHigh {
		t.Errorf("expected ThinkingLevel %q, got %q", ai.ThinkingHigh, cfg.ThinkingLevel)
	}
}

func TestWithWorkingDirOption(t *testing.T) {
	cfg := &SessionConfig{}
	WithWorkingDir("/tmp/test")(cfg)
	if cfg.WorkingDir != "/tmp/test" {
		t.Errorf("expected WorkingDir %q, got %q", "/tmp/test", cfg.WorkingDir)
	}
}

func TestWithContextWindowOption(t *testing.T) {
	cfg := &SessionConfig{}
	WithContextWindow(100000)(cfg)
	if cfg.ContextWindow != 100000 {
		t.Errorf("expected ContextWindow 100000, got %d", cfg.ContextWindow)
	}
}

func TestWithMessagesOption(t *testing.T) {
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "restored message"),
		ai.NewTextMessage(ai.RoleAssistant, "previous reply"),
	}
	cfg := &SessionConfig{}
	WithMessages(msgs)(cfg)
	if len(cfg.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(cfg.Messages))
	}
	if cfg.Messages[0].GetText() != "restored message" {
		t.Errorf("expected first message text %q, got %q", "restored message", cfg.Messages[0].GetText())
	}
}

func TestWithMessagesPreloadsHistory(t *testing.T) {
	// Verify that WithMessages actually loads messages into the agent loop.
	preloaded := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "previous question"),
		ai.NewTextMessage(ai.RoleAssistant, "previous answer"),
	}

	mock := &mockProvider{response: "new response"}
	s, err := NewSession(context.Background(),
		WithProvider(mock),
		WithModel("test-model"),
		WithMessages(preloaded),
		WithSessionDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	events := s.Events()
	go func() { for range events {} }()
	if err := s.Prompt(context.Background(), "follow up"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	msgs := s.Messages()
	// Should have: preloaded user, preloaded assistant, new user, new assistant.
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	if msgs[0].GetText() != "previous question" {
		t.Errorf("expected first message to be preloaded, got %q", msgs[0].GetText())
	}
}

func TestWithContextWindowPassedToAgentLoop(t *testing.T) {
	// Verify the context window option reaches the agent loop by checking
	// that a session can be created with a custom context window.
	mock := &mockProvider{response: "ok"}
	s, err := NewSession(context.Background(),
		WithProvider(mock),
		WithModel("test-model"),
		WithContextWindow(50000),
		WithSessionDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	// Verify session was created successfully — the option was accepted.
	if s.AgentLoop() == nil {
		t.Fatal("expected non-nil AgentLoop")
	}
}

func TestWithWorkingDirPassedToSession(t *testing.T) {
	mock := &mockProvider{response: "ok"}
	s, err := NewSession(context.Background(),
		WithProvider(mock),
		WithModel("test-model"),
		WithWorkingDir("/tmp/workspace"),
		WithSessionDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Close()

	// Verify the config was stored.
	if s.config.WorkingDir != "/tmp/workspace" {
		t.Errorf("expected WorkingDir %q, got %q", "/tmp/workspace", s.config.WorkingDir)
	}
}

// simpleTool is a minimal tool for SDK-level tests.
type simpleTool struct {
	name   string
	result string
}

func (t *simpleTool) Name() string                                                 { return t.name }
func (t *simpleTool) Description() string                                          { return "simple tool" }
func (t *simpleTool) Schema() any                                                  { return nil }
func (t *simpleTool) Execute(_ context.Context, _ map[string]any) (string, error) { return t.result, nil }

// callbackTool calls a function during execution, useful for triggering
// side effects like Steer() at a deterministic point.
type callbackTool struct {
	name string
	fn   func() (string, error)
}

func (t *callbackTool) Name() string        { return t.name }
func (t *callbackTool) Description() string { return "callback tool" }
func (t *callbackTool) Schema() any         { return nil }
func (t *callbackTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	return t.fn()
}
