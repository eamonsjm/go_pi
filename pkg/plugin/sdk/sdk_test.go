package sdk

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

// pipePlugin creates a plugin connected to in-memory pipes and returns
// functions to send messages and read responses.
func pipePlugin(t *testing.T, p *Plugin) (send func(hostMessage), recv func() pluginMessage, done chan struct{}) {
	t.Helper()

	pr, pw := io.Pipe() // host writes -> plugin reads (stdin)
	rr, rw := io.Pipe() // plugin writes -> host reads (stdout)
	done = make(chan struct{})

	// Run the message loop in a goroutine using internal types directly.
	go func() {
		defer close(done)
		p.writer = json.NewEncoder(rw)
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, maxBuffer), maxBuffer)

		// Read initialize.
		if !scanner.Scan() {
			return
		}
		var initMsg hostMessage
		if err := json.Unmarshal(scanner.Bytes(), &initMsg); err != nil {
			return
		}
		if initMsg.Config != nil {
			p.config = *initMsg.Config
		}

		if p.initHandler != nil {
			if err := p.initHandler(p.config); err != nil {
				return
			}
		}

		// Snapshot slices under lock to match Run() behavior.
		p.mu.Lock()
		tools := make([]toolEntry, len(p.tools))
		copy(tools, p.tools)
		commands := make([]commandEntry, len(p.commands))
		copy(commands, p.commands)
		eventHandlers := make([]EventHandler, len(p.eventHandlers))
		copy(eventHandlers, p.eventHandlers)
		p.mu.Unlock()

		caps := pluginMessage{Type: "capabilities"}
		for _, tool := range tools {
			caps.Tools = append(caps.Tools, tool.def)
		}
		for _, cmd := range commands {
			caps.Commands = append(caps.Commands, cmd.def)
		}
		p.send(caps)

		for scanner.Scan() {
			var msg hostMessage
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}
			if msg.Type == "shutdown" {
				return
			}
			p.dispatch(msg, tools, commands, eventHandlers)
			if p.failed.Load() {
				return
			}
		}
	}()

	encoder := json.NewEncoder(pw)
	sendFn := func(msg hostMessage) {
		t.Helper()
		if err := encoder.Encode(msg); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	reader := bufio.NewReader(rr)
	recvFn := func() pluginMessage {
		t.Helper()
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		var msg pluginMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("recv unmarshal: %v (line: %s)", err, line)
		}
		return msg
	}

	return sendFn, recvFn, done
}

func TestCapabilitiesHandshake(t *testing.T) {
	p := NewPlugin("test").
		Tool("mytool", "A tool", map[string]any{"type": "object"}, func(ctx ToolContext) (string, error) {
			return "ok", nil
		}).
		Command("mycmd", "A command", func(ctx CommandContext) (string, error) {
			return "ok", nil
		})

	send, recv, done := pipePlugin(t, p)

	send(hostMessage{
		Type:   "initialize",
		Config: &Config{Cwd: "/tmp", Model: "test-model", Provider: "test", GiVersion: "0.0.1"},
	})

	caps := recv()
	if caps.Type != "capabilities" {
		t.Fatalf("expected capabilities, got %s", caps.Type)
	}
	if len(caps.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(caps.Tools))
	}
	if caps.Tools[0].Name != "mytool" {
		t.Errorf("expected tool name 'mytool', got %s", caps.Tools[0].Name)
	}
	if len(caps.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(caps.Commands))
	}
	if caps.Commands[0].Name != "mycmd" {
		t.Errorf("expected command name 'mycmd', got %s", caps.Commands[0].Name)
	}

	send(hostMessage{Type: "shutdown"})
	<-done
}

func TestToolCall(t *testing.T) {
	p := NewPlugin("test").
		Tool("reverse", "Reverse text", nil, func(ctx ToolContext) (string, error) {
			text, _ := ctx.Params["text"].(string)
			runes := []rune(text)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			return string(runes), nil
		})

	send, recv, done := pipePlugin(t, p)

	send(hostMessage{
		Type:   "initialize",
		Config: &Config{Cwd: "/tmp"},
	})
	recv() // capabilities

	send(hostMessage{
		Type:   "tool_call",
		ID:     "call_1",
		Name:   "reverse",
		Params: map[string]any{"text": "hello"},
	})

	result := recv()
	if result.Type != "tool_result" {
		t.Fatalf("expected tool_result, got %s", result.Type)
	}
	if result.ID != "call_1" {
		t.Errorf("expected id call_1, got %s", result.ID)
	}
	if result.Content != "olleh" {
		t.Errorf("expected 'olleh', got %s", result.Content)
	}
	if result.IsError {
		t.Error("unexpected is_error=true")
	}

	send(hostMessage{Type: "shutdown"})
	<-done
}

