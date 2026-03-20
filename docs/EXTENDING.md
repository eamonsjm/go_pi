# Extending Gi: A Step-by-Step Guide

This guide shows how to extend Gi with custom tools and providers. For architectural overview, see [ARCHITECTURE.md](./ARCHITECTURE.md).

## Adding a Custom Tool (Built-in)

Built-in tools are compiled into Gi and always available. They're fastest and most integrated.

### Example: Weather Tool

We'll add a tool that fetches current weather.

**Step 1: Create the tool file**

Create `pkg/tools/weather.go`:

```go
package tools

import (
	"context"
	"fmt"
)

type WeatherTool struct{}

func (w *WeatherTool) Name() string {
	return "get_weather"
}

func (w *WeatherTool) Description() string {
	return "Get current weather for a city"
}

func (w *WeatherTool) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city": map[string]any{
				"type":        "string",
				"description": "City name (e.g., 'San Francisco')",
			},
			"units": map[string]any{
				"type":        "string",
				"enum":        []string{"celsius", "fahrenheit"},
				"description": "Temperature units (default: fahrenheit)",
			},
		},
		"required": []string{"city"},
	}
}

func (w *WeatherTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	city, ok := params["city"].(string)
	if !ok || city == "" {
		return "", fmt.Errorf("city parameter is required")
	}

	units, _ := params["units"].(string)
	if units == "" {
		units = "fahrenheit"
	}

	// In a real tool, call a weather API here.
	// For now, return mock data.
	if units == "celsius" {
		return fmt.Sprintf("Weather in %s: 20°C, partly cloudy, wind 12 km/h W", city), nil
	}
	return fmt.Sprintf("Weather in %s: 68°F, partly cloudy, wind 12 mph W", city), nil
}
```

**Step 2: Register the tool**

Edit `cmd/gi/main.go` and find the `tools.RegisterDefaults()` call:

```go
registry := tools.NewRegistry()
tools.RegisterDefaults(registry)

// Add this line:
registry.Register(&tools.WeatherTool{})
```

**Step 3: Test it**

```bash
go build -o gi ./cmd/gi/
export ANTHROPIC_API_KEY=sk-...
./gi "What's the weather in San Francisco?"
```

The agent will now have access to the weather tool and can call it during conversation.

### Tool Implementation Best Practices

1. **Clear schema** - JSON Schema must be precise. Models perform better with exact type definitions.
2. **Error handling** - Return meaningful error messages. Models use them to retry or adjust.
3. **Streaming results** - For long operations, consider emitting partial results.
4. **Idempotency** - Tools should be safe to call multiple times with the same parameters.
5. **Context awareness** - Use the `context.Context` for cancellation and timeouts.

### Rich Tools (Multi-block Results)

For tools that return text + images or other multi-block content, implement `RichTool`:

```go
func (w *WeatherTool) ExecuteRich(ctx context.Context, params map[string]any) ([]ai.ContentBlock, error) {
	// Return weather as text + an image chart
	return []ai.ContentBlock{
		{Type: "text", Text: "Weather description..."},
		{Type: "image", Source: imageBase64},
	}, nil
}
```

## Adding a Plugin Tool (External)

Plugins are external executables that extend Gi without recompiling. They're slower but more flexible.

### Example: Database Query Plugin

We'll create a plugin that executes SQL queries.

**Step 1: Create the plugin directory**

```bash
mkdir -p ~/.gi/plugins/sql-plugin
cd ~/.gi/plugins/sql-plugin
```

**Step 2: Create plugin.json manifest**

Create `plugin.json`:

```json
{
  "name": "sql-plugin",
  "version": "0.1.0",
  "description": "Execute SQL queries",
  "executable": "./sql-plugin"
}
```

**Step 3: Create the plugin executable**

Create `main.go`:

```go
package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
)

// Message types (mirror the host protocol)
type HostMessage struct {
	Type   string         `json:"type"`
	ID     string         `json:"id,omitempty"`
	Name   string         `json:"name,omitempty"`
	Params map[string]any `json:"params,omitempty"`
	Config map[string]any `json:"config,omitempty"`
}

type PluginMessage struct {
	Type    string       `json:"type"`
	ID      string       `json:"id,omitempty"`
	Content string       `json:"content,omitempty"`
	Tools   []ToolDef    `json:"tools,omitempty"`
	IsError bool         `json:"is_error,omitempty"`
}

type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

var db *sql.DB

func main() {
	// In production, initialize real database connection
	// db, _ = sql.Open("postgres", os.Getenv("DATABASE_URL"))

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		var msg HostMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "initialize":
			send(PluginMessage{
				Type: "capabilities",
				Tools: []ToolDef{
					{
						Name:        "execute_sql",
						Description: "Execute SQL queries against the database",
						InputSchema: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"query": map[string]any{
									"type":        "string",
									"description": "SQL query to execute",
								},
							},
							"required": []string{"query"},
						},
					},
				},
			})

		case "tool_call":
			if msg.Name == "execute_sql" {
				query, _ := msg.Params["query"].(string)
				// Execute query here
				send(PluginMessage{
					Type:    "tool_result",
					ID:      msg.ID,
					Content: "Query executed successfully (mock)",
				})
			}

		case "shutdown":
			os.Exit(0)
		}
	}
}

func send(msg PluginMessage) {
	data, _ := json.Marshal(msg)
	fmt.Fprintln(os.Stdout, string(data))
}
```

