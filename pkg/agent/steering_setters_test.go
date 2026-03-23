package agent

import (
	"context"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

// --- addSteeringSkipResults unit tests --------------------------------------

func TestAddSteeringSkipResults_EmptySkipAfterID(t *testing.T) {
	// When skipAfterID is empty, no skip results should be added.
	a := NewAgentLoop(&mockProvider{streamFn: textResponse("ok")}, tools.NewRegistry())

	toolCalls := []ai.ContentBlock{
		{Type: ai.ContentTypeToolUse, ToolUseID: "tc-1", ToolName: "foo"},
		{Type: ai.ContentTypeToolUse, ToolUseID: "tc-2", ToolName: "bar"},
	}

	before := len(a.Messages())
	a.addSteeringSkipResults(context.Background(), toolCalls, "")
	after := len(a.Messages())

	if after != before {
		t.Errorf("expected no messages added for empty skipAfterID, got %d new messages", after-before)
	}
}

func TestAddSteeringSkipResults_SingleToolSkipped(t *testing.T) {
	// When skipAfterID matches the only tool call, that tool gets a skip result.
	a := NewAgentLoop(&mockProvider{streamFn: textResponse("ok")}, tools.NewRegistry())

	toolCalls := []ai.ContentBlock{
		{Type: ai.ContentTypeToolUse, ToolUseID: "tc-1", ToolName: "foo"},
	}

	a.addSteeringSkipResults(context.Background(), toolCalls, "tc-1")

	msgs := a.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 skip result message, got %d", len(msgs))
	}

	cb := msgs[0].Content[0]
	if cb.Type != ai.ContentTypeToolResult {
		t.Errorf("expected tool_result, got %s", cb.Type)
	}
	if cb.ToolResultID != "tc-1" {
		t.Errorf("expected ToolResultID 'tc-1', got %q", cb.ToolResultID)
	}
	if !cb.IsError {
		t.Error("skip result should be marked as error")
	}
}

func TestAddSteeringSkipResults_MultipleToolsAfterBoundary(t *testing.T) {
	// When skipAfterID matches tc-2, then tc-2, tc-3, tc-4 all get skip results.
	// tc-1 (before the boundary) does NOT get a skip result.
	a := NewAgentLoop(&mockProvider{streamFn: textResponse("ok")}, tools.NewRegistry())

	toolCalls := []ai.ContentBlock{
		{Type: ai.ContentTypeToolUse, ToolUseID: "tc-1", ToolName: "already-done"},
		{Type: ai.ContentTypeToolUse, ToolUseID: "tc-2", ToolName: "skipped-first"},
		{Type: ai.ContentTypeToolUse, ToolUseID: "tc-3", ToolName: "skipped-second"},
		{Type: ai.ContentTypeToolUse, ToolUseID: "tc-4", ToolName: "skipped-third"},
	}

	a.addSteeringSkipResults(context.Background(), toolCalls, "tc-2")

	msgs := a.Messages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 skip result messages (tc-2, tc-3, tc-4), got %d", len(msgs))
	}

	expectedIDs := []string{"tc-2", "tc-3", "tc-4"}
	for i, msg := range msgs {
		cb := msg.Content[0]
		if cb.ToolResultID != expectedIDs[i] {
			t.Errorf("message %d: expected ToolResultID %q, got %q", i, expectedIDs[i], cb.ToolResultID)
		}
		if !cb.IsError {
			t.Errorf("message %d: skip result should be marked as error", i)
		}
	}
}

func TestAddSteeringSkipResults_NonexistentID(t *testing.T) {
	// When skipAfterID doesn't match any tool call, no skip results are added.
	a := NewAgentLoop(&mockProvider{streamFn: textResponse("ok")}, tools.NewRegistry())

	toolCalls := []ai.ContentBlock{
		{Type: ai.ContentTypeToolUse, ToolUseID: "tc-1", ToolName: "foo"},
		{Type: ai.ContentTypeToolUse, ToolUseID: "tc-2", ToolName: "bar"},
	}

	a.addSteeringSkipResults(context.Background(), toolCalls, "nonexistent")

	msgs := a.Messages()
	if len(msgs) != 0 {
		t.Errorf("expected no messages for nonexistent skipAfterID, got %d", len(msgs))
	}
}

