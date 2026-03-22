# Gi Architecture Guide

Gi is an AI coding agent for the terminal built with Go and [Bubble Tea](https://github.com/charmbracelet/bubbletea). This guide explains the high-level design and how the system fits together.

## System Overview

Gi implements an **agentic loop** where an AI model repeatedly receives context, calls tools, and refines its understanding. The architecture is modular, allowing developers to add new providers, tools, and plugins without modifying core code.

```
┌─────────────────────────────────────────────────┐
│         gi (CLI Entry Point)                    │
│    cmd/gi/main.go → runInteractive()            │
└──────────────┬──────────────────────────────────┘
               │
       ┌───────┴───────┐
       │               │
   ┌───▼────┐     ┌────▼────┐
   │ TUI    │     │ Agent    │
   │ (Bubble│     │ Loop     │
   │ Tea)   │     │          │
   └────────┘     └────┬─────┘
                       │
        ┌──────────────┼──────────────┐
        │              │              │
    ┌───▼──┐      ┌────▼─────┐  ┌────▼────┐
    │ AI   │      │ Tools    │  │ Session │
    │Prov. │      │Registry  │  │Manager  │
    └──────┘      └──────────┘  └─────────┘
```

## Package Structure and Responsibilities

### `cmd/gi/`
The main CLI entry point. Responsible for:
- Parsing command-line flags
- Loading configuration and authentication
- Initializing providers, tools, sessions, and plugins
- Launching the TUI or special modes (JSON, RPC, print)

**Key files:**
- `main.go` - Entry point, flag parsing, mode selection
- `context_test.go`, `fileargs_test.go` - Tests for context handling

### `pkg/ai/`
Abstract provider interface and implementations for multiple LLM providers.

**Key concepts:**
- `Provider` interface - Standard way to call any LLM API
- Concrete implementations - Anthropic, OpenAI, OpenRouter, Azure OpenAI, AWS Bedrock, Google Gemini
- Message types - Support for text, tool use, and structured tool definitions

**Key files:**
- `types.go` - Core types (Message, ToolDef, Usage, etc.)
- `anthropic.go`, `openai.go`, etc. - Provider implementations
- `errors.go` - Provider-specific error handling

### `pkg/agent/`
The agentic loop that orchestrates tool use and model interactions.

**Key concepts:**
- **Agent loop** - Repeatedly calls the model, receives tool requests, executes tools, and feeds results back
- **Events** - Structured output for streaming UI updates, session recording, and plugin integration
- **Compaction** - Automatic context management by summarizing old conversations

**Key files:**
- `types.go` - Event types and AgentEvent structure
- `loop_test.go` - Core loop implementation tests
- `options.go` - AgentLoop configuration
- `compaction.go` - Context compression for long conversations

### `pkg/tools/`
Built-in tools and the tool registry system.

**Key concepts:**
- `Tool` interface - Standard way to implement new tools
- `RichTool` interface - Tools that return multi-block results (text + images)
- `Registry` - Manages all available tools, provides them to the agent

**Built-in tools:**
- File operations (Read, Write, Edit)
- Code search (Glob, Grep)
- Shell commands (Bash)

**Key files:**
- `tool.go` - Tool interface and Registry implementation
- `{tool-name}.go` - Individual tool implementations
- `helpers.go` - Shared utility functions

### `pkg/tui/`
Terminal UI built with Bubble Tea. Renders chat, handles input, displays tool execution.

**Key features:**
- Interactive chat interface
- Slash command system (`/auth`, `/settings`, `/theme`, etc.)
- Multi-line editing
- Plugin integration

**Key files:**
- `app.go` - Main app state machine
- `chat.go` - Chat rendering and input
- `cmd_*.go` - Individual command handlers
- `messages.go` - TUI message types
- `styles.go` - Styling and theme system

### `pkg/session/`
Manages conversation history and state persistence.

**Key concepts:**
- Session storage - Saves and loads conversation history
- Message persistence - Records all interactions for resuming sessions
- Session lifecycle - Create, load, and update sessions

**Key files:**
- `manager.go` - Session lifecycle management
- `manager_test.go` - Tests

### `pkg/config/`
Configuration loading and management.

**Key concepts:**
- Config file format - YAML/JSON in `~/.gi/config.json`
- Default values - Model selection, token limits, theme
- Runtime overrides - CLI flags override config

**Key files:**
- `config_test.go` - Config parsing and validation
- `auth.go` - Authentication configuration

### `pkg/auth/`
OAuth and API key authentication for multiple providers.

**Key concepts:**
- OAuth flow - Browser-based login for Anthropic, OpenAI
- Credential store - Secure storage of API keys and tokens
- Provider resolution - Map provider names to credentials

**Key files:**
- `resolver.go` - Route to correct provider credentials
- `anthropic.go`, `openai.go` - Provider-specific OAuth
- `store.go` - Credential persistence
- `credential.go` - API key and token types

### `pkg/plugin/`
External plugin system via JSONL over stdin/stdout.

**Key concepts:**
- **Plugin process** - Spawns external executable, communicates via JSONL
- **Tool injection** - Plugins provide tools just like built-in tools
- **Command injection** - Plugins provide slash commands
- **Event forwarding** - Agent events streamed to plugins for observability

**Plugin types:**
- Tools plugins - Add new capabilities (e.g., database queries, API calls)
- Command plugins - Add slash commands (e.g., `/deploy`, `/test`)
- Logging plugins - Observe agent execution in real-time

**Key files:**
- `plugin.go` - Protocol message types
- `manager.go` - Plugin discovery and lifecycle
- `bridge.go` - Tool registration for plugins
- `sdk/` - SDK for building plugins in Go

### `pkg/rpc/`
JSON-RPC 2.0 interface for programmatic access.

**Key concepts:**
- Stream mode (`-json`) - JSON event stream over stdout
- RPC mode (`-rpc`) - JSON-RPC 2.0 over stdin/stdout
- Structured events - Machine-readable event types and data

**Key files:**
- `events.go` - Event serialization and types
- `jsonrpc_test.go` - RPC protocol implementation

### `pkg/sdk/`
SDK for building Go programs that use Gi as a library.

**Key concepts:**
- Session creation - `sdk.NewSession(ctx, ...)` initializes an agent with tools
- Tool injection - Pass custom tool registry
- Event consumption - Programmatic access to agent events
- Provider configuration - API key, model, thinking level

**Key files:**
- `sdk.go` - High-level API
- `sdk_test.go` - Examples and tests

## Agent Loop: How Conversations Work

The agent loop is the heart of Gi. Here's how a single turn executes:

```
1. User sends prompt to agent
   ↓
2. Agent calls LLM with:
   - System prompt
   - Conversation history
   - Available tools (with schemas)
   ↓
3. Model responds with:
   - Assistant text (thinking, analysis, output)
   - Tool use requests
   ↓
4. For each tool use request:
   a. Emit tool_exec_start event
   b. Execute tool with parameters
   c. Emit tool_result event
   ↓
5. Feed tool results back to model in same conversation
   ↓
6. Repeat until model stops requesting tools
   ↓
7. Emit turn_end event with final response
```

**Event stream:** The agent emits events at each step:
- `agent_start` - Agent begins processing
- `turn_start` - New turn starting
- `assistant_text` - Model output (streamed)
- `tool_exec_start` - Tool about to run
- `tool_result` - Tool completed
- `turn_end` - Turn finished
- `agent_error` - Error occurred
- `usage_update` - Token usage stats

Consumers (TUI, plugins, RPC clients) listen to these events to track progress.

## Tool System

### Adding a Built-in Tool

Built-in tools are permanent parts of Gi and compiled in. They implement the `Tool` interface:

```go
type Tool interface {
    Name() string                                            // "read", "bash", etc.
    Description() string                                     // Human-readable description
    Schema() any                                             // JSON Schema for parameters
    Execute(ctx context.Context, params map[string]any) (string, error)
}
```

Tools are registered with the `Registry`:

```go
registry := tools.NewRegistry()
registry.Register(&MyTool{})

// Convert to tool definitions for the LLM
defs := registry.ToToolDefs()
```

### Adding a Plugin Tool

Plugin tools are external executables discovered at startup. They communicate via JSONL protocol and are loaded from:
- `~/.gi/plugins/` - User's personal plugins
- `./.gi/plugins/` - Project-specific plugins
- Command-line `--plugin` flag

Plugins report their tools during initialization, and the host bridges them to the agent loop.

## Provider System

### Provider Interface

Providers are implementations of the `Provider` interface in `pkg/ai/`:

```go
type Provider interface {
    Call(ctx context.Context, cfg CallConfig) (*Message, error)
}
```

Each provider wraps a different LLM API (Anthropic, OpenAI, etc.) and translates between Gi's internal message format and the API's specific format.

### Adding a New Provider

To add a new provider (e.g., LLaMA via Ollama):

1. Create `pkg/ai/ollama.go` implementing the `Provider` interface
2. Register in `main.go`'s `resolveProvider()` function
3. Update auth resolver if needed (for API keys)
4. Add tests

## Event System

The event system is Gi's primary output mechanism. All state changes flow through events:

**Event types** (in `pkg/agent/types.go`):
- `EventAssistantText` - LLM output (streamed in chunks)
- `EventAssistantThinking` - Extended thinking output
- `EventToolExecStart` - Tool about to execute
- `EventToolExecEnd` - Tool finished
- `EventToolResult` - Tool output message
- `EventUsageUpdate` - Token usage stats
- `EventAgentError` - Error occurred
- `EventCompaction` - Context auto-compressed

**Event consumers:**
- **TUI** - Renders events as UI updates
- **Session Manager** - Records messages for history
- **Plugins** - Forward events via JSONL
- **JSON-RPC clients** - Receive events as JSON

This decoupling means:
- The agent doesn't need to know about the TUI
- TUI doesn't need to know about plugins
- Adding a new consumer (e.g., webhook, logging) requires no core changes

## Session Management

Sessions store conversation history for later resuming.

**Session lifecycle:**
1. `sessionMgr.NewSession()` - Create new session, assign ID
2. `sessionMgr.SaveMessage(msg)` - Record each turn's messages
3. `sessionMgr.LoadSession(id)` - Restore history
4. `agentLoop.SetMessages(restored)` - Replay into agent

**Storage:** Sessions stored in `~/.gi/sessions/` by default.

## Plugin System

Plugins extend Gi with new tools and commands without modifying core code.

**Plugin lifecycle:**
1. **Discovery** - Scan `~/.gi/plugins/`, `./.gi/plugins/` at startup
2. **Loading** - Start plugin process, capture stdin/stdout
3. **Initialization** - Send config, receive capabilities (tools, commands)
4. **Runtime** - Forward tool calls, commands, and events via JSONL
5. **Shutdown** - Graceful termination on exit

**Communication:** JSONL over stdin/stdout with protocol types like:
- `initialize` - Host requests capabilities
- `tool_call` - Host requests tool execution
- `command` - Host requests command execution
- `event` - Host forwards agent events
- `shutdown` - Host shutting down

## Configuration and Customization

### Configuration File (`~/.gi/config.json`)

```json
{
  "default_provider": "anthropic",
  "default_model": "claude-sonnet-4-20250514",
  "theme": "auto",
  "max_tokens": 8000,
  "thinking_level": "medium"
}
```

### Environment Variables

- `ANTHROPIC_API_KEY` - Anthropic API key
- `OPENAI_API_KEY` - OpenAI API key
- `GEMINI_API_KEY` - Google Gemini API key
- `AZURE_OPENAI_API_KEY` - Azure OpenAI API key
- `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY` - AWS/Bedrock credentials

### Command-line Flags

All major options have both short and long forms:
- `-m`, `--model` - Model to use
- `-p`, `--provider` - Provider to use
- `-t`, `--thinking` - Thinking level
- `-s`, `--session` - Load specific session
- `-n`, `--new` - Start fresh session
- `--plugin` - Load plugins

## Design Principles

1. **Modular** - Each package has one responsibility
2. **Interface-driven** - Easy to swap implementations (providers, tools, storage)
3. **Event-oriented** - Loose coupling via event stream
4. **Streaming** - Real-time feedback via deltas, not batch results
5. **Stateless tools** - Tools don't need to know about agent state
6. **Plugin-friendly** - External extension without modifying core

## Extension Points

| Goal | How | Where |
|------|-----|-------|
| Add a new tool | Implement `Tool` interface | `pkg/tools/` |
| Add a new provider | Implement `Provider` interface | `pkg/ai/` |
| Add a command | Register with `app.RegisterCommand()` | `pkg/tui/` |
| Add a plugin | Create JSONL protocol executable | External |
| Change authentication | Extend `auth.Resolver` | `pkg/auth/` |
| Change session storage | Implement storage interface | `pkg/session/` |
| Add observability | Listen to agent events | Via event channel |

For detailed examples, see [EXTENDING.md](./EXTENDING.md).
