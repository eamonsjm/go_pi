package tui

import (
	"fmt"
	"strconv"
	"strings"

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

// ProviderInfo holds metadata about the active AI provider for display.
type ProviderInfo struct {
	Name     string // e.g. "anthropic"
	Model    string // e.g. "claude-opus-4-6"
	API      string // e.g. "anthropic-messages"
	Auth     string // e.g. "oauth", "api_key", "env"
	Endpoint string // e.g. "https://api.anthropic.com/"
}

// NewSessionInfoCommand creates the /session command which shows the full
// session dashboard: session file, provider, messages, tokens, and cost.
func NewSessionInfoCommand(sessionMgr *session.Manager, chatView *ChatView, getProviderInfo func() ProviderInfo, getSessionUsage func() ai.Usage) *SlashCommand {
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
			info := getProviderInfo()
			usage := getSessionUsage()

			var sb strings.Builder

			// Session Info section.
			sb.WriteString("Session Info\n")
			if fp := sessionMgr.FilePath(); fp != "" {
				fmt.Fprintf(&sb, "  File:     %s\n", fp)
			}
			fmt.Fprintf(&sb, "  ID:       %s\n", id)
			if sessionMgr.HasBranches() {
				branches := sessionMgr.GetBranches()
				fmt.Fprintf(&sb, "  Branches: %d\n", len(branches))
				if ab := sessionMgr.ActiveBranch(); ab != "" {
					fmt.Fprintf(&sb, "  Branch:   %s\n", shortID(ab))
				}
			}

			// Provider section.
			if info.Name != "" {
				sb.WriteString("\nProvider\n")
				fmt.Fprintf(&sb, "  Name:     %s\n", info.Name)
				fmt.Fprintf(&sb, "  Model:    %s\n", info.Model)
				fmt.Fprintf(&sb, "  API:      %s\n", info.API)
				fmt.Fprintf(&sb, "  Auth:     %s\n", info.Auth)
				fmt.Fprintf(&sb, "  Endpoint: %s\n", info.Endpoint)
			}

			// Messages section.
			userN, assistantN, toolCallsN, toolResultsN := countMessageTypes(msgs)
			total := userN + assistantN + toolCallsN + toolResultsN
			sb.WriteString("\nMessages\n")
			fmt.Fprintf(&sb, "  User:         %d\n", userN)
			fmt.Fprintf(&sb, "  Assistant:    %d\n", assistantN)
			fmt.Fprintf(&sb, "  Tool Calls:   %d\n", toolCallsN)
			fmt.Fprintf(&sb, "  Tool Results: %d\n", toolResultsN)
			fmt.Fprintf(&sb, "  Total:        %d\n", total)

			// Tokens section.
			tokenTotal := usage.InputTokens + usage.OutputTokens
			sb.WriteString("\nTokens\n")
			fmt.Fprintf(&sb, "  Input:        %s\n", formatTokens(usage.InputTokens))
			fmt.Fprintf(&sb, "  Output:       %s\n", formatTokens(usage.OutputTokens))
			fmt.Fprintf(&sb, "  Cache Read:   %s\n", formatTokens(usage.CacheRead))
			fmt.Fprintf(&sb, "  Cache Write:  %s\n", formatTokens(usage.CacheWrite))
			fmt.Fprintf(&sb, "  Total:        %s\n", formatTokens(tokenTotal))

			// Cost section.
			cost := calculateCost(usage, info.Model)
			sb.WriteString("\nCost\n")
			fmt.Fprintf(&sb, "  Total:        $%.4f\n", cost)

			chatView.AddSystemMessage(sb.String())
			return nil
		},
	}
}

// countMessageTypes counts messages and content blocks by type.
func countMessageTypes(msgs []ai.Message) (user, assistant, toolCalls, toolResults int) {
	for _, msg := range msgs {
		switch msg.Role {
		case ai.RoleUser:
			hasText := false
			for _, cb := range msg.Content {
				switch cb.Type {
				case ai.ContentTypeText:
					hasText = true
				case ai.ContentTypeToolResult:
					toolResults++
				}
			}
			if hasText {
				user++
			}
		case ai.RoleAssistant:
			assistant++
			for _, cb := range msg.Content {
				if cb.Type == ai.ContentTypeToolUse {
					toolCalls++
				}
			}
		}
	}
	return
}

