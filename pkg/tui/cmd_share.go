package tui

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/session"
)

// NewShareCommand creates the /share command which shares the current session
// as a secret GitHub gist via the gh CLI.
func NewShareCommand(sessionMgr *session.Manager) *SlashCommand {
	return &SlashCommand{
		Name:        "share",
		Description: "Share session as a secret GitHub gist",
		Execute: func(args string) tea.Cmd {
			return func() tea.Msg {
				// Verify gh CLI is available.
				if _, err := exec.LookPath("gh"); err != nil {
					return CommandResultMsg{
						Text:    "gh CLI not found. Install it from https://cli.github.com",
						IsError: true,
					}
				}

				sessionID := sessionMgr.CurrentID()
				if sessionID == "" {
					return CommandResultMsg{Text: "No active session to share.", IsError: true}
				}

				msgs := sessionMgr.GetMessages()
				if len(msgs) == 0 {
					return CommandResultMsg{Text: "Session has no messages to share.", IsError: true}
				}

				markdown := renderSessionMarkdown(sessionID, msgs)
				filename := fmt.Sprintf("session-%s.md", shortID(sessionID))

				// Create secret gist via gh CLI.
				cmd := exec.Command("gh", "gist", "create",
					"--filename", filename,
					"--desc", "go_pi session export",
					"-")
				cmd.Stdin = strings.NewReader(markdown)
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr

				if err := cmd.Run(); err != nil {
					errMsg := strings.TrimSpace(stderr.String())
					if errMsg == "" {
						errMsg = err.Error()
					}
					return CommandResultMsg{
						Text:    "Failed to create gist: " + errMsg,
						IsError: true,
					}
				}

				gistURL := strings.TrimSpace(stdout.String())
				return CommandResultMsg{Text: fmt.Sprintf("Shared as secret gist: %s", gistURL)}
			}
		},
	}
}

// renderSessionMarkdown formats conversation messages as a Markdown document
// suitable for sharing.
func renderSessionMarkdown(sessionID string, msgs []ai.Message) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Session %s\n\n", shortID(sessionID)))

	for _, msg := range msgs {
		role := string(msg.Role)

		for _, block := range msg.Content {
			switch block.Type {
			case ai.ContentTypeText:
				sb.WriteString(fmt.Sprintf("### %s\n\n%s\n\n", role, block.Text))

			case ai.ContentTypeToolUse:
				sb.WriteString(fmt.Sprintf("<details><summary>🔧 %s</summary>\n\n```json\n%v\n```\n\n</details>\n\n", block.ToolName, block.Input))

			case ai.ContentTypeToolResult:
				label := "result"
				if block.IsError {
					label = "error"
				}
				sb.WriteString(fmt.Sprintf("<details><summary>📎 %s</summary>\n\n```\n%s\n```\n\n</details>\n\n", label, block.Content))

			case ai.ContentTypeThinking:
				sb.WriteString(fmt.Sprintf("<details><summary>💭 thinking</summary>\n\n%s\n\n</details>\n\n", block.Thinking))
			}
		}
	}

	sb.WriteString("---\n*Exported from go_pi*\n")
	return sb.String()
}
