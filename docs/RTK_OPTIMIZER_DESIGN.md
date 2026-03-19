# Go RTK Optimizer Design Document

## Executive Summary

This document outlines the design for implementing a Go version of the **pi-rtk-optimizer** within the gi (Go PI) agent. The RTK optimizer reduces token consumption and improves LLM context efficiency through two primary mechanisms:

1. **Command rewriting**: Transparently converting standard CLI commands to their `rtk` equivalents for optimization
2. **Output compression**: Multi-stage filtering pipeline that reduces tool output size while preserving actionable information

**Estimated Effort**: 6–8 engineer weeks (phased implementation)

**Target Integration**: pkg/tools (command execution) and pkg/agent (event hooks)

---

## 1. Architecture Overview

### 1.1 Integration Points in gi Codebase

The RTK optimizer will integrate at three key layers:

```
┌─────────────────────────────────────────────────────┐
│                   Agent Loop                        │
│            (pkg/agent/loop.go)                      │
│  - Manages message history                          │
│  - Emits AgentEvent (tool_exec_start, etc.)         │
│  - Orchestrates LLM↔Tools interaction                │
└────────────────┬──────────────────────────────────┘
                 │
                 ↓ (EventToolExecStart)
┌─────────────────────────────────────────────────────┐
│         RTK Optimizer Hooks Layer                   │
│        (NEW: pkg/tools/rtk_hooks.go)                │
│  - Intercepts tool execution events                 │
│  - Applies command rewriting before execution       │
│  - Applies output compression after execution       │
│  - Tracks compression metrics                       │
└────────────────┬──────────────────────────────────┘
                 │
                 ↓ (Modified command)
┌─────────────────────────────────────────────────────┐
│            Tool Registry                            │
│         (pkg/tools/tool.go)                         │
│  - BashTool (executes shell commands)               │
│  - Other tools (Read, Write, Edit, Glob, Grep)      │
└─────────────────────────────────────────────────────┘
```

### 1.2 Event Hook Integration

The agent loop already emits structured events:

```go
EventToolExecStart  // Called before tool execution (HOOK POINT #1)
EventToolExecEnd    // Called after tool execution (HOOK POINT #2)
EventToolResult     // Called with final result
```

**Implementation strategy**:
- Hook into `pkg/agent` to emit pre/post execution signals
- RTK optimizer subscribes to these events
- Apply transformations transparently in the hook layer
- No changes required to existing tool implementations

### 1.3 Configuration Management

Configuration will be stored in:
- **Global config**: `~/.gi/config.json` (RTK optimizer enabled/disabled, mode)
- **Session-level config**: Temporary settings via `/rtk config` TUI command
- **Project-level config**: `.git/rtk-config.json` for project-specific settings

---

## 2. Command Rewrite Categories

### 2.1 Rewritable Commands

The optimizer will identify and rewrite commands in these categories:

| Category | Example | RTK Rewrite | Purpose |
|----------|---------|-------------|---------|
| **Git** | `git log` | `rtk git log` | Compress git output |
| **Go Toolchain** | `go test ./...` | `rtk go test ./...` | Aggregate test results |
| **Go Build** | `go build ./cmd/...` | `rtk go build ./cmd/...` | Filter build spam |
| **Go Lint** | `golangci-lint run` | `rtk golangci-lint run` | Summarize linter output |
| **Cargo/Rust** | `cargo test` | `rtk cargo test` | Aggregate Rust test results |
| **npm/Node** | `npm test` | `rtk npm test` | Filter npm output |
| **Docker** | `docker build` | `rtk docker build` | Compress build logs |
| **Kubernetes** | `kubectl get pods` | `rtk kubectl get pods` | Format/filter kubectl output |
| **Package Managers** | `apt-get install` | `rtk apt install` | Suppress verbose output |
| **File Operations** | `find`, `grep` (high volume) | Keep original | Use smart truncation |

### 2.2 Command Rewrite Logic

**Rewrite Chain**:
```
Original Command
    ↓
1. Detect command family (git, go, cargo, docker, etc.)
2. Check if RTK binary supports this command
3. If supported & enabled: apply rewrite
4. If not supported or disabled: pass through
5. Execute modified command
6. Apply compression to output
```

**Example Transformation**:
```bash
# Before
go test -v ./... 2>&1

# After (with rtk available)
rtk go test ./...

# After (rtk not available or disabled)
go test -v ./... 2>&1  [no rewrite, proceed to compression]
```

### 2.3 Fallback Behavior

If `rtk` binary is unavailable:
1. Log warning (if verbose mode enabled)
2. Execute original command
3. Still apply output compression filters
4. Continue gracefully

