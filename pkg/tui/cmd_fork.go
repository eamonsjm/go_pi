package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/session"
)

// forkSession is the subset of *session.Manager used by the fork command.
type forkSession interface {
	CurrentID() string
	GetUserEntries() []session.Entry
	GetEntries() []session.Entry
	ForkAt(id string) error
	GetMessages() []ai.Message
	GetBranches() []session.BranchInfo
}

// NewForkCommand creates the /fork command which branches the conversation
// from a previous message.
//
// Usage:
//   - /fork        — fork from the last user message (re-ask with different input)
//   - /fork <n>    — fork from the nth user message (1 = first, -1 = last)
//   - /fork <id>   — fork from a specific entry ID
func NewForkCommand(setMessages func([]ai.Message), sess forkSession, chatView *ChatView) *SlashCommand {
	return &SlashCommand{
		Name:        "fork",
		Description: "Branch the conversation from a previous message",
		Execute: func(args string) tea.Cmd {
			args = strings.TrimSpace(args)

			if sess.CurrentID() == "" {
				chatView.AddSystemMessage("No active session to fork.")
				return nil
			}

			userEntries := sess.GetUserEntries()
			if len(userEntries) == 0 {
				chatView.AddSystemMessage("No user messages to fork from.")
				return nil
			}

			// Determine the fork point.
			var forkEntry session.Entry
			if args == "" {
				// Default: fork from the last user message's parent — so the
				// user can provide a different response at that point.
				forkEntry = userEntries[len(userEntries)-1]
			} else if n, err := strconv.Atoi(args); err == nil {
				// Numeric: index into user messages.
				if n < 0 {
					n = len(userEntries) + n + 1
				}
				if n < 1 || n > len(userEntries) {
					chatView.AddSystemMessage(fmt.Sprintf("Invalid message number: %s (have %d user messages)", args, len(userEntries)))
					return nil
				}
				forkEntry = userEntries[n-1]
			} else {
				// Try as entry ID or prefix.
				entries := sess.GetEntries()
				found := false
				for _, e := range entries {
					if e.ID == args || strings.HasPrefix(e.ID, args) {
						forkEntry = e
						found = true
						break
					}
				}
				if !found {
					chatView.AddSystemMessage(fmt.Sprintf("Entry not found: %s", args))
					return nil
				}
			}

			// Fork at the parent of the selected entry so the user can
			// replace that message. If the entry has no parent, fork from
			// the entry itself (it's the root).
			forkPointID := forkEntry.ParentID
			if forkPointID == "" {
				forkPointID = forkEntry.ID
			}

			// Set the fork point.
			if err := sess.ForkAt(forkPointID); err != nil {
				chatView.AddSystemMessage(fmt.Sprintf("Fork failed: %v", err))
				return nil
			}

			// Restore messages for the new branch point into the agent loop.
			msgs := sess.GetMessages()
			setMessages(msgs)

			// Rebuild the chat view.
			chatView.ClearBlocks()
			rebuildChatFromMessages(chatView, msgs)

			branches := sess.GetBranches()
			chatView.AddSystemMessage(fmt.Sprintf(
				"Forked at message %s — now on a new branch (%d total branches).\nType your message to continue on this branch.",
				shortID(forkPointID), len(branches)))

			return nil
		},
	}
}
