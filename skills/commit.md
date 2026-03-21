---
name: commit
description: Analyze staged changes and create a well-formed conventional commit
user_invocable: true
arguments:
  - name: message
    description: Optional commit message to use instead of auto-generating one
    required: false
---

Create a git commit for the current staged changes. Follow these steps:

1. **Check current state:**
   - Run `git status` to see staged, unstaged, and untracked files
   - Run `git diff --cached` to review what will be committed
   - If nothing is staged, inform the user and suggest files to stage

2. **Review recent commit style:**
   - Run `git log --oneline -5` to see recent commit message patterns
   - Follow the repository's existing conventions

3. **Draft the commit message:**
{{#if message}}
   Use this message: {{message}}
{{/if}}
   - Use conventional commit format: `<type>(<scope>): <description>`
   - Types: feat, fix, refactor, test, docs, chore, perf, style, ci, build
   - Keep the subject line under 72 characters
   - Focus on *why* the change was made, not *what* changed
   - Add a body paragraph if the change is non-trivial

4. **Execute the commit:**
   - Stage any additional files the user requests
   - Create the commit with the drafted message
   - Show the result with `git log --oneline -1`

Do NOT push to remote unless explicitly asked.