---

## 3. Output Compression Pipeline

The compression pipeline is a **multi-stage filter chain** applied after tool execution:

```
Raw Output
    ↓
Stage 1: ANSI Stripping
    └─ Remove color codes, control sequences
    ↓
Stage 2: Language-Specific Filtering
    ├─ Go Test Aggregation (collapse pass/fail summaries)
    ├─ Go Build Filtering (remove compiler spam, keep errors)
    ├─ Git Log Compaction (truncate long histories)
    ├─ Linter Summarization (group by severity)
    ├─ Cargo Summary (extract failure summary)
    └─ npm/yarn Filtering (remove verbose output)
    ↓
Stage 3: Smart Truncation
    └─ If output > threshold (configurable, default 5KB):
       - Find last complete paragraph
       - Preserve first context + last context
       - Add truncation notice
    ↓
Stage 4: Metric Tracking
    └─ Record original size, compressed size, ratio
    ↓
Compressed Output
```

### 3.1 Compression Filters by Tool

#### Go Test Output
```
Before (4.2KB):
    ok  github.com/example/pkg/tools  0.234s
    --- PASS: TestFoo (0.001s)
    --- PASS: TestBar (0.002s)
    [hundreds of similar lines...]
    PASS

After (0.3KB):
    ✓ go test ./... → 1234 tests passed (0.234s)
```

#### Git Log Output
```
Before (8.5KB):
    commit abc1234...
    Author: user <user@example.com>
    Date: Wed Mar 19 12:00:00 2026
    [full multi-line commit message...]
    [repeated 50 times...]

After (0.5KB):
    ✓ 50 commits from abc1234..def5678
    - Latest: "Fix RTK integration" (abc1234, 2026-03-19)
```

#### Go Build Errors
```
Before (1.2KB):
    # github.com/example/pkg/foo
    ./foo.go:42:12: undefined: bar
    ./foo.go:43:5: expected 'package', found 'var'
    [compiler messages...]

After (0.4KB):
    ✗ build failed: 2 errors in ./foo.go
    - Line 42: undefined: bar
    - Line 43: expected 'package', found 'var'
```

#### Linter Output
```
Before (3.2KB):
    [linters summary, per-file lists...]

After (0.6KB):
    ✓ Linter: 5 errors, 12 warnings across 3 files
    - errors: missing-lock (2x), vet (3x)
    - warnings: deadcode (8x), shadow (4x)
```

### 3.2 Configuration Parameters

Compression behavior will be configurable:

```go
type RTKConfig struct {
    Enabled             bool              // Enable optimizer
    Mode                string            // "auto", "suggest", "off"
    CommandRewrite      bool              // Enable command rewriting
    OutputCompression   bool              // Enable output filtering
    CompressionThreshold int              // Max output size (bytes)
    PreserveStructured  bool              // Never compress JSON/structured output
    Verbose             bool              // Log rewrite decisions
    ToolFilters         map[string]bool   // Per-tool compression enable/disable
}
```

---

## 4. Integration with Existing Infrastructure

### 4.1 Tool Registry Modification

**Current Flow**:
```
BashTool.Execute(ctx, params)
    ↓
Run /bin/bash -c "command"
    ↓
Capture stdout/stderr
    ↓
Return output
```

**Modified Flow**:
```
BashTool.Execute(ctx, params)
    ↓
RTK Hook: Pre-execution (BEFORE)
    ├─ Check if command is rewritable
    ├─ Rewrite if enabled + rtk available
    ↓
Run /bin/bash -c "rewritten_command"
    ↓
Capture stdout/stderr
    ↓
RTK Hook: Post-execution (AFTER)
    ├─ Identify output type (test, build, git, etc.)
    ├─ Apply language-specific compression
    ├─ Track metrics
    ↓
Return compressed output
```

**Implementation approach**:
1. Add hook registration to `AgentLoop` constructor
2. Create RTK hook subscriber in `pkg/tools/rtk_hooks.go`
3. Subscribe to `EventToolExecStart` and `EventToolExecEnd`
4. Transform command/output in the hook (modify event data)
5. Existing tools execute unmodified

### 4.2 Agent Loop Event Integration

Modify `pkg/agent/loop.go` to support hook subscribers:

```go
type EventHook interface {
    OnEvent(event *AgentEvent) error
}

type AgentLoop struct {
    // ... existing fields ...
    hooks []EventHook  // NEW
}

func (a *AgentLoop) RegisterHook(h EventHook) {
    a.hooks = append(a.hooks, h)
}

func (a *AgentLoop) emitEvent(event AgentEvent) {
    for _, h := range a.hooks {
        h.OnEvent(&event)  // Allow hooks to modify events
    }
    a.events <- event
}
```

