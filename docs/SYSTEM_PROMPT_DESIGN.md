# System Prompt Routing: Pi Default with OAuth Override

## Problem

The default system prompt is hardcoded to `"You are Claude Code, Anthropic's official CLI for Claude."` This should be the pi identity prompt instead. However, when a user authenticates via OAuth, the Anthropic API expects a specific system prompt format (the Claude Code identity), and we must preserve that.

## Current Architecture

```
buildSystemPrompt()                    # cmd/gi/main.go:405
├── base = "You are Claude Code..."    # hardcoded
├── Walk .claude/SYSTEM.md, CLAUDE.md, AGENTS.md, APPEND_SYSTEM.md
└── return base + collected files

makeAgentLoop(provider, registry, cfg, skillReg)   # cmd/gi/main.go:515
├── systemPrompt = buildSystemPrompt()
├── append skill system reminder
└── agent.WithSystemPrompt(systemPrompt)

AnthropicProvider.Stream()             # pkg/ai/anthropic.go:75
├── if useBearer && no prompt → fallback "You are Claude..."
└── if useBearer → rewrite system block array to string
```

Key observations:
- `buildSystemPrompt()` has no awareness of auth method.
- `makeAgentLoop()` has no awareness of auth method.
- The OAuth fallback in `anthropic.go:77-79` is a safety net, not the intended routing point.
- `resolveProvider()` knows whether we're OAuth but discards that info after creating the provider.

## Design

### Approach: Auth-Aware Prompt Selection at `makeAgentLoop`

The simplest change with the smallest blast radius. No new abstractions, no interface changes.

#### 1. `resolveProvider` returns OAuth state alongside the provider

```go
// cmd/gi/main.go

type providerResult struct {
    provider ai.Provider
    isOAuth  bool
}

func resolveProvider(ctx context.Context, cfg *config.Config, resolver *auth.Resolver) (providerResult, error) {
    // ... existing resolution logic ...
    switch providerName {
    case "anthropic":
        isOAuth := resolver.IsOAuthToken(providerName)
        if isOAuth {
            p, err := ai.NewAnthropicProviderWithToken(key)
            return providerResult{p, true}, err
        }
        p, err := ai.NewAnthropicProvider(key)
        return providerResult{p, false}, err
    // ... other providers unchanged, isOAuth always false ...
    }
}
```

#### 2. `buildSystemPrompt` takes the base prompt as a parameter

```go
const (
    piBasePrompt        = "You are Pi, a personal AI assistant. ..."  // the pi identity
    claudeCodeBasePrompt = "You are Claude Code, Anthropic's official CLI for Claude."
)

func buildSystemPrompt(base string) string {
    // ... rest of function unchanged, just uses `base` instead of hardcoded string ...
}
```

#### 3. `makeAgentLoop` picks the base prompt based on OAuth state

```go
func makeAgentLoop(provider ai.Provider, registry *tools.Registry, cfg *config.Config, skillReg *skill.Registry, isOAuth bool) *agent.AgentLoop {
    base := piBasePrompt
    if isOAuth {
        base = claudeCodeBasePrompt
    }
    systemPrompt := buildSystemPrompt(base)
    // ... rest unchanged ...
}
```

#### 4. Remove the OAuth fallback from `anthropic.go`

The fallback at `anthropic.go:76-79` becomes dead code since the caller always provides a prompt. Remove it to avoid confusion, or keep it as a safety net with a log warning — either way it should never fire.

### Call Sites

Every call to `makeAgentLoop` and `buildSystemPrompt` needs updating:

| Location | Current | After |
|----------|---------|-------|
| `main.go:218` (print mode) | `makeAgentLoop(provider, ...)` | `makeAgentLoop(provider, ..., result.isOAuth)` |
| `main.go:236` (JSON mode) | `makeAgentLoop(provider, ...)` | `makeAgentLoop(provider, ..., result.isOAuth)` |
| `main.go:246` (RPC mode) | `makeAgentLoop(provider, ...)` | `makeAgentLoop(provider, ..., result.isOAuth)` |
| `main.go:254` (interactive) | `makeAgentLoop(provider, ...)` | `makeAgentLoop(provider, ..., result.isOAuth)` |
| `main.go:257` (placeholder) | `buildSystemPrompt()` | `buildSystemPrompt(piBasePrompt)` |

### SDK Path

`pkg/sdk/sdk.go` has its own `SessionConfig.SystemPrompt` field. SDK users set their own prompt explicitly, so no change needed — it bypasses `buildSystemPrompt()` entirely. The SDK default (empty string → minimal prompt) is appropriate since SDK consumers control their own identity.

### What This Does NOT Change

- **Directory-walk prompt collection** (CLAUDE.md, AGENTS.md, etc.) — unchanged, appended to whichever base is selected.
- **Skill system reminders** — unchanged, appended after directory walk.
- **Other providers** (OpenRouter, OpenAI, Gemini, Azure) — always get pi base (they don't have OAuth paths).
- **`anthropic.go` request format** — OAuth still converts system block to string, API key still sends block array. No change.
- **SDK** — callers set their own prompt. No change.
- **`auth.Resolver`** — unchanged.

## File Changes Summary

| File | Change |
|------|--------|
| `cmd/gi/main.go` | `providerResult` struct, `resolveProvider` returns it, `buildSystemPrompt(base)` takes param, `makeAgentLoop` takes `isOAuth`, define prompt constants |
| `pkg/ai/anthropic.go` | Remove or guard the fallback at line 76-79 |
| `cmd/gi/context_test.go` | Update `buildSystemPrompt` calls to pass base param |

## Risks

- **Pi prompt content**: The actual pi base prompt text needs to be defined. This design just creates the routing — the prompt content is a separate decision.
- **OAuth prompt drift**: If the upstream Claude Code prompt changes, our `claudeCodeBasePrompt` constant needs updating. Consider reading it from a file (`.claude/OAUTH_SYSTEM.md`) rather than hardcoding, to make updates easier without recompilation.
- **Session restore**: Restored sessions use whatever prompt was set at creation time. Switching auth methods mid-session won't change the prompt retroactively. This is correct behavior.

## Alternative Considered: Provider Interface Method

Add `IsOAuth() bool` to the `ai.Provider` interface so the prompt can be selected deeper in the stack. Rejected because:
- Forces every provider implementation to add a method.
- Mixes auth concerns into the provider abstraction.
- The information is already available at the call site — just thread it through.
