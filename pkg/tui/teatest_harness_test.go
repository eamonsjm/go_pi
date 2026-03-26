package tui

import (
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
)

// ---------------------------------------------------------------------------
// teatest harness — helpers for integration-testing TUI components
// ---------------------------------------------------------------------------

// Default terminal dimensions used by the test harness.
const (
	TestTermWidth  = 120
	TestTermHeight = 40
)

// TestHarness wraps a teatest.TestModel with convenience methods for
// driving the go_pi TUI in tests. Create one via NewTestHarness.
type TestHarness struct {
	t  testing.TB
	tm *teatest.TestModel

	// submitCh captures text sent via the onSubmit callback.
	submitCh chan string
	// cancelCh is closed when the onCancel callback fires.
	cancelCh chan struct{}
	// steerCh captures text sent via the onSteer callback.
	steerCh chan string
}

// HarnessOption configures a TestHarness before starting the program.
type HarnessOption func(*harnessConfig)

type harnessConfig struct {
	width     int
	height    int
	model     string
	welcome   string
	testOpts  []teatest.TestOption
}

// WithTermSize overrides the default terminal dimensions.
func WithTermSize(w, h int) HarnessOption {
	return func(c *harnessConfig) {
		c.width = w
		c.height = h
	}
}

// WithModelName sets the AI model name shown in the header.
func WithModelName(name string) HarnessOption {
	return func(c *harnessConfig) {
		c.model = name
	}
}

// WithWelcome adds a welcome message to the chat before starting.
func WithWelcome(text string) HarnessOption {
	return func(c *harnessConfig) {
		c.welcome = text
	}
}

// WithTestOption appends a raw teatest.TestOption for advanced use cases.
func WithTestOption(opt teatest.TestOption) HarnessOption {
	return func(c *harnessConfig) {
		c.testOpts = append(c.testOpts, opt)
	}
}

// NewTestHarness creates a fully wired App, starts it inside a
// teatest.TestModel, and returns the harness. The program is
// automatically stopped via t.Cleanup.
func NewTestHarness(t testing.TB, opts ...HarnessOption) *TestHarness {
	t.Helper()

	cfg := &harnessConfig{
		width:  TestTermWidth,
		height: TestTermHeight,
		model:  "test-model",
	}
	for _, o := range opts {
		o(cfg)
	}

	app := NewApp()
	app.SetModel(cfg.model)
	app.SetHasUI(true)

	if cfg.welcome != "" {
		app.ShowWelcome(cfg.welcome)
	}

	submitCh := make(chan string, 8)
	cancelCh := make(chan struct{}, 1)
	steerCh := make(chan string, 8)
	app.SetCallbacks(
		func(text string) { submitCh <- text },
		func(text string) { steerCh <- text },
		func() {
			select {
			case cancelCh <- struct{}{}:
			default:
			}
		},
	)

	testOpts := []teatest.TestOption{
		teatest.WithInitialTermSize(cfg.width, cfg.height),
	}
	testOpts = append(testOpts, cfg.testOpts...)

	tm := teatest.NewTestModel(t, app, testOpts...)
	t.Cleanup(func() {
		// Gracefully quit the program so it doesn't leak.
		if err := tm.Quit(); err != nil {
			// Ignore errors from already-exited programs.
			_ = err
		}
	})

	h := &TestHarness{
		t:        t,
		tm:       tm,
		submitCh: submitCh,
		cancelCh: cancelCh,
		steerCh:  steerCh,
	}

	return h
}

// ---------------------------------------------------------------------------
// Message injection helpers
// ---------------------------------------------------------------------------

// Send sends an arbitrary tea.Msg to the running program.
func (h *TestHarness) Send(msg tea.Msg) {
	h.tm.Send(msg)
}

// Type simulates keyboard input (types the given string).
func (h *TestHarness) Type(s string) {
	h.tm.Type(s)
}

// Resize sends a WindowSizeMsg to trigger layout.
func (h *TestHarness) Resize(w, height int) {
	h.tm.Send(tea.WindowSizeMsg{Width: w, Height: height})
}

// SendAgentEvent injects an agent event into the TUI as a StreamEventMsg.
func (h *TestHarness) SendAgentEvent(event agent.Event) {
	h.tm.Send(StreamEventMsg{Event: event})
}

// SendAgentDone signals the agent loop has finished.
func (h *TestHarness) SendAgentDone() {
	h.tm.Send(AgentDoneMsg{})
}

// SendAgentError injects an agent error message.
func (h *TestHarness) SendAgentError(err error) {
	h.tm.Send(AgentErrorMsg{Err: err})
}

// SendCommandResult injects a slash command result.
func (h *TestHarness) SendCommandResult(text string, isError bool) {
	h.tm.Send(CommandResultMsg{Text: text, IsError: isError})
}

// ---------------------------------------------------------------------------
// Output helpers
// ---------------------------------------------------------------------------

// Output returns the raw output reader from the test model.
func (h *TestHarness) Output() io.Reader {
	return h.tm.Output()
}

