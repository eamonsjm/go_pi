# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-03-19

### Added

#### Core Features
- **Multi-provider AI support** - Support for Anthropic, OpenAI, OpenRouter, Azure OpenAI, AWS Bedrock, and Google Gemini
- **Interactive TUI** - Full-screen terminal UI with streaming responses, thinking indicators, and multi-line editor
- **Agentic tool-use loop** - AI can read files, write files, edit files, run bash commands, search with glob patterns, and grep for content
- **Print mode** - Non-interactive mode: pipe a prompt and get a response on stdout
- **Session persistence** - Save and resume conversations by session ID, with support for session branching
- **Project-aware context** - Automatically loads AGENTS.md, CLAUDE.md, or .gi/SYSTEM.md from the working directory
- **Extended thinking** - Configurable thinking levels (off, low, medium, high) for supported models

#### CLI Features
- **Slash commands** - Built-in commands for aliasing, authentication, session management, settings, and more
- **Configurable keybindings** - Customize hotkeys via `~/.gi/keybindings.json`
- **Short flag aliases** - All CLI options support short flag equivalents (e.g., `-m` for `--model`)
- **Slash command aliasing system** - Create custom aliases for complex slash commands
- **Token optimization** - RTK (Rust Token Killer) integration for output compression and command rewriting

#### Developer Features
- **Plugin system** - Extend gi with custom tools and commands via executable plugins
- **JSON-RPC interface** - Programmatic interface for integration with other tools
- **JSON event stream mode** - Stream structured events for parsing and processing
- **Tool system** - Extensible tool registry for adding custom capabilities
- **Multi-language SDK support** - Python and JavaScript SDK available for programmatic use

#### Configuration
- **Settings management** - Load and merge settings from built-in defaults, global config, and project-local config
- **Authentication system** - Support for API keys via environment variables or `~/.gi/auth.json`
- **Working directory awareness** - Run with `-w` or `--cwd` to analyze code in any directory

### Features in Detail

#### Multi-Provider Support
- Seamless switching between different AI providers
- Auto-detection of available API keys
- Provider-specific configurations and model selection
- Examples: `gi -p anthropic -m claude-sonnet-4-20250514`, `gi -p openai -m gpt-4o`

#### Session Persistence
- Automatic session saving in JSONL format
- Resume conversations with `gi -s <session-id>`
- Session branching for exploring alternatives
- Session compaction for context window optimization
- Session directory: `~/.gi/sessions`

#### Agentic Tool-Use Loop
- **read** - Read file contents with automatic line numbering
- **write** - Create or overwrite files
- **edit** - Precise in-file modifications with exact string replacement
- **bash** - Execute shell commands with 120-second timeout
- **glob** - Find files matching patterns (supports `**`)
- **grep** - Search with regular expressions and context

#### RTK Token Optimization
- Transparent command rewriting for common operations
- Output compression to reduce token usage
- 60-90% token savings on typical dev workflows
- Analytics and history tracking

#### Configurable Keybindings
Custom keybindings via `~/.gi/keybindings.json`:
- Navigation keys (editor, message history)
- Application-level hotkeys (thinking toggle, model cycling, theme switching)
- Extensible system for adding new key combinations

### Documentation
- Comprehensive README with installation, configuration, and usage examples
- Inline CLI help for all commands and flags
- Example workflows in `examples/` directory:
  - `custom-system-prompt` - Using custom system prompts for specialized tasks
  - `multi-model` - Comparing outputs across different AI models
  - `session-persistence` - Saving and resuming conversations
  - `bash-automation` - AI-powered automation workflows

### Known Issues

- **Context window limits** - Conversations are subject to model context limits; use session compaction for long conversations
- **Tool timeout** - Bash commands timeout after 120 seconds; long-running operations may fail
- **Thinking availability** - Extended thinking is only available on models that support it (Anthropic Claude models, OpenAI o1/o3)
- **Plugin loading** - Plugins must be executable; relative paths may not work as expected
- **Session branching UI** - Session branches are tracked internally but have limited UI exploration in v0.1
- **Azure auth** - Azure OpenAI authentication requires additional configuration beyond standard settings

### Limitations

- No support for image inputs/outputs in v0.1
- Streaming not available for all providers (fallback to full response)
- Plugin system requires rebuilding main binary to add tools (hot-loading not yet supported)
- No support for function calling beyond built-in tools

### Architecture Notes

- Built on [Bubble Tea](https://github.com/charmbracelet/bubbletea) for TUI
- Modular provider interface for easy addition of new AI backends
- Event-driven architecture for streaming and async operations
- Dolt-based session storage for version control of conversations

### Contributors

- **ejm** - Core architecture, multi-provider support, session persistence, RTK integration
- **Community feedback** - Bug reports and feature requests from early testers

### Migration Guide

This is the first release, so no migration is needed.

### Upgrading

To upgrade from earlier versions or install for the first time:

```bash
go install github.com/ejm/go_pi/cmd/gi@latest
```

Or clone and build:

```bash
git clone https://github.com/ejm/go_pi.git
cd go_pi
go build -o gi ./cmd/gi/
```

### Future Roadmap (Not in v0.1)

- Image input/output support
- Persistent conversation history search
- Team collaboration features
- Cloud session sync
- VS Code extension
- Advanced context window management
- Hot-loading plugins without recompilation
- Voice input/output support

---

For detailed information about changes, see the [GitHub releases](https://github.com/ejm/go_pi/releases) page.
