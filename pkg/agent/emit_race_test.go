package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/tools"
)

// slowEmitTool is a tool that emits many events concurrently by spawning
// goroutines. This simulates a burst of concurrent emit() calls that might
// race with Prompt() closing the events channel.
type slowEmitTool struct {
	name    string
	delay   time.Duration
	started chan struct{} // closed when Execute begins
}

func (t *slowEmitTool) Name() string        { return t.name }
func (t *slowEmitTool) Description() string { return "slow tool for race testing" }
func (t *slowEmitTool) Schema() any         { return nil }
func (t *slowEmitTool) Execute(ctx context.Context, _ map[string]any) (string, error) {
	if t.started != nil {
		close(t.started)
	}
	select {
	case <-time.After(t.delay):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return "done", nil
}

// TestEmitRaceWithPromptClose stress-tests the exact production bug scenario:
// many concurrent emit() calls (via tool execution events) racing with
// Prompt() closing and recreating the events channel. Under the old code
// (without eventsMu), this would panic with send-on-closed-channel.
//
// Run with: go test -race -count=50 -run TestEmitRaceWithPromptClose
func TestEmitRaceWithPromptClose(t *testing.T) {
	for trial := 0; trial < 10; trial++ {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			// Provider returns a tool call on first invocation, text on second.
			call := 0
			var mu sync.Mutex
			provider := &mockProvider{
				streamFn: func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
					mu.Lock()
					call++
					n := call
					mu.Unlock()
					ch := make(chan ai.StreamEvent, 20)
					go func() {
						defer close(ch)
						if n == 1 {
							ch <- ai.StreamEvent{Type: ai.EventMessageStart}
							ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-1", ToolName: "fast"}
							ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: `{}`}
							ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
							ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
						} else {
							ch <- ai.StreamEvent{Type: ai.EventMessageStart}
							ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "ok"}
							ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
						}
					}()
					return ch, nil
				},
			}

			reg := tools.NewRegistry()
			reg.Register(&mockTool{name: "fast", result: "ok"})
			a := NewAgentLoop(provider, reg)

			// Drain events in a goroutine — the consumer side of the channel.
			evCh := a.Events()
			var drainWg sync.WaitGroup
			drainWg.Add(1)
			go func() {
				defer drainWg.Done()
				for range evCh {
				}
			}()

			err := a.Prompt(context.Background(), "go")
			if err != nil {
				t.Fatalf("Prompt: %v", err)
			}

			// Events channel should be closed (old one), consumer exits.
			drainWg.Wait()
		})
	}
}

// TestConcurrentEmitAndCancel fires Cancel() while emit() calls are in
// progress from a long-running tool execution. The RWMutex must prevent
// send-on-closed-channel panics even under heavy cancellation.
//
// Run with: go test -race -count=20 -run TestConcurrentEmitAndCancel
func TestConcurrentEmitAndCancel(t *testing.T) {
	for trial := 0; trial < 5; trial++ {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			toolStarted := make(chan struct{})
			call := 0
			var mu sync.Mutex
			provider := &mockProvider{
				streamFn: func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
					mu.Lock()
					call++
					n := call
					mu.Unlock()
					ch := make(chan ai.StreamEvent, 20)
					go func() {
						defer close(ch)
						if n == 1 {
							ch <- ai.StreamEvent{Type: ai.EventMessageStart}
							ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-1", ToolName: "blocker"}
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
			reg.Register(&slowEmitTool{name: "blocker", delay: 5 * time.Second, started: toolStarted})
			a := NewAgentLoop(provider, reg)

			// Drain events so the buffer doesn't block emit().
			evCh := a.Events()
			go func() {
				for range evCh {
				}
			}()

			errCh := make(chan error, 1)
			go func() {
				errCh <- a.Prompt(context.Background(), "use blocker")
			}()

			// Wait for the tool to begin executing.
			select {
			case <-toolStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("tool never started")
			}

			// Fire many concurrent Cancel() calls.
			var wg sync.WaitGroup
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					a.Cancel()
				}()
			}
			wg.Wait()

			select {
			case err := <-errCh:
				if err == nil {
					t.Error("expected error from cancelled Prompt")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Prompt did not return after Cancel")
			}
		})
	}
}

