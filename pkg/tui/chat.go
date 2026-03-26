package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"
	"github.com/ejm/go_pi/pkg/agent"
	"github.com/ejm/go_pi/pkg/ai"
	"github.com/muesli/reflow/wordwrap"
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
	blockSystem
	blockPlugin
	blockCompaction
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

	// Plugin fields (blockPlugin only).
	pluginName string
	logLevel   string // "info", "warn", "error" — non-empty for log messages

	// streaming is true while the assistant is actively receiving deltas.
	// renderAssistant uses plain text with basic lipgloss styling instead
	// of glamour.Render() when streaming to avoid O(N²) re-rendering.
	streaming bool

	// rendered caches the last rendered output for this block. Empty means
	// the block needs re-rendering. Cleared whenever block content changes.
	rendered string

	// glamourPrefix holds glamour-rendered output for text[:prefixTextLen].
	// Set when transitioning from idle (non-streaming) back to streaming:
	// the idle glamour render is snapshotted as the prefix so previously
	// rendered text maintains its appearance. Cleared on resize, theme
	// change, or when streaming ends.
	glamourPrefix string
	prefixTextLen int
}

// ---------------------------------------------------------------------------
// ChatView
// ---------------------------------------------------------------------------

// ChatView renders the conversation history inside a scrollable viewport.
//
// A zero-value ChatView is not usable. Use [NewChatView] to construct one.
type ChatView struct {
	viewport viewport.Model
	blocks   []chatBlock
	width    int
	height   int

	// dirty is set when block content changes during streaming. The App's
	// tick-based render loop checks this flag and calls rebuildContent()
	// at ~30fps instead of on every delta.
	dirty bool

	// Markdown renderer (created once, re-used).
	renderer *glamour.TermRenderer

	// hasNewBelow is true when content has grown while the viewport is
	// scrolled away from the bottom. Cleared when the viewport reaches
	// the bottom again (via auto-scroll or user scroll).
	hasNewBelow bool
}

// ChatViewOption configures a ChatView during construction.
type ChatViewOption func(*chatViewConfig)

type chatViewConfig struct {
	glamourStyle string
	width        int
	height       int
}

// WithGlamourStyle overrides the theme's glamour style (e.g. "dark", "light").
func WithGlamourStyle(style string) ChatViewOption {
	return func(c *chatViewConfig) { c.glamourStyle = style }
}

// WithViewportSize sets the initial viewport dimensions.
func WithViewportSize(w, h int) ChatViewOption {
	return func(c *chatViewConfig) { c.width = w; c.height = h }
}

// NewChatView creates a ChatView with sensible defaults.
func NewChatView(opts ...ChatViewOption) *ChatView {
	cfg := &chatViewConfig{
		width:  80,
		height: 20,
	}
	for _, o := range opts {
		o(cfg)
	}

	vp := viewport.New(cfg.width, cfg.height)
	vp.SetContent("")
	vp.YPosition = 0

	glamourStyle := cfg.glamourStyle
	if glamourStyle == "" {
		glamourStyle = ActiveTheme().GlamourStyle
	}
	if glamourStyle == "" {
		glamourStyle = "dark"
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle),
		glamour.WithWordWrap(cfg.width-2),
	)
	if err != nil {
		log.Printf("tui: failed to create markdown renderer: %v", err)
	}

	return &ChatView{
		viewport: vp,
		renderer: r,
	}
}

// idleGlamourRender switches any actively streaming assistant block to
// non-streaming mode, triggering a glamour re-render on the next rebuild.
// Called when no text deltas arrive for 100ms during a streaming pause.
// The block's streaming flag is restored to true on the next delta
// (HandleEvent sets streaming=true on EventAssistantText).
func (c *ChatView) idleGlamourRender() {
	for i := range c.blocks {
		if c.blocks[i].kind == blockAssistantText && c.blocks[i].streaming {
			c.blocks[i].streaming = false
			c.blocks[i].rendered = ""
			c.blocks[i].glamourPrefix = ""
			c.blocks[i].prefixTextLen = 0
		}
	}
	c.rebuildContent()
}

// invalidateRenderCaches clears all cached block renders (e.g. on resize or
// theme change where every block must be re-rendered with new parameters).
func (c *ChatView) invalidateRenderCaches() {
	for i := range c.blocks {
		c.blocks[i].rendered = ""
		c.blocks[i].glamourPrefix = ""
		c.blocks[i].prefixTextLen = 0
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
	glamourStyle := ActiveTheme().GlamourStyle
	if glamourStyle == "" {
		glamourStyle = "dark"
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle),
		glamour.WithWordWrap(wrap),
	)
	if err != nil {
		log.Printf("tui: failed to recreate markdown renderer on resize (width=%d): %v", w, err)
	} else {
		c.renderer = r
	}

	c.invalidateRenderCaches()
	c.rebuildContent()
}

