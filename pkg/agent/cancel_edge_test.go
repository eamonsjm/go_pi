package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

// TestCancelOnNilCancel verifies that calling Cancel() before any Prompt()
// does not panic. The cancel field is nil initially.
func TestCancelOnNilCancel(t *testing.T) {
	provider := &mockProvider{streamFn: textResponse("ok")}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg)

	// Should not panic.
	a.Cancel()
}

// TestCancelAfterPromptCompletes verifies that calling Cancel() after Prompt()
// has fully returned does not panic. The cancel field is set back to nil.
func TestCancelAfterPromptCompletes(t *testing.T) {
	provider := &mockProvider{streamFn: textResponse("done")}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg)

	if err := a.Prompt(context.Background(), "hi"); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	// cancel should be nil again after Prompt returns.
	a.Cancel()
}

// TestCancelDuringProviderStream cancels while the provider stream is actively
// sending events. The stream should abort and Prompt should return an error.
func TestCancelDuringProviderStream(t *testing.T) {
	provider := &mockProvider{
		streamFn: func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
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
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg)

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "stream forever")
	}()

	// Give Prompt time to start streaming.
	time.Sleep(30 * time.Millisecond)
	a.Cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected error from cancelled stream")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Prompt did not return after Cancel during stream")
	}
}

// TestCancelDuringToolExecution cancels while a tool is actively executing.
// The tool itself should see context cancellation.
func TestCancelDuringToolExecution(t *testing.T) {
	call := 0
	provider := &mockProvider{
		streamFn: func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 20)
			call++
			current := call
			go func() {
				defer close(ch)
				if current == 1 {
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-1", ToolName: "slow"}
					ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: `{}`}
					ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
					ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
				} else {
					<-ctx.Done()
					ch <- ai.StreamEvent{Type: ai.EventError, Error: ctx.Err()}
				}
			}()
			return ch, nil
		},
	}

	reg := tools.NewRegistry()
	// Tool that blocks until context is cancelled.
	reg.Register(&ctxBlockingTool{name: "slow"})
	a := NewAgentLoop(provider, reg)

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "use slow tool")
	}()

	// Wait for the tool to start executing.
	time.Sleep(50 * time.Millisecond)
	a.Cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected error when cancelled during tool execution")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Prompt did not return after Cancel during tool execution")
	}
}

// TestCancelMultipleTimesNoPanic calls Cancel() many times concurrently
// during an active Prompt to ensure no panics or deadlocks.
func TestCancelMultipleTimesNoPanic(t *testing.T) {
	provider := &mockProvider{
		streamFn: func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			ch := make(chan ai.StreamEvent, 10)
			go func() {
				defer close(ch)
				ch <- ai.StreamEvent{Type: ai.EventMessageStart}
				<-ctx.Done()
				ch <- ai.StreamEvent{Type: ai.EventError, Error: ctx.Err()}
			}()
			return ch, nil
		},
	}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg)

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "hi")
	}()

	time.Sleep(20 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.Cancel() // Should never panic.
		}()
	}

	wg.Wait()

	select {
	case <-errCh:
		// OK — cancelled.
	case <-time.After(3 * time.Second):
		t.Fatal("Prompt did not return after multiple Cancel calls")
	}
}

// TestCancelBeforePromptStarts calls Cancel(), then starts Prompt.
// The cancel should be a no-op since there's no active context yet.
func TestCancelBeforePromptStarts(t *testing.T) {
	provider := &mockProvider{streamFn: textResponse("success")}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg)

	a.Cancel() // Should be no-op.

	ch := a.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "after cancel")
	}()

	events := drainEvents(ch, 2*time.Second)

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Prompt after pre-Cancel should succeed, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Prompt did not return")
	}

	if !hasEventType(events, EventAssistantText) {
		t.Error("expected EventAssistantText")
	}
}

// TestCancelBetweenConsecutivePrompts ensures that cancelling after one Prompt
// finishes doesn't affect the next Prompt call.
func TestCancelBetweenConsecutivePrompts(t *testing.T) {
	provider := &mockProvider{streamFn: textResponse("response")}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg)

	// First prompt.
	if err := a.Prompt(context.Background(), "first"); err != nil {
		t.Fatalf("first Prompt: %v", err)
	}

	// Cancel between prompts.
	a.Cancel()

	// Second prompt should work fine.
	if err := a.Prompt(context.Background(), "second"); err != nil {
		t.Fatalf("second Prompt after Cancel: %v", err)
	}

	msgs := a.Messages()
	if len(msgs) != 4 { // user1, assistant1, user2, assistant2
		t.Errorf("expected 4 messages, got %d", len(msgs))
	}
}

// TestPromptWithNilProvider verifies that Prompt returns an error when no
// provider is configured.
func TestPromptWithNilProvider(t *testing.T) {
	reg := tools.NewRegistry()
	a := NewAgentLoop(nil, reg)

	err := a.Prompt(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
}

// ctxBlockingTool blocks in Execute until the context is cancelled.
type ctxBlockingTool struct {
	name string
}

func (t *ctxBlockingTool) Name() string        { return t.name }
func (t *ctxBlockingTool) Description() string  { return "blocks until cancelled" }
func (t *ctxBlockingTool) Schema() any          { return nil }
func (t *ctxBlockingTool) Execute(ctx context.Context, _ map[string]any) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