// TestSteerEmitCancelInterleaving exercises all three operations concurrently:
// Steer() sends a steering message, emit() sends events, and Cancel() aborts —
// all racing against Prompt()'s channel close.
//
// Run with: go test -race -count=20 -run TestSteerEmitCancelInterleaving
func TestSteerEmitCancelInterleaving(t *testing.T) {
	for trial := 0; trial < 5; trial++ {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			toolStarted := make(chan struct{})
			call := 0
			var mu sync.Mutex
			provider := &mockProvider{
				streamFn: func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
					mu.Lock()
					call++
					n := call
					mu.Unlock()
					ch := make(chan ai.StreamEvent, 20)
					go func() {
						defer close(ch)
						switch n {
						case 1:
							ch <- ai.StreamEvent{Type: ai.EventMessageStart}
							ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-1", ToolName: "waiter"}
							ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: `{}`}
							ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
							ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
						default:
							// After steering/cancel, model returns text or error.
							select {
							case <-ctx.Done():
								ch <- ai.StreamEvent{Type: ai.EventError, Error: ctx.Err()}
							case <-time.After(50 * time.Millisecond):
								ch <- ai.StreamEvent{Type: ai.EventMessageStart}
								ch <- ai.StreamEvent{Type: ai.EventTextDelta, Delta: "final"}
								ch <- ai.StreamEvent{Type: ai.EventMessageEnd}
							}
						}
					}()
					return ch, nil
				},
			}

			reg := tools.NewRegistry()
			reg.Register(&slowEmitTool{name: "waiter", delay: 5 * time.Second, started: toolStarted})
			a := NewAgentLoop(provider, reg)

			evCh := a.Events()
			go func() {
				for range evCh {
				}
			}()

			errCh := make(chan error, 1)
			go func() {
				errCh <- a.Prompt(context.Background(), "interleave test")
			}()

			select {
			case <-toolStarted:
			case <-time.After(3 * time.Second):
				t.Fatal("tool never started")
			}

			// Fire Steer, Cancel, and additional Steer calls concurrently.
			var wg sync.WaitGroup
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					a.Steer("interrupt!")
				}()
			}
			// Small delay then cancel to ensure interleaving.
			wg.Add(1)
			go func() {
				defer wg.Done()
				time.Sleep(5 * time.Millisecond)
				a.Cancel()
			}()
			wg.Wait()

			select {
			case <-errCh:
				// Either cancelled or completed with steering — both are fine,
				// the point is no panics or deadlocks.
			case <-time.After(5 * time.Second):
				t.Fatal("Prompt did not return after Steer+Cancel")
			}
		})
	}
}

// TestRapidPromptCycles calls Prompt() many times in quick succession. Each
// call closes and recreates the events channel. This stresses the
// eventsMu Lock/Unlock path in Prompt()'s cleanup and the RLock in emit().
//
// Run with: go test -race -count=10 -run TestRapidPromptCycles
func TestRapidPromptCycles(t *testing.T) {
	provider := &mockProvider{streamFn: textResponse("hi")}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg)

	for i := 0; i < 50; i++ {
		evCh := a.Events()

		// Start a consumer that may or may not finish before the channel closes.
		go func() {
			for range evCh {
			}
		}()

		if err := a.Prompt(context.Background(), "cycle"); err != nil {
			t.Fatalf("Prompt cycle %d: %v", i, err)
		}
	}
}