// AtBottom returns true if the viewport is at the bottom of the content.
func (c *ChatView) AtBottom() bool {
	return c.viewport.AtBottom()
}

// ---------------------------------------------------------------------------
// Event handlers — called by the parent App on each agent.Event
// ---------------------------------------------------------------------------

// HandleEvent processes a single agent event and updates the internal block
// list. Returns true if content changed (caller should re-render).
func (c *ChatView) HandleEvent(ev agent.Event) bool {
	switch ev.Type {

	// ---- streaming text ----
	case agent.EventAssistantText:
		if blk := c.lastBlock(blockAssistantText); blk != nil {
			// If transitioning from non-streaming (e.g., after idle
			// timeout), snapshot the current text as the glamour prefix
			// so incremental rendering continues without a styling flash.
			if !blk.streaming && c.renderer != nil {
				if md, err := c.renderer.Render(blk.text); err == nil {
					blk.glamourPrefix = strings.TrimRight(md, "\n")
					blk.prefixTextLen = len(blk.text)
				}
			}
			blk.text += ev.Delta
			blk.streaming = true
			blk.rendered = ""
		} else {
			c.blocks = append(c.blocks, chatBlock{
				kind:      blockAssistantText,
				text:      ev.Delta,
				streaming: true,
			})
		}
		c.dirty = true
		return true

	// ---- thinking ----
	case agent.EventAssistantThinking:
		if blk := c.lastBlock(blockThinking); blk != nil {
			blk.text += ev.Delta
			blk.rendered = ""
		} else {
			c.blocks = append(c.blocks, chatBlock{
				kind:      blockThinking,
				text:      ev.Delta,
				collapsed: true,
			})
		}
		c.dirty = true
		return true

	// ---- tool calls ----
	case agent.EventToolExecStart:
		c.blocks = append(c.blocks, chatBlock{
			kind:     blockToolCall,
			toolID:   ev.ToolCallID,
			toolName: ev.ToolName,
			toolArgs: ev.ToolArgs,
		})
		c.dirty = true
		return true

	case agent.EventToolExecEnd:
		// Find matching tool-call block and attach result.
		for i := len(c.blocks) - 1; i >= 0; i-- {
			b := &c.blocks[i]
			if b.kind == blockToolCall && b.toolID == ev.ToolCallID {
				b.toolResult = ev.ToolResult
				b.toolError = ev.ToolError
				b.collapsed = true
				b.rendered = ""
				break
			}
		}
		c.dirty = true
		return true

	// ---- turn boundaries ----
	case agent.EventTurnEnd:
		// Streaming is done — mark all assistant blocks as non-streaming
		// so they get a full glamour render on the next rebuild.
		for i := range c.blocks {
			if c.blocks[i].kind == blockAssistantText && c.blocks[i].streaming {
				c.blocks[i].streaming = false
				c.blocks[i].rendered = ""
				c.blocks[i].glamourPrefix = ""
				c.blocks[i].prefixTextLen = 0
			}
		}
		c.dirty = true
		return true

	// ---- agent lifecycle ----
	case agent.EventAgentEnd:
		// Safety net: finalize any blocks still marked as streaming.
		// In the normal case EventTurnEnd already cleared them, but if
		// the stream errored before EventTurnEnd fired, blocks may still
		// have streaming=true. Clear them so the next rebuildContent()
		// uses glamour.Render() for the final styled output.
		changed := false
		for i := range c.blocks {
			if c.blocks[i].kind == blockAssistantText && c.blocks[i].streaming {
				c.blocks[i].streaming = false
				c.blocks[i].rendered = ""
				c.blocks[i].glamourPrefix = ""
				c.blocks[i].prefixTextLen = 0
				changed = true
			}
		}
		return changed

	// ---- compaction ----
	case agent.EventCompaction:
		c.blocks = append(c.blocks, chatBlock{
			kind:      blockCompaction,
			text:      ev.Delta,
			collapsed: true,
		})
		c.dirty = true
		return true

	// ---- auto-compaction ----
	case agent.EventAutoCompaction:
		c.blocks = append(c.blocks, chatBlock{
			kind:      blockCompaction,
			text:      "Context automatically compacted to free space.\n\n" + ev.Delta,
			collapsed: true,
		})
		c.dirty = true
		return true

	// ---- errors ----
	case agent.EventAgentError:
		// Suppress context-canceled errors — these are expected when the
		// user presses Ctrl+C or Escape to interrupt a running prompt.
		if ev.Error != nil && errors.Is(ev.Error, context.Canceled) {
			return false
		}
		msg := "unknown error"
		if ev.Error != nil {
			var apiErr *ai.APIError
			if errors.As(ev.Error, &apiErr) {
				msg = apiErr.UserMessage()
			} else {
				msg = ev.Error.Error()
			}
		}
		// Deduplicate: the same error can arrive via the events channel
		// (EventAgentError emitted in run()) AND via AgentErrorMsg (Prompt()
		// return value). Skip if the last block is already an identical error.
		if n := len(c.blocks); n > 0 && c.blocks[n-1].kind == blockError && c.blocks[n-1].text == msg {
			return false
		}
		c.blocks = append(c.blocks, chatBlock{
			kind: blockError,
			text: msg,
		})
		c.dirty = true
		return true

	default:
		return false
	}
}