func TestAddSteeringSkipResults_EmitEvents(t *testing.T) {
	// Verify that skip results emit EventToolResult events.
	a := NewAgentLoop(&mockProvider{streamFn: textResponse("ok")}, tools.NewRegistry())

	toolCalls := []ai.ContentBlock{
		{Type: ai.ContentTypeToolUse, ToolUseID: "tc-1", ToolName: "skipped"},
		{Type: ai.ContentTypeToolUse, ToolUseID: "tc-2", ToolName: "also-skipped"},
	}

	ch := a.Events()
	a.addSteeringSkipResults(context.Background(), toolCalls, "tc-1")

	// Drain events.
	var events []AgentEvent
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				goto done
			}
			events = append(events, ev)
		case <-timeout:
			goto done
		}
	}
done:

	count := 0
	for _, ev := range events {
		if ev.Type == EventToolResult {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 EventToolResult events, got %d", count)
	}
}

// --- setter method tests ----------------------------------------------------

func TestSetModel(t *testing.T) {
	a := NewAgentLoop(&mockProvider{streamFn: textResponse("ok")}, tools.NewRegistry())

	// Verify default.
	if a.model != "" {
		t.Errorf("expected empty default model, got %q", a.model)
	}

	a.SetModel("claude-3-opus")
	a.mu.Lock()
	got := a.model
	a.mu.Unlock()

	if got != "claude-3-opus" {
		t.Errorf("expected model 'claude-3-opus', got %q", got)
	}
}

func TestSetThinking(t *testing.T) {
	a := NewAgentLoop(&mockProvider{streamFn: textResponse("ok")}, tools.NewRegistry())

	// Default is ThinkingOff.
	a.mu.Lock()
	before := a.thinking
	a.mu.Unlock()
	if before != ai.ThinkingOff {
		t.Errorf("expected default ThinkingOff, got %s", before)
	}

	a.SetThinking(ai.ThinkingHigh)

	a.mu.Lock()
	after := a.thinking
	a.mu.Unlock()
	if after != ai.ThinkingHigh {
		t.Errorf("expected ThinkingHigh, got %s", after)
	}
}

func TestSetMaxTokens(t *testing.T) {
	a := NewAgentLoop(&mockProvider{streamFn: textResponse("ok")}, tools.NewRegistry())

	// Default is 8192.
	a.mu.Lock()
	before := a.maxTokens
	a.mu.Unlock()
	if before != 8192 {
		t.Errorf("expected default maxTokens 8192, got %d", before)
	}

	a.SetMaxTokens(16384)

	a.mu.Lock()
	after := a.maxTokens
	a.mu.Unlock()
	if after != 16384 {
		t.Errorf("expected maxTokens 16384, got %d", after)
	}
}

func TestSetSystemPrompt(t *testing.T) {
	a := NewAgentLoop(&mockProvider{streamFn: textResponse("ok")}, tools.NewRegistry())

	// Default is empty.
	a.mu.Lock()
	before := a.systemPrompt
	a.mu.Unlock()
	if before != "" {
		t.Errorf("expected empty default systemPrompt, got %q", before)
	}

	a.SetSystemPrompt("You are a helpful assistant.")

	a.mu.Lock()
	after := a.systemPrompt
	a.mu.Unlock()
	if after != "You are a helpful assistant." {
		t.Errorf("expected updated systemPrompt, got %q", after)
	}
}

func TestProviderName_WithProvider(t *testing.T) {
	a := NewAgentLoop(&mockProvider{streamFn: textResponse("ok")}, tools.NewRegistry())

	name := a.ProviderName()
	if name != "mock" {
		t.Errorf("expected 'mock', got %q", name)
	}
}

