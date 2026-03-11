# Pi Plugin System

## Overview

Pi plugins are external executables that communicate with the host via JSONL over stdin/stdout. This makes plugins language-agnostic -- they can be written in Go, Python, Node.js, Rust, or any language that can read and write JSON lines.

Each plugin runs as a subprocess managed by the Pi host process. The host sends structured messages to the plugin's stdin and reads responses from the plugin's stdout. The plugin's stderr is captured for logging but does not participate in the protocol.

## Plugin Capabilities

A plugin can declare any combination of the following capabilities:

- **tools** -- Register custom tools that the LLM can call. Plugin tools appear alongside built-in tools (read, write, edit, bash, glob, grep) and are indistinguishable from the model's perspective.

- **commands** -- Register slash commands that the user can invoke from the TUI (e.g., `/greet`). Plugin commands integrate with the existing `CommandRegistry`.

- **events** -- Receive agent lifecycle event notifications (`agent_start`, `agent_end`, `tool_exec_start`, `tool_exec_end`, etc.). This enables plugins to observe the agent's behavior, collect metrics, or trigger side effects.

- **inject** -- Emit messages into the conversation. A plugin can inject context (e.g., project-specific instructions, retrieved documentation) as user messages that appear in the conversation history.

## Discovery & Loading

Plugins are discovered from three locations, searched in order:

1. **Global plugins**: `~/.pi/plugins/` directory
2. **Project-local plugins**: `.pi/plugins/` directory (relative to cwd)
3. **CLI flag**: `--plugin ./path/to/plugin` (can be specified multiple times)

### Plugin Directory Layout

Each plugin lives in its own subdirectory and contains either:

- A `plugin.json` manifest that points to the executable, or
- A single executable file (the directory name is used as the plugin name)

Example directory structure:

```
~/.pi/plugins/
  rtk-optimizer/
    plugin.json
    rtk-optimizer          # the executable
  my-tool/
    my-tool                # executable, no manifest needed
```

### Plugin Manifest Format

```json
{
  "name": "rtk-optimizer",
  "version": "1.0.0",
  "description": "Token optimization for dev operations",
  "executable": "./rtk-optimizer",
  "capabilities": ["tools", "commands", "events"]
}
```

| Field          | Required | Description                                                |
|----------------|----------|------------------------------------------------------------|
| `name`         | Yes      | Unique plugin name (alphanumeric, hyphens, underscores)    |
| `version`      | No       | SemVer version string                                      |
| `description`  | No       | Human-readable description                                 |
| `executable`   | Yes      | Path to the executable, relative to the manifest directory |
| `capabilities` | No       | Array of capability strings; defaults to all capabilities  |

If no manifest is present, the host looks for an executable matching the directory name. All capabilities are assumed.

## Protocol

Communication uses **JSONL** (newline-delimited JSON) over stdin/stdout. Each message is a single JSON object terminated by a newline (`\n`). Messages must not span multiple lines.

### Host -> Plugin Messages

#### initialize

Sent once after the plugin process starts. The plugin must respond with a `capabilities` message.

```json
{
  "type": "initialize",
  "config": {
    "cwd": "/home/user/project",
    "model": "claude-sonnet-4-20250514",
    "provider": "anthropic",
    "pi_version": "0.1.0"
  }
}
```

#### tool_call

Sent when the LLM invokes a plugin-provided tool. The plugin must respond with a `tool_result` message with a matching `id`.

```json
{
  "type": "tool_call",
  "id": "toolu_01ABC123",
  "name": "rtk_optimize",
  "params": {
    "command": "git status"
  }
}
```

#### command

Sent when the user invokes a plugin-provided slash command. The plugin must respond with a `command_result` message.

```json
{
  "type": "command",
  "name": "greet",
  "args": "world"
}
```

#### event

Sent when an agent lifecycle event occurs. No response is expected.

```json
{
  "type": "event",
  "event": {
    "type": "tool_exec_start",
    "tool_name": "bash",
    "tool_call_id": "toolu_01XYZ",
    "tool_args": {"command": "go build ./..."}
  }
}
```

The `event.type` field corresponds to the `AgentEventType` constants defined in `pkg/agent/types.go`:
- `agent_start` -- agent loop has begun
- `agent_end` -- agent loop has completed
- `turn_start` -- new LLM turn starting
- `turn_end` -- LLM turn completed
- `tool_exec_start` -- tool execution beginning
- `tool_exec_end` -- tool execution completed
- `usage_update` -- token usage update
- `agent_error` -- an error occurred

