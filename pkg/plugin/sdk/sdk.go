// Package sdk provides a lightweight SDK for building Gi plugins. It handles
// the JSONL protocol, init/capabilities handshake, and message routing so
// plugin authors can focus on tool and command logic.
//
// Example:
//
//	p := sdk.NewPlugin("my-plugin").
//		Tool("greet", "Say hello", schema, func(ctx sdk.ToolContext) (string, error) {
//			name, _ := ctx.Params["name"].(string)
//			return fmt.Sprintf("Hello, %s!", name), nil
//		}).
//		Command("status", "Show status", func(ctx sdk.CommandContext) (string, error) {
//			return "all good", nil
//		}).
//		OnEvent(func(e sdk.Event) {
//			log.Printf("event: %s", e.Type)
//		})
//
//	p.Run()
package sdk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// maxBuffer is the maximum JSONL message size (1 MB), matching the host.
const maxBuffer = 1024 * 1024

// ToolHandler is a function that handles a tool_call.
type ToolHandler func(ctx ToolContext) (string, error)

// CommandHandler is a function that handles a command invocation.
type CommandHandler func(ctx CommandContext) (string, error)

// EventHandler is a function that handles an event notification.
type EventHandler func(e Event)

// InitHandler is a function called when the plugin receives the initialize
// message. It receives the config and may return an error to abort startup.
type InitHandler func(cfg Config) error

// ToolContext is passed to a ToolHandler with the call details.
type ToolContext struct {
	ID     string
	Name   string
	Params map[string]any
	Config Config
}

// CommandContext is passed to a CommandHandler with the call details.
type CommandContext struct {
	Name   string
	Args   string
	Config Config
}