// WaitForOutput blocks until the output matches the given condition or the
// test times out (calls t.Fatal on timeout). The default timeout is 2 seconds.
func (h *TestHarness) WaitForOutput(condition func(out []byte) bool, waitOpts ...teatest.WaitForOption) {
	h.t.Helper()
	defaults := []teatest.WaitForOption{
		teatest.WithDuration(2 * time.Second),
		teatest.WithCheckInterval(50 * time.Millisecond),
	}
	defaults = append(defaults, waitOpts...)
	teatest.WaitFor(h.t, h.tm.Output(), condition, defaults...)
}

// WaitForText waits until the given substring appears in the output.
func (h *TestHarness) WaitForText(substr string, waitOpts ...teatest.WaitForOption) {
	h.t.Helper()
	h.WaitForOutput(func(out []byte) bool {
		return strings.Contains(string(out), substr)
	}, waitOpts...)
}

// FinalModel quits the program and returns the final tea.Model, cast back to
// *App for convenience. Use finalOpts to set a custom timeout.
func (h *TestHarness) FinalModel(finalOpts ...teatest.FinalOpt) *App {
	h.t.Helper()
	defaults := []teatest.FinalOpt{
		teatest.WithFinalTimeout(5 * time.Second),
	}
	defaults = append(defaults, finalOpts...)
	m := h.tm.FinalModel(h.t, defaults...)
	app, ok := m.(*App)
	if !ok {
		h.t.Fatalf("FinalModel: expected *App, got %T", m)
	}
	return app
}

// ---------------------------------------------------------------------------
// Callback inspection
// ---------------------------------------------------------------------------

// SubmitText returns the most recently submitted text from the onSubmit
// callback, blocking up to timeout. Returns ("", false) if nothing is
// received in time.
func (h *TestHarness) SubmitText(timeout time.Duration) (string, bool) {
	select {
	case text := <-h.submitCh:
		return text, true
	case <-time.After(timeout):
		return "", false
	}
}

// SteerText returns the most recently submitted steering text, blocking up
// to timeout. Returns ("", false) if nothing is received in time.
func (h *TestHarness) SteerText(timeout time.Duration) (string, bool) {
	select {
	case text := <-h.steerCh:
		return text, true
	case <-time.After(timeout):
		return "", false
	}
}

// WaitCancel blocks until the onCancel callback fires, or the timeout
// expires. Returns false on timeout.
func (h *TestHarness) WaitCancel(timeout time.Duration) bool {
	select {
	case <-h.cancelCh:
		return true
	case <-time.After(timeout):
		return false
	}
}

// ---------------------------------------------------------------------------
// Agent event builder helpers
// ---------------------------------------------------------------------------

// TextDeltaEvent creates a StreamEventMsg wrapping an assistant text delta.
func TextDeltaEvent(text string) StreamEventMsg {
	return StreamEventMsg{
		Event: agent.Event{
			Type:  agent.EventAssistantText,
			Delta: text,
		},
	}
}

// ThinkingDeltaEvent creates a StreamEventMsg wrapping a thinking delta.
func ThinkingDeltaEvent(text string) StreamEventMsg {
	return StreamEventMsg{
		Event: agent.Event{
			Type:  agent.EventAssistantThinking,
			Delta: text,
		},
	}
}

// ToolExecStartEvent creates a StreamEventMsg wrapping a tool execution start.
func ToolExecStartEvent(toolName string, args map[string]any) StreamEventMsg {
	return StreamEventMsg{
		Event: agent.Event{
			Type:     agent.EventToolExecStart,
			ToolName: toolName,
			ToolArgs: args,
		},
	}
}

// ToolExecEndEvent creates a StreamEventMsg wrapping a tool execution end.
func ToolExecEndEvent(toolName, result string, isError bool) StreamEventMsg {
	return StreamEventMsg{
		Event: agent.Event{
			Type:       agent.EventToolExecEnd,
			ToolName:   toolName,
			ToolResult: result,
			ToolError:  isError,
		},
	}
}

// ToolResultEvent creates a StreamEventMsg wrapping a tool result with a
// full ai.Message attached.
func ToolResultEvent(msg *ai.Message) StreamEventMsg {
	return StreamEventMsg{
		Event: agent.Event{
			Type:    agent.EventToolResult,
			Message: msg,
		},
	}
}

// AgentStartEvent creates a StreamEventMsg for agent_start.
func AgentStartEvent() StreamEventMsg {
	return StreamEventMsg{
		Event: agent.Event{Type: agent.EventAgentStart},
	}
}

// AgentEndEvent creates a StreamEventMsg for agent_end.
func AgentEndEvent() StreamEventMsg {
	return StreamEventMsg{
		Event: agent.Event{Type: agent.EventAgentEnd},
	}
}

// ---------------------------------------------------------------------------
// Smoke test — verifies the harness itself works
// ---------------------------------------------------------------------------

func TestHarness_Smoke(t *testing.T) {
	h := NewTestHarness(t, WithWelcome("Hello from test harness"))

	// The App renders "Initializing..." until a WindowSizeMsg arrives.
	// The teatest library sends the initial window size automatically, so
	// the app should initialise and show the welcome message.
	h.WaitForText("Hello from test harness")
}

func TestHarness_ModelName(t *testing.T) {
	h := NewTestHarness(t, WithModelName("claude-opus"))

	// After initialisation the header should display the model name.
	h.WaitForText("claude-opus")
}
