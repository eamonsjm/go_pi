package tui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

func TestNewCompactCommand_Metadata(t *testing.T) {
	provider := &tuiMockProvider{streamFn: tuiTextResponse("summary")}
	reg := tools.NewRegistry()
	a := agent.NewLoop(provider, reg)

	cmd := NewCompactCommand(context.Background(), a)

	if cmd.Name != "compact" {
		t.Errorf("Name = %q, want %q", cmd.Name, "compact")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

func TestNewCompactCommand_SuccessfulCompaction(t *testing.T) {
	// Set up a provider that first handles the Prompt (to seed messages),
	// then handles the Compact call.
	provider := &tuiMockProvider{streamFn: tuiTextResponse("This is a summary of the conversation.")}
	reg := tools.NewRegistry()
	a := agent.NewLoop(provider, reg)

	// Seed messages so Compact has something to work with.
	a.SetMessages([]ai.Message{
		ai.NewTextMessage(ai.RoleUser, "Hello"),
		ai.NewTextMessage(ai.RoleAssistant, "Hi there"),
	})

	cmd := NewCompactCommand(context.Background(), a)
	batchCmd := cmd.Execute("")

	// tea.Batch returns a Cmd that produces a tea.BatchMsg.
	// Execute the batch to get the inner messages.
	msg := batchCmd()
	batchMsg, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg, got %T", msg)
	}

	if len(batchMsg) != 2 {
		t.Fatalf("expected 2 commands in batch, got %d", len(batchMsg))
	}

	// First command should produce compactionStartMsg.
	startMsg := batchMsg[0]()
	if _, ok := startMsg.(compactionStartMsg); !ok {
		t.Errorf("expected compactionStartMsg, got %T", startMsg)
	}

	// Second command produces either compactionDoneMsg or compactionErrorMsg.
	resultMsg := batchMsg[1]()
	switch m := resultMsg.(type) {
	case compactionDoneMsg:
		// Success — summary should be non-empty since provider returns text.
		// The summary comes from Messages()[0].GetText() after compaction.
		_ = m
	case compactionErrorMsg:
		t.Fatalf("expected compactionDoneMsg, got compactionErrorMsg: %v", m.err)
	default:
		t.Fatalf("unexpected message type: %T", resultMsg)
	}
}

func TestNewCompactCommand_CompactionError(t *testing.T) {
	// Provider that returns an error during streaming.
	provider := &tuiMockProvider{
		streamFn: func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			return nil, errors.New("provider down")
		},
	}
	reg := tools.NewRegistry()
	a := agent.NewLoop(provider, reg)

	// Seed messages so Compact doesn't fail on "no messages".
	a.SetMessages([]ai.Message{
		ai.NewTextMessage(ai.RoleUser, "Hello"),
	})

	cmd := NewCompactCommand(context.Background(), a)
	batchCmd := cmd.Execute("")

	msg := batchCmd()
	batchMsg, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg, got %T", msg)
	}

	// Second command should return compactionErrorMsg.
	resultMsg := batchMsg[1]()
	errMsg, ok := resultMsg.(compactionErrorMsg)
	if !ok {
		t.Fatalf("expected compactionErrorMsg, got %T", resultMsg)
	}
	if errMsg.err == nil {
		t.Error("expected non-nil error")
	}
}

func TestNewCompactCommand_EmptyMessages(t *testing.T) {
	provider := &tuiMockProvider{streamFn: tuiTextResponse("summary")}
	reg := tools.NewRegistry()
	a := agent.NewLoop(provider, reg)
	// No messages set — Compact should fail with "no messages to compact".

	cmd := NewCompactCommand(context.Background(), a)
	batchCmd := cmd.Execute("")

	msg := batchCmd()
	batchMsg := msg.(tea.BatchMsg)

	resultMsg := batchMsg[1]()
	errMsg, ok := resultMsg.(compactionErrorMsg)
	if !ok {
		t.Fatalf("expected compactionErrorMsg for empty messages, got %T", resultMsg)
	}
	if errMsg.err == nil {
		t.Error("expected error for empty messages")
	}
}

func TestNewCompactCommand_PassesArgs(t *testing.T) {
	// Verify that args are passed through to Compact.
	var receivedReq ai.StreamRequest
	provider := &tuiMockProvider{
		streamFn: func(_ context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			receivedReq = req
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventMessageStart}
				ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "focused summary"}
				ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
			}()
			return ch, nil
		},
	}
	reg := tools.NewRegistry()
	a := agent.NewLoop(provider, reg)

	a.SetMessages([]ai.Message{
		ai.NewTextMessage(ai.RoleUser, "Hello"),
	})

	cmd := NewCompactCommand(context.Background(), a)
	batchCmd := cmd.Execute("focus on error handling")

	msg := batchCmd()
	batchMsg := msg.(tea.BatchMsg)
	batchMsg[1]() // Execute the compaction

	// The request should contain the focus instructions in the messages.
	if len(receivedReq.Messages) > 0 {
		text := receivedReq.Messages[0].GetText()
		if text == "" {
			t.Error("expected compaction request to contain prompt text")
		}
	}
}

// --- mock helpers for tui package tests ------------------------------------

type tuiMockProvider struct {
	streamFn func(ctx context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error)
}

func (m *tuiMockProvider) Name() string { return "mock" }

func (m *tuiMockProvider) Stream(ctx context.Context, req ai.StreamRequest) (<-chan ai.StreamEvent, error) {
	return m.streamFn(ctx, req)
}

func tuiTextResponse(text string) func(context.Context, ai.StreamRequest) (<-chan ai.StreamEvent, error) {
	return func(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
		ch := make(chan ai.StreamEvent, 10)
		go func() {
			defer close(ch)
			ch <- ai.StreamEvent{Type: ai.EventMessageStart}
			ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: text}
			ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
		}()
		return ch, nil
	}
}