### 4.3 Tool Execution Flow

Existing `executeTool` method in `loop.go`:
```go
func (a *AgentLoop) executeTool(ctx context.Context, tc *ai.ToolCall) (*ai.ToolResult, error) {
    // emit EventToolExecStart
    // tool.Execute(ctx, params)
    // emit EventToolExecEnd
    // emit EventToolResult
}
```

**Hook integration points**:
1. After `EventToolExecStart` emission: allow hooks to modify `ToolArgs`
2. After tool execution: capture output before returning
3. Before `EventToolResult` emission: allow hooks to modify output

---

## 5. Command Rewriting Engine

### 5.1 Detection and Rewrite Rules

```go
// pkg/tools/rtk_rewriter.go (NEW)

type CommandRewriter struct {
    rtkBinaryPath string
    config        RTKConfig
}

type CommandRule struct {
    Family        string          // "git", "go", "cargo", etc.
    Patterns      []string        // regex patterns to match
    ShouldRewrite func(cmd string) bool
    Rewrite       func(cmd string) string
}

func (r *CommandRewriter) Rewrite(cmd string) (string, bool) {
    // 1. Extract command family from cmd
    // 2. Find matching rule
    // 3. Apply rewrite if enabled
    // 4. Verify rtk supports this command
    // 5. Return rewritten command and whether it was rewritten
}
```

### 5.2 Rewrite Rules Registry

Built-in rules for:
- **Git**: `git log`, `git status`, `git diff`, `git branch`, `git commit`
- **Go**: `go test`, `go build`, `go run`, `go mod`, `go fmt`
- **Cargo**: `cargo test`, `cargo build`, `cargo check`, `cargo clippy`
- **npm/yarn**: `npm test`, `npm run`, `yarn test`, `yarn build`
- **Docker**: `docker build`, `docker logs`, `docker ps`
- **Kubernetes**: `kubectl get`, `kubectl describe`, `kubectl apply`

---

## 6. Output Compression Engine

### 6.1 Language-Specific Filters

```go
// pkg/tools/rtk_compress.go (NEW)

type OutputCompressor struct {
    filters map[string]Filter  // tool → filter
    config  RTKConfig
}

type Filter interface {
    CanHandle(toolName string, output string) bool
    Compress(output string) string
}

// Concrete filters:
// - GoTestFilter
// - GoBuildFilter
// - GitFilter
// - LinterFilter
// - CargoFilter
// - etc.
```

### 6.2 Compression Metrics Tracking

```go
type CompressionMetrics struct {
    Tool             string
    CommandFamily    string
    OriginalSize     int
    CompressedSize   int
    CompressionRatio float64
    Duration         time.Duration
    Timestamp        time.Time
}

// Track cumulative savings in session
type SessionMetrics struct {
    TotalOriginalSize   int
    TotalCompressedSize int
    TotalSavings        float64  // percentage
    ByTool              map[string]*CompressionMetrics
}
```

---

## 7. TUI Configuration Interface

### 7.1 RTK Config Command

Introduce new command: `/rtk config`

**Interactive Modal**:
```
╭─── RTK Optimizer Configuration ─────────────────────────────╮
│                                                              │
│  [✓] Enable Optimizer                                       │
│  [✓] Command Rewriting (requires rtk binary)                │
│  [✓] Output Compression                                     │
│                                                              │
│  Mode: (auto)  │ Suggest │ Off │                            │
│                                                              │
│  Compression Threshold: 5000 bytes                          │
│  Preserve Structured Data (JSON): [✓]                       │
│                                                              │
│  Per-Tool Filters:                                          │
│    [✓] Go Test      [✓] Git Log     [✓] Build Errors        │
│    [✓] Linter       [✓] Cargo       [✓] Docker              │
│                                                              │
│  [Save]  [Cancel]  [Reset to Defaults]                      │
│                                                              │
╰──────────────────────────────────────────────────────────────╯
```

### 7.2 Metrics Display

New view: `/rtk metrics` or `/rtk status`

```
RTK Optimizer Session Metrics
─────────────────────────────────────────
Total Original Output Size:     2.4 MB
Total Compressed Size:          0.8 MB
Overall Compression Ratio:      67% reduction
Estimated Token Savings:        ~8,400 tokens

By Tool:
  go test         →  94% saved  (1.2 MB → 0.07 MB)
  git log         →  85% saved  (0.6 MB → 0.09 MB)
  go build        →  72% saved  (0.3 MB → 0.08 MB)
  golangci-lint   →  58% saved  (0.2 MB → 0.08 MB)
  cargo test      →  90% saved  (0.08 MB → 0.008 MB)
```

