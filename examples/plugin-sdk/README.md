# Gi Plugin SDK

Lightweight SDK packages for writing Gi plugins in Go and Python. These are thin
wrappers around the stdin/stdout JSONL protocol that handle the
init/capabilities handshake, message routing, and response formatting.

## Go SDK

**Package**: `github.com/ejm/go_pi/pkg/plugin/sdk`

### Quick Start

```go
package main

import (
    "fmt"
    "strings"

    "github.com/ejm/go_pi/pkg/plugin/sdk"
)

func main() {
    p := sdk.NewPlugin("my-plugin").
        Tool("word_count", "Count words", sdk.Schema(
            sdk.Prop("text", "string", "Text to count"),
            sdk.Required("text"),
        ), func(ctx sdk.ToolContext) (string, error) {
            text, _ := ctx.Params["text"].(string)
            return fmt.Sprintf("%d", len(strings.Fields(text))), nil
        }).
        Command("wc", "Word count", func(ctx sdk.CommandContext) (string, error) {
            return fmt.Sprintf("%d", len(strings.Fields(ctx.Args))), nil
        }).
        OnEvent(func(e sdk.Event) {
            // Handle agent lifecycle events
        })

    p.Run()
}
```

### API

#### `sdk.NewPlugin(name string) *Plugin`

Create a new plugin. Returns a builder that supports method chaining.

#### `Plugin.Tool(name, description string, inputSchema any, handler ToolHandler) *Plugin`

Register a tool. The handler receives a `ToolContext` with `ID`, `Name`,
`Params`, and `Config` fields. Return a string result or an error.

#### `Plugin.Command(name, description string, handler CommandHandler) *Plugin`

Register a slash command. The handler receives a `CommandContext` with `Name`,
`Args`, and `Config` fields.

#### `Plugin.OnEvent(handler EventHandler) *Plugin`

Register an event handler. Called for each agent lifecycle event (fire-and-forget).

#### `Plugin.OnInit(handler InitHandler) *Plugin`

Register an init handler called after the initialize message is received.

#### `Plugin.Inject(role, content string)`

Send an inject_message to add context to the conversation. Thread-safe.

#### `Plugin.Log(level, message string)`

Send a log message to the host. Thread-safe.

#### `Plugin.Run()`

Start the message loop. Blocks until stdin closes or shutdown is received.

#### Schema Helpers

```go
schema := sdk.Schema(
    sdk.Prop("text", "string", "The input text"),
    sdk.Prop("count", "integer", "Repeat count"),
    sdk.Required("text"),
)
```

## Python SDK

**Module**: `gi_plugin.py` (single file, no dependencies)

### Quick Start

```python
from gi_plugin import Plugin

plugin = Plugin("my-plugin")

@plugin.tool("word_count", "Count words", {
    "type": "object",
    "properties": {
        "text": {"type": "string", "description": "Text to count"},
    },
    "required": ["text"],
})
def word_count(params, config):
    return f"{len(params['text'].split())} words"

@plugin.command("wc", "Word count")
def wc(args, config):
    return f"{len(args.split())} words"

@plugin.event
def on_event(event):
    pass  # Handle agent lifecycle events

plugin.run()
```

### API

#### `Plugin(name: str)`

Create a new plugin.

#### `@plugin.tool(name, description, input_schema=None)`

Decorator to register a tool handler. The function receives `(params: dict, config: Config)`.

#### `@plugin.command(name, description)`

Decorator to register a command handler. The function receives `(args: str, config: Config)`.

#### `@plugin.event`

Decorator to register an event handler. The function receives `(event: dict)`.

#### `@plugin.on_init`

Decorator to register an init handler. The function receives `(config: Config)`.

#### `plugin.inject(role, content)`

Send an inject_message to add context to the conversation.

#### `plugin.log(level, message)`

Send a log message to the host.

#### `plugin.run()`

Start the message loop. Blocks until stdin closes or shutdown is received.

## What the SDK handles for you

- **Init/capabilities handshake**: Automatically responds to `initialize` with
  your declared tools and commands.
- **Message routing**: Dispatches `tool_call`, `command`, `event`, and `shutdown`
  messages to the correct handlers.
- **Response formatting**: Wraps your return values in the correct JSONL response
  envelope with matching IDs.
- **Error handling**: Catches handler exceptions and returns `is_error: true`
  responses.
- **Logging**: Provides `Log()`/`log()` methods for structured debug output.

## Installing a plugin

1. Build your plugin (Go: `go build`, Python: make executable with `chmod +x`)
2. Create a directory under `~/.gi/plugins/` or `.gi/plugins/`
3. Add a `plugin.json` manifest:

```json
{
  "name": "my-plugin",
  "version": "1.0.0",
  "description": "My custom plugin",
  "executable": "./my-plugin"
}
```

See [PLUGINS.md](../../docs/PLUGINS.md) for the full protocol specification.
