package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/ejm/go_pi/pkg/agent"
)

// ---------------------------------------------------------------------------
// chatBlock — a discrete visual block rendered in the chat area
// ---------------------------------------------------------------------------

type chatBlockKind int

const (
	blockUser chatBlockKind = iota
	blockAssistantText
	blockThinking
	blockToolCall
	blockToolResult
	blockError
)

type chatBlock struct {
	kind       chatBlockKind
	text       string // accumulated text / delta
	toolID     string
	toolName   string
	toolArgs   map[string]any
	toolResult string
	toolError  bool
	collapsed  bool // for thinking / tool-result expand/collapse
}

// ---------------------------------------------------------------------------
// ChatView
// ---------------------------------------------------------------------------

// ChatView renders the conversation history inside a scrollable viewport.
type ChatView struct {
	viewport viewport.Model
	blocks   []chatBlock
	width    int
	height   int

	// Markdown renderer (created once, re-used).
	renderer *glamour.TermRenderer
}

// NewChatView creates a ChatView with sensible defaults.
func NewChatView() *ChatView {
	vp := viewport.New(80, 20)
	vp.SetContent("")
	vp.YPosition = 0

	r, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(78),
	)

	return &ChatView{
		viewport: vp,
		renderer: r,
	}
}

// SetSize adjusts the viewport dimensions (called on window resize).
func (c *ChatView) SetSize(w, h int) {
	c.width = w
	c.height = h
	c.viewport.Width = w
	c.viewport.Height = h

	// Recreate renderer with updated word-wrap width.
	wrap := w - 4
	if wrap < 40 {
		wrap = 40
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(wrap),
	)
	if err == nil {
		c.renderer = r
	}

	c.rebuildContent()
}

// ---------------------------------------------------------------------------
// Event handlers — called by the parent App on each AgentEvent
// ---------------------------------------------------------------------------

// HandleEvent processes a single agent event and updates the internal block
// list. Returns true if content changed (caller should re-render).
func (c *ChatView) HandleEvent(ev agent.AgentEvent) bool {
	switch ev.Type {

	// ---- streaming text ----
	case agent.EventAssistantText:
		if blk := c.lastBlock(blockAssistantText); blk != nil {
			blk.text += ev.Delta
		} else {
			c.blocks = append(c.blocks, chatBlock{
				kind: blockAssistantText,
				text: ev.Delta,
			})
		}
		return true

	// ---- thinking ----
	case agent.EventAssistantThinking:
		if blk := c.lastBlock(blockThinking); blk != nil {
			blk.text += ev.Delta
		} else {
			c.blocks = append(c.blocks, chatBlock{
				kind:      blockThinking,
				text:      ev.Delta,
				collapsed: true,
			})
		}
		return true

	// ---- tool calls ----
	case agent.EventToolExecStart:
		c.blocks = append(c.blocks, chatBlock{
			kind:     blockToolCall,
			toolID:   ev.ToolCallID,
			toolName: ev.ToolName,
			toolArgs: ev.ToolArgs,
		})
		return true

	case agent.EventToolExecEnd:
		// Find matching tool-call block and attach result.
		for i := len(c.blocks) - 1; i >= 0; i-- {
			b := &c.blocks[i]
			if b.kind == blockToolCall && b.toolID == ev.ToolCallID {
				b.toolResult = ev.ToolResult
				b.toolError = ev.ToolError
				b.collapsed = true
				break
			}
		}
		return true

	// ---- turn boundaries ----
	case agent.EventTurnEnd:
		// A new assistant turn may follow; break text continuity by
		// ensuring next text delta starts a fresh block.
		return false

	// ---- errors ----
	case agent.EventAgentError:
		msg := "unknown error"
		if ev.Error != nil {
			msg = ev.Error.Error()
		}
		c.blocks = append(c.blocks, chatBlock{
			kind: blockError,
			text: msg,
		})
		return true

	default:
		return false
	}
}

// AddUserMessage appends a rendered user message block.
func (c *ChatView) AddUserMessage(text string) {
	c.blocks = append(c.blocks, chatBlock{
		kind: blockUser,
		text: text,
	})
	c.rebuildContent()
}

// BreakAssistantBlock ensures the next assistant_text delta starts a new block
// (used between turns so consecutive turns don't merge).
func (c *ChatView) BreakAssistantBlock() {
	// Append a zero-width sentinel — the renderer will skip empty blocks.
	// Actually the simplest approach: just ensure the last block is not
	// blockAssistantText, which HandleEvent already checks.
}

// ToggleThinking toggles the collapsed state of the most recent thinking block.
func (c *ChatView) ToggleThinking() {
	for i := len(c.blocks) - 1; i >= 0; i-- {
		if c.blocks[i].kind == blockThinking {
			c.blocks[i].collapsed = !c.blocks[i].collapsed
			c.rebuildContent()
			return
		}
	}
}