**Step 4: Build and test**

```bash
go build -o sql-plugin .
chmod +x sql-plugin

# Now when you run gi, it will discover and load the plugin
export ANTHROPIC_API_KEY=sk-...
gi "What tables are in the database?"
```

### Plugin Protocol Reference

**Host → Plugin:**
- `initialize` - Host requests capabilities (tools, commands)
- `tool_call` - Host requests tool execution
- `command` - Host requests command execution
- `event` - Host forwards agent events
- `shutdown` - Host is shutting down

**Plugin → Host:**
- `capabilities` - Plugin reports tools and commands (response to `initialize`)
- `tool_result` - Tool execution result
- `command_result` - Command execution result
- `log` - Plugin logs a message
- `inject_message` - Plugin injects a message into conversation
- `ui_request` - Plugin requests UI (dialog, input, etc.)

### Plugin Best Practices

1. **Fast startup** - Initialize minimal state at startup. Defer heavy setup.
2. **Graceful shutdown** - Handle `shutdown` message cleanly.
3. **Error recovery** - Return clear error messages in `is_error: true` responses.
4. **Stateless tools** - Don't rely on plugin-level state. Handle per-call.
5. **Language agnostic** - Any language can implement the JSONL protocol (Python, Node, Rust, etc.).

### Using the Go Plugin SDK

For Go plugins, use the provided SDK:

```go
import "github.com/ejm/go_pi/pkg/plugin/sdk"

// In your plugin main.go:
func main() {
	p := sdk.NewPlugin()

	p.RegisterTool("execute_sql", "Execute SQL queries",
		map[string]any{...}, // schema
		func(ctx context.Context, params map[string]any) (string, error) {
			// Implementation
		})

	if err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
```

## Adding a Custom Provider

Providers implement support for new LLM APIs (Claude, GPT, Gemini, etc.).

### Provider Interface

```go
type Provider interface {
    Call(ctx context.Context, cfg CallConfig) (*Message, error)
}
```

### Example: Adding Ollama Support

Create `pkg/ai/ollama.go`:

```go
package ai

import (
	"context"
	"fmt"
)

type OllamaProvider struct {
	baseURL string
	model   string
}

func NewOllamaProvider(baseURL, model string) *OllamaProvider {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &OllamaProvider{
		baseURL: baseURL,
		model:   model,
	}
}

func (o *OllamaProvider) Call(ctx context.Context, cfg CallConfig) (*Message, error) {
	// Translate Gi message format to Ollama API format
	// Call Ollama API
	// Translate response back to Gi message format

	// Placeholder implementation
	return &Message{
		Role: RoleAssistant,
		Text: "Response from Ollama",
	}, nil
}

func (o *OllamaProvider) Name() string {
	return "ollama"
}

func (o *OllamaProvider) MaxTokens() int {
	return 4096 // Ollama model dependent
}
```

Register in `cmd/gi/main.go`:

```go
func resolveProvider(cfg *config.Config, resolver *auth.Resolver) (ai.Provider, error) {
	providerName := cfg.DefaultProvider
	// ... existing code ...

	switch providerName {
	// ... existing cases ...
	case "ollama":
		return ai.NewOllamaProvider(
			os.Getenv("OLLAMA_BASE_URL"),
			model,
		)
	}
}
```

### Provider Implementation Checklist

1. **Message format translation** - Convert between Gi's format and the API's format
2. **Streaming support** - Handle streamed responses (tokens, tool calls)
3. **Tool support** - Properly format and handle tool definitions and results
4. **Error handling** - Map API errors to Gi's error types
5. **Usage tracking** - Report token usage for cost tracking
6. **Thinking support** - If the model supports extended thinking, expose it
7. **Context windows** - Know and respect the model's maximum tokens

## Using the SDK for Custom Applications

The SDK allows you to use Gi as a library in your own Go programs.

### Example: Custom AI Application