// Config is the plugin configuration received during initialization.
type Config struct {
	Cwd       string `json:"cwd"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	GiVersion string `json:"gi_version"`
}

// Event is an agent lifecycle event forwarded to the plugin.
type Event struct {
	Type       string         `json:"type"`
	ToolName   string         `json:"tool_name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolArgs   map[string]any `json:"tool_args,omitempty"`
	ToolResult string         `json:"tool_result,omitempty"`
	ToolError  bool           `json:"tool_error,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// toolEntry holds a tool definition and its handler.
type toolEntry struct {
	def     toolDef
	handler ToolHandler
}

// commandEntry holds a command definition and its handler.
type commandEntry struct {
	def     commandDef
	handler CommandHandler
}

// --- Wire protocol types (internal) ---

type hostMessage struct {
	Type   string         `json:"type"`
	ID     string         `json:"id,omitempty"`
	Name   string         `json:"name,omitempty"`
	Params map[string]any `json:"params,omitempty"`
	Args   string         `json:"args,omitempty"`
	Config *Config        `json:"config,omitempty"`
	Event  *Event         `json:"event,omitempty"`
}

type pluginMessage struct {
	Type     string       `json:"type"`
	ID       string       `json:"id,omitempty"`
	Content  string       `json:"content,omitempty"`
	Text     string       `json:"text,omitempty"`
	IsError  bool         `json:"is_error,omitempty"`
	Tools    []toolDef    `json:"tools,omitempty"`
	Commands []commandDef `json:"commands,omitempty"`
	Role     string       `json:"role,omitempty"`
	Level    string       `json:"level,omitempty"`
	Message  string       `json:"message,omitempty"`
}

type toolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

type commandDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Plugin is the main entry point for building a Gi plugin.
type Plugin struct {
	name          string
	tools         []toolEntry
	commands      []commandEntry
	eventHandlers []EventHandler
	initHandler   InitHandler
	config        Config

	mu     sync.Mutex
	writer *json.Encoder
}

// NewPlugin creates a new plugin with the given name.
func NewPlugin(name string) *Plugin {
	return &Plugin{name: name}
}

// Tool registers a tool with the given name, description, JSON Schema, and handler.
// The schema should be a JSON Schema object describing the tool's input parameters.
// Returns the plugin for chaining.
func (p *Plugin) Tool(name, description string, inputSchema any, handler ToolHandler) *Plugin {
	p.tools = append(p.tools, toolEntry{
		def: toolDef{
			Name:        name,
			Description: description,
			InputSchema: inputSchema,
		},
		handler: handler,
	})
	return p
}

// Command registers a slash command with the given name, description, and handler.
// Returns the plugin for chaining.
func (p *Plugin) Command(name, description string, handler CommandHandler) *Plugin {
	p.commands = append(p.commands, commandEntry{
		def: commandDef{
			Name:        name,
			Description: description,
		},
		handler: handler,
	})
	return p
}

// OnEvent registers an event handler. Multiple handlers can be registered.
// Returns the plugin for chaining.
func (p *Plugin) OnEvent(handler EventHandler) *Plugin {
	p.eventHandlers = append(p.eventHandlers, handler)
	return p
}

// OnInit registers an initialization handler called when the plugin receives
// the initialize message. Returns the plugin for chaining.
func (p *Plugin) OnInit(handler InitHandler) *Plugin {
	p.initHandler = handler
	return p
}

// Run starts the plugin message loop. It blocks until stdin is closed or a
// shutdown message is received. Run handles the init/capabilities handshake
// automatically and routes messages to the registered handlers.
func (p *Plugin) Run() {
	p.writer = json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, maxBuffer), maxBuffer)

	// Wait for initialize message.
	if !scanner.Scan() {
		return
	}
	var initMsg hostMessage
	if err := json.Unmarshal(scanner.Bytes(), &initMsg); err != nil {
		p.logf("error", "failed to parse initialize message: %v", err)
		return
	}
	if initMsg.Type != "initialize" {
		p.logf("error", "expected initialize, got %s", initMsg.Type)
		return
	}
	if initMsg.Config != nil {
		p.config = *initMsg.Config
	}

	// Call init handler if registered.
	if p.initHandler != nil {
		if err := p.initHandler(p.config); err != nil {
			p.logf("error", "init handler failed: %v", err)
			return
		}
	}

	// Send capabilities.
	caps := pluginMessage{Type: "capabilities"}
	for _, t := range p.tools {
		caps.Tools = append(caps.Tools, t.def)
	}
	for _, c := range p.commands {
		caps.Commands = append(caps.Commands, c.def)
	}
	p.send(caps)

	p.logf("info", "%s initialized (%d tools, %d commands)", p.name, len(p.tools), len(p.commands))

	// Main message loop.
	for scanner.Scan() {
		var msg hostMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			p.logf("warn", "failed to parse message: %v", err)
			continue
		}
		p.dispatch(msg)
	}
}

// Inject sends an inject_message to add context to the conversation.
// Safe to call from any goroutine.
func (p *Plugin) Inject(role, content string) {
	p.send(pluginMessage{
		Type:    "inject_message",
		Role:    role,
		Content: content,
	})
}

// Log sends a log message to the host. Safe to call from any goroutine.
func (p *Plugin) Log(level, message string) {
	p.send(pluginMessage{
		Type:    "log",
		Level:   level,
		Message: message,
	})
}

func (p *Plugin) dispatch(msg hostMessage) {
	switch msg.Type {
	case "tool_call":
		p.handleToolCall(msg)
	case "command":
		p.handleCommand(msg)
	case "event":
		p.handleEvent(msg)
	case "shutdown":
		os.Exit(0)
	}
}

func (p *Plugin) handleToolCall(msg hostMessage) {
	for _, t := range p.tools {
		if t.def.Name == msg.Name {
			ctx := ToolContext{
				ID:     msg.ID,
				Name:   msg.Name,
				Params: msg.Params,
				Config: p.config,
			}
			result, err := t.handler(ctx)
			if err != nil {
				p.send(pluginMessage{
					Type:    "tool_result",
					ID:      msg.ID,
					Content: err.Error(),
					IsError: true,
				})
				return
			}
			p.send(pluginMessage{
				Type:    "tool_result",
				ID:      msg.ID,
				Content: result,
			})
			return
		}
	}
	p.send(pluginMessage{
		Type:    "tool_result",
		ID:      msg.ID,
		Content: fmt.Sprintf("unknown tool: %s", msg.Name),
		IsError: true,
	})
}

func (p *Plugin) handleCommand(msg hostMessage) {
	for _, c := range p.commands {
		if c.def.Name == msg.Name {
			ctx := CommandContext{
				Name:   msg.Name,
				Args:   msg.Args,
				Config: p.config,
			}
			result, err := c.handler(ctx)
			if err != nil {
				p.send(pluginMessage{
					Type:    "command_result",
					Text:    err.Error(),
					IsError: true,
				})
				return
			}
			p.send(pluginMessage{
				Type: "command_result",
				Text: result,
			})
			return
		}
	}
	p.send(pluginMessage{
		Type:    "command_result",
		Text:    fmt.Sprintf("unknown command: %s", msg.Name),
		IsError: true,
	})
}

func (p *Plugin) handleEvent(msg hostMessage) {
	if msg.Event == nil {
		return
	}
	for _, h := range p.eventHandlers {
		h(*msg.Event)
	}
}

func (p *Plugin) send(msg pluginMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.writer.Encode(msg)
}

func (p *Plugin) logf(level, format string, args ...any) {
	p.Log(level, fmt.Sprintf(format, args...))
}

// Schema is a helper for building JSON Schema objects for tool input parameters.
// It returns a map suitable for use as the inputSchema argument to Plugin.Tool.
//
// Example:
//
//	sdk.Schema(
//		sdk.Prop("text", "string", "The text to process"),
//		sdk.Required("text"),
//	)
func Schema(opts ...SchemaOption) map[string]any {
	s := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// SchemaOption configures a JSON Schema object.
type SchemaOption func(map[string]any)

// Prop adds a property to the schema.
func Prop(name, typ, description string) SchemaOption {
	return func(s map[string]any) {
		props := s["properties"].(map[string]any)
		props[name] = map[string]any{
			"type":        typ,
			"description": description,
		}
	}
}

// Required marks the given property names as required.
func Required(names ...string) SchemaOption {
	return func(s map[string]any) {
		s["required"] = names
	}
}