#### shutdown

Sent when Pi is exiting. The plugin should perform any cleanup and exit.

```json
{
  "type": "shutdown"
}
```

### Plugin -> Host Messages

#### capabilities

Response to `initialize`. Declares the tools and commands the plugin provides.

```json
{
  "type": "capabilities",
  "tools": [
    {
      "name": "rtk_optimize",
      "description": "Optimize a command for reduced token output",
      "input_schema": {
        "type": "object",
        "properties": {
          "command": {
            "type": "string",
            "description": "The command to optimize"
          }
        },
        "required": ["command"]
      }
    }
  ],
  "commands": [
    {
      "name": "rtk-stats",
      "description": "Show token optimization statistics"
    }
  ]
}
```

#### tool_result

Response to a `tool_call`. The `id` must match the request.

```json
{
  "type": "tool_result",
  "id": "toolu_01ABC123",
  "content": "Optimized output:\n  M src/main.go\n  ?? new_file.txt",
  "is_error": false
}
```

#### command_result

Response to a `command` invocation.

```json
{
  "type": "command_result",
  "text": "Token savings: 2,847 tokens (73% reduction)",
  "is_error": false
}
```

#### inject_message

Proactively inject a message into the conversation. This can be sent at any time after initialization.

```json
{
  "type": "inject_message",
  "role": "user",
  "content": "[Context from rtk-optimizer] Project uses Go modules with vendoring enabled."
}
```

#### log

Emit a log message. These are displayed in the Pi debug log, not in the conversation.

```json
{
  "type": "log",
  "level": "info",
  "message": "rtk-optimizer loaded with 3 optimization rules"
}
```

Valid levels: `info`, `warn`, `error`.

## Lifecycle

### Startup Sequence

```
  Host                              Plugin
   |                                  |
   |  spawn subprocess                |
   |--------------------------------->|
   |                                  |
   |  {"type": "initialize", ...}     |
   |--------------------------------->|
   |                                  |
   |  {"type": "capabilities", ...}   |
   |<---------------------------------|
   |                                  |
   |  [register tools & commands]     |
   |                                  |
```

### Tool Call Execution

```
  LLM           Host                Plugin
   |              |                   |
   | tool_use     |                   |
   |------------->|                   |
   |              |                   |
   |              | {"type":          |
   |              |  "tool_call",...} |
   |              |------------------>|
   |              |                   |
   |              |                   | [execute]
   |              |                   |
   |              | {"type":          |
   |              |  "tool_result",.} |
   |              |<------------------|
   |              |                   |
   | tool_result  |                   |
   |<-------------|                   |
   |              |                   |
```

### Event Notification (fire-and-forget)

```
  Agent Loop      Host                Plugin
   |                |                   |
   | emit event     |                   |
   |--------------->|                   |
   |                |                   |
   |                | {"type":"event",  |
   |                |  "event": {...}}  |
   |                |------------------>|
   |                |                   |
   |                |         [no response expected]
   |                |                   |
```

### Shutdown Sequence

```
  Host                              Plugin
   |                                  |
   |  {"type": "shutdown"}            |
   |--------------------------------->|
   |                                  |
   |                       [cleanup]  |
   |                                  |
   |           [process exit]         |
   |<---------------------------------|
   |                                  |
   |  [wait up to 5s, then SIGKILL]   |
   |                                  |
```

### Full Lifecycle

1. **Discovery**: The host scans plugin directories (`~/.pi/plugins/`, `.pi/plugins/`) and processes `--plugin` CLI flags.

2. **Loading**: For each discovered plugin, the host reads the manifest (if present), validates it, and records the plugin's path and declared capabilities.

3. **Spawning**: The host starts each plugin as a subprocess with stdin/stdout pipes. The plugin's stderr is connected to the host's logging system.

4. **Initialization**: The host sends an `initialize` message with configuration (cwd, model, provider, pi version). The plugin responds with its `capabilities` declaration listing tools and commands.

5. **Registration**: The host registers plugin-provided tools into the `tools.Registry` and commands into the `CommandRegistry`, alongside built-in tools and commands. Name collisions are resolved by preferring built-in tools (plugin tools with conflicting names are rejected with a warning).

6. **Execution**: During the agent loop:
   - When the LLM calls a plugin tool, the host sends a `tool_call` message and waits for a `tool_result` response.
   - When the user invokes a plugin command, the host sends a `command` message and waits for a `command_result` response.
   - Agent lifecycle events are forwarded to all plugins that declared the `events` capability.
   - Plugins may send `inject_message` at any time to add context to the conversation.

