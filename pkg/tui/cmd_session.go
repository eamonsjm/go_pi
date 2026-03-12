package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/session"
)

// NewNewSessionCommand creates the /new command which starts a fresh session.
func NewNewSessionCommand(agentLoop *agent.AgentLoop, sessionMgr *session.Manager, chatView *ChatView, header *Header) *SlashCommand {
	return &SlashCommand{
		Name:        "new",
		Description: "Start a fresh session",
		Execute: func(args string) tea.Cmd {
			// Clear the agent loop conversation history.
			agentLoop.SetMessages(nil)

			// Create a new session in the session manager.
			id := sessionMgr.NewSession()

			// Clear the chat view and show confirmation.
			chatView.ClearBlocks()
			chatView.AddSystemMessage(fmt.Sprintf("Started new session: %s", id))

			// Update the header with the short session ID.
			header.SetSession(shortID(id))

			return nil
		},
	}
}

// NewResumeCommand creates the /resume command which lists or loads a session.
//
// When invoked with no arguments, it lists available sessions in the chat.
// When invoked with an ID or index number, it loads that session directly.
func NewResumeCommand(agentLoop *agent.AgentLoop, sessionMgr *session.Manager, chatView *ChatView, header *Header) *SlashCommand {
	// lastListed holds the sessions from the most recent /resume listing so
	// that numeric indices can be resolved on a subsequent /resume <n> call.
	var lastListed []session.SessionInfo

	return &SlashCommand{
		Name:        "resume",
		Description: "Resume a previous session (use /resume or /resume <id>)",
		Execute: func(args string) tea.Cmd {
			args = strings.TrimSpace(args)

			// No argument: list sessions.
			if args == "" {
				sessions := sessionMgr.ListSessions()
				if len(sessions) == 0 {
					chatView.AddSystemMessage("No saved sessions found.")
					return nil
				}
				lastListed = sessions
				chatView.AddSystemMessage(formatSessionList(sessions))
				return nil
			}

			// Argument provided: resolve to a session ID.
			targetID := resolveSessionArg(args, lastListed, sessionMgr.ListSessions())
			if targetID == "" {
				chatView.AddSystemMessage(fmt.Sprintf("Session not found: %s", args))
				return nil
			}

			// Load the session.
			if err := sessionMgr.LoadSession(targetID); err != nil {
				chatView.AddSystemMessage(fmt.Sprintf("Failed to load session: %v", err))
				return nil
			}

			// Restore messages into the agent loop.
			msgs := sessionMgr.GetMessages()
			agentLoop.SetMessages(msgs)

			// Rebuild the chat view from the restored messages.
			chatView.ClearBlocks()
			rebuildChatFromMessages(chatView, msgs)
			chatView.AddSystemMessage(fmt.Sprintf("Resumed session %s (%d messages)", shortID(targetID), len(msgs)))

			// Update header.
			header.SetSession(shortID(targetID))

			return nil
		},
	}
}

// NewSessionInfoCommand creates the /session command which shows current session info.
func NewSessionInfoCommand(sessionMgr *session.Manager, chatView *ChatView) *SlashCommand {
	return &SlashCommand{
		Name:        "session",
		Description: "Show current session info",
		Execute: func(args string) tea.Cmd {
			id := sessionMgr.CurrentID()
			if id == "" {
				chatView.AddSystemMessage("No active session.")
				return nil
			}

			msgs := sessionMgr.GetMessages()

			// Find the creation time from the session list.
			var createdAt time.Time
			for _, s := range sessionMgr.ListSessions() {
				if s.ID == id {
					createdAt = s.CreatedAt
					break
				}
			}

			var sb strings.Builder
			sb.WriteString("Session Info\n")
			sb.WriteString(fmt.Sprintf("  ID:       %s\n", id))
			sb.WriteString(fmt.Sprintf("  Messages: %d\n", len(msgs)))
			if !createdAt.IsZero() {
				sb.WriteString(fmt.Sprintf("  Created:  %s\n", createdAt.Format("2006-01-02 15:04:05")))
			}
			if sessionMgr.HasBranches() {
				branches := sessionMgr.GetBranches()
				sb.WriteString(fmt.Sprintf("  Branches: %d\n", len(branches)))
				activeBranch := sessionMgr.ActiveBranch()
				if activeBranch != "" {
					sb.WriteString(fmt.Sprintf("  Branch:   %s\n", shortID(activeBranch)))
				}
			}

			chatView.AddSystemMessage(sb.String())
			return nil
		},
	}
}

