# gi

An AI coding agent for the terminal, implemented in Go with [Bubble Tea](https://github.com/charmbracelet/bubbletea).

> **Disclaimer:** This project is an experiment in using [Gas Town](https://github.com/ejm/gt) for multi-agent development coordination. It serves as a testbed for exploring how multiple AI agents can collaborate on a shared codebase through structured workflows and issue tracking.

## Features

- **Multi-provider AI** -- supports Anthropic, OpenAI, and OpenRouter out of the box
- **Agentic tool-use loop** -- the model can read, write, edit files, run shell commands, and search your codebase
- **Interactive TUI** -- full-screen terminal UI with streaming responses, thinking indicators, and a multi-line editor
- **Print mode** -- pipe a prompt in and get a response on stdout, no TUI required
- **Extended thinking** -- configurable thinking levels (off, low, medium, high) for supported models
- **Session persistence** -- conversations are saved and can be resumed by session ID
- **Project-aware** -- automatically loads AGENTS.md, CLAUDE.md, or .gi/SYSTEM.md from the working directory

## Install

### Prerequisites

- Go 1.22+

### From source

```bash
go install github.com/ejm/go_pi/cmd/gi@latest
```

Or clone and build manually:

```bash
git clone https://github.com/ejm/go_pi.git
cd go_pi
go build -o gi ./cmd/gi/
```

## Configuration

### API keys

Keys can be set via environment variables or stored in `~/.gi/auth.json`. Environment variables take precedence over the file.

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
export OPENROUTER_API_KEY="sk-or-..."
export OPENAI_API_KEY="sk-..."
```

Or create `~/.gi/auth.json`:

```json
{
  "keys": {
    "anthropic": "sk-ant-...",
    "openrouter": "sk-or-...",
    "openai": "sk-..."
  }
}
```

### Settings

Settings are loaded by merging (in order of precedence):

1. Built-in defaults
2. Global settings: `~/.gi/settings.json`
3. Project-local settings: `.gi/settings.json` (in the working directory)

Example `settings.json`:

```json
{
  "default_provider": "anthropic",
  "default_model": "claude-sonnet-4-20250514",
  "thinking_level": "off",
  "max_tokens": 8192
}
```

## Usage

### Interactive mode

```bash
gi
gi -model claude-sonnet-4-20250514
gi -provider openrouter -model anthropic/claude-sonnet-4-20250514
gi -thinking high
gi -session abc123
gi -cwd /path/to/project
```

### Print mode

Send a single prompt and print the response to stdout (no TUI):

```bash
gi -p "explain this code"
```

### CLI flags

| Flag | Description |
|------|-------------|
| `-model` | Model name (e.g. `claude-sonnet-4-20250514`, `gpt-4o`) |
| `-provider` | Provider name: `anthropic`, `openrouter`, `openai` |
| `-thinking` | Thinking level: `off`, `low`, `medium`, `high` |
| `-p` | Print mode -- send prompt, print response, exit |
| `-session` | Resume a previous session by ID |
| `-cwd` | Set the working directory |

### Key bindings

| Key | Action |
|-----|--------|
| Enter | Send message (or steer the agent while it is running) |
| Shift+Enter | Insert newline in the editor |
| Up arrow | Recall previous message (when editor is empty) |
| Ctrl+C | Cancel running agent; press twice while idle to quit |
| Escape | Cancel running agent |
| t | Toggle thinking block expand/collapse (while agent is running) |
| r | Toggle tool result expand/collapse (while agent is running) |

## Providers

| Provider | Env var | Default model |
|----------|---------|---------------|
| Anthropic | `ANTHROPIC_API_KEY` | `claude-sonnet-4-20250514` |
| OpenRouter | `OPENROUTER_API_KEY` | `anthropic/claude-sonnet-4-20250514` |
| OpenAI | `OPENAI_API_KEY` | `gpt-4o` |

If no provider is specified, gi auto-detects based on which API key is available (checked in the order above).

## Tools

gi exposes the following tools to the AI model:

| Tool | Description |
|------|-------------|
| **read** | Read file contents with line numbers. Detects binary files. Truncates to 2000 lines by default. |
| **write** | Write content to a file, creating parent directories as needed. |
| **edit** | Exact string replacement in a file. The target string must appear exactly once. |
| **bash** | Execute a shell command via `/bin/bash`. 120-second timeout. Output truncated to 100 KB. |
| **glob** | Find files matching a glob pattern (supports `**`). Skips hidden directories by default. |
| **grep** | Search file contents with a regular expression. Returns matching lines with paths and line numbers. |

## Project structure

```
go_pi/
  cmd/
    gi/
      main.go           # Entry point, flag parsing, provider/agent wiring
  pkg/
    agent/
      loop.go           # Agentic tool-use loop
      options.go         # Functional options for AgentLoop
      types.go           # Event types
    ai/
      types.go           # Provider interface, Message, ContentBlock, streaming types
      anthropic.go       # Anthropic API provider
      openai.go          # OpenAI API provider
      openrouter.go      # OpenRouter provider (delegates to OpenAI provider)
    config/
      config.go          # Settings loading and merging
      auth.go            # API key management (env vars + auth.json)
    session/
      manager.go         # Session persistence
    tools/
      tool.go            # Tool interface and registry
      read.go            # read tool
      write.go           # write tool
      edit.go            # edit tool
      bash.go            # bash tool
      glob.go            # glob tool
      grep.go            # grep tool
    tui/
      app.go             # Root Bubble Tea model
      chat.go            # Chat/message view
      editor.go          # Input editor
      header.go          # Header bar
      footer.go          # Footer bar (token usage)
      messages.go        # TUI message types
      styles.go          # Colors and lipgloss styles
  go.mod
  go.sum
```

## Development

```bash
# Run tests
go test ./...

# Build
go build -o gi ./cmd/gi/

# Vet
go vet ./...
```

## License

MIT