---

## 8. Implementation Phases

### Phase 1: Foundation (Weeks 1-2)
- [ ] Event hook infrastructure in AgentLoop
- [ ] RTK hook registration & event subscriber
- [ ] Basic command detection (git, go, cargo families)
- [ ] Basic output compression for go test

**Deliverable**: 30% token savings on go test heavy workflows

### Phase 2: Core Compressors (Weeks 3-4)
- [ ] Go build/lint output compression
- [ ] Git log/status/diff compression
- [ ] Cargo test/build compression
- [ ] npm/yarn output filtering

**Deliverable**: 50% token savings on multi-tool workflows

### Phase 3: Command Rewriting (Weeks 5-6)
- [ ] Command rewriter infrastructure
- [ ] rtk binary detection & version checking
- [ ] Integration with compression pipeline
- [ ] Fallback handling (rtk unavailable)

**Deliverable**: Further 10-15% token savings with rtk binary

### Phase 4: TUI & Configuration (Weeks 7-8)
- [ ] `/rtk config` command modal
- [ ] `/rtk metrics` view
- [ ] Per-tool enable/disable toggles
- [ ] Session metrics tracking

**Deliverable**: User-controllable compression, visibility into savings

---

## 9. File Structure

```
go_pi/
├── pkg/
│   └── tools/
│       ├── rtk_hooks.go          (NEW) Hook subscriber & dispatcher
│       ├── rtk_rewriter.go        (NEW) Command rewriting engine
│       ├── rtk_compress.go        (NEW) Output compression logic
│       ├── rtk_filters/           (NEW) Directory of language-specific filters
│       │   ├── go_test.go         (NEW)
│       │   ├── go_build.go        (NEW)
│       │   ├── git.go             (NEW)
│       │   ├── linter.go          (NEW)
│       │   ├── cargo.go           (NEW)
│       │   └── npm.go             (NEW)
│       └── tool.go                (MODIFIED) Add hook registration
├── pkg/
│   └── agent/
│       └── loop.go                (MODIFIED) Add hook support, event emission
├── pkg/
│   └── tui/
│       └── rtk_modal.go           (NEW) TUI configuration interface
├── docs/
│   └── RTK_OPTIMIZER_DESIGN.md    (THIS FILE)
└── examples/
    └── rtk-optimization-guide.md  (NEW) User-facing guide
```

---

## 10. Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| **Hook at AgentLoop level** | Centralized, applies to all tools, minimal coupling |
| **Per-tool compression filters** | Flexible, language-aware, easy to extend |
| **Graceful fallback to output compression** | Works even without rtk binary |
| **Event-based architecture** | Non-intrusive, allows hooks to be enabled/disabled dynamically |
| **Session-level configuration** | Users can adjust behavior mid-session via `/rtk config` |
| **Preserve structured output** | JSON/CSV/structured logs won't be corrupted |
| **Metrics tracking** | Users see value of optimization, can measure impact |

---

## 11. Testing Strategy

### Unit Tests
- Command detection/rewriting logic
- Output compression filters (each language)
- Hook event dispatch
- Configuration loading/saving

### Integration Tests
- End-to-end flow: command rewrite → execute → compress
- Hook subscriber receives correct events
- Fallback when rtk binary unavailable
- Metrics tracking accuracy

### Manual Testing
- Real agent sessions with various command types
- Verify token count reduction
- Test `/rtk config` modal interaction
- Test `/rtk metrics` display

---

## 12. Future Extensions

### Possible Enhancements
1. **Adaptive compression**: Learn which filters are most effective per user
2. **ML-based filtering**: Use model to decide what output is important
3. **Cross-tool correlation**: Link outputs (e.g., test failure → source code)
4. **Streaming compression**: Apply filters to real-time output streams
5. **Plugin API**: Allow users to define custom compression rules
6. **Integration with other agents**: Share compression metrics across agents

---

## 13. Success Metrics

- **Token Reduction**: Achieve 50-70% reduction on output-heavy workloads
- **Latency**: <50ms overhead per tool execution
- **Reliability**: 99.9% fallthrough (no data loss even if compression breaks)
- **Usability**: Users find RTK config intuitive, adopt optimization
- **Coverage**: Support all major CLI tools used in modern Go/Rust development

---

## 14. References

- Original pi-rtk-optimizer: https://github.com/MasuRii/pi-rtk-optimizer
- gi Agent: https://github.com/ejm/go_pi
- RTK Binary (Rust implementation): https://github.com/rtr-tools/rtk