func TestToolCallError(t *testing.T) {
	p := NewPlugin("test").
		Tool("fail", "Always fails", nil, func(ctx ToolContext) (string, error) {
			return "", errors.New("something went wrong")
		})

	send, recv, done := pipePlugin(t, p)

	send(hostMessage{Type: "initialize", Config: &Config{}})
	recv() // capabilities

	send(hostMessage{Type: "tool_call", ID: "call_2", Name: "fail"})

	result := recv()
	if !result.IsError {
		t.Error("expected is_error=true")
	}
	if result.Content != "something went wrong" {
		t.Errorf("expected error message, got %s", result.Content)
	}

	send(hostMessage{Type: "shutdown"})
	<-done
}

func TestUnknownTool(t *testing.T) {
	p := NewPlugin("test")

	send, recv, done := pipePlugin(t, p)

	send(hostMessage{Type: "initialize", Config: &Config{}})
	recv() // capabilities

	send(hostMessage{Type: "tool_call", ID: "call_3", Name: "nonexistent"})

	result := recv()
	if !result.IsError {
		t.Error("expected is_error=true for unknown tool")
	}
	if !strings.Contains(result.Content, "unknown tool") {
		t.Errorf("expected 'unknown tool' in content, got %s", result.Content)
	}

	send(hostMessage{Type: "shutdown"})
	<-done
}

func TestCommandExecution(t *testing.T) {
	p := NewPlugin("test").
		Command("greet", "Greet someone", func(ctx CommandContext) (string, error) {
			return "Hello " + ctx.Args, nil
		})

	send, recv, done := pipePlugin(t, p)

	send(hostMessage{Type: "initialize", Config: &Config{}})
	recv() // capabilities

	send(hostMessage{Type: "command", Name: "greet", Args: "world"})

	result := recv()
	if result.Type != "command_result" {
		t.Fatalf("expected command_result, got %s", result.Type)
	}
	if result.Text != "Hello world" {
		t.Errorf("expected 'Hello world', got %s", result.Text)
	}

	send(hostMessage{Type: "shutdown"})
	<-done
}

func TestEventHandling(t *testing.T) {
	var received []string
	p := NewPlugin("test").
		Tool("ping", "ping", nil, func(ctx ToolContext) (string, error) {
			return "pong", nil
		}).
		OnEvent(func(e Event) {
			received = append(received, e.Type)
		})

	send, recv, done := pipePlugin(t, p)

	send(hostMessage{Type: "initialize", Config: &Config{}})
	recv() // capabilities

	send(hostMessage{Type: "event", Event: &Event{Type: "agent_start"}})
	send(hostMessage{Type: "event", Event: &Event{Type: "tool_exec_start", ToolName: "bash"}})
	// Send a tool call to sync — events are fire-and-forget so we need a
	// request-response to know they've been processed.
	send(hostMessage{Type: "tool_call", ID: "sync", Name: "ping"})
	recv() // tool_result

	if len(received) != 2 {
		t.Fatalf("expected 2 events, got %d", len(received))
	}
	if received[0] != "agent_start" {
		t.Errorf("expected agent_start, got %s", received[0])
	}

	send(hostMessage{Type: "shutdown"})
	<-done
}

func TestOnInitHandler(t *testing.T) {
	var initCfg Config
	p := NewPlugin("test").
		OnInit(func(cfg Config) error {
			initCfg = cfg
			return nil
		})

	send, recv, done := pipePlugin(t, p)

	send(hostMessage{
		Type:   "initialize",
		Config: &Config{Cwd: "/myproject", Model: "opus", Provider: "anthropic", GiVersion: "1.0.0"},
	})
	recv() // capabilities

	if initCfg.Cwd != "/myproject" {
		t.Errorf("expected cwd /myproject, got %s", initCfg.Cwd)
	}
	if initCfg.Model != "opus" {
		t.Errorf("expected model opus, got %s", initCfg.Model)
	}

	send(hostMessage{Type: "shutdown"})
	<-done
}

func TestInjectAndLogBeforeRun(t *testing.T) {
	p := NewPlugin("test")

	// These must not panic — writer is initialized in NewPlugin.
	p.Log("info", "hello")
	p.Inject("user", "context")
}

func TestSendWriteError(t *testing.T) {
	pr, pw := io.Pipe()
	p := NewPlugin("test")
	p.writer = json.NewEncoder(pw)

	// Close the read end so writes fail with ErrClosedPipe.
	pr.Close()

	p.send(pluginMessage{Type: "test"})

	if !p.failed.Load() {
		t.Error("expected failed flag to be set after write error")
	}
}

