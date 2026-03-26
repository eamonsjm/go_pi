package tui

import (
	"os/exec"
	"runtime"
	"strings"
	"unicode/utf8"

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

				var clipCmd *exec.Cmd
				switch runtime.GOOS {
				case "darwin":
					clipCmd = exec.Command("pbcopy")
				case "windows":
					clipCmd = exec.Command("clip.exe")
				default:
					// Try wayland first, then X11.
					if _, err := exec.LookPath("wl-copy"); err == nil {
						clipCmd = exec.Command("wl-copy")
					} else if _, err := exec.LookPath("xclip"); err == nil {
						clipCmd = exec.Command("xclip", "-selection", "clipboard")
					} else if _, err := exec.LookPath("xsel"); err == nil {
						clipCmd = exec.Command("xsel", "--clipboard", "--input")
					} else {
						return CommandResultMsg{
							Text:    "Failed to copy: " + exec.ErrNotFound.Error(),
							IsError: true,
						}
					}
				}

				clipCmd.Stdin = strings.NewReader(text)
				if err := clipCmd.Run(); err != nil {
					return CommandResultMsg{
						Text:    "Failed to copy: " + err.Error(),
						IsError: true,
					}
				}

				// Truncate preview for the confirmation message (rune-aware to avoid splitting multi-byte UTF-8).
				preview := text
				if utf8.RuneCountInString(preview) > 80 {
					r := []rune(preview)
					preview = string(r[:77]) + "..."
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
