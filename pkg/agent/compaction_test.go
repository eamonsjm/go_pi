package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	if !errors.Is(err, errNoMessages) {
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

func TestCompactToolResultUTF8MultiByteTruncation(t *testing.T) {
	// Tool result containing multi-byte UTF-8 characters near the 500-byte
	// boundary must not produce invalid UTF-8 after truncation.
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

	// Build content: 498 ASCII bytes + a 4-byte emoji (U+1F600 "😀") = 502 bytes.
	// A naive result[:500] would slice into the middle of the emoji.
	content := strings.Repeat("a", 498) + "😀" + strings.Repeat("b", 100)
	msgs := []ai.Message{
		{
			Role: ai.RoleUser,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeToolResult, ToolResultID: "tc-1", Content: content},
			},
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg, WithMessages(msgs))

	err := a.Compact(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Extract the truncated tool result from the transcript.
	idx := strings.Index(capturedPrompt, "[tool_result]: ")
	if idx < 0 {
		t.Fatal("transcript missing [tool_result]")
	}
	resultPart := capturedPrompt[idx+len("[tool_result]: "):]
	// The result ends at the next newline.
	if nl := strings.Index(resultPart, "\n"); nl >= 0 {
		resultPart = resultPart[:nl]
	}

	// The truncated result must be valid UTF-8.
	if !utf8.ValidString(resultPart) {
		t.Errorf("truncated tool result is not valid UTF-8: %q", resultPart)
	}
	// Should contain the truncation marker.
	if !strings.Contains(resultPart, "...") {
		t.Error("expected truncation marker '...'")
	}
	// The emoji sits at byte 498-501; truncation should back up to byte 498.
	if !strings.HasPrefix(resultPart, strings.Repeat("a", 498)+"...") {
		t.Errorf("expected truncation at rune boundary before emoji, got: %q", resultPart[:20])
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

// ---------------------------------------------------------------------------
// Auto-compaction tests
// ---------------------------------------------------------------------------

func TestMaybeAutoCompactNotTriggeredBelowThreshold(t *testing.T) {
	provider := &mockProvider{}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg,
		WithContextWindow(200000),
		WithReserveTokens(16384),
	)

	// Set lastInputTokens below threshold.
	a.lastInputTokens = 100000 // well below 200000-16384 = 183616

	err := a.maybeAutoCompact(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No compaction should have occurred — messages unchanged.
}

func TestMaybeAutoCompactNotTriggeredWhenDisabled(t *testing.T) {
	provider := &mockProvider{}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg,
		WithContextWindow(0), // disabled
	)
	a.lastInputTokens = 999999

	err := a.maybeAutoCompact(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMaybeAutoCompactNotTriggeredNoUsageData(t *testing.T) {
	provider := &mockProvider{}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg,
		WithContextWindow(200000),
	)
	// lastInputTokens is 0 (no usage data yet).

	err := a.maybeAutoCompact(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMaybeAutoCompactSkippedTooFewMessages(t *testing.T) {
	// A single large tool result can exceed the token threshold, but
	// compaction on fewer than minAutoCompactMessages messages wastes
	// an LLM call without meaningful reduction.
	var compactionCalled bool
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			compactionCalled = true
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "should not happen"}
			}()
			return ch, nil
		},
	}

	// Create a conversation with fewer than minAutoCompactMessages messages
	// where one tool result is enormous.
	hugeTool := strings.Repeat("x", 800000) // ~200k tokens at 4 chars/token
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "read that file"),
		{
			Role: ai.RoleAssistant,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeToolUse, ToolUseID: "tc-1", ToolName: "read_file"},
			},
		},
		{
			Role: ai.RoleUser,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeToolResult, ToolResultID: "tc-1", Content: hugeTool},
			},
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg,
		WithContextWindow(200000),
		WithReserveTokens(16384),
		WithMessages(msgs),
	)

	// Simulate token count exceeding threshold.
	a.lastInputTokens = 190000

	err := a.maybeAutoCompact(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if compactionCalled {
		t.Error("compaction should NOT be called with too few messages")
	}
}

func TestAutoCompactTriggeredAboveThreshold(t *testing.T) {
	var compactionCalled bool
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			compactionCalled = true
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "Auto summary"}
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	// Use large old messages and a small keepRecentTokens budget so the
	// cut point falls between old and recent messages.
	a := NewAgentLoop(provider, reg,
		WithContextWindow(1000),
		WithReserveTokens(100),
		WithKeepRecentTokens(10), // ~40 chars budget — only fits the recent pair
		WithMessages([]ai.Message{
			ai.NewTextMessage(ai.RoleUser, strings.Repeat("old context ", 50)),
			ai.NewTextMessage(ai.RoleAssistant, strings.Repeat("old response ", 50)),
			ai.NewTextMessage(ai.RoleUser, "recent question"),
			ai.NewTextMessage(ai.RoleAssistant, "recent answer"),
		}),
	)

	// Set lastInputTokens above threshold (1000 - 100 = 900).
	a.lastInputTokens = 950

	ch := a.Events()
	err := a.maybeAutoCompact(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !compactionCalled {
		t.Fatal("expected compaction to be triggered")
	}

	// Verify auto-compaction event was emitted.
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	found := false
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				if !found {
					t.Error("expected auto-compaction event")
				}
				goto done
			}
			if ev.Type == EventAutoCompaction {
				found = true
				if ev.Delta != "Auto summary" {
					t.Errorf("expected 'Auto summary', got %q", ev.Delta)
				}
			}
		case <-timer.C:
			goto done
		}
	}