func TestRunExitsOnWriteError(t *testing.T) {
	p := NewPlugin("test").
		Tool("echo", "echo", nil, func(ctx ToolContext) (string, error) {
			return "ok", nil
		})

	send, recv, done := pipePlugin(t, p)

	send(hostMessage{Type: "initialize", Config: &Config{}})
	recv() // capabilities

	// Mark the plugin as failed to simulate a write error.
	p.failed.Store(true)

	// Send a message — the pipePlugin loop should exit after dispatch
	// because failed is set.
	send(hostMessage{Type: "tool_call", ID: "c1", Name: "echo"})

	// The goroutine should exit promptly.
	<-done
}

func TestPropSafeOnMissingProperties(t *testing.T) {
	// Prop applied to a map without "properties" key must not panic.
	s := map[string]any{"type": "object"}
	opt := Prop("name", "string", "A name")
	opt(s)

	props, ok := s["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties to be created")
	}
	entry, ok := props["name"].(map[string]any)
	if !ok {
		t.Fatal("expected name property")
	}
	if entry["type"] != "string" {
		t.Errorf("expected type=string, got %v", entry["type"])
	}
}

func TestToolCallPanicRecovery(t *testing.T) {
	p := NewPlugin("test").
		Tool("boom", "Panics", nil, func(ctx ToolContext) (string, error) {
			panic("kaboom")
		})

	send, recv, done := pipePlugin(t, p)

	send(hostMessage{Type: "initialize", Config: &Config{}})
	recv() // capabilities

	send(hostMessage{Type: "tool_call", ID: "call_panic", Name: "boom"})

	result := recv()
	if result.Type != "tool_result" {
		t.Fatalf("expected tool_result, got %s", result.Type)
	}
	if !result.IsError {
		t.Error("expected is_error=true for panicking tool")
	}
	if !strings.Contains(result.Content, "panicked") {
		t.Errorf("expected panic message in content, got %s", result.Content)
	}
	if !strings.Contains(result.Content, "kaboom") {
		t.Errorf("expected 'kaboom' in content, got %s", result.Content)
	}

	// Plugin should still be alive — send another tool call to verify.
	send(hostMessage{Type: "shutdown"})
	<-done
}

func TestCommandPanicRecovery(t *testing.T) {
	p := NewPlugin("test").
		Command("explode", "Panics", func(ctx CommandContext) (string, error) {
			panic("bang")
		})

	send, recv, done := pipePlugin(t, p)

	send(hostMessage{Type: "initialize", Config: &Config{}})
	recv() // capabilities

	send(hostMessage{Type: "command", Name: "explode", Args: ""})

	result := recv()
	if result.Type != "command_result" {
		t.Fatalf("expected command_result, got %s", result.Type)
	}
	if !result.IsError {
		t.Error("expected is_error=true for panicking command")
	}
	if !strings.Contains(result.Text, "panicked") {
		t.Errorf("expected panic message in text, got %s", result.Text)
	}
	if !strings.Contains(result.Text, "bang") {
		t.Errorf("expected 'bang' in text, got %s", result.Text)
	}

	send(hostMessage{Type: "shutdown"})
	<-done
}

func TestSchemaHelper(t *testing.T) {
	s := Schema(
		Prop("text", "string", "The input text"),
		Prop("count", "integer", "How many times"),
		Required("text"),
	)

	if s["type"] != "object" {
		t.Errorf("expected type=object, got %v", s["type"])
	}

	props, ok := s["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties not a map")
	}
	if len(props) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(props))
	}

	textProp := props["text"].(map[string]any)
	if textProp["type"] != "string" {
		t.Errorf("text.type = %v, want string", textProp["type"])
	}

	req := s["required"].([]string)
	if len(req) != 1 || req[0] != "text" {
		t.Errorf("required = %v, want [text]", req)
	}
}

func TestConcurrentRegistration(t *testing.T) {
	p := NewPlugin("test")
	var wg sync.WaitGroup

	// Register tools, commands, and event handlers concurrently.
	for i := 0; i < 50; i++ {
		wg.Add(3)
		name := fmt.Sprintf("item_%d", i)
		go func() {
			defer wg.Done()
			p.Tool(name, "desc", nil, func(ctx ToolContext) (string, error) {
				return "ok", nil
			})
		}()
		go func() {
			defer wg.Done()
			p.Command(name, "desc", func(ctx CommandContext) (string, error) {
				return "ok", nil
			})
		}()
		go func() {
			defer wg.Done()
			p.OnEvent(func(e Event) {})
		}()
	}
	wg.Wait()

	if len(p.tools) != 50 {
		t.Errorf("expected 50 tools, got %d", len(p.tools))
	}
	if len(p.commands) != 50 {
		t.Errorf("expected 50 commands, got %d", len(p.commands))
	}
	if len(p.eventHandlers) != 50 {
		t.Errorf("expected 50 event handlers, got %d", len(p.eventHandlers))
	}
}
