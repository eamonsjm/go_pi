package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/session"
)

// treeSession is the subset of *session.Manager used by the tree command.
type treeSession interface {
	CurrentID() string
	GetBranches() []session.BranchInfo
	FormatTree() string
	SwitchBranch(leafID string) error
	GetMessages() []ai.Message
}

// NewTreeCommand creates the /tree command which displays the session's
// branch structure and allows switching between branches.
//
// Usage:
//   - /tree        — show the branch tree
//   - /tree <n>    — switch to branch number n
func NewTreeCommand(setMessages func([]ai.Message), sess treeSession, chatView *ChatView, setSession func(string)) *SlashCommand {
	return &SlashCommand{
		Name:        "tree",
		Description: "Show or navigate the session branch tree",
		Execute: func(args string) tea.Cmd {
			args = strings.TrimSpace(args)

			if sess.CurrentID() == "" {
				chatView.AddSystemMessage("No active session.")
				return nil
			}

			// No arguments: display tree.
			if args == "" {
				tree := sess.FormatTree()
				chatView.AddSystemMessage(tree)
				return nil
			}

			// Numeric argument: switch to that branch.
			n, err := strconv.Atoi(args)
			if err != nil {
				chatView.AddSystemMessage(fmt.Sprintf("Invalid branch number: %s", args))
				return nil
			}

			branches := sess.GetBranches()
			if len(branches) == 0 {
				chatView.AddSystemMessage("No branches in session.")
				return nil
			}
			if n < 1 || n > len(branches) {
				chatView.AddSystemMessage(fmt.Sprintf("Branch %d not found (have %d branches)", n, len(branches)))
				return nil
			}

			target := branches[n-1]
			if target.IsActive {
				chatView.AddSystemMessage(fmt.Sprintf("Already on branch %d.", n))
				return nil
			}

			// Switch to the target branch.
			if err := sess.SwitchBranch(target.LeafID); err != nil {
				chatView.AddSystemMessage(fmt.Sprintf("Switch failed: %v", err))
				return nil
			}

			// Restore messages for the new branch into the agent loop.
			msgs := sess.GetMessages()
			setMessages(msgs)

			// Rebuild the chat view.
			chatView.ClearBlocks()
			rebuildChatFromMessages(chatView, msgs)

			preview := target.Preview
			if preview == "" {
				preview = shortID(target.LeafID)
			}
			chatView.AddSystemMessage(fmt.Sprintf(
				"Switched to branch %d: %s (%d messages)",
				n, preview, len(msgs)))

			setSession(shortID(sess.CurrentID()))

			return nil
		},
	}
}
