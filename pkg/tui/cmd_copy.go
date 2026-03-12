package tui

import (
	"os/exec"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/session"
)

// NewCopyCommand creates the /copy command which copies the last assistant
// message text to the system clipboard.
func NewCopyCommand(sessionMgr *session.Manager) *SlashCommand {
	return &SlashCommand{
		Name:        "copy",
		Description: "Copy last assistant message to clipboard",
		Execute: func(args string) tea.Cmd {
			return func() tea.Msg {
				msgs := sessionMgr.GetMessages()
				text := lastAssistantText(msgs)
				if text == "" {
					return CommandResultMsg{Text: "No assistant message to copy.", IsError: true}
				}

				if err := copyToClipboard(text); err != nil {
					return CommandResultMsg{
						Text:    "Failed to copy: " + err.Error(),
						IsError: true,
					}
				}

				// Truncate preview for the confirmation message.
				preview := text
				if len(preview) > 80 {
					preview = preview[:77] + "..."
				}
				return CommandResultMsg{Text: "Copied to clipboard: " + preview}
			}
		},
	}
}

// lastAssistantText walks the messages in reverse to find the last assistant
// text content.
func lastAssistantText(msgs []ai.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == ai.RoleAssistant {
			text := msgs[i].GetText()
			if text != "" {
				return text
			}
		}
	}
	return ""
}

// copyToClipboard writes text to the system clipboard using platform-specific
// tools. It tries xclip, xsel, and wl-copy on Linux, pbcopy on macOS.
func copyToClipboard(text string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	default:
		// Try wayland first, then X11.
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		} else if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else {
			return exec.ErrNotFound
		}
	}

	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