func TestProviderName_NilProvider(t *testing.T) {
	a := NewAgentLoop(nil, tools.NewRegistry())

	name := a.ProviderName()
	if name != "" {
		t.Errorf("expected empty string for nil provider, got %q", name)
	}
}

func TestMetrics_ReturnsCollector(t *testing.T) {
	a := NewAgentLoop(&mockProvider{streamFn: textResponse("ok")}, tools.NewRegistry())

	m := a.Metrics()
	if m == nil {
		t.Fatal("Metrics() returned nil")
	}

	// The metrics instance should be the same one used internally.
	a.mu.Lock()
	internal := a.metrics
	a.mu.Unlock()
	if m != internal {
		t.Error("Metrics() returned a different instance than the internal metrics")
	}
}

func TestMetrics_AccumulatesUsageData(t *testing.T) {
	a := NewAgentLoop(&mockProvider{streamFn: textResponse("ok")}, tools.NewRegistry())

	m := a.Metrics()

	// Record some data and verify it accumulates.
	m.Record(tools.CategoryGit, 1000, 200, 0)
	m.Record(tools.CategoryGit, 500, 100, 0)

	cmdMetrics := m.GetCommandMetrics()
	gitMetrics := cmdMetrics[tools.CategoryGit]
	if gitMetrics == nil {
		t.Fatal("expected git metrics to be recorded")
	}
	if gitMetrics.Count != 2 {
		t.Errorf("expected count 2, got %d", gitMetrics.Count)
	}
	if gitMetrics.TotalBytes != 1500 {
		t.Errorf("expected TotalBytes 1500, got %d", gitMetrics.TotalBytes)
	}
}

func TestSetModel_UsedInSubsequentTurn(t *testing.T) {
	// Verify that SetModel changes are reflected in the request sent to the provider.
	var capturedModel string
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			capturedModel = req.Model
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventMessageStart}
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "ok"}
				ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
			}()
			return ch, nil
		},
	}

	a := NewAgentLoop(provider, tools.NewRegistry())
	a.SetModel("test-model-v2")

	if err := a.Prompt(context.Background(), "hi"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	if capturedModel != "test-model-v2" {
		t.Errorf("expected model 'test-model-v2' in request, got %q", capturedModel)
	}
}

func TestSetThinking_UsedInSubsequentTurn(t *testing.T) {
	var capturedThinking ai.ThinkingLevel
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			capturedThinking = req.ThinkingLevel
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventMessageStart}
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "ok"}
				ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
			}()
			return ch, nil
		},
	}

	a := NewAgentLoop(provider, tools.NewRegistry())
	a.SetThinking(ai.ThinkingMedium)

	if err := a.Prompt(context.Background(), "hi"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	if capturedThinking != ai.ThinkingMedium {
		t.Errorf("expected ThinkingMedium in request, got %s", capturedThinking)
	}
}

func TestSetMaxTokens_UsedInSubsequentTurn(t *testing.T) {
	var capturedMaxTokens int
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			capturedMaxTokens = req.MaxTokens
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventMessageStart}
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "ok"}
				ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
			}()
			return ch, nil
		},
	}

	a := NewAgentLoop(provider, tools.NewRegistry())
	a.SetMaxTokens(2048)

	if err := a.Prompt(context.Background(), "hi"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	if capturedMaxTokens != 2048 {
		t.Errorf("expected MaxTokens 2048 in request, got %d", capturedMaxTokens)
	}
}

func TestSetSystemPrompt_UsedInSubsequentTurn(t *testing.T) {
	var capturedPrompt string
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			capturedPrompt = req.SystemPrompt
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventMessageStart}
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "ok"}
				ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
			}()
			return ch, nil
		},
	}

	a := NewAgentLoop(provider, tools.NewRegistry())
	a.SetSystemPrompt("Be concise.")

	if err := a.Prompt(context.Background(), "hi"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	if capturedPrompt != "Be concise." {
		t.Errorf("expected system prompt 'Be concise.' in request, got %q", capturedPrompt)
	}
}