// tokenPricing holds per-million-token prices for a model.
type tokenPricing struct {
	inputPerM      float64
	outputPerM     float64
	cacheReadPerM  float64
	cacheWritePerM float64
}

// modelPricingTable maps model name prefixes to pricing.
var modelPricingTable = []struct {
	prefix  string
	pricing tokenPricing
}{
	{"claude-opus-4", tokenPricing{15.0, 75.0, 1.50, 18.75}},
	{"claude-sonnet-4", tokenPricing{3.0, 15.0, 0.30, 3.75}},
	{"claude-haiku-4", tokenPricing{0.80, 4.0, 0.08, 1.0}},
	{"gpt-4o", tokenPricing{2.50, 10.0, 0, 0}},
	{"gpt-4-turbo", tokenPricing{10.0, 30.0, 0, 0}},
	{"gemini-2.0", tokenPricing{0.10, 0.40, 0, 0}},
	{"gemini-1.5-pro", tokenPricing{1.25, 5.0, 0, 0}},
}

// defaultPricing is used when no model-specific pricing is found (Sonnet rates).
var defaultPricing = tokenPricing{3.0, 15.0, 0.30, 3.75}

// calculateCost computes USD cost from token usage and model name.
func calculateCost(usage ai.Usage, model string) float64 {
	pricing := defaultPricing
	for _, entry := range modelPricingTable {
		if strings.HasPrefix(model, entry.prefix) {
			pricing = entry.pricing
			break
		}
	}

	nonCacheInput := usage.InputTokens - usage.CacheRead
	if nonCacheInput < 0 {
		nonCacheInput = 0
	}
	input := float64(nonCacheInput) * pricing.inputPerM / 1_000_000
	cacheRead := float64(usage.CacheRead) * pricing.cacheReadPerM / 1_000_000
	cacheWrite := float64(usage.CacheWrite) * pricing.cacheWritePerM / 1_000_000
	output := float64(usage.OutputTokens) * pricing.outputPerM / 1_000_000
	total := input + cacheRead + cacheWrite + output
	if total < 0 {
		total = 0
	}
	return total
}

// providerAPIType returns the API protocol name for a provider.
func providerAPIType(name string) string {
	switch name {
	case "anthropic":
		return "anthropic-messages"
	case "openai", "azure":
		return "openai-chat"
	case "openrouter":
		return "openai-chat"
	case "gemini":
		return "gemini-stream"
	case "bedrock":
		return "bedrock-converse"
	default:
		return name
	}
}

// providerEndpoint returns the default API endpoint for a provider.
func providerEndpoint(name string) string {
	switch name {
	case "anthropic":
		return "https://api.anthropic.com/"
	case "openai":
		return "https://api.openai.com/"
	case "openrouter":
		return "https://openrouter.ai/"
	case "gemini":
		return "https://generativelanguage.googleapis.com/"
	case "azure":
		return "(Azure deployment)"
	case "bedrock":
		return "(AWS Bedrock)"
	default:
		return ""
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
				switch msg.Role {
				case ai.RoleUser:
					chatView.AddUserMessage(block.Text)
				case ai.RoleAssistant:
					chatView.HandleEvent(agent.AgentEvent{
						Type:  agent.EventAssistantText,
						Delta: block.Text,
					})
					// Break text continuity so the next block starts fresh.
					chatView.HandleEvent(agent.AgentEvent{
						Type: agent.EventTurnEnd,
					})
				}
			case ai.ContentTypeToolUse:
				var args map[string]any
				if m, ok := block.Input.(map[string]any); ok {
					args = m
				}
				chatView.HandleEvent(agent.AgentEvent{
					Type:       agent.EventToolExecStart,
					ToolCallID: block.ToolUseID,
					ToolName:   block.ToolName,
					ToolArgs:   args,
				})
			case ai.ContentTypeToolResult:
				chatView.HandleEvent(agent.AgentEvent{
					Type:       agent.EventToolExecEnd,
					ToolCallID: block.ToolResultID,
					ToolResult: block.Content,
					ToolError:  block.IsError,
				})
			}
		}
	}
	// Single rebuild after all blocks are replayed — avoids O(N²) cost of
	// rebuilding after every individual assistant text block.
	chatView.rebuildContent()
}

// shortID returns the first 12 characters of a session ID for display.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
