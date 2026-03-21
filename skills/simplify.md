---
name: simplify
description: Refactor code for clarity, simplicity, and efficiency
user_invocable: true
arguments:
  - name: file
    description: File to simplify (defaults to files changed in current branch)
    required: false
---

Review and refactor code to reduce complexity while preserving behavior. Follow these steps:

1. **Identify target code:**
{{#if file}}
   Read and analyze: {{file}}
{{/if}}
   - If no file specified, check `git diff --name-only` for recently changed files
   - Focus on code that was recently modified (higher impact)

2. **Analyze for simplification opportunities:**

   | Pattern | Simplification |
   |---------|---------------|
   | Nested if/else chains | Early returns, guard clauses |
   | Repeated code blocks | Extract helper only if 3+ occurrences |
   | Long functions (>40 lines) | Break into focused functions |
   | Complex conditionals | Named booleans, truth tables |
   | Over-engineering | Remove unused abstractions, premature generalization |
   | Dead code | Delete commented-out code, unused variables, unreachable branches |
   | Verbose error handling | Consolidate common patterns, use error wrapping |
   | Type stuttering | Simplify `var x Type = Type{}` to `x := Type{}` |

3. **Go-specific simplifications:**
   - Replace `if err != nil { return err }` chains with early returns
   - Use `errors.As`/`errors.Is` instead of type assertions on errors
   - Prefer `strings.Cut` over `strings.Index` + slice
   - Use `slices` and `maps` stdlib packages where appropriate
   - Simplify channel/goroutine patterns (do you actually need concurrency?)
   - Remove unnecessary pointer receivers on small value types

4. **Apply changes:**
   - Make minimal, focused edits — do not rewrite working code for style alone
   - Preserve all existing behavior and test coverage
   - Each edit should make the code obviously simpler, not just different
   - Run `go build ./...` after changes to verify compilation