```go
package main

import (
	"context"
	"fmt"

	"github.com/ejm/go_pi/pkg/sdk"
	"github.com/ejm/go_pi/pkg/tools"
)

func main() {
	// Create session with custom tools
	registry := tools.NewRegistry()
	registry.Register(&MyCustomTool{})

	s, err := sdk.NewSession(
		sdk.WithAPIKey("anthropic", "sk-..."),
		sdk.WithTools(registry),
		sdk.WithModel("claude-sonnet-4-20250514"),
		sdk.WithSystemPrompt("You are a helpful assistant."),
	)
	if err != nil {
		panic(err)
	}
	defer s.Close()

	// Run agent
	ctx := context.Background()
	events := s.Events()

	go func() {
		s.Prompt(ctx, "What can you do?")
	}()

	for event := range events {
		switch event.Type {
		case agent.EventAssistantText:
			fmt.Print(event.Delta)
		case agent.EventToolExecStart:
			fmt.Printf("[Calling %s]\n", event.ToolName)
		case agent.EventAgentEnd:
			return
		}
	}
}

// Implement your own tool
type MyCustomTool struct{}

func (t *MyCustomTool) Name() string {
	return "my_tool"
}

func (t *MyCustomTool) Description() string {
	return "Does something useful"
}

func (t *MyCustomTool) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"input": map[string]any{
				"type": "string",
			},
		},
	}
}

func (t *MyCustomTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	return "Result", nil
}
```

## Extension Patterns

### Pattern 1: Domain-Specific Agent

Create a specialized agent with custom tools for a specific domain (data analysis, code review, etc.):

```go
func newDataAnalysisSession() (*sdk.Session, error) {
	registry := tools.NewRegistry()
	registry.Register(&SQLQueryTool{})
	registry.Register(&DataVizTool{})
	registry.Register(&StatisticalAnalysisTool{})

	return sdk.NewSession(
		sdk.WithTools(registry),
		sdk.WithSystemPrompt("You are a data analyst..."),
	)
}
```

### Pattern 2: Tool Wrapper

Wrap an existing tool with logic (caching, retry, validation):

```go
type CachingBashTool struct {
	inner *tools.BashTool
	cache map[string]string
}

func (c *CachingBashTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	cmd := params["command"].(string)

	if result, ok := c.cache[cmd]; ok {
		return result, nil
	}

	result, err := c.inner.Execute(ctx, params)
	if err == nil {
		c.cache[cmd] = result
	}
	return result, err
}
```

### Pattern 3: Observability Plugin

Create a plugin that logs all agent activity:

```go
// Plugin receives events and logs to external system
type LoggingPlugin struct {
	backend LogBackend
}

func (l *LoggingPlugin) handleEvent(event EventPayload) {
	l.backend.Log(&LogEntry{
		Timestamp: time.Now(),
		EventType: event.Type,
		ToolName:  event.ToolName,
		// ...
	})
}
```

## Testing Extensions

### Testing a Tool

```go
func TestWeatherTool(t *testing.T) {
	tool := &tools.WeatherTool{}

	result, err := tool.Execute(context.Background(), map[string]any{
		"city": "San Francisco",
		"units": "celsius",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "San Francisco") {
		t.Errorf("result missing city name: %s", result)
	}
}
```

### Testing a Provider

Mock the LLM response:

```go
type MockProvider struct {
	response string
}

func (m *MockProvider) Call(ctx context.Context, cfg CallConfig) (*Message, error) {
	return &Message{
		Role: RoleAssistant,
		Text: m.response,
	}, nil
}

// Use in tests:
session := agent.NewAgentLoop(
	&MockProvider{response: "test response"},
	registry,
)
```

### Testing a Plugin

Run the plugin as a subprocess and test JSONL protocol:

```go
func TestPluginInitialization(t *testing.T) {
	pm := plugin.NewManager(registry)
	if err := pm.LoadPlugin("./sql-plugin"); err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	procs := pm.Plugins()
	if len(procs) == 0 {
		t.Fatal("plugin not loaded")
	}
}
```

## Debugging Extensions

### Enable Debug Logging

Plugins can log to stderr (visible in TUI):

```go
func sendLog(level, message string) {
	msg := PluginMessage{
		Type:    "log",
		Level:   level,
		Message: message,
	}
	data, _ := json.Marshal(msg)
	fmt.Fprintln(os.Stdout, string(data))
}
```

### Inspect Agent Events

The agent loop emits detailed events:

```go
for event := range events {
	fmt.Printf("[%s] %+v\n", event.Type, event)
}
```

### Test Tools Standalone

Use the SDK to test tools without the full agent:

```go
tool := &MyTool{}
result, err := tool.Execute(context.Background(), map[string]any{
	"param": "value",
})
fmt.Printf("Result: %s, Error: %v\n", result, err)
```

## Common Patterns and Pitfalls

| Issue | Solution |
|-------|----------|
| Tool is too slow | Make it return partial results or stream output |
| Tool errors aren't clear | Return descriptive error messages |
| Plugin doesn't load | Check `plugin.json` exists and executable is in PATH |
| Plugin hangs | Ensure stdin/stdout are not blocking |
| Model doesn't use tool | Check schema is precise; LLMs ignore vague tools |
| Expensive API calls | Cache results or use a rate limiter |

## What's Next?

- Read [ARCHITECTURE.md](./ARCHITECTURE.md) for deeper design understanding
- Check `examples/` directory for complete working examples
- Look at `pkg/tools/` for reference implementations
- Check `examples/hello-plugin/` for a minimal plugin
- See `examples/custom-tool/` for a tool integrated via SDK
