# MCP Server Support Design for gi (v6)

## 1. Architecture

MCP servers integrate as a new transport layer alongside the existing plugin system. Both share the tool registry but use different protocols:

```
cmd/gi/main.go
├── pkg/plugin/manager.go       (existing JSONL plugins)
├── pkg/mcp/manager.go          (MCP server manager)
│   ├── pkg/mcp/server.go       (per-server lifecycle)
│   ├── pkg/mcp/protocol.go     (MCP JSON-RPC client)
│   ├── pkg/mcp/transport/      (stdio, streamable-http)
│   ├── pkg/mcp/tool_bridge.go  (MCPTool — implements tools.RichTool)
│   ├── pkg/mcp/resource.go     (resources + templates + subscriptions)
│   ├── pkg/mcp/prompt.go       (prompt → skill bridge)
│   └── pkg/mcp/permission.go   (per-server permission config)
└── pkg/tools/tool.go           (shared Tool registry)
```

**Key principle**: MCP servers register tools through the same `tools.Registry` as plugins. The agent loop doesn't know or care whether a tool came from a plugin, MCP server, or built-in.

An `MCPServer` struct manages a single MCP server connection. An `MCPManager` manages all configured servers, analogous to `plugin.Manager`.

## 2. Configuration

Three tiers, matching the existing config pattern in `pkg/config/config.go`:

**~/.gi/settings.json** (global):
```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/home/user"],
      "env": {"NODE_ENV": "production"}
    }
  }
}
```

**.gi/settings.json** (project-level, overrides global):
```json
{
  "mcpServers": {
    "db": {
      "command": "mcp-server-postgres",
      "args": ["postgresql://localhost/${DB_NAME}"]
    }
  }
}
```

**Streamable HTTP transport** (remote servers):
```json
{
  "mcpServers": {
    "remote-api": {
      "url": "https://mcp.example.com/mcp",
      "headers": {"Authorization": "Bearer ${MCP_API_KEY}"}
    }
  }
}
```

### Environment Variable Interpolation

All string fields in `MCPServerConfig` support `${VAR_NAME}` interpolation from the process environment. This applies to:
- `command`, `args[]` — allows dynamic paths
- `url` — allows dynamic endpoints
- `headers` values — allows secret injection
- `env` values — allows derived env vars

Interpolation happens at config load time via a single `expandEnvVars(s string) string` helper. Unresolved variables expand to empty string and emit a warning log. Nested interpolation (`${${VAR}}`) is not supported.

**Security: project-level interpolation is restricted.** Global config (`~/.gi/settings.json`) has full interpolation access since the user controls it. Project-level config (`.gi/settings.json`) only interpolates variables explicitly allowlisted in global config, or requires user confirmation before connecting to MCP servers defined in project config. This prevents a malicious repository from exfiltrating environment variables (e.g., `${AWS_SECRET_ACCESS_KEY}`) via crafted MCP server URLs.

```go
// Project-level MCP servers require explicit user approval on first connection.
// Display: "Project config requests MCP connection to <url>. Allow? [y/N]"
// Approved servers are cached in ~/.gi/approved_mcp_servers.json keyed by
// (project_path, server_name, url_pattern).
```

```go
// pkg/mcp/config.go
type MCPServerConfig struct {
    Command  string            `json:"command,omitempty"`
    Args     []string          `json:"args,omitempty"`
    Env      map[string]string `json:"env,omitempty"`
    URL      string            `json:"url,omitempty"`       // Streamable HTTP endpoint
    Headers  map[string]string `json:"headers,omitempty"`   // for Streamable HTTP auth

    // Permission overrides
    Permissions *MCPPermissionConfig `json:"permissions,omitempty"`

    // Sampling limits (§6)
    Sampling *SamplingConfig `json:"sampling,omitempty"`

    // Instruction handling
    Instructions string `json:"instructions,omitempty"` // "use" (default) or "ignore"
}

// Added to config.Config:
MCPServers map[string]MCPServerConfig `json:"mcpServers,omitempty"`
```

## 3. Tool Bridging

Each MCP server's tools are discovered via `tools/list` and registered as `tools.RichTool`.

### RichTool Implementation

MCPTool implements `tools.RichTool` (not just `tools.Tool`) because MCP tool results can contain multiple content items — text, images, audio, embedded resources. The agent loop already checks for `RichTool` at `pkg/tools/tool.go:26` and calls `ExecuteRich` when available, producing `ai.NewRichToolResultMessage`.

```go
// pkg/mcp/tool_bridge.go
type MCPTool struct {
    server       *MCPServer
    name         string              // namespaced: "mcp__servername__toolname"
    originalName string              // name as known by the MCP server
    title        string              // human-readable display name from Tool.title
    desc         string
    inputSchema  map[string]any      // JSON Schema from server
    annotations  *ToolAnnotations    // tool behavior hints
}

// Implements tools.Tool
func (t *MCPTool) Name() string        { return t.name }
func (t *MCPTool) Description() string { return t.desc }
func (t *MCPTool) Title() string       { return t.title }
func (t *MCPTool) Schema() any         { return t.inputSchema }
func (t *MCPTool) Execute(ctx context.Context, params map[string]any) (string, error) {
    blocks, err := t.ExecuteRich(ctx, params)
    if err != nil {
        return "", err
    }
    // Flatten to string for simple callers
    var b strings.Builder
    for _, block := range blocks {
        b.WriteString(block.Text)
    }
    return b.String(), nil
}

// Implements tools.RichTool — returns []ai.ContentBlock for multi-content results
func (t *MCPTool) ExecuteRich(ctx context.Context, params map[string]any) ([]ai.ContentBlock, error) {
    result, err := t.server.CallTool(ctx, t.originalName, params)
    if err != nil {
        // Only return Go errors for transport-level failures
        if errors.Is(err, ErrServerCrashed) {
            return nil, fmt.Errorf("MCP server %q crashed during tool call %q: %w",
                t.server.Name, t.originalName, err)
        }
        return nil, err
    }

    blocks := t.convertResult(result)

    // When MCP isError=true, return a RichToolError so the agent loop
    // preserves the content blocks AND sets isError=true on the message.
    if result.IsError {
        return nil, &tools.RichToolError{Blocks: blocks}
    }

    return blocks, nil
}
```

### isError Handling via RichToolError

MCP tool results include an `isError` boolean. When `isError` is true, the error content must be returned as content blocks to the LLM — NOT as a Go `error`. Go errors are reserved for transport-level failures (server crash, timeout, connection lost).

**Problem:** The `RichTool` interface returns `([]ai.ContentBlock, error)`. There is no way to return `(blocks, isError=true, err=nil)`. The agent loop at `loop.go:531` passes `false` for isError when `err == nil`:

```go
// Current code (loop.go:531):
return ai.NewRichToolResultMessage(tc.ToolUseID, blocks, false)
```

**Solution: `RichToolError` type.** When `ExecuteRich` returns a `*tools.RichToolError` as the error, the agent loop extracts the content blocks and sets `isError=true`. This requires a minimal change to the loop and no change to the `RichTool` interface signature.

```go
// pkg/tools/tool.go — new type
// RichToolError is returned by RichTool.ExecuteRich when the tool executed
// successfully at transport level but the result represents an error the
// LLM should reason about (e.g., MCP isError=true). The Blocks contain
// the error content that should be presented to the LLM as a tool result
// with isError=true, NOT flattened to a Go error string.
type RichToolError struct {
    Blocks []ai.ContentBlock
}

func (e *RichToolError) Error() string {
    // Fallback string for callers that don't type-assert.
    var b strings.Builder
    for _, block := range e.Blocks {
        if block.Type == ai.ContentTypeText {
            b.WriteString(block.Text)
        }
    }
    return b.String()
}
```

**Agent loop change** (in `pkg/agent/loop.go`, replacing lines 497-531):

