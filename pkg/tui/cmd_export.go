package tui

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/session"
)

// NewExportCommand creates the /export command which exports the current
// session to a standalone HTML file with syntax highlighting.
//
// Usage:
//
//	/export            — export to ~/.gi/exports/<session-id>.html
//	/export path.html  — export to the specified path
func NewExportCommand(sessionMgr *session.Manager) *SlashCommand {
	return &SlashCommand{
		Name:        "export",
		Description: "Export session to standalone HTML file",
		Execute: func(args string) tea.Cmd {
			return func() tea.Msg {
				sessionID := sessionMgr.CurrentID()
				if sessionID == "" {
					return CommandResultMsg{Text: "No active session to export.", IsError: true}
				}

				msgs := sessionMgr.GetMessages()
				if len(msgs) == 0 {
					return CommandResultMsg{Text: "Session has no messages to export.", IsError: true}
				}

				// Determine output path.
				outPath := strings.TrimSpace(args)
				if outPath == "" {
					home, _ := os.UserHomeDir()
					dir := filepath.Join(home, ".gi", "exports")
					if err := os.MkdirAll(dir, 0o700); err != nil {
						return CommandResultMsg{Text: "Failed to create export dir: " + err.Error(), IsError: true}
					}
					outPath = filepath.Join(dir, sessionID+".html")
				}

				htmlContent := renderSessionHTML(sessionID, msgs)

				if err := os.WriteFile(outPath, []byte(htmlContent), 0o600); err != nil {
					return CommandResultMsg{Text: "Failed to write file: " + err.Error(), IsError: true}
				}

				return CommandResultMsg{Text: fmt.Sprintf("Exported session to %s", outPath)}
			}
		},
	}
}

// renderSessionHTML produces a standalone HTML document from conversation
// messages. The output includes embedded CSS for styling and a small JS
// snippet for collapsible tool-call sections.
func renderSessionHTML(sessionID string, msgs []ai.Message) string {
	var body strings.Builder
	for _, msg := range msgs {
		role := string(msg.Role)
		roleClass := role

		for _, block := range msg.Content {
			switch block.Type {
			case ai.ContentTypeText:
				text := html.EscapeString(block.Text)
				text = renderCodeBlocks(text)
				fmt.Fprintf(&body, "<div class=\"message %s\"><div class=\"role\">%s</div><div class=\"content\">%s</div></div>\n",
					roleClass, role, text)

			case ai.ContentTypeToolUse:
				inputJSON, err := json.MarshalIndent(block.Input, "", "  ")
				if err != nil {
					inputJSON = []byte(fmt.Sprintf("%v", block.Input))
				}
				fmt.Fprintf(&body, "<div class=\"message tool\"><div class=\"role\">tool_use</div>"+
					"<details><summary>%s</summary><pre><code>%s</code></pre></details></div>\n",
					html.EscapeString(block.ToolName),
					html.EscapeString(string(inputJSON)))

			case ai.ContentTypeToolResult:
				errClass := ""
				if block.IsError {
					errClass = " error"
				}
				fmt.Fprintf(&body, "<div class=\"message tool-result%s\"><div class=\"role\">tool_result</div>"+
					"<details><summary>result</summary><pre><code>%s</code></pre></details></div>\n",
					errClass,
					html.EscapeString(block.Content))

			case ai.ContentTypeThinking:
				fmt.Fprintf(&body, "<div class=\"message thinking\"><div class=\"role\">thinking</div>"+
					"<details><summary>thinking</summary><pre>%s</pre></details></div>\n",
					html.EscapeString(block.Thinking))
			}
		}
	}

	return fmt.Sprintf(htmlTemplate, html.EscapeString(sessionID), time.Now().Format("2006-01-02 15:04:05"), body.String())
}

// renderCodeBlocks converts markdown-style fenced code blocks (```) in
// already-escaped HTML text into <pre><code> elements with a language class.
func renderCodeBlocks(escaped string) string {
	lines := strings.Split(escaped, "\n")
	var out strings.Builder
	inCode := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") && !inCode {
			lang := strings.TrimPrefix(trimmed, "```")
			lang = strings.TrimSpace(lang)
			cls := ""
			if lang != "" {
				cls = fmt.Sprintf(" class=\"language-%s\"", lang)
			}
			fmt.Fprintf(&out, "<pre><code%s>", cls)
			inCode = true
			continue
		}
		if trimmed == "```" && inCode {
			out.WriteString("</code></pre>\n")
			inCode = false
			continue
		}
		if inCode {
			out.WriteString(line + "\n")
		} else {
			out.WriteString(line + "<br>\n")
		}
	}
	if inCode {
		out.WriteString("</code></pre>\n")
	}
	return out.String()
}

const htmlTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Session %s</title>
<style>
  :root {
    --bg: #1e1e2e;
    --fg: #cdd6f4;
    --user-bg: #313244;
    --assistant-bg: #1e1e2e;
    --tool-bg: #181825;
    --thinking-bg: #11111b;
    --error-bg: #45243a;
    --border: #45475a;
    --accent: #89b4fa;
    --user-accent: #a6e3a1;
    --dim: #6c7086;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body {
    font-family: 'SF Mono', 'Cascadia Code', 'Fira Code', 'JetBrains Mono', monospace;
    font-size: 14px;
    line-height: 1.6;
    background: var(--bg);
    color: var(--fg);
    padding: 2rem;
    max-width: 900px;
    margin: 0 auto;
  }
  h1 {
    color: var(--accent);
    font-size: 1.2rem;
    margin-bottom: 0.25rem;
  }
  .meta {
    color: var(--dim);
    font-size: 0.85rem;
    margin-bottom: 2rem;
    border-bottom: 1px solid var(--border);
    padding-bottom: 1rem;
  }
  .message {
    margin-bottom: 1rem;
    padding: 1rem;
    border-radius: 8px;
    border: 1px solid var(--border);
  }
  .message.user { background: var(--user-bg); }
  .message.assistant { background: var(--assistant-bg); }
  .message.tool, .message.tool-result { background: var(--tool-bg); font-size: 0.9rem; }
  .message.thinking { background: var(--thinking-bg); font-size: 0.9rem; }
  .message.error { background: var(--error-bg); }
  .role {
    font-weight: 700;
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    margin-bottom: 0.5rem;
    color: var(--accent);
  }
  .message.user .role { color: var(--user-accent); }
  .content { white-space: pre-wrap; word-break: break-word; }
  pre {
    background: #11111b;
    padding: 0.75rem 1rem;
    border-radius: 6px;
    overflow-x: auto;
    margin: 0.5rem 0;
  }
  code { font-family: inherit; }
  details { cursor: pointer; }
  details summary {
    color: var(--dim);
    font-size: 0.85rem;
    padding: 0.25rem 0;
  }
  details summary:hover { color: var(--accent); }
</style>
</head>
<body>
<h1>go_pi session export</h1>
<div class="meta">Exported %s</div>
%s
</body>
</html>`
