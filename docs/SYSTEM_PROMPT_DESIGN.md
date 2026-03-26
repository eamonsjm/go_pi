# System Prompt Routing: Pi Default with OAuth Override

## Problem

The default system prompt is hardcoded to `"You are Claude Code, Anthropic's official CLI for Claude."` This should be the pi identity prompt instead. However, when a user authenticates via OAuth (Anthropic or OpenAI), the upstream API may expect a specific system prompt identity, and we must preserve that.

## Current Architecture

```
buildSystemPrompt()                    # cmd/gi/main.go:459
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

### OAuth Paths

**Two providers have registered OAuth flows:**

| Provider | OAuth Registration | resolveProvider Handling | Notes |
|----------|-------------------|------------------------|-------|
| Anthropic | `auth.NewAnthropicOAuth()` at `main.go:371` | Checks `IsOAuthToken` → `NewAnthropicProviderWithToken` (Bearer + beta headers) | Has explicit OAuth branch |
| OpenAI | `auth.NewOpenAIOAuth()` at `main.go:372` | **No `IsOAuthToken` check** — always calls `NewOpenAIProvider(key)` | Token flows through since OpenAI API is always Bearer anyway |

Key observations:
- `buildSystemPrompt()` has no awareness of auth method.
- `makeAgentLoop()` has no awareness of auth method.
- The OAuth fallback in `anthropic.go:77-79` is a safety net, not the intended routing point.
- `resolveProvider()` knows whether we're OAuth but discards that info after creating the provider.
- OpenAI OAuth tokens work silently because `NewOpenAIProvider` always uses `Authorization: Bearer` regardless — but the prompt routing doesn't know it's an OAuth session.

## Design

### Approach: Provider-Aware Prompt Selection at `makeAgentLoop`

Instead of a simple `isOAuth` boolean, use a per-provider prompt map so each OAuth path gets its own expected base prompt. This handles the fact that Anthropic OAuth and OpenAI OAuth may need different identity prompts.

#### 1. Prompt files (data, not code)

Base prompts live as plain markdown files in `cmd/gi/prompts/`, embedded at compile time via `go:embed`:

```
cmd/gi/prompts/
├── default.md            # Pi identity — used for all non-OAuth paths
├── oauth_anthropic.md    # Claude Code identity — Anthropic OAuth
└── oauth_openai.md       # Claude Code identity — OpenAI/Codex OAuth
```

```go
// cmd/gi/main.go

import "embed"

//go:embed prompts/default.md
var defaultBasePrompt string

//go:embed prompts/oauth_anthropic.md
var oauthAnthropicPrompt string

//go:embed prompts/oauth_openai.md
var oauthOpenAIPrompt string

// oauthBasePrompts maps provider names to the system prompt their OAuth
// path expects. Providers not in this map use defaultBasePrompt.
var oauthBasePrompts = map[string]*string{
    "anthropic": &oauthAnthropicPrompt,
    "openai":    &oauthOpenAIPrompt,
}
```

**Why `go:embed`?** The prompts are baked into the binary at build time — no runtime filesystem dependency, no risk of missing files in deployment. But editing them is just editing a `.md` file, no Go code changes needed.

**Why not runtime file reads?** The directory-walk prompt collection (CLAUDE.md, AGENTS.md, etc.) already handles user-customizable runtime prompts. These base identity prompts are build-time decisions — they define what the product *is*, not how a user configures it.

#### 2. `resolveProvider` returns provider name and OAuth state

```go
type providerResult struct {
    provider     ai.Provider
    providerName string
    isOAuth      bool
}

func resolveProvider(ctx context.Context, cfg *config.Config, resolver *auth.Resolver) (providerResult, error) {
    // ... existing resolution logic ...

    switch providerName {
    case "anthropic":
        isOAuth := resolver.IsOAuthToken(providerName)
        if isOAuth {
            p, err := ai.NewAnthropicProviderWithToken(key)
            return providerResult{p, "anthropic", true}, err
        }
        p, err := ai.NewAnthropicProvider(key)
        return providerResult{p, "anthropic", false}, err

    case "openai":
        isOAuth := resolver.IsOAuthToken(providerName)
        p, err := ai.NewOpenAIProvider(key)
        return providerResult{p, "openai", isOAuth}, err

    // ... other providers: isOAuth always false ...
    }
}
```

#### 3. `buildSystemPrompt` takes the base prompt as a parameter

```go
func buildSystemPrompt(base string) string {
    // ... rest of function unchanged, just uses `base` instead of hardcoded string ...
}
```

#### 4. `makeAgentLoop` selects the base prompt

```go
func selectBasePrompt(providerName string, isOAuth bool) string {
    if isOAuth {
        if prompt, ok := oauthBasePrompts[providerName]; ok {
            return strings.TrimSpace(*prompt)
        }
    }
    return strings.TrimSpace(defaultBasePrompt)
}

