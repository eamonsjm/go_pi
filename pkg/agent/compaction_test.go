package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

func TestCompactEmptyMessages(t *testing.T) {
	provider := &mockProvider{}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg)

	err := a.Compact(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
	if err.Error() != "no messages to compact" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCompactBasicTranscript(t *testing.T) {
	// Set up a provider that captures the request and returns a summary.
	var capturedReq ai.StreamRequest
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			capturedReq = req
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "Summary of conversation"}
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg,
		WithSystemPrompt("test system prompt"),
		WithModel("test-model"),
		WithMessages([]ai.Message{
			ai.NewTextMessage(ai.RoleUser, "Hello"),
			ai.NewTextMessage(ai.RoleAssistant, "Hi there"),
			ai.NewTextMessage(ai.RoleUser, "How are you?"),
		}),
	)

	// Subscribe to events before compacting.
	ch := a.Events()

	err := a.Compact(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The compaction request should use the configured model and system prompt.
	if capturedReq.Model != "test-model" {
		t.Errorf("expected model 'test-model', got %q", capturedReq.Model)
	}
	if capturedReq.SystemPrompt != "test system prompt" {
		t.Errorf("expected system prompt preserved, got %q", capturedReq.SystemPrompt)
	}

	// The compaction prompt should contain the conversation transcript.
	if len(capturedReq.Messages) != 1 {
		t.Fatalf("expected 1 compaction message, got %d", len(capturedReq.Messages))
	}
	prompt := capturedReq.Messages[0].GetText()
	if !strings.Contains(prompt, "[user]: Hello") {
		t.Error("transcript missing user message 'Hello'")
	}
	if !strings.Contains(prompt, "[assistant]: Hi there") {
		t.Error("transcript missing assistant message 'Hi there'")
	}
	if !strings.Contains(prompt, "[user]: How are you?") {
		t.Error("transcript missing user message 'How are you?'")
	}

	// After compaction, messages should be replaced with a single summary.
	msgs := a.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after compaction, got %d", len(msgs))
	}
	if msgs[0].Role != ai.RoleUser {
		t.Errorf("expected compacted message role user, got %s", msgs[0].Role)
	}
	text := msgs[0].GetText()
	if !strings.Contains(text, "Summary of conversation") {
		t.Errorf("compacted message missing summary text, got %q", text)
	}
	if !strings.Contains(text, "summary of the conversation so far") {
		t.Error("compacted message missing context prefix")
	}

	// A compaction event should have been emitted.
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	found := false
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if !found {
					t.Error("expected compaction event")
				}
				return
			}
			if ev.Type == EventCompaction {
				found = true
				if ev.Delta != "Summary of conversation" {
					t.Errorf("expected compaction delta 'Summary of conversation', got %q", ev.Delta)
				}
			}
		case <-timer.C:
			if !found {
				t.Error("timed out waiting for compaction event")
			}
			return
		}
	}
}

func TestCompactWithInstructions(t *testing.T) {
	var capturedPrompt string
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			capturedPrompt = req.Messages[0].GetText()
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "focused summary"}
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg, WithMessages([]ai.Message{
		ai.NewTextMessage(ai.RoleUser, "discuss code"),
	}))

	err := a.Compact(context.Background(), "focus on code changes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(capturedPrompt, "Additional focus: focus on code changes") {
		t.Errorf("expected instructions in prompt, got %q", capturedPrompt)
	}
}

func TestCompactToolResultTruncation(t *testing.T) {
	// Tool results longer than 500 chars should be truncated in the transcript.
	var capturedPrompt string
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			capturedPrompt = req.Messages[0].GetText()
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "compacted"}
			}()
			return ch, nil
		},
	}

	// Create a tool result with content exceeding 500 chars.
	longContent := strings.Repeat("x", 600)
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "run tool"),
		{
			Role: ai.RoleAssistant,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeToolUse, ToolUseID: "tc-1", ToolName: "read_file"},
			},
		},
		{
			Role: ai.RoleUser,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeToolResult, ToolResultID: "tc-1", Content: longContent},
			},
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg, WithMessages(msgs))

	err := a.Compact(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The transcript should contain a truncated tool result (500 chars + "...").
	if !strings.Contains(capturedPrompt, "[tool_result]:") {
		t.Error("transcript missing tool_result")
	}
	// Should NOT contain the full 600-char content.
	if strings.Contains(capturedPrompt, longContent) {
		t.Error("expected tool result to be truncated, but found full content")
	}
	// Should contain the truncation marker.
	if !strings.Contains(capturedPrompt, "...") {
		t.Error("expected truncation marker '...' in tool result")
	}
	// The truncated portion should be exactly 500 chars.
	truncated := strings.Repeat("x", 500) + "..."
	if !strings.Contains(capturedPrompt, truncated) {
		t.Error("expected exactly 500-char truncation with '...' suffix")
	}
}