```go
// Check if tool supports rich (multi-block) results.
if richTool, ok := tool.(tools.RichTool); ok {
    blocks, err := richTool.ExecuteRich(ctx, params)

    // Check for RichToolError — tool-level error with preserved content blocks.
    // This is distinct from transport errors (which flatten to plain text).
    var richErr *tools.RichToolError
    if errors.As(err, &richErr) {
        // Tool executed but result is an error the LLM should reason about.
        // Preserve the content blocks; set isError=true.
        resultText := richErr.Error()
        resultText, hookErr := a.hooks.After(ctx, tc.ToolName, params, resultText, nil)
        if hookErr != nil {
            resultText = hookErr.Error()
        }
        a.emit(ctx, AgentEvent{
            Type:       EventToolExecEnd,
            ToolCallID: tc.ToolUseID,
            ToolName:   tc.ToolName,
            ToolResult: resultText,
            ToolError:  true,
        })
        return ai.NewRichToolResultMessage(tc.ToolUseID, richErr.Blocks, true)
    }

    isError := err != nil
    var resultText string
    if isError {
        resultText = err.Error()
    } else {
        var sb strings.Builder
        for _, b := range blocks {
            if b.Type == ai.ContentTypeText {
                sb.WriteString(b.Text)
            }
        }
        resultText = sb.String()
    }

    // Fire after-execution hooks
    resultText, hookErr := a.hooks.After(ctx, tc.ToolName, params, resultText, nil)
    if hookErr != nil {
        isError = true
        resultText = hookErr.Error()
    }

    a.emit(ctx, AgentEvent{
        Type:       EventToolExecEnd,
        ToolCallID: tc.ToolUseID,
        ToolName:   tc.ToolName,
        ToolResult: resultText,
        ToolError:  isError,
    })
    if isError {
        return ai.NewToolResultMessage(tc.ToolUseID, resultText, true)
    }
    return ai.NewRichToolResultMessage(tc.ToolUseID, blocks, false)
}
```

**Why this approach over alternatives:**
- Changing `ExecuteRich() ([]ai.ContentBlock, bool, error)` breaks all existing `RichTool` implementors.
- Adding a `RichToolWithMeta` interface adds unnecessary proliferation.
- `RichToolError` requires zero interface changes, minimal loop change, and only MCP tools need to use it.

### Content Type Conversion

```go
func (t *MCPTool) convertResult(result MCPToolResult) []ai.ContentBlock {
    blocks := make([]ai.ContentBlock, 0, len(result.Content))
    for _, item := range result.Content {
        switch item.Type {
        case "text":
            blocks = append(blocks, ai.ContentBlock{
                Type: ai.ContentTypeText,
                Text: item.Text,
            })
        case "image":
            blocks = append(blocks, ai.ContentBlock{
                Type:      ai.ContentTypeImage,
                MediaType: item.MimeType,
                ImageData: item.Data,
            })
        case "audio":
            // Audio content rendered as metadata annotation (native audio playback deferred)
            blocks = append(blocks, ai.ContentBlock{
                Type: ai.ContentTypeText,
                Text: fmt.Sprintf("[audio: %s, %d bytes, encoding=%s]",
                    item.MimeType, len(item.Data), item.Encoding),
            })
        case "resource":
            // Embedded resource — render as text with URI annotation
            blocks = append(blocks, ai.ContentBlock{
                Type: ai.ContentTypeText,
                Text: fmt.Sprintf("[resource: %s]\n%s", item.Resource.URI, item.Resource.Text),
            })
        default:
            log.Warn("Unrecognized MCP content type", "type", item.Type)
            blocks = append(blocks, ai.ContentBlock{
                Type: ai.ContentTypeText,
                Text: fmt.Sprintf("[unsupported content type: %s]", item.Type),
            })
        }
    }
    return blocks
}
```

### Tool Annotations

MCP tools can have annotations that describe behavior hints. These are preserved and used for permission decisions and UI display:

```go
type ToolAnnotations struct {
    Title           string `json:"title,omitempty"`           // Human-readable name
    ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`    // Tool doesn't modify state
    DestructiveHint *bool  `json:"destructiveHint,omitempty"` // Tool may delete/destroy
    IdempotentHint  *bool  `json:"idempotentHint,omitempty"`  // Safe to retry
    OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`   // Interacts with external systems
}

// Spec-defined defaults for annotation fields.
// When a hint pointer is nil (absent from server response), these defaults apply:
//   readOnlyHint:    false  (assume tool may modify state)
//   destructiveHint: true   (assume tool may be destructive)
//   idempotentHint:  false  (assume tool is not idempotent)
//   openWorldHint:   true   (assume tool interacts with external systems)
//
// This is security-conservative by design: tools without explicit annotations
// are treated as potentially destructive and externally-connected, requiring
// user confirmation. Only tools that explicitly declare readOnlyHint=true
// are candidates for auto-approval.
func annotationReadOnly(a *ToolAnnotations) bool {
    if a == nil || a.ReadOnlyHint == nil {
        return false // spec default
    }
    return *a.ReadOnlyHint
}

func annotationDestructive(a *ToolAnnotations) bool {
    if a == nil || a.DestructiveHint == nil {
        return true // spec default: assume destructive
    }
    return *a.DestructiveHint
}
```

Annotations inform the permission layer (§10): tools with `readOnlyHint: true` are candidates for auto-approval; tools where `destructiveHint` resolves to true (including the default) always require confirmation.

### Tool Discovery with Task Support Filtering

During tool discovery, tools with `execution.taskSupport: "required"` are filtered out. These tools can only be called via task-augmented requests (Tasks are deferred to Phase 2), so registering them would cause the LLM to attempt calls that always fail. Tools with `taskSupport: "optional"` or `"forbidden"` (the default) are registered normally.

```go
func (s *MCPServer) discoverTools(ctx context.Context) ([]tools.Tool, error) {
    var allTools []tools.Tool
    var cursor string
    for pages := 0; pages < maxPaginationPages; pages++ {
        page, err := s.client.ListTools(ctx, cursor)
        if err != nil {
            return nil, err
        }
        for _, mcpTool := range page.Tools {
            if mcpTool.Execution.TaskSupport == "required" {
                log.Info("Skipping MCP tool (requires task support, deferred to Phase 2)",
                    "server", s.Name, "tool", mcpTool.Name)
                continue
            }
            allTools = append(allTools, s.bridgeTool(mcpTool))
        }
        if page.NextCursor == "" || len(allTools) >= maxTotalItems {
            break
        }
        cursor = page.NextCursor
    }
    return allTools, nil
}
```

### Namespacing and Collision Resolution

Tools are prefixed `mcp__<server>__<tool>` to avoid collisions. The LLM sees the full name; the bridge strips the prefix when calling the MCP server.

**Priority / collision resolution**:
1. Built-in tools always win (Read, Write, Edit, Bash, Glob, Grep — registered via `tools.RegisterDefaults`)
2. Plugin tools take precedence over MCP tools (plugins are locally authored, registered via `plugin.PluginTool`)
3. Among MCP servers, config order determines priority
4. Collision logged as warning; lower-priority tool gets namespaced prefix forced

**Schema mapping**: MCP `inputSchema` is JSON Schema but may use features not all providers handle well. The bridge:
- Flattens simple `$ref` references inline
- Converts `oneOf` to a single object with optional fields where possible
- Logs warning and passes through for complex schemas
- Validates params locally before sending to MCP server (fail fast)

### Title in ToolDef

`MCPTool.Title()` provides a human-readable display name from the MCP Tool.title field. The current `ai.ToolDef` struct has only `Name`, `Description`, and `InputSchema` — it does not have a `Title` field.

**Decision: Phase 2.** The Title is not needed by the LLM (the LLM uses Name and Description for tool selection). Title is intended for human-facing UI display (tool listing, permission prompts). When gi's TUI adds a tool browser or richer permission dialogs, `ai.ToolDef` can be extended with `Title string`. For now, `MCPTool.Title()` is available to any code that holds a `Tool` reference and type-asserts to `MCPTool` or checks for a `Titled` interface, but it does not flow through the `ToolDef` serialization path.

## 3.1 Registry Changes

### Problem

MCP introduces dynamic tool changes: `notifications/tools/list_changed` requires re-discovering tools and updating the registry mid-session. The current registries have two gaps:

1. **No Unregister/Remove**: When an MCP server disconnects or drops a tool, stale entries remain. The LLM calls them and gets "tool not found" errors.
2. **No thread safety**: Both `tools.Registry` and `skill.Registry` use plain `map[string]T` with no mutex. Concurrent reads (agent loop executing tools in parallel) and writes (MCP re-discovery) cause data races.

The current codebase gets away without locking because registries are populated once at startup (built-ins + plugins). MCP breaks this assumption.

### Solution: Add sync.RWMutex and bulk update methods

