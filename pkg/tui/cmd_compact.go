package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ejm/go_pi/pkg/agent"
)

// compactionStartMsg signals that compaction has begun.
type compactionStartMsg struct{}

// compactionDoneMsg carries the result of a completed compaction.
type compactionDoneMsg struct {
	summary string
}

// compactionErrorMsg carries a compaction error.
type compactionErrorMsg struct {
	err error
}

// NewCompactCommand returns a SlashCommand for /compact that triggers
// conversation compaction via the agent loop. The provided context should be
// tied to the application lifecycle so that compaction is cancelled when the
// user quits.
func NewCompactCommand(ctx context.Context, agentLoop *agent.Loop) *SlashCommand {
	return &SlashCommand{
		Name:        "compact",
		Description: "Summarize conversation to free context space",
		Execute: func(args string) tea.Cmd {
			return tea.Batch(
				func() tea.Msg { return compactionStartMsg{} },
				func() tea.Msg {
					err := agentLoop.Compact(ctx, args)
					if err != nil {
						return compactionErrorMsg{err: err}
					}
					// Read the summary from the agent's events channel.
					// The Compact method already emitted an EventCompaction,
					// but we also capture the summary directly here by reading
					// the single compacted message.
					msgs := agentLoop.Messages()
					summary := ""
					if len(msgs) > 0 {
						summary = msgs[0].GetText()
					}
					return compactionDoneMsg{summary: summary}
				},
			)
		},
	}
}