// TestManyToolCallsWithCancel has the model return many tool calls, then
// Cancel() fires mid-flight. Each tool execution emits start/end events —
// the race is between those emit() calls and the eventual channel close.
//
// Run with: go test -race -count=20 -run TestManyToolCallsWithCancel
func TestManyToolCallsWithCancel(t *testing.T) {
	const numTools = 8
	toolStarted := make(chan struct{}, numTools)

	call := 0
	var mu sync.Mutex
	provider := &mockProvider{
		streamFn: func(ctx context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
			mu.Lock()
			call++
			n := call
			mu.Unlock()
			ch := make(chan ai.StreamEvent, 50)
			go func() {
				defer close(ch)
				if n == 1 {
					ch <- ai.StreamEvent{Type: ai.EventMessageStart}
					for i := 0; i < numTools; i++ {
						id := string(rune('a' + i))
						ch <- ai.StreamEvent{Type: ai.EventToolUseStart, ToolCallID: "tc-" + id, ToolName: "t" + id}
						ch <- ai.StreamEvent{Type: ai.EventToolUseDelta, PartialInput: `{}`}
						ch <- ai.StreamEvent{Type: ai.EventToolUseEnd}
					}
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
	for i := 0; i < numTools; i++ {
		id := string(rune('a' + i))
		reg.Register(&slowEmitTool{
			name:    "t" + id,
			delay:   5 * time.Second,
			started: toolStarted,
		})
	}
	a := NewAgentLoop(provider, reg)

	evCh := a.Events()
	go func() {
		for range evCh {
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "many tools")
	}()

	// Wait for the first tool to start executing (tools run sequentially).
	select {
	case <-toolStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("first tool never started")
	}

	a.Cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Error("expected error from cancelled Prompt with many tools")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Prompt did not return after Cancel with many tools")
	}
}

// TestEmitAfterContextCancel verifies that emit() handles a cancelled context
// gracefully. When the context is done, the select in emit() should fall through
// to the ctx.Done() case rather than blocking or panicking.
func TestEmitAfterContextCancel(t *testing.T) {
	provider := &mockProvider{streamFn: textResponse("hi")}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg)
	a.ensureInit()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	// emit() should not panic or block even with a cancelled context.
	// The events channel has buffer space, but the ctx.Done case should fire.
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.emit(ctx, AgentEvent{Type: EventAssistantText, Delta: "race"})
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All emit calls completed — no panic.
	case <-time.After(3 * time.Second):
		t.Fatal("emit() calls blocked with cancelled context")
	}
}

// TestConsumerLagDuringClose simulates a slow consumer that hasn't fully
// drained the events channel when Prompt() tries to close it. The
// eventsMu ensures the close waits for in-flight emit() sends.
func TestConsumerLagDuringClose(t *testing.T) {
	for trial := 0; trial < 10; trial++ {
		t.Run("", func(t *testing.T) {
			t.Parallel()
			provider := &mockProvider{streamFn: textResponse("hi")}
			reg := tools.NewRegistry()
			a := NewAgentLoop(provider, reg)

			evCh := a.Events()

			// Slow consumer: introduces a delay between reads.
			var consumerDone sync.WaitGroup
			consumerDone.Add(1)
			go func() {
				defer consumerDone.Done()
				for range evCh {
					// Simulate slow processing.
					time.Sleep(time.Millisecond)
				}
			}()

			err := a.Prompt(context.Background(), "lag test")
			if err != nil {
				t.Fatalf("Prompt: %v", err)
			}

			// Consumer should eventually see channel close.
			done := make(chan struct{})
			go func() {
				consumerDone.Wait()
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("consumer did not see channel close")
			}
		})
	}
}

// TestBackToBackPromptEventChannels verifies that after each Prompt() call
// closes the events channel, the next Events() call returns a valid new
// channel that correctly receives events.
func TestBackToBackPromptEventChannels(t *testing.T) {
	provider := &mockProvider{streamFn: textResponse("reply")}
	reg := tools.NewRegistry()
	a := NewAgentLoop(provider, reg)

	for i := 0; i < 20; i++ {
		ch := a.Events()

		var events []AgentEvent
		var drainDone sync.WaitGroup
		drainDone.Add(1)
		go func() {
			defer drainDone.Done()
			for ev := range ch {
				events = append(events, ev)
			}
		}()

		if err := a.Prompt(context.Background(), "turn"); err != nil {
			t.Fatalf("Prompt %d: %v", i, err)
		}

		drainDone.Wait()

		if !hasEventType(events, EventAgentStart) {
			t.Errorf("iteration %d: missing EventAgentStart", i)
		}
		if !hasEventType(events, EventAgentEnd) {
			t.Errorf("iteration %d: missing EventAgentEnd", i)
		}
	}
}