```go
// pkg/tools/tool.go — updated Registry
type Registry struct {
    mu    sync.RWMutex
    tools map[string]Tool
}

func NewRegistry() *Registry {
    return &Registry{tools: make(map[string]Tool)}
}

func (r *Registry) Register(t Tool) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.tools[t.Name()] = t
}

func (r *Registry) Unregister(name string) {
    r.mu.Lock()
    defer r.mu.Unlock()
    delete(r.tools, name)
}

// ReplaceByPrefix atomically removes all tools with the given prefix
// and registers the new set. Used by MCP when re-discovering tools
// after notifications/tools/list_changed.
//
// Example: ReplaceByPrefix("mcp__filesystem__", newTools)
// removes all "mcp__filesystem__*" tools and registers newTools.
//
// Lock held for O(n) where n = total registered tools. Acceptable for
// expected registry sizes (< 1000 tools). If registries grow significantly,
// consider a concurrent map or sharded locking.
func (r *Registry) ReplaceByPrefix(prefix string, newTools []Tool) {
    r.mu.Lock()
    defer r.mu.Unlock()
    for name := range r.tools {
        if strings.HasPrefix(name, prefix) {
            delete(r.tools, name)
        }
    }
    for _, t := range newTools {
        r.tools[t.Name()] = t
    }
}

// AllWithPrefix returns all tools whose name starts with the given prefix.
func (r *Registry) AllWithPrefix(prefix string) []Tool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    var result []Tool
    for name, t := range r.tools {
        if strings.HasPrefix(name, prefix) {
            result = append(result, t)
        }
    }
    return result
}

func (r *Registry) Get(name string) (Tool, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    t, ok := r.tools[name]
    return t, ok
}

func (r *Registry) All() []Tool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    all := make([]Tool, 0, len(r.tools))
    for _, t := range r.tools {
        all = append(all, t)
    }
    slices.SortFunc(all, func(a, b Tool) int {
        return strings.Compare(a.Name(), b.Name())
    })
    return all
}

func (r *Registry) ToToolDefs() []ai.ToolDef {
    all := r.All() // Already holds RLock internally
    defs := make([]ai.ToolDef, len(all))
    for i, t := range all {
        defs[i] = ai.ToolDef{
            Name:        t.Name(),
            Description: t.Description(),
            InputSchema: t.Schema(),
        }
    }
    return defs
}
```

```go
// pkg/skill/skill.go — updated Registry (same pattern)
type Registry struct {
    mu     sync.RWMutex
    skills map[string]*Skill
}

func (r *Registry) Unregister(name string) {
    r.mu.Lock()
    defer r.mu.Unlock()
    delete(r.skills, name)
}

func (r *Registry) ReplaceByPrefix(prefix string, newSkills []*Skill) {
    r.mu.Lock()
    defer r.mu.Unlock()
    for name := range r.skills {
        if strings.HasPrefix(name, prefix) {
            delete(r.skills, name)
        }
    }
    for _, s := range newSkills {
        r.skills[s.Name] = s
    }
}

func (r *Registry) AllWithPrefix(prefix string) []*Skill {
    r.mu.RLock()
    defer r.mu.RUnlock()
    var result []*Skill
    for name, s := range r.skills {
        if strings.HasPrefix(name, prefix) {
            result = append(result, s)
        }
    }
    return result
}

// All read methods (Get, Names, All, UserInvocable, Len) acquire r.mu.RLock().
// All write methods (Register, Unregister, ReplaceByPrefix) acquire r.mu.Lock().
```

**Impact on existing code:** Adding `sync.RWMutex` is purely additive — existing callers that only `Register` at startup and `Get`/`All` during the loop see no behavior change. The mutex adds negligible overhead for the read-heavy access pattern (RLock is uncontended when no writes are happening).

**MCP re-discovery flow:**

```go
// In MCPServer, when notifications/tools/list_changed is received:
func (s *MCPServer) handleToolsListChanged(ctx context.Context) error {
    newTools, err := s.discoverTools(ctx) // paginated tools/list
    if err != nil {
        return err
    }
    prefix := "mcp__" + s.Name + "__"
    s.manager.toolRegistry.ReplaceByPrefix(prefix, newTools)
    // Notify the LLM about the change
    s.manager.injectSystemMessage(fmt.Sprintf(
        "[MCP server %q tools updated — %d tools registered]",
        s.Name, len(newTools)))
    return nil
}
```

## 3.2 Hook Interaction with RichTool Results

The `Hook.AfterExecute` signature operates on strings:

```go
type Hook interface {
    BeforeExecute(ctx context.Context, toolName string, params map[string]any) error
    AfterExecute(ctx context.Context, toolName string, params map[string]any, result string, err error) (string, error)
}
```

For `RichTool` results, the agent loop (lines 505-511) extracts text from content blocks for hook processing, then discards the hook's modified string and returns the original blocks. This means:

- ANSI stripping (from RTK hooks) does NOT apply to MCP text content
- Compression hooks do NOT apply to MCP text content
- RTK metrics do NOT account for MCP tool output size

**This is intentional and correct for Phase 1.** MCP results are structured content blocks (text, images, embedded resources), not shell output. Applying string-level transformations (ANSI stripping, compression) to structured content would corrupt the data. The hooks are designed for shell tool output (Bash, Read, etc.) where ANSI codes and verbose output are expected.

**The hook string is used for:**
- Event emission (`AgentEvent.ToolResult`) — for logging and metrics
- Hook error detection — hookErr can still abort the tool result

**Phase 2 consideration:** If MCP text content needs transformation (e.g., token compression for large MCP responses), introduce `AfterExecuteRich(ctx, toolName, params, []ai.ContentBlock, error) ([]ai.ContentBlock, error)` on the Hook interface. This is opt-in: hooks that don't implement it fall back to the string path. Not needed for Phase 1 since MCP results are typically compact (tool output, not multi-MB shell dumps).

## 4. Resources

### Discovery and Caching

On startup after `tools/list`, call `resources/list` to discover available resources. Cache the resource index (URI, name, description, mimeType).

### Resource Templates

In addition to static resources, MCP servers expose resource templates via `resources/templates/list`. Templates are URI templates (RFC 6570) with parameters:

```go
type ResourceTemplate struct {
    URITemplate string          `json:"uriTemplate"` // e.g., "file:///{path}"
    Name        string          `json:"name"`
    Title       string          `json:"title,omitempty"`       // human-readable display name
    Description string          `json:"description,omitempty"`
    MimeType    string          `json:"mimeType,omitempty"`
    Annotations *ToolAnnotations `json:"annotations,omitempty"` // behavior hints
    // icons field deferred to Phase 2 (see §12)
}
```

The per-server resource tool accepts template URIs and expands them with provided parameters before calling `resources/read`.

### Subscriptions and Unsubscribe

```go
// Subscribe to resource updates
func (s *MCPServer) SubscribeResource(ctx context.Context, uri string) error
// Unsubscribe from resource updates
func (s *MCPServer) UnsubscribeResource(ctx context.Context, uri string) error
```

Subscribe on first access; unsubscribe on server shutdown or when the agent indicates it no longer needs the resource. Server sends `notifications/resources/list_changed` when the resource list itself changes (re-fetch index) and `notifications/resources/updated` when subscribed resource content changes (re-fetch content).

### Pagination

`resources/list` and `resources/templates/list` support cursor-based pagination:

```go
type PaginatedRequest struct {
    Cursor string `json:"cursor,omitempty"`
}

type PaginatedResponse struct {
    NextCursor string `json:"nextCursor,omitempty"`
}
```

The MCPServer client iterates: send request, collect results, if `nextCursor` is non-empty send another request with that cursor. All pages are aggregated before registering resources. Same pattern applies to `tools/list` and `prompts/list`.

**Safety limits**: Pagination loops are bounded to prevent runaway iteration from buggy or malicious servers:

```go
const (
    maxPaginationPages = 100   // max iterations before stopping
    maxTotalItems      = 10000 // max items across all pages
)
```

If either limit is reached, the loop terminates with a warning log and returns items collected so far.

### Per-Server Resource Tool

Each MCP server that advertises resources gets its own namespaced resource read tool:

```go
// Registered per-server during resource discovery
// Name: "mcp__filesystem__read_resource"
// Name: "mcp__db__read_resource"
type MCPResourceTool struct {
    server *MCPServer
    name   string // "mcp__<server>__read_resource"
}

func (t *MCPResourceTool) Name() string { return t.name }
func (t *MCPResourceTool) Description() string {
    return fmt.Sprintf("Read a resource from MCP server %q. Provide a resource URI.", t.server.Name)
}
func (t *MCPResourceTool) Schema() any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "uri": map[string]any{
                "type":        "string",
                "description": "Resource URI (e.g., file:///path or template URI with parameters)",
            },
        },
        "required": []string{"uri"},
    }
}

func (t *MCPResourceTool) Execute(ctx context.Context, params map[string]any) (string, error) {
    uri, _ := params["uri"].(string)
    if uri == "" {
        return "", fmt.Errorf("uri parameter is required")
    }
    content, err := t.server.ReadResource(ctx, uri)
    if err != nil {
        return "", err
    }
    return content, nil
}
```

