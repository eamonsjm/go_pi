package tui

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ejm/go_pi/pkg/ai"
)

// secretPattern describes a regex pattern that matches a type of secret.
type secretPattern struct {
	Name    string
	Pattern *regexp.Regexp
}

// secretPatterns contains compiled regexes for common secret formats.
var secretPatterns = []secretPattern{
	// AWS access keys
	{Name: "AWS access key", Pattern: regexp.MustCompile(`(?i)AKIA[0-9A-Z]{16}`)},
	// AWS secret keys (40 char base64 near "aws" or "secret")
	{Name: "AWS secret key", Pattern: regexp.MustCompile(`(?i)(?:aws.{0,20})?['\"]?[A-Za-z0-9/+=]{40}['\"]?\s*$`)},

	// GitHub tokens
	{Name: "GitHub token", Pattern: regexp.MustCompile(`(?:ghp|gho|ghs|ghr|github_pat)_[A-Za-z0-9_]{20,}`)},

	// Generic API keys / tokens (key=value or key: value patterns)
	{Name: "API key/token", Pattern: regexp.MustCompile(`(?i)(?:api[_-]?key|api[_-]?secret|access[_-]?token|auth[_-]?token|secret[_-]?key)\s*[=:]\s*['\"]?[A-Za-z0-9/+=_\-.]{16,}['\"]?`)},

	// Passwords in config
	{Name: "password", Pattern: regexp.MustCompile(`(?i)(?:password|passwd|pwd)\s*[=:]\s*['\"]?[^\s'"]{8,}['\"]?`)},

	// Private keys
	{Name: "private key", Pattern: regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`)},

	// Bearer tokens
	{Name: "bearer token", Pattern: regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9\-._~+/]+=*`)},

	// Connection strings with embedded credentials
	{Name: "connection string", Pattern: regexp.MustCompile(`(?i)(?:mongodb|postgres|mysql|redis|amqp)(?:\+\w+)?://[^@\s]+:[^@\s]+@`)},

	// Slack tokens
	{Name: "Slack token", Pattern: regexp.MustCompile(`xox[bpors]-[A-Za-z0-9-]+`)},

	// Stripe keys
	{Name: "Stripe key", Pattern: regexp.MustCompile(`(?:sk|pk|rk)_(?:test|live)_[A-Za-z0-9]{20,}`)},

	// Generic long hex strings that look like secrets (32+ hex chars after a key-like label)
	{Name: "hex secret", Pattern: regexp.MustCompile(`(?i)(?:secret|token|key|credential)\s*[=:]\s*['\"]?[0-9a-f]{32,}['\"]?`)},
}

// secretFinding records one detected secret in the session content.
type secretFinding struct {
	PatternName string
	Context     string // short excerpt around the match
}

// scanMessagesForSecrets scans all tool_result and text content blocks for
// patterns that look like secrets. It returns a list of findings.
func scanMessagesForSecrets(msgs []ai.Message) []secretFinding {
	var findings []secretFinding
	seen := make(map[string]bool) // deduplicate by pattern+context

	for _, msg := range msgs {
		for _, block := range msg.Content {
			var text string
			switch block.Type {
			case ai.ContentTypeToolResult:
				text = block.Content
			case ai.ContentTypeText:
				text = block.Text
			default:
				continue
			}
			if text == "" {
				continue
			}

			for _, sp := range secretPatterns {
				matches := sp.Pattern.FindAllStringIndex(text, -1)
				for _, loc := range matches {
					excerpt := excerptAround(text, loc[0], loc[1])
					key := sp.Name + "|" + excerpt
					if seen[key] {
						continue
					}
					seen[key] = true
					findings = append(findings, secretFinding{
						PatternName: sp.Name,
						Context:     excerpt,
					})
				}
			}
		}
	}
	return findings
}