func makeAgentLoop(provider ai.Provider, registry *tools.Registry, cfg *config.Config, skillReg *skill.Registry, result providerResult) *agent.AgentLoop {
    base := selectBasePrompt(result.providerName, result.isOAuth)
    systemPrompt := buildSystemPrompt(base)
    // ... rest unchanged ...
}
```

#### 5. Remove the OAuth fallback from `anthropic.go`

The fallback at `anthropic.go:76-79` becomes dead code since the caller always provides a prompt. Remove it to avoid confusion, or keep it as a safety net with a log warning — either way it should never fire.

### Call Sites

Every call to `makeAgentLoop` and `buildSystemPrompt` needs updating:

| Location | Current | After |
|----------|---------|-------|
| `main.go:218` (print mode) | `makeAgentLoop(provider, ...)` | `makeAgentLoop(provider, ..., result)` |
| `main.go:236` (JSON mode) | `makeAgentLoop(provider, ...)` | `makeAgentLoop(provider, ..., result)` |
| `main.go:246` (RPC mode) | `makeAgentLoop(provider, ...)` | `makeAgentLoop(provider, ..., result)` |
| `main.go:254` (interactive) | `makeAgentLoop(provider, ...)` | `makeAgentLoop(provider, ..., result)` |
| `main.go:257` (placeholder) | `buildSystemPrompt()` | `buildSystemPrompt(piBasePrompt)` |

### SDK Path

`pkg/sdk/sdk.go` has its own `SessionConfig.SystemPrompt` field. SDK users set their own prompt explicitly, so no change needed — it bypasses `buildSystemPrompt()` entirely. The SDK default (empty string → minimal prompt) is appropriate since SDK consumers control their own identity.

### What This Does NOT Change

- **Directory-walk prompt collection** (CLAUDE.md, AGENTS.md, etc.) — unchanged, appended to whichever base is selected.
- **Skill system reminders** — unchanged, appended after directory walk.
- **Non-OAuth providers** (OpenRouter, Gemini, Azure, Bedrock, Ollama) — always get pi base.
- **`anthropic.go` request format** — OAuth still converts system block to string, API key still sends block array. No change.
- **`openai.go` request format** — always Bearer, no format changes needed.
- **SDK** — callers set their own prompt. No change.
- **`auth.Resolver`** — unchanged.

## File Changes Summary

| File | Change |
|------|--------|
| `cmd/gi/prompts/default.md` | **New** — Pi base prompt (placeholder content, needs authoring) |
| `cmd/gi/prompts/oauth_anthropic.md` | **New** — Claude Code prompt for Anthropic OAuth |
| `cmd/gi/prompts/oauth_openai.md` | **New** — Claude Code prompt for OpenAI/Codex OAuth |
| `cmd/gi/main.go` | `go:embed` directives, `providerResult` struct, `resolveProvider` returns it, `buildSystemPrompt(base)` takes param, `selectBasePrompt` helper, `oauthBasePrompts` map |
| `pkg/ai/anthropic.go` | Remove or guard the fallback at line 76-79 |
| `cmd/gi/context_test.go` | Update `buildSystemPrompt` calls to pass base param |

## Risks

- **Pi prompt content**: `cmd/gi/prompts/default.md` has placeholder content — the actual pi identity prompt needs authoring.
- **OAuth prompt drift**: If the upstream expected prompts change, edit the `.md` files — no Go code changes needed, but a rebuild is required since they're embedded.
- **Session restore**: Restored sessions use whatever prompt was set at creation time. Switching auth methods mid-session won't change the prompt retroactively. This is correct behavior.
- **OpenAI OAuth silent pass-through**: Currently `resolveProvider` doesn't check `IsOAuthToken("openai")` — it works by accident since OpenAI always uses Bearer. This design makes it explicit, which is an improvement.

## Alternative Considered: Provider Interface Method

Add `IsOAuth() bool` to the `ai.Provider` interface so the prompt can be selected deeper in the stack. Rejected because:
- Forces every provider implementation to add a method.
- Mixes auth concerns into the provider abstraction.
- The information is already available at the call site — just thread it through.