This routes through MCPPermissionHook naturally: `parseMCPToolName("mcp__filesystem__read_resource")` returns `server="filesystem"`, so the permission config for `filesystem` applies. The `auto_approve` and `deny` lists can include `"read_resource"` to control access.

## 5. Prompts → Skills

MCP prompts return structured message arrays, not template strings. gi's first-class concept for reusable prompts is **skills** (defined in `pkg/skill/`). Prompts map to skills.

### Prompt-to-Skill Bridge

```go
// pkg/mcp/prompt.go
type MCPPromptSkill struct {
    server     *MCPServer
    promptName string
    desc       string
    args       []MCPPromptArgument
}

// MCP prompt arguments preserve the required/optional distinction
type MCPPromptArgument struct {
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
    Required    bool   `json:"required,omitempty"`
}
```

Each MCP prompt is registered in the `skill.Registry` as a `skill.Skill` with:
- `Name`: `mcp__<server>__<prompt>` (namespaced to avoid collisions)
- `Description`: from MCP prompt description
- `UserInvocable`: true (so it appears in `/help` skill list)
- `Source`: "mcp" (new source type alongside "built-in", "user", "project")

When invoked via the existing `SkillTool`, the bridge calls `prompts/get` on the MCP server with parsed arguments. The returned message array is injected into the conversation.

**Argument validation:** Before calling `prompts/get`, the bridge validates that all arguments with `Required: true` have been provided. Missing required arguments produce a user-visible error: `"missing required argument %q for MCP prompt %q"`.

```go
func (p *MCPPromptSkill) LoadBody() (string, error) {
    // Return a body template that tells the SkillTool to delegate to MCP.
    // The actual MCP call happens in a custom Execute path.
    return fmt.Sprintf("[MCP prompt from server %q — invoke via Skill tool]", p.server.Name), nil
}
```

Since `skill.Skill.LoadBody()` returns a template string but MCP prompts return message arrays, the bridge intercepts at the `SkillTool.Execute` level. When the skill's Source is "mcp", `SkillTool.Execute` calls the MCP bridge directly instead of rendering the template.

### Pagination

`prompts/list` supports the same cursor-based pagination as resources (with the same safety limits). All pages aggregated before registering skills.

## 6. Sampling

MCP servers can request the client to sample from the LLM via `sampling/createMessage`:

```go
func (s *MCPServer) handleSamplingRequest(req SamplingRequest) (SamplingResponse, error) {
    // Check approval before executing sampling request (SkipApproval defaults
    // to false so the zero-value config is the secure path).
    if !s.config.Sampling.SkipApproval {
        if !s.manager.isInteractive() {
            return SamplingResponse{}, fmt.Errorf(
                "MCP server %q requested sampling but approval is required and no interactive session is available",
                s.Name)
        }
        approved, err := s.manager.confirmSampling(s.Name, req)
        if err != nil || !approved {
            return SamplingResponse{}, fmt.Errorf("user denied sampling request from MCP server %q", s.Name)
        }
    }

    resp, err := s.manager.provider.Stream(ctx, ai.StreamRequest{
        Model:       req.ModelPreferences.selectModel(s.manager.availableModels),
        Messages:    convertMCPMessages(req.Messages),
        SystemPrompt: req.SystemPrompt,
        MaxTokens:   min(req.MaxTokens, s.config.Sampling.MaxTokens),
    })
    return collectResponse(resp), err
}
```

**Security**: Sampling is opt-in per server in config. Default: disabled. When enabled, approval is required by default.

```go
type SamplingConfig struct {
    Enabled         bool `json:"enabled"`
    MaxTokens       int  `json:"maxTokens"`
    // Skip the approval prompt for each sampling request.
    // Default: false (approval required). Set true only for trusted servers.
    SkipApproval bool `json:"skipApproval,omitempty"`
}
```

In non-interactive mode, if `SkipApproval` is false (the default), sampling requests are rejected with an error message explaining that approval is required.

```json
{"mcpServers": {"helper": {"sampling": {"enabled": true, "maxTokens": 4096}}}}
```

## 7. Notifications and Logging

### Client → Server Notifications

| Notification | When Sent |
|-------------|-----------|
| `notifications/initialized` | After successful `initialize` response |
| `notifications/cancelled` | When cancelling an in-flight request |
| `notifications/roots/list_changed` | When working directory changes |

### Request Cancellation

When the agent loop context is cancelled (user hits Ctrl+C, steering interrupt, timeout), the MCPServer sends `notifications/cancelled` for any in-flight requests:

```go
type CancelledNotification struct {
    Method string `json:"method"` // "notifications/cancelled"
    Params struct {
        RequestID json.RawMessage `json:"requestId"` // ID of the request to cancel
        Reason    string          `json:"reason,omitempty"`
    } `json:"params"`
}
```

Implementation: `MCPServer` tracks in-flight request IDs. When `ctx.Done()` fires during a pending request, it sends `notifications/cancelled` before returning `context.Canceled`.

### Roots/List

MCP clients can advertise their workspace roots to servers. This enables servers to understand the project structure.

**Client capability**: Advertise `roots: {listChanged: true}` in the `initialize` request's `capabilities` field.

**Server request**: When the server sends a `roots/list` request, the client responds with the current working directory:

```go
func (m *MCPManager) handleRootsList() RootsListResponse {
    return RootsListResponse{
        Roots: []Root{
            {URI: "file://" + m.workingDir, Name: filepath.Base(m.workingDir)},
        },
    }
}
```

**Change notification**: If the working directory changes (e.g., user switches project), send `notifications/roots/list_changed` to all connected servers.

### Server → Client Notifications

| Notification | Action |
|-------------|--------|
| `notifications/resources/updated` | Re-fetch subscribed resource content |
| `notifications/resources/list_changed` | Re-discover resources, update index, notify LLM |
| `notifications/tools/list_changed` | Re-discover tools, update registry, notify LLM |
| `notifications/prompts/list_changed` | Re-discover prompts, update skill registry, notify LLM |
| `notifications/message` (logging) | Forward to gi logger with `[mcp:<server>]` prefix |
| `notifications/progress` | Log progress (full handling deferred to Phase 2) |
| Unknown notification | Log at debug level, do not error |

### LLM Notification on List Changes

Any `list_changed` notification triggers both a registry update AND an LLM system message injection:

```go
// In MCPManager — unified handler for all list_changed notifications
func (m *MCPManager) handleListChanged(ctx context.Context, server *MCPServer, kind string) {
    var added, removed int
    switch kind {
    case "tools":
        oldTools := m.toolRegistry.AllWithPrefix("mcp__" + server.Name + "__")
        newTools, err := server.discoverTools(ctx)
        if err != nil {
            log.Error("Failed to re-discover tools", "server", server.Name, "error", err)
            return
        }
        prefix := "mcp__" + server.Name + "__"
        m.toolRegistry.ReplaceByPrefix(prefix, newTools)
        added, removed = diffCount(oldTools, newTools)
        m.injectSystemMessage(fmt.Sprintf(
            "[MCP server %q tools updated — %d added, %d removed, %d total]",
            server.Name, added, removed, len(newTools)))

    case "resources":
        server.rediscoverResources(ctx)
        m.injectSystemMessage(fmt.Sprintf(
            "[MCP server %q resources updated]", server.Name))

    case "prompts":
        oldSkills := m.skillRegistry.AllWithPrefix("mcp__" + server.Name + "__")
        newSkills, err := server.discoverPrompts(ctx)
        if err != nil {
            log.Error("Failed to re-discover prompts", "server", server.Name, "error", err)
            return
        }
        prefix := "mcp__" + server.Name + "__"
        m.skillRegistry.ReplaceByPrefix(prefix, newSkills)
        added, removed = diffCount(oldSkills, newSkills)
        m.injectSystemMessage(fmt.Sprintf(
            "[MCP server %q prompts updated — %d added, %d removed]",
            server.Name, added, removed))
    }
}

// Lock ordering (innermost last):
// 1. MCPManager.mu
// 2. tools.Registry.mu
// 3. skill.Registry.mu
// Never acquire a higher-numbered lock while holding a lower-numbered one.
// handleListChanged releases any registry lock before calling injectSystemMessage.

// injectSystemMessage queues a system message for the next agent loop turn.
// The agent loop checks this queue at the start of each turn and prepends
// any pending messages to the conversation.
func (m *MCPManager) injectSystemMessage(msg string) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.pendingSystemMessages = append(m.pendingSystemMessages, msg)
}

// DrainSystemMessages returns and clears pending system messages.
// Called by the agent loop at the start of each turn.
func (m *MCPManager) DrainSystemMessages() []string {
    m.mu.Lock()
    defer m.mu.Unlock()
    msgs := m.pendingSystemMessages
    m.pendingSystemMessages = nil
    return msgs
}
```