// AddUserMessage appends a rendered user message block.
// Always scrolls to bottom since the user actively submitted input.
func (c *ChatView) AddUserMessage(text string) {
	c.blocks = append(c.blocks, chatBlock{
		kind: blockUser,
		text: text,
	})
	c.rebuildContent()
	c.viewport.GotoBottom()
}

// AddSystemMessage appends a system/informational message block.
func (c *ChatView) AddSystemMessage(text string) {
	c.blocks = append(c.blocks, chatBlock{
		kind: blockSystem,
		text: text,
	})
	c.rebuildContent()
}

// AddPluginMessage appends a plugin inject or log message block with plugin
// attribution. Log messages (logLevel non-empty) are rendered with level
// indicators; inject messages are rendered as markdown content.
func (c *ChatView) AddPluginMessage(pluginName, content string, isLog bool, logLevel string) {
	lvl := ""
	if isLog {
		lvl = logLevel
		if lvl == "" {
			lvl = "info"
		}
	}
	c.blocks = append(c.blocks, chatBlock{
		kind:       blockPlugin,
		text:       content,
		pluginName: pluginName,
		logLevel:   lvl,
	})
	c.rebuildContent()
}

// RefreshTheme recreates the glamour renderer to match the active theme.
func (c *ChatView) RefreshTheme() {
	glamourStyle := ActiveTheme().GlamourStyle
	if glamourStyle == "" {
		glamourStyle = "dark"
	}
	wrap := c.width - 4
	if wrap < 40 {
		wrap = 40
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle),
		glamour.WithWordWrap(wrap),
	)
	if err == nil {
		c.renderer = r
	}
	c.invalidateRenderCaches()
	c.rebuildContent()
}

// ClearBlocks removes all chat blocks and resets the viewport.
func (c *ChatView) ClearBlocks() {
	c.blocks = nil
	c.rebuildContent()
}

