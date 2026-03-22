package sdk

import (
	"context"
	"errors"
	"strings"
	"testing"

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