// excerptAround returns a short snippet of text around [start, end), with
// the matched portion partially masked.
func excerptAround(text string, start, end int) string {
	// Clamp inputs to valid range to prevent slice bounds panics.
	if start < 0 {
		start = 0
	}
	if end > len(text) {
		end = len(text)
	}
	if start >= end || start >= len(text) {
		return ""
	}

	// Get some surrounding context
	ctxStart := start - 20
	if ctxStart < 0 {
		ctxStart = 0
	}
	ctxEnd := end + 20
	if ctxEnd > len(text) {
		ctxEnd = len(text)
	}

	// Align to valid rune boundaries to avoid slicing multi-byte UTF-8 characters.
	for ctxStart < len(text) && !utf8.RuneStart(text[ctxStart]) {
		ctxStart++
	}
	for ctxEnd < len(text) && !utf8.RuneStart(text[ctxEnd]) {
		ctxEnd++
	}

	excerpt := text[ctxStart:ctxEnd]

	// Track match position within the excerpt so newline trimming
	// doesn't use a stale offset after the string is shifted.
	matchOffset := start - ctxStart

	// Trim to the last newline before the match for cleaner display.
	if matchOffset > 0 && matchOffset <= len(excerpt) {
		if idx := strings.LastIndexByte(excerpt[:matchOffset], '\n'); idx >= 0 {
			excerpt = excerpt[idx+1:]
		}
	}
	if idx := strings.IndexByte(excerpt, '\n'); idx >= 0 {
		excerpt = excerpt[:idx]
	}

	// Truncate long excerpts (rune-aware to avoid splitting multi-byte UTF-8).
	if utf8.RuneCountInString(excerpt) > 80 {
		r := []rune(excerpt)
		excerpt = string(r[:77]) + "..."
	}
	return excerpt
}

// redactSecrets replaces detected secret patterns in text with [REDACTED].
func redactSecrets(text string) string {
	for _, sp := range secretPatterns {
		text = sp.Pattern.ReplaceAllStringFunc(text, func(match string) string {
			// For key=value patterns, preserve the key portion
			if eqIdx := strings.IndexAny(match, "=:"); eqIdx >= 0 {
				prefix := match[:eqIdx+1]
				// Check if there's a space after = or :
				rest := match[eqIdx+1:]
				trimmed := strings.TrimLeft(rest, " ")
				spaces := rest[:len(rest)-len(trimmed)]
				return prefix + spaces + "[REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return text
}

// redactSessionMessages returns a deep copy of messages with secrets redacted
// from tool_result and text content blocks.
func redactSessionMessages(msgs []ai.Message) []ai.Message {
	out := make([]ai.Message, len(msgs))
	for i, msg := range msgs {
		out[i] = ai.Message{
			Role:    msg.Role,
			Content: make([]ai.ContentBlock, len(msg.Content)),
		}
		for j, block := range msg.Content {
			out[i].Content[j] = block
			switch block.Type {
			case ai.ContentTypeToolResult:
				out[i].Content[j].Content = redactSecrets(block.Content)
			case ai.ContentTypeText:
				out[i].Content[j].Text = redactSecrets(block.Text)
			}
		}
	}
	return out
}

// formatSecretWarning produces a user-facing warning summarizing detected secrets.
func formatSecretWarning(findings []secretFinding) string {
	var sb strings.Builder
	sb.WriteString("⚠ Potential secrets detected in session:\n\n")

	// Group by pattern name
	groups := make(map[string]int)
	for _, f := range findings {
		groups[f.PatternName]++
	}

	for name, count := range groups {
		fmt.Fprintf(&sb, "  • %s (%d match", name, count)
		if count > 1 {
			sb.WriteString("es")
		}
		sb.WriteString(")\n")
	}

	sb.WriteString("\nSecrets will be redacted with [REDACTED] before sharing.\n")
	sb.WriteString("Use /share --force to proceed, or review your session first.\n")
	sb.WriteString("\nNote: \"Secret\" gists are accessible to anyone with the URL.")
	return sb.String()
}