// ToggleThinking toggles the collapsed state of the most recent thinking block.
func (c *ChatView) ToggleThinking() {
	for i := len(c.blocks) - 1; i >= 0; i-- {
		if c.blocks[i].kind == blockThinking {
			c.blocks[i].collapsed = !c.blocks[i].collapsed
			c.blocks[i].rendered = ""
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
			c.blocks[i].rendered = ""
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
	if c.viewport.AtBottom() {
		c.hasNewBelow = false
	}
	return cmd
}

func (c *ChatView) View() string {
	view := c.viewport.View()
	if !c.hasNewBelow {
		return view
	}

	// Overlay a "new content below" indicator on the last line.
	lines := strings.Split(view, "\n")
	if len(lines) == 0 {
		return view
	}

	indicator := Styles().NewContentBelowStyle.Render("↓ new content below ↓")
	indicatorWidth := ansi.StringWidth(indicator)

	padding := (c.width - indicatorWidth) / 2
	if padding < 0 {
		padding = 0
	}

	lines[len(lines)-1] = strings.Repeat(" ", padding) + indicator
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Content rendering
// ---------------------------------------------------------------------------

// rebuildContent renders all blocks into a single string and pushes it into
// the viewport. Auto-scrolls to the bottom only if the viewport was already
// at the bottom (so manual scroll-back is preserved during streaming).
//
// Uses per-block render caching: only blocks with an empty `rendered` field
// are re-rendered. During streaming this means only the active assistant
// block is re-rendered on each delta, turning the cost from O(all_blocks)
// to O(1 block).
func (c *ChatView) rebuildContent() {
	wasAtBottom := c.viewport.AtBottom()
	oldYOffset := c.viewport.YOffset

	var sb strings.Builder

	for i := range c.blocks {
		blk := &c.blocks[i]
		if blk.rendered == "" {
			var buf strings.Builder
			if i > 0 {
				buf.WriteString("\n")
			}
			c.renderBlock(&buf, blk)
			blk.rendered = buf.String()
		}
		sb.WriteString(blk.rendered)
	}

	c.viewport.SetContent(sb.String())
	c.dirty = false
	if wasAtBottom {
		c.viewport.GotoBottom()
		c.hasNewBelow = false
	} else {
		// Preserve scroll position when not at bottom (e.g. user scrolled
		// up during streaming). SetYOffset clamps to valid range, so if
		// content shrunk past the old offset we end up at the new bottom.
		c.viewport.SetYOffset(oldYOffset)
		c.hasNewBelow = true
	}
}

// renderBlock dispatches to the appropriate block renderer.
func (c *ChatView) renderBlock(sb *strings.Builder, blk *chatBlock) {
	switch blk.kind {
	case blockUser:
		c.renderUser(sb, *blk)
	case blockAssistantText:
		c.renderAssistant(sb, *blk)
	case blockThinking:
		c.renderThinking(sb, *blk)
	case blockToolCall:
		c.renderToolCall(sb, *blk)
	case blockError:
		c.renderError(sb, *blk)
	case blockSystem:
		c.renderSystem(sb, *blk)
	case blockPlugin:
		c.renderPlugin(sb, *blk)
	case blockCompaction:
		c.renderCompaction(sb, *blk)
	}
}

func (c *ChatView) renderUser(sb *strings.Builder, blk chatBlock) {
	s := Styles()
	label := s.UserRoleStyle.Render("You:")
	sb.WriteString(label + " ")
	w := c.width - ansi.StringWidth(label) - 1
	if w < 20 {
		w = 20
	}
	sb.WriteString(s.UserMsgStyle.Render(wordwrap.String(blk.text, w)))
	sb.WriteString("\n")
}

func (c *ChatView) renderAssistant(sb *strings.Builder, blk chatBlock) {
	label := Styles().AssistantRoleStyle.Render("Assistant:")
	sb.WriteString(label + "\n")

	if blk.streaming {
		// During streaming, show the glamour prefix (snapshotted from
		// the last idle timeout) plus word-wrapped plain text for the
		// new tail. Word-wrapping at the same width as glamour keeps
		// line counts stable and minimises jumps on transition.
		tail := blk.text[blk.prefixTextLen:]
		if blk.glamourPrefix != "" {
			sb.WriteString(blk.glamourPrefix)
			if tail != "" {
				sb.WriteString("\n")
			}
		}
		if tail != "" {
			w := c.width - 4
			if w < 40 {
				w = 40
			}
			sb.WriteString(Styles().AssistantMsgStyle.Render(wordwrap.String(tail, w)))
		}
	} else {
		rendered := blk.text
		if c.renderer != nil {
			if md, err := c.renderer.Render(blk.text); err == nil {
				rendered = strings.TrimRight(md, "\n")
			}
		}
		sb.WriteString(rendered)
	}
	sb.WriteString("\n")
}

func (c *ChatView) renderThinking(sb *strings.Builder, blk chatBlock) {
	s := Styles()
	indicator := s.ThinkingLabelStyle.Render("  thinking...")
	if blk.collapsed {
		lines := strings.Count(blk.text, "\n") + 1
		summary := s.MutedStyle.Render(fmt.Sprintf(" (%d lines — Ctrl+T to expand)", lines))
		sb.WriteString(indicator + summary + "\n")
	} else {
		sb.WriteString(indicator + "\n")
		// Indent thinking text.
		for _, line := range strings.Split(blk.text, "\n") {
			sb.WriteString(s.ThinkingStyle.Render("  "+line) + "\n")
		}
	}
}

func (c *ChatView) renderToolCall(sb *strings.Builder, blk chatBlock) {
	s := Styles()

	// Tool invocation line.
	argsStr := formatArgs(blk.toolArgs)
	header := fmt.Sprintf("  %s(%s)",
		s.ToolCallStyle.Render(blk.toolName),
		s.ToolArgsStyle.Render(argsStr),
	)
	sb.WriteString(header + "\n")

	// Tool result (if available).
	if blk.toolResult != "" {
		if blk.toolError {
			sb.WriteString(s.ToolErrorStyle.Render("  error: "+truncateLines(blk.toolResult, 10)) + "\n")
		} else if blk.collapsed {
			preview := truncateLines(blk.toolResult, 10)
			sb.WriteString(s.ToolResultStyle.Render(preview) + "\n")
			total := strings.Count(blk.toolResult, "\n") + 1
			if total > 10 {
				more := s.MutedStyle.Render(fmt.Sprintf("  ... %d more lines (Ctrl+R to expand)", total-10))
				sb.WriteString(more + "\n")
			}
		} else {
			sb.WriteString(s.ToolResultStyle.Render(blk.toolResult) + "\n")
		}
	} else {
		sb.WriteString(s.SpinnerStyle.Render("  running...") + "\n")
	}
}

func (c *ChatView) renderError(sb *strings.Builder, blk chatBlock) {
	w := c.msgWidth()
	sb.WriteString(Styles().ErrorMsgStyle.Render(wordwrap.String("Error: "+blk.text, w)) + "\n")
}

func (c *ChatView) renderSystem(sb *strings.Builder, blk chatBlock) {
	w := c.msgWidth()
	sb.WriteString(Styles().SystemMsgStyle.Render(wordwrap.String(blk.text, w)) + "\n")
}

func (c *ChatView) renderPlugin(sb *strings.Builder, blk chatBlock) {
	s := Styles()
	label := s.PluginNameStyle.Render(fmt.Sprintf("[%s]", blk.pluginName))

	if blk.logLevel != "" {
		// Log message with level-appropriate styling, wrapped to fit.
		w := c.width - ansi.StringWidth(label) - 1
		if w < 20 {
			w = 20
		}
		switch blk.logLevel {
		case "error":
			sb.WriteString(label + " " + s.ErrorMsgStyle.Render(wordwrap.String(blk.text, w)) + "\n")
		case "warn":
			sb.WriteString(label + " " + s.PluginLogWarnStyle.Render(wordwrap.String(blk.text, w)) + "\n")
		default:
			sb.WriteString(label + " " + s.MutedStyle.Render(wordwrap.String(blk.text, w)) + "\n")
		}
		return
	}

	// Inject message — render content as markdown with plugin attribution.
	sb.WriteString(label + "\n")
	rendered := blk.text
	if c.renderer != nil {
		if md, err := c.renderer.Render(blk.text); err == nil {
			rendered = strings.TrimRight(md, "\n")
		}
	}
	sb.WriteString(rendered + "\n")
}

func (c *ChatView) renderCompaction(sb *strings.Builder, blk chatBlock) {
	s := Styles()
	header := s.SystemMsgStyle.Render("--- Context compacted ---")
	sb.WriteString(header + "\n")
	if blk.collapsed {
		lines := strings.Count(blk.text, "\n") + 1
		summary := s.MutedStyle.Render(fmt.Sprintf("  (%d lines — press 'c' to expand)", lines))
		sb.WriteString(summary + "\n")
	} else {
		rendered := blk.text
		if c.renderer != nil {
			if md, err := c.renderer.Render(blk.text); err == nil {
				rendered = strings.TrimRight(md, "\n")
			}
		}
		sb.WriteString(rendered + "\n")
	}
}

// AddCompactionBlock replaces all existing blocks with a compaction summary block.
func (c *ChatView) AddCompactionBlock(summary string) {
	c.blocks = nil
	c.blocks = append(c.blocks, chatBlock{
		kind:      blockCompaction,
		text:      summary,
		collapsed: true,
	})
	c.rebuildContent()
}

// ToggleCompaction toggles the collapsed state of the most recent compaction block.
func (c *ChatView) ToggleCompaction() {
	for i := len(c.blocks) - 1; i >= 0; i-- {
		if c.blocks[i].kind == blockCompaction {
			c.blocks[i].collapsed = !c.blocks[i].collapsed
			c.blocks[i].rendered = ""
			c.rebuildContent()
			return
		}
	}
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

// msgWidth returns the available width for full-width message text.
func (c *ChatView) msgWidth() int {
	w := c.width - 2
	if w < 20 {
		w = 20
	}
	return w
}

// formatArgs produces a compact key: value summary of tool arguments.
func formatArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(args))
	for _, k := range keys {
		v := args[k]
		s, ok := v.(string)
		if !ok {
			b, err := json.Marshal(v)
			if err != nil {
				s = fmt.Sprintf("<%T>", v)
			} else {
				s = string(b)
			}
		}
		// Truncate long values (rune-aware to avoid splitting multi-byte UTF-8).
		if utf8.RuneCountInString(s) > 60 {
			r := []rune(s)
			s = string(r[:57]) + "..."
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