done:
	if !found {
		t.Error("expected EventAutoCompaction event")
	}

	// Messages should have compacted summary + recent messages.
	msgs := a.Messages()
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages (summary + recent), got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].GetText(), "Auto summary") {
		t.Errorf("first message should be the compaction summary, got %q", msgs[0].GetText())
	}

	// lastInputTokens should be reset to 0.
	a.mu.Lock()
	if a.lastInputTokens != 0 {
		t.Errorf("expected lastInputTokens reset to 0, got %d", a.lastInputTokens)
	}
	a.mu.Unlock()
}

func TestAutoCompactPreservesRecentMessages(t *testing.T) {
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "summary of old"}
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	// keepRecentTokens = 200 tokens ≈ 800 chars.
	// "recent question" = 15 chars, "recent answer" = 13 chars = 28 chars total.
	// This should be well within the budget, keeping both recent messages.
	a := NewAgentLoop(provider, reg,
		WithContextWindow(1000),
		WithReserveTokens(100),
		WithKeepRecentTokens(200),
		WithMessages([]ai.Message{
			ai.NewTextMessage(ai.RoleUser, strings.Repeat("old ", 300)),       // ~1200 chars
			ai.NewTextMessage(ai.RoleAssistant, strings.Repeat("resp ", 300)), // ~1500 chars
			ai.NewTextMessage(ai.RoleUser, "recent question"),
			ai.NewTextMessage(ai.RoleAssistant, "recent answer"),
		}),
	)
	a.lastInputTokens = 950

	err := a.maybeAutoCompact(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := a.Messages()
	// Should be: [summary, recent question, recent answer]
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].GetText(), "summary of old") {
		t.Error("first message should be compaction summary")
	}
	if msgs[1].GetText() != "recent question" {
		t.Errorf("expected 'recent question', got %q", msgs[1].GetText())
	}
	if msgs[2].GetText() != "recent answer" {
		t.Errorf("expected 'recent answer', got %q", msgs[2].GetText())
	}
}

func TestFindSafeCutPointDoesNotSplitToolPairs(t *testing.T) {
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "do something"),
		{
			Role: ai.RoleAssistant,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeText, Text: "I'll read the file"},
				{Type: ai.ContentTypeToolUse, ToolUseID: "tc-1", ToolName: "read_file"},
			},
		},
		{
			Role: ai.RoleUser,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeToolResult, ToolResultID: "tc-1", Content: "file contents here"},
			},
		},
		ai.NewTextMessage(ai.RoleAssistant, "here's what I found"),
		ai.NewTextMessage(ai.RoleUser, "thanks"),
	}

	// With a very small keepRecentTokens, the cut might land on the
	// tool_result message (index 2). It should be adjusted to index 1
	// (the assistant message with tool_use).
	cutIdx := findSafeCutPoint(msgs, 10) // very small budget

	// The cut should never land on index 2 (tool_result).
	if cutIdx == 2 {
		t.Error("cut point should not land on tool_result message")
	}

	// Messages from cutIdx onward should not start with a tool_result.
	if cutIdx < len(msgs) {
		msg := msgs[cutIdx]
		for _, c := range msg.Content {
			if c.Type == ai.ContentTypeToolResult {
				t.Errorf("cut point at index %d starts with tool_result", cutIdx)
				break
			}
		}
	}
}

func TestFindSafeCutPointConsecutiveToolPairs(t *testing.T) {
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "run two tools"),
		{
			Role: ai.RoleAssistant,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeToolUse, ToolUseID: "tc-1", ToolName: "tool1"},
				{Type: ai.ContentTypeToolUse, ToolUseID: "tc-2", ToolName: "tool2"},
			},
		},
		ai.NewToolResultMessage("tc-1", "result1", false),
		ai.NewToolResultMessage("tc-2", "result2", false),
		ai.NewTextMessage(ai.RoleAssistant, "both tools done"),
		ai.NewTextMessage(ai.RoleUser, "ok"),
	}

	cutIdx := findSafeCutPoint(msgs, 5) // very small budget

	// Verify the cut doesn't land on either tool_result.
	if cutIdx >= len(msgs) {
		return // edge case handled
	}
	msg := msgs[cutIdx]
	for _, c := range msg.Content {
		if c.Type == ai.ContentTypeToolResult {
			t.Errorf("cut point at index %d includes tool_result", cutIdx)
		}
	}
}