// NewNameCommand creates the /name command which sets the session display name.
func NewNameCommand(sessionMgr *session.Manager, header *Header, chatView *ChatView) *SlashCommand {
	return &SlashCommand{
		Name:        "name",
		Description: "Set session display name",
		Execute: func(args string) tea.Cmd {
			name := strings.TrimSpace(args)
			if name == "" {
				chatView.AddSystemMessage("Usage: /name <display name>")
				return nil
			}

			header.SetSession(name)
			chatView.AddSystemMessage(fmt.Sprintf("Session name set to: %s", name))
			return nil
		},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// formatSessionList formats a slice of SessionInfo into a numbered list for
// display in the chat area.
func formatSessionList(sessions []session.SessionInfo) string {
	var sb strings.Builder
	sb.WriteString("Available sessions:\n")
	for i, s := range sessions {
		dateStr := s.UpdatedAt.Format("2006-01-02 15:04")
		line := fmt.Sprintf("  [%d] %s  %s  (%d entries)", i+1, shortID(s.ID), dateStr, s.Entries)
		if s.Branches > 1 {
			line += fmt.Sprintf("  [%d branches]", s.Branches)
		}
		if s.Preview != "" {
			line += fmt.Sprintf("  %q", s.Preview)
		}
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\nUse /resume <number> or /resume <id> to load a session.")
	return sb.String()
}

// resolveSessionArg resolves a user argument to a session ID. The argument
// may be a numeric index (1-based from the last listing), a full session ID,
// or a prefix of a session ID.
func resolveSessionArg(arg string, lastListed, allSessions []session.SessionInfo) string {
	// Try numeric index first.
	if n, err := strconv.Atoi(arg); err == nil && n >= 1 {
		list := lastListed
		if len(list) == 0 {
			list = allSessions
		}
		if n <= len(list) {
			return list[n-1].ID
		}
	}

	// Try exact or prefix match against all sessions.
	sessions := allSessions
	if len(sessions) == 0 {
		return ""
	}

	// Exact match.
	for _, s := range sessions {
		if s.ID == arg {
			return s.ID
		}
	}

	// Prefix match.
	var match string
	for _, s := range sessions {
		if strings.HasPrefix(s.ID, arg) {
			if match != "" {
				// Ambiguous prefix — don't resolve.
				return ""
			}
			match = s.ID
		}
	}
	return match
}

// rebuildChatFromMessages replays conversation messages into the chat view so
// the user sees the restored conversation history.
func rebuildChatFromMessages(chatView *ChatView, msgs []ai.Message) {
	for _, msg := range msgs {
		for _, block := range msg.Content {
			switch block.Type {
			case ai.ContentTypeText:
				if msg.Role == ai.RoleUser {
					chatView.AddUserMessage(block.Text)
				} else if msg.Role == ai.RoleAssistant {
					chatView.HandleEvent(agent.AgentEvent{
						Type:  agent.EventAssistantText,
						Delta: block.Text,
					})
					chatView.rebuildContent()
					// Break text continuity so the next block starts fresh.
					chatView.HandleEvent(agent.AgentEvent{
						Type: agent.EventTurnEnd,
					})
				}
			}
		}
	}
}

// shortID returns the first 12 characters of a session ID for display.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
