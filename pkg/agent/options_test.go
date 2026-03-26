package agent

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/tools"
)

func TestWithLoggerNilPreservesDefault(t *testing.T) {
	provider := &mockProvider{streamFn: textResponse("ok")}
	reg := tools.NewRegistry()
	a := NewLoop(provider, reg, WithLogger(nil))

	// When nil is passed, the constructor should keep log.Default().
	if a.logger != log.Default() {
		t.Error("WithLogger(nil) should preserve log.Default()")
	}
}

func TestWithLoggerCustom(t *testing.T) {
	var buf bytes.Buffer
	custom := log.New(&buf, "TEST: ", 0)

	provider := &mockProvider{
		streamFn: toolThenText("bad_tool", "tc-log", `{}`, "done"),
	}
	reg := tools.NewRegistry()
	// Do NOT register "bad_tool" — executeTool will log nothing for unknown
	// tools, but we can trigger a log line via invalid JSON on a known tool.
	// Instead, use a tool that produces invalid JSON in the stream to trigger
	// the "invalid tool input JSON" log path.

	// Actually, let's trigger the "steer message dropped" path — fill the
	// steer channel, then send another steer.
	provider2 := &mockProvider{streamFn: textResponse("ok")}
	a := NewLoop(provider2, reg, WithLogger(custom))

	// Fill the steer channel (capacity 2).
	a.Steer("fill-1")
	a.Steer("fill-2")
	// Third steer should be dropped and logged.
	a.Steer("dropped")

	if !strings.Contains(buf.String(), "steer message dropped") {
		t.Errorf("expected custom logger to capture 'steer message dropped', got %q", buf.String())
	}

	// Also verify it's actually our custom logger on the agent.
	if a.logger != custom {
		t.Error("WithLogger should set the agent's logger to the provided logger")
	}

	// Suppress unused variable warning for first provider setup.
	_ = provider
}

func TestWithLoggerCustomDuringPrompt(t *testing.T) {
	// Verify the custom logger captures operational messages during a prompt
	// that triggers the invalid-tool-input-JSON log path.
	var buf bytes.Buffer
	custom := log.New(&buf, "", 0)

	provider := &mockProvider{
		streamFn: toolThenText("log_tool", "tc-1", `not-valid-json`, "done"),
	}
	reg := tools.NewRegistry()
	reg.Register(&mockTool{name: "log_tool", result: "ok"})
	a := NewLoop(provider, reg, WithLogger(custom))

	ch := a.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "trigger log")
	}()

	drainEvents(ch, 2*time.Second)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Prompt returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt did not return")
	}

	if !strings.Contains(buf.String(), "invalid tool input JSON") {
		t.Errorf("expected custom logger to capture 'invalid tool input JSON', got %q", buf.String())
	}
}

// ctxCaptureTool is a mock tool that records the context it receives.
type ctxCaptureTool struct {
	name    string
	ctxChan chan context.Context
}

func (t *ctxCaptureTool) Name() string        { return t.name }
func (t *ctxCaptureTool) Description() string { return "captures context" }
func (t *ctxCaptureTool) Schema() any         { return nil }
func (t *ctxCaptureTool) Execute(ctx context.Context, _ map[string]any) (string, error) {
	t.ctxChan <- ctx
	return "ok", nil
}

func TestWithWorkingDirPassedToTools(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	captureTool := &ctxCaptureTool{name: "capture", ctxChan: ctxCh}

	provider := &mockProvider{
		streamFn: toolThenText("capture", "tc-wd", `{}`, "done"),
	}
	reg := tools.NewRegistry()
	reg.Register(captureTool)

	wantDir := "/tmp/test-workdir"
	a := NewLoop(provider, reg, WithWorkingDir(wantDir))

	ch := a.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "use capture tool")
	}()

	// Wait for the tool to receive the context.
	select {
	case ctx := <-ctxCh:
		got := tools.WorkingDirFromContext(ctx)
		if got != wantDir {
			t.Errorf("WorkingDirFromContext = %q, want %q", got, wantDir)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tool did not execute within timeout")
	}

	drainEvents(ch, 2*time.Second)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Prompt returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt did not return")
	}
}

func TestWithWorkingDirEmptyDoesNotSetContext(t *testing.T) {
	// When workingDir is empty, the context should NOT have a working dir
	// value — WorkingDirFromContext falls back to os.Getwd().
	ctxCh := make(chan context.Context, 1)
	captureTool := &ctxCaptureTool{name: "capture", ctxChan: ctxCh}

	provider := &mockProvider{
		streamFn: toolThenText("capture", "tc-wd2", `{}`, "done"),
	}
	reg := tools.NewRegistry()
	reg.Register(captureTool)

	// No WithWorkingDir option — workingDir stays "".
	a := NewLoop(provider, reg)

	ch := a.Events()
	errCh := make(chan error, 1)
	go func() {
		errCh <- a.Prompt(context.Background(), "use capture tool")
	}()

	select {
	case ctx := <-ctxCh:
		// With no working dir set, the value should NOT be in the context.
		// WorkingDirFromContext will return os.Getwd() as fallback.
		got := tools.WorkingDirFromContext(ctx)
		if got == "" {
			t.Error("WorkingDirFromContext returned empty string (os.Getwd failed?)")
		}
		// The key check: it should NOT be our custom dir.
		if got == "/tmp/test-workdir" {
			t.Error("context should not contain a custom working dir when none was set")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tool did not execute within timeout")
	}

	drainEvents(ch, 2*time.Second)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Prompt returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prompt did not return")
	}
}