func TestEstimateMessageChars(t *testing.T) {
	msg := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeText, Text: "hello world"}, // 11 chars
			{Type: ai.ContentTypeToolUse, ToolName: "read"}, // 4 + 50 = 54 chars
		},
	}
	chars := estimateMessageChars(msg)
	if chars != 65 {
		t.Errorf("expected 65 chars, got %d", chars)
	}
}

func TestAutoCompactFallsBackToFullCompaction(t *testing.T) {
	// With only 1 message, autoCompact should return without error.
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

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg,
		WithContextWindow(100),
		WithReserveTokens(10),
		WithMessages([]ai.Message{
			ai.NewTextMessage(ai.RoleUser, "only message"),
		}),
	)
	a.lastInputTokens = 95

	err := a.maybeAutoCompact(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAutoCompactWithStreamError(t *testing.T) {
	provider := &mockProvider{
		streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventError, Error: fmt.Errorf("rate limited")}
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg,
		WithContextWindow(1000),
		WithReserveTokens(100),
		WithMessages([]ai.Message{
			ai.NewTextMessage(ai.RoleUser, "old msg"),
			ai.NewTextMessage(ai.RoleAssistant, "old resp"),
			ai.NewTextMessage(ai.RoleUser, "another msg"),
			ai.NewTextMessage(ai.RoleAssistant, "recent"),
		}),
	)
	a.lastInputTokens = 950

	err := a.maybeAutoCompact(context.Background())
	if err == nil {
		t.Fatal("expected error from stream")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("expected rate limited error, got %v", err)
	}
}

func TestAutoCompactIntegrationWithRunLoop(t *testing.T) {
	// Simulate a full run where the first turn reports high usage,
	// triggering auto-compaction before the second turn.
	callCount := 0
	provider := &mockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 20)
			callCount++
			current := callCount
			go func() {
				defer close(ch)
				switch current {
				case 1:
					// First turn: respond with text and report high usage.
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "initial response"}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd, Usage: &ai.Usage{
						InputTokens: 950, OutputTokens: 50,
					}}
				case 2:
					// Compaction call: return summary.
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "compacted summary"}
				case 3:
					// Follow-up turn after compaction: final response.
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "post-compaction response"}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd, Usage: &ai.Usage{
						InputTokens: 200, OutputTokens: 30,
					}}
				}
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg,
		WithContextWindow(1000),
		WithReserveTokens(100),
		WithKeepRecentTokens(50),
		WithMessages([]ai.Message{
			ai.NewTextMessage(ai.RoleUser, strings.Repeat("old context ", 100)),
			ai.NewTextMessage(ai.RoleAssistant, "old response"),
			ai.NewTextMessage(ai.RoleUser, "more old context"),
		}),
	)

	// Queue a follow-up so the loop runs twice (triggering compaction check).
	a.FollowUp("follow-up after compaction")

	ch := a.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "initial prompt")
	}()

	events := drainEvents(ch, 5*time.Second)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Prompt returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Prompt did not return")
	}

	// Verify auto-compaction event was emitted.
	foundAutoCompact := false
	for _, ev := range events {
		if ev.Type == EventAutoCompaction {
			foundAutoCompact = true
		}
	}
	if !foundAutoCompact {
		t.Error("expected EventAutoCompaction event during run loop")
	}

	// Verify at least 3 provider calls: initial turn + compaction + post-compaction turn.
	if callCount < 3 {
		t.Errorf("expected at least 3 provider calls, got %d", callCount)
	}
}

func TestFindSafeCutPointPreservesMinimumMessages(t *testing.T) {
	// Even with a huge keepRecentTokens, we should never return a cut
	// index >= len(msgs) that would leave nothing to summarize.
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "hello"),
		ai.NewTextMessage(ai.RoleAssistant, "hi"),
	}

	cutIdx := findSafeCutPoint(msgs, 1000000) // huge budget
	// cutIdx should be <= len(msgs)-1, keeping at least one message.
	if cutIdx >= len(msgs) {
		t.Errorf("cut index %d should be less than %d", cutIdx, len(msgs))
	}
}

func TestAutoCompactOptionsApplied(t *testing.T) {
	provider := &mockProvider{}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg,
		WithContextWindow(100000),
		WithReserveTokens(8000),
		WithKeepRecentTokens(2000),
	)

	if a.contextWindow != 100000 {
		t.Errorf("expected contextWindow 100000, got %d", a.contextWindow)
	}
	if a.reserveTokens != 8000 {
		t.Errorf("expected reserveTokens 8000, got %d", a.reserveTokens)
	}
	if a.keepRecentTokens != 2000 {
		t.Errorf("expected keepRecentTokens 2000, got %d", a.keepRecentTokens)
	}
}
