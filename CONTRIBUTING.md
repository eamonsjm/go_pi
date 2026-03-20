# Contributing to gi

Thank you for your interest in contributing to gi! This guide will help you get started with development, testing, and submitting your changes.

## Table of Contents

- [Local Development Setup](#local-development-setup)
- [Building from Source](#building-from-source)
- [Running Tests](#running-tests)
- [Code Style Guidelines](#code-style-guidelines)
- [Git Workflow](#git-workflow)
- [Testing Your Changes](#testing-your-changes)
- [Reporting Bugs](#reporting-bugs)
- [Suggesting Features](#suggesting-features)
- [Project Structure](#project-structure)
- [Common Commands](#common-commands)

## Local Development Setup

### Prerequisites

- **Go 1.24.2 or later** — Download from [golang.org](https://golang.org/dl)
- **Git** — For version control
- A terminal with bash or zsh

### Verify Go Installation

```bash
go version      # Should show Go 1.24.2 or later
go env GOPATH   # Shows your Go workspace
```

### Clone the Repository

```bash
git clone https://github.com/ejm/go_pi.git
cd go_pi
```

### Install Dependencies

```bash
go mod download
go mod tidy
```

This downloads all required dependencies listed in `go.mod`:
- charmbracelet/bubbles — Reusable TUI components
- charmbracelet/bubbletea — Terminal UI framework
- charmbracelet/glamour — Terminal markdown renderer
- charmbracelet/lipgloss — Terminal styling
- AWS SDK v2, Google Gemini SDK, and other AI provider libraries

## Building from Source

### Build the Binary

```bash
go build -o gi ./cmd/gi/
```

This creates an executable named `gi` in your current directory.

### Install to $GOPATH/bin

```bash
go install ./cmd/gi/
```

This installs `gi` to your `$GOPATH/bin` directory (usually `~/go/bin`). Add this to your `$PATH` to run `gi` from anywhere:

```bash
export PATH=$PATH:$(go env GOPATH)/bin
```

### Verify the Build

```bash
./gi --help
```

You should see the help output with available flags and commands.

## Running Tests

### Run All Tests

```bash
go test ./...
```

This runs all tests in the project and reports coverage.

### Run Tests in a Specific Package

```bash
go test ./pkg/agent/...
go test ./pkg/ai/...
go test ./pkg/auth/...
```

### Run Tests with Verbose Output

```bash
go test -v ./...
```

This shows the name of each test as it runs and provides more detailed output.

### Run Tests with Coverage

```bash
go test -cover ./...
```

This shows test coverage percentage for each package. For detailed coverage report:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out  # Opens HTML report in browser
```

### Run a Specific Test

```bash
go test -run TestAgentLoop ./pkg/agent/
```

This runs only tests matching the given regex pattern.

### Run Tests and Benchmarks

```bash
go test -bench=. ./...
```

This runs both tests and any benchmarks in the codebase.

### Test Organization

Tests are colocated with their source files with `_test.go` suffix:
- `pkg/agent/loop_test.go` tests `loop.go`
- `pkg/ai/anthropic_test.go` tests `anthropic.go`

Write tests for:
- New functions and exported APIs
- Bug fixes (add test that reproduces the bug, then fix it)
- Edge cases and error handling
- Integration tests for provider implementations

## Code Style Guidelines

### Go Code Standards

Follow the [Effective Go](https://golang.org/doc/effective_go) guide and [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments). Key principles:

#### Naming Conventions

- **Packages**: Use short, lowercase names without underscores
  - ✅ `pkg/agent`, `pkg/ai`, `pkg/auth`
  - ❌ `pkg/agent_loop`, `pkg/ai_providers`

- **Functions/Methods**: Use camelCase, start with lowercase or uppercase (exported)
  - ✅ `func (a *Agent) processMessage()`, `func NewAgent()`
  - ❌ `func (a *Agent) ProcessMessage()` (unexported)

- **Constants**: Use UPPER_SNAKE_CASE for exported, camelCase for unexported
  - ✅ `const DefaultModel = "claude-sonnet-4-20250514"`
  - ✅ `const defaultTimeout = 30 * time.Second`

- **Interfaces**: Use short, concise names ending in `-er`
  - ✅ `type Provider interface`, `type Writer interface`
  - ❌ `type ProviderInterface interface`

#### Code Organization

- **Imports**: Group by standard library, third-party, and local imports, separated by blank lines
  ```go
  import (
    "context"
    "fmt"

    "github.com/charmbracelet/bubbletea"

    "github.com/ejm/go_pi/pkg/agent"
  )
  ```

- **Function length**: Keep functions focused and under 50 lines when possible
- **Comments**: Comment exported functions and non-obvious code
  ```go
  // Agent represents an AI agent that executes tool-use loops.
  type Agent struct { ... }

  // NewAgent creates a new Agent with the given options.
  func NewAgent(opts ...Option) *Agent { ... }
  ```

- **Error handling**: Always check errors
  ```go
  data, err := ioutil.ReadFile(filename)
  if err != nil {
    return fmt.Errorf("read file: %w", err)  // Wrap with context
  }
  ```

#### Style Checks

Run `go fmt` to auto-format your code:

```bash
go fmt ./...
```

Run `go vet` to check for common mistakes:

```bash
go vet ./...
```

### File Organization

- **cmd/gi/**: Entry point, main function, flag parsing
- **pkg/agent/**: Agent loop, message handling, compaction
- **pkg/ai/**: Provider interfaces and implementations (Anthropic, OpenAI, etc.)
- **pkg/auth/**: Authentication, API key storage, OAuth flows
- **pkg/config/**: Settings and configuration loading
- **pkg/session/**: Session persistence
- **pkg/tools/**: Tool implementations (read, write, edit, bash, glob, grep)
- **pkg/tui/**: Terminal UI components using Bubble Tea
- **pkg/plugin/**: Plugin system and manager
- **pkg/rpc/**: JSON-RPC and streaming interfaces
- **examples/**: Example integrations and usage patterns

## Git Workflow

### Branch Naming Convention

Create a feature branch with a descriptive name:

```bash
git checkout -b feature/short-description
git checkout -b fix/bug-description
git checkout -b docs/what-youre-adding
```

Examples:
- `feature/slash-command-aliasing`
- `fix/prevent-up-arrow-history-trigger`
- `docs/contributing-guide`

### Making Commits

Write clear, focused commit messages:

```bash
git add <files>
git commit -m "type: description (issue)"
```

**Commit types:**
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `refactor`: Code refactoring without functionality change
- `test`: Test additions or fixes
- `chore`: Dependencies, tooling, maintenance

**Examples:**
```
feat: add slash command aliasing system
fix: prevent up/down arrows from triggering history when chat is scrolled
docs: add CONTRIBUTING.md guide
test: add tests for agent loop cancellation edge cases
```

### Creating a Pull Request

1. **Push your branch:**
   ```bash
   git push origin feature/your-description
   ```

2. **Create a PR** on GitHub with a clear title and description:
   - What problem does it solve or what feature does it add?
   - How did you test it?
   - Any breaking changes or important notes?

3. **Respond to feedback** — Address review comments promptly

4. **Keep it up to date** — Rebase on main if it diverges:
   ```bash
   git fetch origin
   git rebase origin/main
   git push --force-with-lease
   ```

### Merging

A maintainer will merge your PR once:
- All tests pass
- Code review is complete
- CI checks are green

Do NOT merge your own PR.

## Testing Your Changes

### Before Submitting

1. **Build successfully:**
   ```bash
   go build -o gi ./cmd/gi/
   ```

2. **Pass all tests:**
   ```bash
   go test ./...
   ```

3. **Pass style checks:**
   ```bash
   go fmt ./...
   go vet ./...
   ```

4. **Test manually** in the TUI:
   ```bash
   ./gi
   ```

   Test your specific feature or fix in interactive mode.

5. **Test in print mode** (if relevant):
   ```bash
   echo "your prompt" | ./gi --print
   ```

### Writing Tests for Your Changes

If you're adding a feature or fixing a bug, include tests:

```go
package agent

import (
  "testing"
)

func TestNewFeature(t *testing.T) {
  // Arrange
  agent := NewAgent(WithOption("value"))

  // Act
  result := agent.SomeMethod()

  // Assert
  if result != expected {
    t.Errorf("got %v, want %v", result, expected)
  }
}
```

Test structure:
- **Arrange**: Set up test data and fixtures
- **Act**: Call the function being tested
- **Assert**: Check the results

### Running Specific Tests During Development

```bash
# Run only tests for the ai package
go test -v ./pkg/ai/...

# Run only tests containing "Provider"
go test -v -run TestProvider ./...

# Run with short timeout (useful for iterating)
go test -timeout 10s ./...
```

## Reporting Bugs

When reporting a bug, include:

1. **What you did:** Steps to reproduce
2. **What you expected:** Expected behavior
3. **What happened:** Actual behavior with error messages
4. **Environment:** Go version, OS, terminal
5. **Example output:** Error messages, logs, or screenshots

**Example issue:**
```
Title: Up/Down arrows trigger history when chat is scrolled

When the chat panel is scrolled down and I press the up arrow key,
it opens the message history instead of scrolling. This is unexpected.

Expected: Arrow keys should only navigate history when the editor is empty.
Actual: Arrows trigger history even when scrolled in chat view.

Environment: Go 1.24, Ubuntu 24.04, zsh
```

## Suggesting Features

When suggesting a feature, describe:

1. **The problem:** What user need does this address?
2. **The solution:** How should it work?
3. **Alternatives:** What other approaches could work?
4. **Example usage:** How would users interact with it?

**Example feature request:**
```
Title: Support for persistent slash command aliases

Problem: Users frequently use the same flags combination (e.g., -p openai -m gpt-4o).
They want to create aliases like `/ai-latest` that remember these settings.

Solution: Add `/alias` command that lets users define custom slash commands
that expand to longer combinations.

Usage:
  /alias myai -p openai -m gpt-4o
  myai "explain this code"  # Expands to: gi -p openai -m gpt-4o "explain this code"
```

## Project Structure

### Key Directories

```
go_pi/
├── cmd/gi/              # Entry point
├── pkg/
│   ├── agent/          # Agent loop and message handling
│   ├── ai/             # AI provider implementations
│   ├── auth/           # Authentication and key management
│   ├── config/         # Configuration loading
│   ├── session/        # Session persistence
│   ├── tools/          # Tool implementations
│   ├── tui/            # Terminal UI components
│   ├── plugin/         # Plugin system
│   └── rpc/            # JSON-RPC interface
├── examples/            # Example integrations
├── docs/                # Documentation
└── README.md            # Project README
```

### Dependencies by Function

- **TUI**: charmbracelet/bubbletea, bubbles, lipgloss, glamour
- **AI**: anthropic-sdk-go, openai-go, aws-sdk-go-v2
- **Utilities**: fmt, context, io

See `go.mod` for the complete dependency list.

## Common Commands

### Development

```bash
# Build binary
go build -o gi ./cmd/gi/

# Install to $GOPATH/bin
go install ./cmd/gi/

# Run the binary
./gi

# Print mode (pipe in a prompt)
echo "explain Go interfaces" | ./gi --print
```

### Testing

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run specific package tests
go test -v ./pkg/agent/...

# Run specific test by name
go test -run TestAgentLoop ./...
```

### Code Quality

```bash
# Format all Go files
go fmt ./...

# Check for common mistakes
go vet ./...

# Check one file
go fmt file.go
go vet ./pkg/agent/loop.go
```

### Debugging

```bash
# Build with debug symbols (larger binary, good for debugging)
go build -gcflags="all=-N -l" -o gi ./cmd/gi/

# Run tests with verbose output
go test -v ./...

# Run tests with timeout
go test -timeout 30s ./...

# Check dependencies
go list -m all
```

### Dependency Management

```bash
# Download dependencies
go mod download

# Tidy up unused dependencies
go mod tidy

# Verify dependencies are valid
go mod verify

# Update a specific dependency
go get -u github.com/charmbracelet/bubbletea
```

## Questions?

If you have questions:

1. **Check the README** — Common usage and setup is documented there
2. **Read existing code** — Look at similar implementations for patterns
3. **Open a discussion** — Use GitHub Discussions for questions
4. **Create an issue** — For bugs or feature requests

Thank you for contributing to gi! 🚀