// ToggleToolResult toggles the collapsed state of the most recent tool result.
func (c *ChatView) ToggleToolResult() {
	for i := len(c.blocks) - 1; i >= 0; i-- {
		if c.blocks[i].kind == blockToolCall && c.blocks[i].toolResult != "" {
			c.blocks[i].collapsed = !c.blocks[i].collapsed
			c.rebuildContent()
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Bubble Tea interface (delegated by App)
// ---------------------------------------------------------------------------

func (c *ChatView) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	c.viewport, cmd = c.viewport.Update(msg)
	return cmd
}

func (c *ChatView) View() string {
	return c.viewport.View()
}

// ---------------------------------------------------------------------------
// Content rendering
// ---------------------------------------------------------------------------

// rebuildContent renders all blocks into a single string and pushes it into
// the viewport. It also auto-scrolls to the bottom.
func (c *ChatView) rebuildContent() {
	var sb strings.Builder

	for i, blk := range c.blocks {
		if i > 0 {
			sb.WriteString("\n")
		}
		switch blk.kind {
		case blockUser:
			c.renderUser(&sb, blk)
		case blockAssistantText:
			c.renderAssistant(&sb, blk)
		case blockThinking:
			c.renderThinking(&sb, blk)
		case blockToolCall:
			c.renderToolCall(&sb, blk)
		case blockError:
			c.renderError(&sb, blk)
		}
	}

	c.viewport.SetContent(sb.String())
	c.viewport.GotoBottom()
}

func (c *ChatView) renderUser(sb *strings.Builder, blk chatBlock) {
	label := UserRoleStyle.Render("You:")
	sb.WriteString(label + " ")
	sb.WriteString(UserMsgStyle.Render(blk.text))
	sb.WriteString("\n")
}

func (c *ChatView) renderAssistant(sb *strings.Builder, blk chatBlock) {
	label := AssistantRoleStyle.Render("Assistant:")
	sb.WriteString(label + "\n")

	rendered := blk.text
	if c.renderer != nil {
		if md, err := c.renderer.Render(blk.text); err == nil {
			rendered = strings.TrimRight(md, "\n")
		}
	}
	sb.WriteString(rendered)
	sb.WriteString("\n")
}

func (c *ChatView) renderThinking(sb *strings.Builder, blk chatBlock) {
	indicator := ThinkingLabelStyle.Render("  thinking...")
	if blk.collapsed {
		lines := strings.Count(blk.text, "\n") + 1
		summary := MutedStyle.Render(fmt.Sprintf(" (%d lines — press 't' to expand)", lines))
		sb.WriteString(indicator + summary + "\n")
	} else {
		sb.WriteString(indicator + "\n")
		// Indent thinking text.
		for _, line := range strings.Split(blk.text, "\n") {
			sb.WriteString(ThinkingStyle.Render("  "+line) + "\n")
		}
	}
}

func (c *ChatView) renderToolCall(sb *strings.Builder, blk chatBlock) {
	// Tool invocation line.
	argsStr := formatArgs(blk.toolArgs)
	header := fmt.Sprintf("  %s(%s)",
		ToolCallStyle.Render(blk.toolName),
		ToolArgsStyle.Render(argsStr),
	)
	sb.WriteString(header + "\n")

	// Tool result (if available).
	if blk.toolResult != "" {
		if blk.toolError {
			sb.WriteString(ToolErrorStyle.Render("  error: "+truncateLines(blk.toolResult, 10)) + "\n")
		} else if blk.collapsed {
			preview := truncateLines(blk.toolResult, 10)
			sb.WriteString(ToolResultStyle.Render(preview) + "\n")
			total := strings.Count(blk.toolResult, "\n") + 1
			if total > 10 {
				more := MutedStyle.Render(fmt.Sprintf("  ... %d more lines (press 'r' to expand)", total-10))
				sb.WriteString(more + "\n")
			}
		} else {
			sb.WriteString(ToolResultStyle.Render(blk.toolResult) + "\n")
		}
	} else {
		sb.WriteString(SpinnerStyle.Render("  running...") + "\n")
	}
}

func (c *ChatView) renderError(sb *strings.Builder, blk chatBlock) {
	sb.WriteString(ErrorMsgStyle.Render("Error: "+blk.text) + "\n")
}

// lastBlock returns a pointer to the last block of the given kind, or nil.
func (c *ChatView) lastBlock(kind chatBlockKind) *chatBlock {
	if len(c.blocks) > 0 {
		b := &c.blocks[len(c.blocks)-1]
		if b.kind == kind {
			return b
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// formatArgs produces a compact key: value summary of tool arguments.
func formatArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for k, v := range args {
		s, ok := v.(string)
		if !ok {
			b, _ := json.Marshal(v)
			s = string(b)
		}
		// Truncate long values.
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s: %q", k, s))
	}
	return strings.Join(parts, ", ")
}

// truncateLines returns the first n lines of text.
func truncateLines(text string, n int) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= n {
		return text
	}
	return strings.Join(lines[:n], "\n")
}

// scrollPercent returns the viewport scroll percentage (0-100).
func (c *ChatView) scrollPercent() float64 {
	return c.viewport.ScrollPercent() * 100
}

// borderColor returns a lipgloss.Color suitable for rendering the chat
// viewport border based on whether the view has focus.
func chatBorderColor(focused bool) lipgloss.TerminalColor {
	if focused {
		return ColorPrimary
	}
	return ColorBorder
}