func TestCompactToolResultAtBoundary(t *testing.T) {
	// Tool result with exactly 500 chars should NOT be truncated.
	var capturedPrompt string
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			capturedPrompt = req.Messages[0].GetText()
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "compacted"}
			}()
			return ch, nil
		},
	}

	exactContent := strings.Repeat("y", 500)
	msgs := []ai.Message{
		{
			Role: ai.RoleUser,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeToolResult, ToolResultID: "tc-1", Content: exactContent},
			},
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg, WithMessages(msgs))

	err := a.Compact(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 500 chars exactly — should NOT be truncated.
	if strings.Contains(capturedPrompt, "...") {
		t.Error("500-char content should not be truncated")
	}
	if !strings.Contains(capturedPrompt, exactContent) {
		t.Error("exact 500-char content should appear in full")
	}
}

func TestCompactToolResultJustOverBoundary(t *testing.T) {
	// Tool result with 501 chars should be truncated.
	var capturedPrompt string
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			capturedPrompt = req.Messages[0].GetText()
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "compacted"}
			}()
			return ch, nil
		},
	}

	overContent := strings.Repeat("z", 501)
	msgs := []ai.Message{
		{
			Role: ai.RoleUser,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeToolResult, ToolResultID: "tc-1", Content: overContent},
			},
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg, WithMessages(msgs))

	err := a.Compact(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(capturedPrompt, strings.Repeat("z", 500)+"...") {
		t.Error("501-char content should be truncated to 500 + '...'")
	}
	if strings.Contains(capturedPrompt, overContent) {
		t.Error("full 501-char content should not appear")
	}
}

func TestCompactToolUseWithoutMatchingResult(t *testing.T) {
	// A tool_use block with no corresponding tool_result should still
	// appear in the transcript without error.
	var capturedPrompt string
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			capturedPrompt = req.Messages[0].GetText()
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "compacted"}
			}()
			return ch, nil
		},
	}

	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "do something"),
		{
			Role: ai.RoleAssistant,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeToolUse, ToolUseID: "tc-orphan", ToolName: "read_file"},
			},
		},
		// No tool_result message follows — this is the edge case.
		ai.NewTextMessage(ai.RoleAssistant, "never mind"),
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg, WithMessages(msgs))

	err := a.Compact(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(capturedPrompt, "[tool_call]: read_file") {
		t.Error("transcript missing tool_call for orphan tool_use")
	}
	if !strings.Contains(capturedPrompt, "[assistant]: never mind") {
		t.Error("transcript missing subsequent assistant message")
	}
}

func TestCompactMultiBlockMessage(t *testing.T) {
	// A message with both text and tool_use content blocks should produce
	// both [role]: text and [tool_call]: entries in the transcript.
	var capturedPrompt string
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			capturedPrompt = req.Messages[0].GetText()
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "compacted"}
			}()
			return ch, nil
		},
	}

	msgs := []ai.Message{
		{
			Role: ai.RoleAssistant,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeText, Text: "Let me check that file"},
				{Type: ai.ContentTypeToolUse, ToolUseID: "tc-1", ToolName: "read_file"},
				{Type: ai.ContentTypeToolUse, ToolUseID: "tc-2", ToolName: "write_file"},
			},
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg, WithMessages(msgs))

	err := a.Compact(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(capturedPrompt, "[assistant]: Let me check that file") {
		t.Error("transcript missing text block from multi-block message")
	}
	if !strings.Contains(capturedPrompt, "[tool_call]: read_file") {
		t.Error("transcript missing first tool_call from multi-block message")
	}
	if !strings.Contains(capturedPrompt, "[tool_call]: write_file") {
		t.Error("transcript missing second tool_call from multi-block message")
	}
}