7. **Shutdown**: When Pi exits, the host sends a `shutdown` message to each plugin and waits up to 5 seconds for the process to exit. If the plugin has not exited after 5 seconds, it is killed with SIGKILL.

## Error Handling

### Plugin Crash

If a plugin process exits unexpectedly:
- The host logs an error with the plugin name and exit status.
- All tools and commands from that plugin are unregistered.
- The agent continues operating with remaining built-in and plugin tools.
- No attempt is made to restart the plugin within the same session.

### Plugin Timeout

Tool call execution has a default timeout of 30 seconds. If a plugin does not respond within this window:
- The pending tool call returns an error result to the LLM.
- The plugin is not killed (it may still be processing), but the host will not wait for its response.
- Subsequent tool calls to the same plugin proceed normally.

### Invalid Messages

If a plugin sends a message that cannot be parsed as JSON or has an unrecognized `type`:
- The message is logged at `warn` level and discarded.
- The plugin is not terminated -- it may continue operating.

### Initialization Failure

If a plugin fails to respond to `initialize` within 10 seconds:
- The host logs an error and kills the plugin process.
- No tools or commands from that plugin are registered.
- Other plugins are not affected.

## Security Considerations

- **Same permissions**: Plugins run with the same OS permissions as the Pi host process. A plugin can read, write, and execute anything the user can.

- **No sandboxing**: There is no filesystem or network sandboxing. This is consistent with Pi's built-in tools (e.g., `bash` can execute arbitrary commands).

- **Trust model**: Users must trust the plugins they install, just as they trust the Pi binary itself. Plugin discovery is limited to well-known directories and explicit CLI flags -- Pi does not download or auto-install plugins.

- **No secret isolation**: Plugin config may include API keys or tokens if passed through environment variables. Plugins have access to the same environment as the host.

## Writing a Plugin

### Minimal Go Plugin

```go
package main

import (
    "bufio"
    "encoding/json"
    "fmt"
    "os"
)

type Message struct {
    Type   string         `json:"type"`
    ID     string         `json:"id,omitempty"`
    Name   string         `json:"name,omitempty"`
    Params map[string]any `json:"params,omitempty"`
    Config map[string]any `json:"config,omitempty"`
}

type Response struct {
    Type    string `json:"type"`
    ID      string `json:"id,omitempty"`
    Content string `json:"content,omitempty"`
    IsError bool   `json:"is_error,omitempty"`
    Tools   []Tool `json:"tools,omitempty"`
}

type Tool struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    InputSchema any    `json:"input_schema"`
}

func main() {
    scanner := bufio.NewScanner(os.Stdin)
    scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

    for scanner.Scan() {
        var msg Message
        if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
            continue
        }

        switch msg.Type {
        case "initialize":
            send(Response{
                Type: "capabilities",
                Tools: []Tool{{
                    Name:        "hello",
                    Description: "Say hello",
                    InputSchema: map[string]any{
                        "type": "object",
                        "properties": map[string]any{
                            "name": map[string]any{
                                "type":        "string",
                                "description": "Name to greet",
                            },
                        },
                        "required": []string{"name"},
                    },
                }},
            })

        case "tool_call":
            name, _ := msg.Params["name"].(string)
            send(Response{
                Type:    "tool_result",
                ID:      msg.ID,
                Content: fmt.Sprintf("Hello, %s!", name),
            })

        case "shutdown":
            os.Exit(0)
        }
    }
}

func send(r Response) {
    data, _ := json.Marshal(r)
    fmt.Fprintln(os.Stdout, string(data))
}
```

### Minimal Python Plugin

```python
import json
import sys

def send(msg):
    print(json.dumps(msg), flush=True)

for line in sys.stdin:
    msg = json.loads(line)

    if msg["type"] == "initialize":
        send({
            "type": "capabilities",
            "tools": [{
                "name": "hello",
                "description": "Say hello",
                "input_schema": {
                    "type": "object",
                    "properties": {
                        "name": {"type": "string", "description": "Name to greet"}
                    },
                    "required": ["name"]
                }
            }]
        })

    elif msg["type"] == "tool_call":
        name = msg.get("params", {}).get("name", "world")
        send({
            "type": "tool_result",
            "id": msg["id"],
            "content": f"Hello, {name}!"
        })

    elif msg["type"] == "shutdown":
        sys.exit(0)
```
