---
name: review
description: Review code changes for bugs, style, security, and Go patterns
user_invocable: true
arguments:
  - name: file
    description: Specific file or path to review (defaults to all changed files)
    required: false
---

Review code changes for quality issues. Follow these steps:

1. **Identify what to review:**
{{#if file}}
   Review the specified file: {{file}}
   - Read the file and its recent diff (`git diff {{file}}` or `git diff HEAD~1 -- {{file}}`)
{{/if}}
   - If no file specified, review all uncommitted changes (`git diff`) and staged changes (`git diff --cached`)
   - If working tree is clean, review the last commit (`git diff HEAD~1`)

2. **Check each category:**

   | Category | Look for |
   |----------|----------|
   | **Bugs** | Off-by-one errors, nil/zero-value mishandling, unchecked errors, race conditions, resource leaks |
   | **Security** | Injection vectors, hardcoded secrets, unsafe input handling, path traversal |
   | **Go patterns** | Error wrapping with `%w`, defer for cleanup, receiver naming, interface compliance, goroutine leaks |
   | **Style** | Naming conventions, function length, dead code, unnecessary comments, consistent formatting |
   | **Completeness** | Missing error paths, untested branches, incomplete validation, missing context propagation |

3. **Report findings:**
   - Group by severity: critical > warning > suggestion
   - Reference specific file and line numbers
   - Explain *why* each finding matters
   - Suggest a concrete fix for each issue

4. **Summary:**
   - State whether the code is ready to merge or needs changes
   - Highlight the most important finding if any