func TestCompactPreservesContext(t *testing.T) {
	// Verify the compacted message wraps the summary with the context prefix
	// so downstream code knows it's a compacted summary.
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "Key decisions: use Go. Files: main.go."}
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg, WithMessages([]ai.Message{
		ai.NewTextMessage(ai.RoleUser, "some conversation"),
	}))

	err := a.Compact(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := a.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 compacted message, got %d", len(msgs))
	}

	text := msgs[0].GetText()
	prefix := "[This is a summary of the conversation so far, generated by context compaction]"
	if !strings.HasPrefix(text, prefix) {
		t.Errorf("compacted message should start with context prefix, got %q", text[:min(len(text), 100)])
	}
	if !strings.Contains(text, "Key decisions: use Go. Files: main.go.") {
		t.Error("compacted message missing the LLM-generated summary")
	}
}

func TestCompactEmptySummaryError(t *testing.T) {
	// If the provider returns no text, Compact should error.
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				// No text events — empty summary.
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg, WithMessages([]ai.Message{
		ai.NewTextMessage(ai.RoleUser, "hello"),
	}))

	err := a.Compact(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty summary")
	}
	if !strings.Contains(err.Error(), "empty summary") {
		t.Errorf("expected 'empty summary' error, got %v", err)
	}

	// Messages should be unchanged since compaction failed.
	msgs := a.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected messages unchanged, got %d", len(msgs))
	}
	if msgs[0].GetText() != "hello" {
		t.Error("messages should be unchanged after failed compaction")
	}
}

func TestCompactStreamError(t *testing.T) {
	// If the provider stream returns an error event, Compact should propagate it.
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventError, Error: fmt.Errorf("rate limit exceeded")}
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg, WithMessages([]ai.Message{
		ai.NewTextMessage(ai.RoleUser, "hello"),
	}))

	err := a.Compact(context.Background(), "")
	if err == nil {
		t.Fatal("expected error from stream")
	}
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("expected rate limit error, got %v", err)
	}
}

func TestCompactProviderStartError(t *testing.T) {
	// If the provider.Stream call itself fails, Compact should propagate it.
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg, WithMessages([]ai.Message{
		ai.NewTextMessage(ai.RoleUser, "hello"),
	}))

	err := a.Compact(context.Background(), "")
	if err == nil {
		t.Fatal("expected error from stream start")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("expected connection refused error, got %v", err)
	}
}

func TestCompactThinkingBlocksExcludedFromTranscript(t *testing.T) {
	// Thinking blocks have no text (GetText returns ""), and no tool_use/tool_result
	// type, so they should not appear in the transcript. Verify no crash.
	var capturedPrompt string
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			capturedPrompt = req.Messages[0].GetText()
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "compacted"}
			}()
			return ch, nil
		},
	}

	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "think about this"),
		{
			Role: ai.RoleAssistant,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeThinking, Thinking: "internal reasoning"},
				{Type: ai.ContentTypeText, Text: "here's my answer"},
			},
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg, WithMessages(msgs))

	err := a.Compact(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The thinking content should NOT appear in the transcript (it's not
	// handled by the text/tool_use/tool_result switch).
	if strings.Contains(capturedPrompt, "internal reasoning") {
		t.Error("thinking content should not appear in compaction transcript")
	}
	// The text content should appear.
	if !strings.Contains(capturedPrompt, "[assistant]: here's my answer") {
		t.Error("transcript missing assistant text")
	}
}

func TestCompactDoesNotMutateOriginalMessages(t *testing.T) {
	// Compact takes a snapshot of messages via cloneMessages. Verify the
	// original slice reference is fully replaced, not mutated.
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "summary"}
			}()
			return ch, nil
		},
	}

	original := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "first"),
		ai.NewTextMessage(ai.RoleAssistant, "second"),
	}
	// Keep a reference to verify no mutation.
	originalCopy := make([]ai.Message, len(original))
	copy(originalCopy, original)

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg, WithMessages(original))

	err := a.Compact(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The original slice passed to WithMessages should not be affected
	// (WithMessages copies it in).
	if len(original) != 2 {
		t.Error("original slice was mutated")
	}
	if original[0].GetText() != "first" || original[1].GetText() != "second" {
		t.Error("original message contents were mutated")
	}
}