The agent loop integration point is in the turn loop at `pkg/agent/loop.go`. Before constructing the next request, the loop calls `m.mcpManager.DrainSystemMessages()` and prepends any messages as system-level content.

### Logging

`logging/setLevel` — MCPManager sets log level on servers matching gi's verbosity.

Server log messages (`notifications/message`) forwarded to gi's logger with `[mcp:<server>]` prefix. MCP log levels mapped to gi's levels:

```go
var mcpToGiLogLevel = map[string]slog.Level{
    "debug":     slog.LevelDebug,
    "info":      slog.LevelInfo,
    "notice":    slog.LevelInfo,    // no direct equivalent; map to Info
    "warning":   slog.LevelWarn,
    "error":     slog.LevelError,
    "critical":  slog.LevelError,   // Go slog has no Critical; map to Error
    "alert":     slog.LevelError,
    "emergency": slog.LevelError,
}
```

## 8. Lifecycle

### Initialize Handshake with Version Negotiation and Instructions

```
Client                              Server
  |                                   |
  |-- initialize ------>              |
  |   protocolVersion: "2025-11-25"   |
  |   capabilities: {roots: {...},    |
  |     sampling: {}}                 |
  |   clientInfo: {name: "gi", ...}   |
  |                                   |
  |              <------ result ------|
  |   protocolVersion: "2025-11-25"   |
  |   capabilities: {tools: {...},    |
  |     resources: {...}, ...}        |
  |   serverInfo: {name: "...", ...}  |
  |   instructions: "..."            |
  |                                   |
  |-- notifications/initialized -->   |
  |                                   |
```

**Version Negotiation**: Client sends `protocolVersion: "2025-11-25"` (latest supported). Server responds with its supported version. If versions are incompatible:
- If server version is older but still in our supported set → proceed with server's version
- If server version is unknown/unsupported → log error, disconnect, skip this server
- Supported versions: `["2025-11-25", "2025-03-26", "2024-11-05"]`

**notifications/initialized**: After receiving the `initialize` response, the client MUST send a `notifications/initialized` notification (no params). This is a spec requirement — the server will not accept requests until it receives this notification.

**Server Instructions**: The `initialize` response may include an `instructions` string field. This provides human-readable text describing the server's purpose and how to use its tools. The MCPManager sanitizes and injects this into the agent's system prompt.

**Instruction sanitization** (defense against prompt injection from untrusted servers):

```go
func (m *MCPManager) ServerInstructions() string {
    var b strings.Builder
    for _, s := range m.servers {
        if s.instructions == "" {
            continue
        }
        if s.config.Instructions == "ignore" {
            continue
        }

        // Sanitize: length cap, tag stripping, sandbox wrapping
        instr := s.instructions
        if len(instr) > 2000 {
            instr = instr[:2000] + " [truncated]"
        }
        // Strip angle brackets to prevent closing system tags
        instr = strings.ReplaceAll(instr, "<", "&lt;")
        instr = strings.ReplaceAll(instr, ">", "&gt;")

        fmt.Fprintf(&b, "\n<mcp-server-instructions server=%q>\n%s\n</mcp-server-instructions>\n",
            s.Name, instr)
    }
    return b.String()
}
```

Users can disable instruction injection per server via config:
```json
{"mcpServers": {"untrusted": {"instructions": "ignore"}}}
```

### Full Startup Sequence

```
1. Parse config (global + project) with env var interpolation
2. For project-level MCP servers: check approval cache or prompt user
3. For each server (SEQUENTIAL per server, servers MAY be concurrent):
   a. Spawn process (stdio) or establish HTTP connection (Streamable HTTP)
   b. Send initialize request with:
      - protocolVersion: "2025-11-25"
      - capabilities: {roots: {listChanged: true}, sampling: {}} (if sampling enabled)
      - clientInfo: {name: "gi", version: gi.Version}
   c. Receive server capabilities + instructions
   d. Version negotiation check
   e. Send notifications/initialized
   f. For Streamable HTTP: store session ID and negotiated version
   g. tools/list (paginated) → filter taskSupport:"required" → register in tools.Registry
   h. resources/list (paginated) → cache index + register per-server resource tool
   i. resources/templates/list (paginated) → cache templates
   j. prompts/list (paginated) → register in skill.Registry
```

**Sequential initialization constraint.** For Streamable HTTP servers, the initialize handshake (steps b-f) MUST complete before any subsequent requests (steps g-j). This ensures the session ID and negotiated protocol version are set before `tools/list` etc. send requests that require these headers. The startup sequence is inherently sequential per-server (each step depends on the previous), so this is guaranteed by the existing flow. Multiple servers MAY be initialized concurrently (each server's sequence is independent).

### Health and Recovery

- Ping via MCP spec `ping` method (not custom heartbeat)
- Exponential backoff on failure (1s→30s, matching `plugin.RestartConfig` pattern)
- Auto-reconnect on crash (configurable max retries)

**Error during tool call**:
- Server process dies mid-call → return Go error wrapping `ErrServerCrashed`
- Agent sees structured error, can decide to retry or report
- MCPManager triggers auto-restart in background
- Pending tool calls from other goroutines get `ErrServerRestarting`

### Shutdown

- Send MCP `ping` as graceful check, then close transport
- Wait for graceful close (5s timeout, matching plugin shutdown at `pkg/plugin/process.go`)
- Kill process if unresponsive

**Hot reload: DEFERRED.** Phase 1 requires restart to pick up config changes.

## 9. Transport

### stdio (default, local servers)

- Spawn subprocess, communicate over stdin/stdout
- Reuse process management patterns from `pkg/plugin/process.go` (rlimit, timeout, restart)
- JSON-RPC 2.0 framing per MCP spec (newline-delimited)

### Streamable HTTP

Replaces the deprecated SSE transport per MCP spec revision 2025-11-25.

**How it works**: A single HTTP endpoint handles both directions:
- **Client → Server**: HTTP POST with JSON-RPC request body. Server responds with either a direct JSON-RPC response body, or opens an SSE stream on the response for streaming.
- **Server → Client**: Server may keep an SSE stream open on GET or POST responses for sending notifications and streaming results.
- **Session management**: Server may return `Mcp-Session-Id` header. Client includes it in subsequent requests for session affinity.

```go
// pkg/mcp/transport/streamable_http.go
type StreamableHTTP struct {
    endpoint string            // e.g., "https://mcp.example.com/mcp"
    headers  map[string]string // auth headers (interpolated)

    // Protected by mu for concurrent access from parallel tool calls.
    mu                sync.RWMutex
    sessionID         string // from Mcp-Session-Id response header
    negotiatedVersion string // from initialize handshake

    httpClient *http.Client

    // GET stream for server-initiated messages
    getStream     *sseConnection
    getStreamOnce sync.Once

    // SSE event ID tracking for reconnection
    lastEventIDs  map[string]string // stream → last event ID
    lastEventMu   sync.Mutex

    // Unified channel for all incoming messages
    incoming chan json.RawMessage
}

func (t *StreamableHTTP) Send(ctx context.Context, msg json.RawMessage) error {
    // Validate that msg is a single JSON-RPC object, not a batch array.
    // The 2025-11-25 spec forbids JSON-RPC batching in POST request bodies.
    if len(msg) > 0 && msg[0] == '[' {
        return fmt.Errorf("JSON-RPC batch requests are not allowed per MCP spec; send individual messages")
    }

    req, _ := http.NewRequestWithContext(ctx, "POST", t.endpoint, bytes.NewReader(msg))
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Accept", "application/json, text/event-stream")

    // Read negotiatedVersion and sessionID under RLock.
    t.mu.RLock()
    negotiatedVersion := t.negotiatedVersion
    sessionID := t.sessionID
    t.mu.RUnlock()

    // MCP-Protocol-Version header is REQUIRED on all HTTP requests after initialization.
    if negotiatedVersion != "" {
        req.Header.Set("MCP-Protocol-Version", negotiatedVersion)
    }

    // Mcp-Session-Id per 2025-11-25 spec canonical casing
    if sessionID != "" {
        req.Header.Set("Mcp-Session-Id", sessionID)
    }

    for k, v := range t.headers {
        req.Header.Set(k, v)
    }

    resp, err := t.httpClient.Do(req)
    if err != nil {
        return err
    }

    // Content-Type dispatch: handle both response formats
    switch resp.Header.Get("Content-Type") {
    case "text/event-stream":
        go t.parseSSEStream(resp.Body, t.incoming)
    case "application/json":
        // Read body, parse as single JSON-RPC response, send to incoming channel
        body, err := io.ReadAll(resp.Body)
        resp.Body.Close()
        if err != nil {
            return fmt.Errorf("reading JSON response: %w", err)
        }
        t.incoming <- body
    default:
        // Log warning, attempt JSON parse as fallback
        body, _ := io.ReadAll(resp.Body)
        resp.Body.Close()
        if len(body) > 0 {
            log.Warn("Unexpected Content-Type from MCP server, attempting JSON parse",
                "content_type", resp.Header.Get("Content-Type"))
            t.incoming <- body
        }
    }

    // Store Mcp-Session-Id from response header under write lock.
    if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
        if err := validateSessionID(sid); err != nil {
            log.Warn("Invalid session ID from MCP server", "error", err)
        } else {
            t.mu.Lock()
            t.sessionID = sid
            t.mu.Unlock()
        }
    }
    return nil
}

// Connect establishes the transport connection. For StreamableHTTP, this is
// a no-op (connections are made on demand via Send). The method exists to
// satisfy the Transport interface and allow transports that need explicit
// initialization (like stdio process spawning).
func (t *StreamableHTTP) Connect(ctx context.Context) error {
    return nil
}

// SetNegotiatedVersion stores the protocol version from the initialize handshake.
// Called by MCPServer after processing the initialize response.
// Write-locked to prevent races with concurrent Send() calls.
func (t *StreamableHTTP) SetNegotiatedVersion(version string) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.negotiatedVersion = version
}

// OpenServerStream issues a GET to receive server-initiated messages.
// Call once after initialization; reconnects automatically on disconnect.
func (t *StreamableHTTP) OpenServerStream(ctx context.Context) error {
    var err error
    t.getStreamOnce.Do(func() {
        req, _ := http.NewRequestWithContext(ctx, "GET", t.endpoint, nil)
        req.Header.Set("Accept", "text/event-stream")

        t.mu.RLock()
        if t.sessionID != "" {
            req.Header.Set("Mcp-Session-Id", t.sessionID)
        }
        if t.negotiatedVersion != "" {
            req.Header.Set("MCP-Protocol-Version", t.negotiatedVersion)
        }
        t.mu.RUnlock()

        for k, v := range t.headers {
            req.Header.Set(k, v)
        }

        // Track last event ID for reconnection
        t.lastEventMu.Lock()
        if lastID, ok := t.lastEventIDs["get"]; ok {
            req.Header.Set("Last-Event-ID", lastID)
        }
        t.lastEventMu.Unlock()

        var resp *http.Response
        resp, err = t.httpClient.Do(req)
        if err != nil {
            return
        }
        if resp.StatusCode != http.StatusOK {
            resp.Body.Close()
            err = fmt.Errorf("GET stream returned HTTP %d", resp.StatusCode)
            return
        }
        go t.parseSSEStream(resp.Body, t.incoming)
    })
    return err
}

func (t *StreamableHTTP) Receive() <-chan json.RawMessage {
    // Returns unified channel for all incoming messages
    // (from POST response streams and GET streams).
    //
    // Note on batch responses: The 2025-11-25 spec does NOT define batch
    // support — JSONRPCMessage is a union of three single-message types,
    // not including arrays. The defensive demuxing below handles the case
    // where a server sends a JSON array anyway (robustness principle), but
    // compliant servers will NOT send batch responses.
    return t.incoming
}

func (t *StreamableHTTP) parseSSEStream(body io.ReadCloser, ch chan<- json.RawMessage) {
    defer body.Close()
    scanner := bufio.NewScanner(body)
    var data strings.Builder
    var eventID string

    for scanner.Scan() {
        line := scanner.Text()
        switch {
        case strings.HasPrefix(line, "data: "):
            data.WriteString(strings.TrimPrefix(line, "data: "))
        case strings.HasPrefix(line, "id: "):
            eventID = strings.TrimPrefix(line, "id: ")
        case line == "":
            if data.Len() > 0 {
                msg := json.RawMessage(data.String())
                // Track event ID for reconnection
                if eventID != "" {
                    t.lastEventMu.Lock()
                    t.lastEventIDs["current"] = eventID
                    t.lastEventMu.Unlock()
                }
                // Defensive: demux batch arrays
                if len(msg) > 0 && msg[0] == '[' {
                    var batch []json.RawMessage
                    if json.Unmarshal(msg, &batch) == nil {
                        for _, m := range batch {
                            ch <- m
                        }
                    } else {
                        ch <- msg
                    }
                } else {
                    ch <- msg
                }
                data.Reset()
                eventID = ""
            }
        }
    }
}

// HTTP DELETE for session termination
func (t *StreamableHTTP) Close() error {
    t.mu.RLock()
    sessionID := t.sessionID
    negotiatedVersion := t.negotiatedVersion
    t.mu.RUnlock()

    if sessionID == "" {
        return nil // no session to terminate
    }
    req, err := http.NewRequestWithContext(context.Background(), "DELETE", t.endpoint, nil)
    if err != nil {
        return err
    }
    req.Header.Set("Mcp-Session-Id", sessionID)
    if negotiatedVersion != "" {
        req.Header.Set("MCP-Protocol-Version", negotiatedVersion)
    }
    for k, v := range t.headers {
        req.Header.Set(k, v)
    }
    resp, err := t.httpClient.Do(req)
    if err != nil {
        // Best-effort: log but don't fail shutdown
        return fmt.Errorf("session termination DELETE failed: %w", err)
    }
    resp.Body.Close()
    // Server may respond 405 Method Not Allowed (disallowing client-initiated
    // termination). This is a valid response, not an error.
    if resp.StatusCode == http.StatusMethodNotAllowed {
        return nil
    }
    // 200/202/204 are all acceptable responses
    if resp.StatusCode >= 400 {
        return fmt.Errorf("session termination returned HTTP %d", resp.StatusCode)
    }
    return nil
}

// validateSessionID checks that a session ID contains only visible ASCII
// characters (0x21-0x7E) per the MCP spec.
func validateSessionID(id string) error {
    for _, c := range id {
        if c < 0x21 || c > 0x7E {
            return fmt.Errorf("invalid session ID character: %U", c)
        }
    }
    if len(id) > 1024 { // reasonable max length
        return fmt.Errorf("session ID too long: %d bytes", len(id))
    }
    return nil
}
```

### Deprecated SSE Transport Backwards Compatibility

Phase 1 only implements Streamable HTTP. Older MCP servers that only support the deprecated SSE transport (separate POST endpoint + SSE GET endpoint) will be unreachable. This is a known limitation. If a Streamable HTTP POST gets 404/405, a warning is logged suggesting the server may use the deprecated SSE transport. Full fallback support is deferred to Phase 2.

### Transport Interface

```go
// pkg/mcp/transport/transport.go
type Transport interface {
    Connect(ctx context.Context) error
    Send(ctx context.Context, msg json.RawMessage) error
    Receive() <-chan json.RawMessage
    Close() error
}
```

Both `Stdio` and `StreamableHTTP` implement this interface. `Connect()` handles transport-specific initialization: `Stdio.Connect()` spawns the subprocess, `StreamableHTTP.Connect()` is a no-op (connections are made on demand).

**Note**: For stdio transport, `MCP-Protocol-Version` is not applicable (HTTP-only concept). The `SetNegotiatedVersion` method is only called on `StreamableHTTP` instances.

**Session ID race prevention.** The sequential startup sequence (§8) ensures that the initialize handshake completes (setting sessionID and negotiatedVersion) before any subsequent requests. This prevents the race where a pipelined request (e.g., `tools/list` sent before `initialize` response received) would lack these headers. The `sync.RWMutex` handles the post-initialization case where concurrent tool calls from parallel execution all need to read these fields safely. The two mechanisms are complementary: sequential init prevents the first-request race; the mutex prevents the concurrent-access race.

**Note on SSE stream disconnection semantics**: Disconnection of an SSE stream is NOT implicit cancellation. If the stream drops, the client MUST send explicit `notifications/cancelled` for any in-flight requests if cancellation is intended.

## 10. Security and Permissions

### Permission Gap

**Current state**: gi has no formal permission system. All tools execute without approval. The existing `tools.Hook` system (in `pkg/tools/rtk_hooks.go`) can intercept tool execution but is used for compression/translation, not access control. The `plugin.Manifest` has no permission fields. The agent loop at `pkg/agent/loop.go:executeTool` calls tools unconditionally.

**Gap**: MCP servers are third-party code. Unlike built-in tools (authored by gi developers) or plugins (authored by the user), MCP servers may come from untrusted sources. Running their tools without any permission check is a security gap.

### Proposed Minimal Permission Layer

Rather than building a full permission system, add a thin MCP-specific permission config that integrates with the existing hook system:

```go
// pkg/mcp/permission.go
type MCPPermissionConfig struct {
    // AutoApprove lists tool names (without namespace prefix) that execute without
    // user confirmation. Default: empty (all tools require confirmation).
    AutoApprove []string `json:"auto_approve,omitempty"`

    // Deny lists tool names that are blocked entirely. Takes precedence over AutoApprove.
    Deny []string `json:"deny,omitempty"`
}
```

**Implementation**: Register a `tools.Hook` that intercepts MCP tool execution:

```go
type MCPPermissionHook struct {
    configs map[string]*MCPPermissionConfig // server name → config
    confirm func(serverName, toolName, desc string) (bool, error) // UI callback
}

func (h *MCPPermissionHook) BeforeExecute(ctx context.Context, toolName string, params map[string]any) error {
    server, tool := parseMCPToolName(toolName) // split "mcp__server__tool"
    if server == "" {
        return nil // not an MCP tool, skip
    }

    cfg := h.configs[server]
    if cfg != nil && slices.Contains(cfg.Deny, tool) {
        return fmt.Errorf("MCP tool %q on server %q is denied by configuration", tool, server)
    }
    if cfg != nil && slices.Contains(cfg.AutoApprove, tool) {
        return nil // auto-approved
    }

    // Tool annotation defaults inform the permission decision.
    // Per 2025-11-25 spec defaults:
    //   readOnlyHint:    false  (assume tool may modify state)
    //   destructiveHint: true   (assume tool may be destructive)
    //   idempotentHint:  false  (assume tool is not idempotent)
    //   openWorldHint:   true   (assume tool interacts with external systems)
    //
    // Consequence: ALL tools without explicit annotations default to
    // destructiveHint=true, which means they always require confirmation.
    // This is intentional — security-conservative. Only tools that explicitly
    // set readOnlyHint=true bypass the confirmation prompt.
    annotations := h.getAnnotations(server, tool)
    if annotationReadOnly(annotations) {
        return nil // explicitly marked safe
    }

    // Default: require user confirmation via TUI
    approved, err := h.confirm(server, tool, "MCP tool execution")
    if err != nil || !approved {
        return fmt.Errorf("user denied MCP tool %q on server %q", tool, server)
    }
    return nil
}
```

**Config example**:
```json
{
  "mcpServers": {
    "filesystem": {
      "command": "...",
      "permissions": {
        "auto_approve": ["read_file", "list_directory", "read_resource"],
        "deny": ["delete_file"]
      }
    }
  }
}
```

The per-server resource tool `mcp__<server>__read_resource` routes through this permission hook naturally. Resource access is subject to the same permission config as other MCP tools. The `read_resource` tool name can appear in `auto_approve` for servers whose resources are safe to read without confirmation.

**Phase 1 scope**: Only MCP tools go through permission checks. Built-in and plugin tools are unaffected. A broader permission system is out of scope.

### Threat Model

| Threat | Mitigation | Status |
|--------|-----------|--------|
| Malicious MCP server executes destructive tools | Permission hook, annotation defaults conservative | Mitigated |
| Server returns prompt injection in instructions | Length cap, tag stripping, sandbox wrapping, per-server disable | Mitigated |
| Project config exfiltrates env vars | Project-level interpolation restricted, user approval required | Mitigated |
| Server exhausts API credits via sampling | MaxTokens cap, sampling approval callback, disabled in non-interactive | Mitigated |
| Resource contains sensitive data | Per-server resource tool + permission hook | Mitigated |
| Server returns infinite pagination | Pagination safety limits (100 pages / 10K items) | Mitigated |
| Server sends malformed session ID | Session ID format validation | Mitigated |
| Concurrent tool calls race on transport state | sync.RWMutex on transport fields | Mitigated |
| Server dynamically adds/removes tools mid-session | ReplaceByPrefix + LLM notification | Mitigated |

### Positive Security Patterns

1. **Deny takes precedence over AutoApprove** — correct priority ordering
2. **Conservative annotation defaults** — destructiveHint=true by default
3. **Sampling disabled by default** — opt-in per server with approval
4. **Process isolation** for stdio transport — matches plugin sandbox
5. **Network isolation** — only configured URLs, no auto-discovery
6. **Trust boundary marking** — namespaced tool names identify MCP origin

### Other Security Measures

**Process isolation** (stdio): Same sandbox as plugins. Env vars explicitly allowlisted per `MCPServerConfig.Env`.

**Network isolation** (Streamable HTTP): Only configured URLs. No auto-discovery.

**Trust boundaries**: MCP servers untrusted by default. Tool results marked with source (via namespaced tool name). Resource contents treated as untrusted input.

## 11. Edge Cases

### Unknown Notification Types

If the server sends a notification with an unrecognized method, log it at debug level and discard. Do not error or disconnect. This enables forward-compatibility with future spec extensions.

### Unsupported Server Capabilities

If the server advertises capabilities the client doesn't support (e.g., a future `experimental` capability), ignore them. Only act on recognized capabilities: `tools`, `resources`, `prompts`, `logging`.

### Tool Call Timeout

If a tool call exceeds the configured timeout (default: 30s, matching `plugin.TimeoutConfig`):
1. Send `notifications/cancelled` with the request ID
2. Return a timeout error as content (not Go error) so the LLM can decide whether to retry
3. Do NOT kill the server process — the server may still be processing

### Large Resource Content

Resources can be arbitrarily large. Mitigations:
- `resources/read` response capped at 1MB. If larger, truncate and append `[truncated: resource exceeded 1MB limit]`
- Resource content is NOT cached in memory indefinitely — evict after 5 minutes or when subscription update arrives
- Binary resources (non-text mimeType) are base64-encoded and limited to 512KB

### Unicode and Binary Content

- Text content: validate UTF-8, replace invalid sequences with U+FFFD
- Image content: accept `image/png`, `image/jpeg`, `image/gif`, `image/webp`; reject other types with warning
- Audio content: rendered as metadata annotation text (native playback deferred)
- Other binary: reject with error content "unsupported content type: <type>"

### Malformed JSON-RPC from Server

If the server sends malformed JSON-RPC (invalid JSON, missing required fields):
- Log the raw message at error level
- If it has an `id` field, respond with JSON-RPC error -32700 (Parse error)
- If it's a notification (no id), discard
- After 5 consecutive malformed messages, disconnect and attempt restart

### Concurrent Tool Calls to Same Server

The agent loop may call multiple tools concurrently (parallel tool use). The MCPServer must handle concurrent requests:
- JSON-RPC 2.0 naturally supports this via request IDs
- Each request gets a unique ID, responses are correlated by ID
- The transport layer must be thread-safe (`sync.Mutex` on writes, demux on reads)
- The server may process requests concurrently or sequentially — that's its decision

### Server Restart During Active Subscriptions

When a server crashes and auto-restarts:
1. Re-run the full initialize handshake (including `notifications/initialized`)
2. Re-discover tools, resources, prompts (they may have changed)
3. Re-subscribe to all previously subscribed resources
4. Notify the agent via injected system message: `[MCP server <name> restarted — tools/resources may have changed]`

### MCP-Specific Error Code Constants

Define standard JSON-RPC and MCP error codes as Go constants for consistent error handling:

```go
// pkg/mcp/protocol.go

// Standard JSON-RPC 2.0 error codes
const (
    ErrCodeParseError     = -32700 // Invalid JSON
    ErrCodeInvalidRequest = -32600 // Not a valid JSON-RPC request
    ErrCodeMethodNotFound = -32601 // Method does not exist
    ErrCodeInvalidParams  = -32602 // Invalid method parameters
    ErrCodeInternalError  = -32603 // Internal JSON-RPC error
)

// MCP / implementation-defined error codes
// The JSON-RPC spec reserves -32000 to -32099 for implementation-defined server errors.
// MCP servers may return codes in this range for server-specific failures.
const (
    ErrCodeServerMin = -32099 // Start of implementation-defined range
    ErrCodeServerMax = -32000 // End of implementation-defined range
)

// JSONRPCError represents a JSON-RPC 2.0 error response
type JSONRPCError struct {
    Code    int             `json:"code"`
    Message string          `json:"message"`
    Data    json.RawMessage `json:"data,omitempty"`
}

func (e *JSONRPCError) Error() string {
    return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// IsServerError returns true if the error code is in the implementation-defined
// range (-32000 to -32099). These are MCP server-specific errors.
func (e *JSONRPCError) IsServerError() bool {
    return e.Code >= ErrCodeServerMin && e.Code <= ErrCodeServerMax
}

// Mapping: JSON-RPC errors → Go error handling
// -32700 (Parse error)       → log + retry if transient, disconnect if persistent
// -32600 (Invalid request)   → log + return as tool error content (client bug)
// -32601 (Method not found)  → return as tool error content (server doesn't support method)
// -32602 (Invalid params)    → return as tool error content (schema mismatch)
// -32603 (Internal error)    → return as tool error content (server-side failure)
// -32099 to -32000 (Server)  → return as tool error content with code in message
//
// Implementation-defined errors are treated as tool-level failures.
// They are returned as content blocks (like isError=true) so the LLM can
// reason about the failure. The error code is included in the message for
// debugging: "[MCP error -32050] <server message>".
// These are NOT treated as transport failures — the server is still healthy.
```

### Request ID Generation

Outgoing request IDs are generated using a monotonically increasing counter, ensuring uniqueness within a session:

```go
// pkg/mcp/protocol.go
type MCPClient struct {
    transport transport.Transport
    nextID    atomic.Int64 // monotonically increasing request IDs
    // ... other fields
}

func (c *MCPClient) newRequestID() int64 {
    return c.nextID.Add(1)
}
```

### Request Parameter Handling

Protocol message parsing uses `json.RawMessage` for params, ensuring that unknown fields (such as `_meta`) are preserved rather than silently dropped:

```go
type JSONRPCRequest struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      json.RawMessage `json:"id,omitempty"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"` // preserves all fields including _meta
}
```

## 12. Deferred Features (2025-11-25)

The following features are part of the MCP 2025-11-25 spec but are explicitly deferred to Phase 2 or later. This section acknowledges their existence to prevent implementers from assuming the design provides complete 2025-11-25 coverage.

### Tasks, Elicitation, Tool Output Schemas

**Tasks** (`tasks/list`, `tasks/cancel`, task-augmented requests): The 2025-11-25 spec adds a task system for durable, long-running work. Tasks have lifecycle states and can be tracked across multiple requests. Deferred because gi's agent loop currently assumes synchronous tool execution; supporting async tasks requires significant loop changes.

**Elicitation** (`elicitation/create`): Allows MCP servers to request structured user input during tool execution. Deferred because it requires TUI integration for the confirmation dialog. When implemented, the permission hook (§10) is the natural integration point — elicitation requests would use the same `confirm` callback pattern.

**Tool Output Schemas** (`outputSchema` field on Tool): Allows tools to declare the schema of their output for structured validation. Deferred because gi currently treats all tool output as unstructured text/content blocks. When implemented, this enables type-safe tool chaining.

**structuredContent**: `CallToolResult` may include a `structuredContent` field alongside `content`. Phase 1 ignores `structuredContent` and uses `content` only. When output schemas are supported, `structuredContent` enables machine-parseable results validated against the tool's `outputSchema`. Servers MUST still include `content` for backwards compatibility, so Phase 1 behavior is correct.

### Icons

The 2025-11-25 spec adds `icons` arrays to tools, resources, resource templates, and prompts. Icons provide visual metadata for UI rendering. Deferred because gi's TUI does not currently render icons. The structs can be extended to include `Icons []Icon` fields when needed.

### Sampling Enhancements

The 2025-11-25 spec adds to `sampling/createMessage`:
- `tools`: Tool definitions the sampled model can use (tool-use in sampling)
- `toolChoice`: Constraint on tool selection (auto, any, specific)
- `metadata`: Opaque passthrough metadata

These require extending the sampling handler to support nested tool calling, which is non-trivial. The `sampling.tools` sub-capability must be negotiated. Deferred to Phase 2.

### OAuth/OIDC Authentication

The 2025-11-25 spec includes detailed OAuth 2.1 and OIDC Discovery flows for Streamable HTTP authentication. The current design only supports static `Authorization` headers via config. For production remote MCP servers, dynamic auth with token refresh is important. Deferred because it requires implementing the full OAuth 2.1 client flow including PKCE, token storage, and refresh.

### Progress Notifications

`notifications/progress` reports progress on long-running requests via `progressToken`, `progress`, and `total` fields. Phase 1 logs these at info level. Full support (progress bars, ETA display) deferred to Phase 2. The `_meta.progressToken` field on outgoing requests is also deferred — without it, servers won't send progress notifications for our requests.

### Request Metadata (`_meta`)

The MCP spec allows a `_meta` object in request params for progress tokens and other metadata. Phase 1 does not attach `_meta` to outgoing requests. When progress support is implemented, `_meta.progressToken` will be included on tool calls and resource reads. Incoming `_meta` fields are preserved via `json.RawMessage` params parsing.

### Completions

`completions/complete` provides argument auto-completion for resources and prompts. Not critical for agent use (agents don't need tab-complete), but useful for interactive CLI scenarios. Deferred to Phase 2.

### Deprecated SSE Transport Fallback

Older MCP servers that only support the deprecated SSE transport (separate POST + SSE GET endpoints) are not reachable in Phase 1. If needed, Phase 2 can implement the spec's recommended fallback: try Streamable HTTP POST first, if 400/404/405, fall back to legacy SSE connection pattern.

### Hot Reload

Config changes require restart. Hot reload (detecting config changes and reconnecting/disconnecting servers without restart) deferred to Phase 2.

### ToolDef Title Field

`ai.ToolDef` does not include a Title field. The Title from MCPTool is available on the Go object but does not flow to the LLM via ToolDef serialization. This is acceptable for Phase 1 (LLMs use Name + Description, not Title). When gi's TUI adds richer tool display, extend ToolDef with `Title string`. See §3 for details.

## Implementation Plan (6 phases)

| Phase | Scope | Key Files |
|-------|-------|-----------|
| 1 | Transport + Protocol | `pkg/mcp/transport/` (stdio, streamable-http with session management, Content-Type dispatch, SSE streams, session ID validation), `pkg/mcp/protocol.go` (JSON-RPC client, error codes, request ID generation, initialize with version negotiation and instructions) |
| 2 | Tool Bridge + Registry | `pkg/mcp/tool_bridge.go` (RichTool impl, isError via RichToolError, annotations, title, audio/default content handling, taskSupport filtering), `pkg/tools/tool.go` (RichToolError type, Registry sync.RWMutex + Unregister + ReplaceByPrefix + AllWithPrefix), pagination with safety limits |
| 3 | MCPManager + Config | `pkg/mcp/manager.go` (lifecycle, config loading with env interpolation and project-level restrictions, list_changed notification + LLM injection, instruction sanitization), `pkg/config/config.go` (MCPServerConfig) |
| 4 | Resources + Templates | `pkg/mcp/resource.go` (list, templates, subscribe/unsubscribe, pagination, per-server resource tool) |
| 5 | Prompts + Sampling + Notifications | `pkg/mcp/prompt.go` (prompt→skill, required args), `pkg/skill/skill.go` (Registry sync.RWMutex + ReplaceByPrefix + AllWithPrefix), sampling handler with approval callback, notifications, `pkg/mcp/permission.go` |
| 6 | Integration + Tests | Wire into `cmd/gi/main.go`, integration tests, edge case handling, docs, hook interaction documentation |

DEPENDS ON: gi core tool registry (`pkg/tools/tool.go`), skill registry (`pkg/skill/skill.go`), plugin patterns (`pkg/plugin/`), config system (`pkg/config/config.go`)
