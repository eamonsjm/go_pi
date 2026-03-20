package tui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---------------------------------------------------------------------------
// Kill ring — emacs-style delete/yank ring
// ---------------------------------------------------------------------------

const maxKillRingSize = 32

type killDirection int

const (
	killNone     killDirection = iota
	killForward                // Ctrl+K, Alt+D
	killBackward               // Ctrl+U, Ctrl+W
)

type killRing struct {
	entries []string
	idx     int
	lastDir killDirection
}

func (kr *killRing) push(text string, dir killDirection) {
	if text == "" {
		return
	}
	if kr.lastDir == dir && len(kr.entries) > 0 {
		last := len(kr.entries) - 1
		if dir == killForward {
			kr.entries[last] += text
		} else {
			kr.entries[last] = text + kr.entries[last]
		}
	} else {
		kr.entries = append(kr.entries, text)
		if len(kr.entries) > maxKillRingSize {
			kr.entries = kr.entries[len(kr.entries)-maxKillRingSize:]
		}
	}
	kr.idx = len(kr.entries) - 1
	kr.lastDir = dir
}

func (kr *killRing) current() string {
	if len(kr.entries) == 0 {
		return ""
	}
	return kr.entries[kr.idx]
}

func (kr *killRing) prev() string {
	if len(kr.entries) == 0 {
		return ""
	}
	kr.idx--
	if kr.idx < 0 {
		kr.idx = len(kr.entries) - 1
	}
	return kr.entries[kr.idx]
}

func (kr *killRing) resetDirection() {
	kr.lastDir = killNone
}

// ---------------------------------------------------------------------------
// Undo stack
// ---------------------------------------------------------------------------

const maxUndoDepth = 100

type undoEntry struct {
	value string
	row   int
	col   int
}

type undoStack struct {
	entries []undoEntry
}

func (u *undoStack) push(value string, row, col int) {
	if len(u.entries) > 0 && u.entries[len(u.entries)-1].value == value {
		return
	}
	u.entries = append(u.entries, undoEntry{value: value, row: row, col: col})
	if len(u.entries) > maxUndoDepth {
		u.entries = u.entries[len(u.entries)-maxUndoDepth:]
	}
}

func (u *undoStack) pop() (undoEntry, bool) {
	if len(u.entries) == 0 {
		return undoEntry{}, false
	}
	entry := u.entries[len(u.entries)-1]
	u.entries = u.entries[:len(u.entries)-1]
	return entry, true
}

// diffKilled finds the contiguous substring removed from before that
// produced after. Works by matching common prefix and suffix.
func diffKilled(before, after string) string {
	if len(before) <= len(after) {
		return ""
	}
	rb := []rune(before)
	ra := []rune(after)
	prefixLen := 0
	for prefixLen < len(ra) && rb[prefixLen] == ra[prefixLen] {
		prefixLen++
	}
	suffixLen := 0
	for suffixLen < len(ra)-prefixLen &&
		rb[len(rb)-1-suffixLen] == ra[len(ra)-1-suffixLen] {
		suffixLen++
	}
	return string(rb[prefixLen : len(rb)-suffixLen])
}

// classifyKillKey returns the kill direction if msg is a kill-ring key.
func classifyKillKey(msg tea.KeyMsg) killDirection {
	switch msg.Type {
	case tea.KeyCtrlK:
		return killForward
	case tea.KeyCtrlU:
		return killBackward
	case tea.KeyCtrlW:
		return killBackward
	}
	if msg.Alt {
		if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && (msg.Runes[0] == 'd' || msg.Runes[0] == 'D') {
			return killForward
		}
		if msg.Type == tea.KeyDelete {
			return killForward
		}
		if msg.Type == tea.KeyBackspace {
			return killBackward
		}
	}
	return killNone
}

// isAltY returns true if msg is Alt+Y (yank-pop).
func isAltY(msg tea.KeyMsg) bool {
	return msg.Alt && msg.Type == tea.KeyRunes && len(msg.Runes) == 1 &&
		(msg.Runes[0] == 'y' || msg.Runes[0] == 'Y')
}

// isEditingKey returns true for keys that modify editor content.
func isEditingKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight,
		tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown,
		tea.KeyCtrlC, tea.KeyEscape, tea.KeyTab,
		tea.KeyCtrlLeft, tea.KeyCtrlRight, tea.KeyCtrlUp, tea.KeyCtrlDown:
		return false
	}
	if msg.Alt && msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		r := unicode.ToLower(msg.Runes[0])
		if r == 'f' || r == 'b' {
			return false // Alt+F/B are navigation
		}
	}
	return true
}

// editorState tracks whether the agent is idle, running, or thinking so the
// border color can reflect that.
type editorState int

const (
	editorIdle     editorState = iota
	editorRunning              // agent executing
	editorThinking             // model is thinking
)

// editorCommandMsg is sent when the user submits a slash command.
type editorCommandMsg struct {
	name string
	args string
}

// Editor wraps a bubbles/textarea and manages submission, steering, and
// keyboard shortcuts.
type Editor struct {
	textarea textarea.Model
	state    editorState
	width    int

	// commands is the registry used for slash command dispatch and autocomplete.
	commands *CommandRegistry

	// history stores previously submitted messages for up/down-arrow recall.
	// Index 0 is the oldest entry; len-1 is the most recent.
	history []string

	// historyIdx is the current position in the history ring.
	// When equal to len(history), the user is at the "empty" position
	// (i.e. composing a new message, not recalling an old one).
	historyIdx int

	// draft holds the in-progress text the user was typing before they
	// started scrolling through history, so it can be restored on down-arrow.
	draft string

	// ctrlCCount tracks consecutive Ctrl-C presses while idle so the user
	// must press it twice to quit.
	ctrlCCount int

	// tabMatches holds the set of matching commands during Tab cycling.
	// tabIndex is the current position in that set.  Both are reset on
	// any non-Tab keypress.
	tabMatches []*SlashCommand
	tabIndex   int

	// completer holds active @filepath tab completion state, or nil when
	// not completing.
	completer *fileCompletion

	// kills is the emacs-style kill ring for Ctrl+Y / Alt+Y yank.
	kills killRing

	// undo tracks editor states for Ctrl+_ undo.
	undo undoStack

	// yankLen is the rune length of the last yanked text. Zero means the
	// last operation was not a yank (disables yank-pop).
	yankLen int
}

// NewEditor creates an Editor ready for use.
func NewEditor() *Editor {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (Enter to send, Shift+Enter for newline)"
	ta.CharLimit = 0 // unlimited
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	// Don't focus immediately — terminal color query responses can leak into
	// the textarea before the TUI is fully initialized. Focus is deferred
	// until the first WindowSizeMsg arrives (see App.Update).

	// Style the textarea itself.
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Base = lipgloss.NewStyle().Foreground(ColorText)
	ta.BlurredStyle.Base = lipgloss.NewStyle().Foreground(ColorMuted)

	// Extend word navigation to also accept Ctrl+Left/Right.
	ta.KeyMap.WordForward = key.NewBinding(
		key.WithKeys("alt+right", "alt+f", "ctrl+right"),
	)
	ta.KeyMap.WordBackward = key.NewBinding(
		key.WithKeys("alt+left", "alt+b", "ctrl+left"),
	)

	return &Editor{
		textarea: ta,
		state:    editorIdle,
	}
}

// SetWidth adjusts the editor to the given terminal width.
func (e *Editor) SetWidth(w int) {
	e.width = w
	// Account for border (1 left + 1 right).
	inner := w - 2
	if inner < 10 {
		inner = 10
	}
	e.textarea.SetWidth(inner)
}

// SetState updates the editor state (affects border color).
func (e *Editor) SetState(s editorState) {
	e.state = s
	// Reset Ctrl-C counter whenever state changes.
	e.ctrlCCount = 0
}

// Focus gives the textarea keyboard focus.
func (e *Editor) Focus() {
	e.textarea.Focus()
}

// Blur removes keyboard focus from the textarea.
func (e *Editor) Blur() {
	e.textarea.Blur()
}

// Value returns the current textarea content.
func (e *Editor) Value() string {
	return e.textarea.Value()
}

// Reset clears the textarea content.
func (e *Editor) Reset() {
	e.textarea.Reset()
}

// SetCommands sets the command registry used for slash command parsing and
// autocomplete.
func (e *Editor) SetCommands(reg *CommandRegistry) {
	e.commands = reg
}

// Height returns the outer height (including border and any visible hint lines).
func (e *Editor) Height() int {
	// textarea lines + 2 for top/bottom border
	h := e.textarea.Height() + 2

	hint := e.fileCompletionHint()
	if hint == "" {
		hint = e.commandHint()
	}
	if hint != "" {
		h += strings.Count(hint, "\n") + 1 // hint lines + newline separator
	}
	return h
}

// ---------------------------------------------------------------------------
// Bubble Tea interface
// ---------------------------------------------------------------------------

// editorSubmitMsg is sent when the user presses Enter to submit text.
type editorSubmitMsg struct {
	text string
}

// editorSteerMsg is sent when the user submits text while the agent is running.
type editorSteerMsg struct {
	text string
}

// editorCancelMsg is sent when the user presses Ctrl-C or Escape to cancel.
type editorCancelMsg struct{}

// editorQuitMsg is sent when the user double-presses Ctrl-C while idle.
type editorQuitMsg struct{}

// editorShellResultMsg is sent when a shell command execution completes.
type editorShellResultMsg struct {
	command   string // The original command (without ! or !!)
	output    string // Command output
	sendToAI  bool   // true for !, false for !!
	errorMsg  string // Non-empty if command failed
}

// executeShellCommand runs a shell command and returns output and any error.
// It handles both ! (sendToAI=true) and !! (sendToAI=false) commands.
func executeShellCommand(fullText string) tea.Cmd {
	return func() tea.Msg {
		// Detect ! or !! prefix
		sendToAI := false
		command := ""

		if strings.HasPrefix(fullText, "!!") {
			sendToAI = false
			command = strings.TrimPrefix(fullText, "!!")
		} else if strings.HasPrefix(fullText, "!") {
			sendToAI = true
			command = strings.TrimPrefix(fullText, "!")
		}

		command = strings.TrimSpace(command)
		if command == "" {
			return editorShellResultMsg{
				command:  "",
				sendToAI: sendToAI,
				errorMsg: "no command provided",
			}
		}

		// Execute command using /bin/sh (more portable than /bin/bash)
		var buf bytes.Buffer
		cmd := exec.Command("/bin/sh", "-c", command)
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		cmd.Stdin = os.Stdin

		err := cmd.Run()
		output := buf.String()

		if err != nil {
			return editorShellResultMsg{
				command:  command,
				output:   output,
				sendToAI: sendToAI,
				errorMsg: err.Error(),
			}
		}

		return editorShellResultMsg{
			command:  command,
			output:   output,
			sendToAI: sendToAI,
			errorMsg: "",
		}
	}
}

func (e *Editor) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Snapshot for undo before any editing key.
		if isEditingKey(msg) {
			e.undo.push(
				e.textarea.Value(),
				e.textarea.Line(),
				e.textarea.LineInfo().ColumnOffset,
			)
		}

		// Detect kill keys for ring capture.
		killDir := classifyKillKey(msg)
		var preKillValue string
		if killDir != killNone {
			preKillValue = e.textarea.Value()
		}

		// Keys we fully intercept (never pass to textarea).
		switch msg.Type {

		case tea.KeyCtrlC:
			if e.state != editorIdle {
				e.ctrlCCount = 0
				return func() tea.Msg { return editorCancelMsg{} }
			}
			e.ctrlCCount++
			if e.ctrlCCount >= 2 {
				return func() tea.Msg { return editorQuitMsg{} }
			}
			return nil

		case tea.KeyCtrlD:
			if e.state == editorIdle && e.textarea.Value() == "" {
				return func() tea.Msg { return editorQuitMsg{} }
			}
			// Fall through to textarea for forward-delete.

		case tea.KeyEscape:
			if e.state != editorIdle {
				return func() tea.Msg { return editorCancelMsg{} }
			}
			return nil

		case tea.KeyTab:
			if e.handleTabCompletion() {
				return nil
			}
			return e.handleTab()

		case tea.KeyEnter:
			text := e.textarea.Value()
			if text == "" {
				return nil
			}
			if len(e.history) == 0 || e.history[len(e.history)-1] != text {
				e.history = append(e.history, text)
			}
			e.historyIdx = len(e.history)
			e.draft = ""
			e.textarea.Reset()
			e.ctrlCCount = 0

			// Handle shell commands (! and !! prefix)
			if strings.HasPrefix(text, "!") && !strings.HasPrefix(text, "//") {
				// Detect !! (local only) or ! (send to AI)
				if strings.HasPrefix(text, "!!") || strings.HasPrefix(text, "!") {
					return executeShellCommand(text)
				}
			}

			if strings.HasPrefix(text, "/") {
				name, args := parseSlashCommand(text)
				return func() tea.Msg { return editorCommandMsg{name: name, args: args} }
			}
			if e.state == editorIdle {
				return func() tea.Msg { return editorSubmitMsg{text: text} }
			}
			return func() tea.Msg { return editorSteerMsg{text: text} }

		case tea.KeyCtrlY:
			e.handleYank()
			return nil

		case tea.KeyCtrlUnderscore:
			e.handleUndo()
			return nil

		case tea.KeyUp:
			if len(e.history) > 0 && e.textarea.Line() == 0 {
				if e.historyIdx == len(e.history) {
					e.draft = e.textarea.Value()
				}
				if e.historyIdx > 0 {
					e.historyIdx--
					e.textarea.SetValue(e.history[e.historyIdx])
					e.textarea.CursorEnd()
					return nil
				}
				return nil
			}

		case tea.KeyDown:
			if len(e.history) > 0 &&
				e.textarea.Line() == e.textarea.LineCount()-1 &&
				e.historyIdx < len(e.history) {
				e.historyIdx++
				if e.historyIdx == len(e.history) {
					e.textarea.SetValue(e.draft)
				} else {
					e.textarea.SetValue(e.history[e.historyIdx])
				}
				e.textarea.CursorEnd()
				return nil
			}
		}

		// Alt+Y (yank-pop) — can't match in Type switch.
		if isAltY(msg) {
			e.handleYankPop()
			return nil
		}

		// Reset counters on non-matching keys.
		if msg.Type != tea.KeyCtrlC {
			e.ctrlCCount = 0
		}
		if msg.Type != tea.KeyTab {
			e.tabMatches = nil
			e.tabIndex = 0
			e.completer = nil
		}
		// Non-yank resets yank-pop tracking.
		if msg.Type != tea.KeyCtrlY {
			e.yankLen = 0
		}
		// Non-kill resets kill accumulation direction.
		if killDir == killNone {
			e.kills.resetDirection()
		}

		// Pass to textarea.
		var cmd tea.Cmd
		e.textarea, cmd = e.textarea.Update(msg)

		// Capture killed text for the ring.
		if killDir != killNone {
			killed := diffKilled(preKillValue, e.textarea.Value())
			e.kills.push(killed, killDir)
		}

		return cmd
	}

	var cmd tea.Cmd
	e.textarea, cmd = e.textarea.Update(msg)
	return cmd
}

// handleYank inserts the current kill-ring entry at the cursor.
func (e *Editor) handleYank() {
	text := e.kills.current()
	if text == "" {
		return
	}
	e.textarea.InsertString(text)
	e.yankLen = len([]rune(text))
}

// handleYankPop replaces the last yanked text with the previous kill-ring entry.
func (e *Editor) handleYankPop() {
	if e.yankLen == 0 {
		return
	}
	value := e.textarea.Value()
	runeOffset := e.cursorRuneOffset()
	runes := []rune(value)

	start := runeOffset - e.yankLen
	if start < 0 {
		start = 0
	}
	// Remove previously yanked text.
	remaining := append(append([]rune(nil), runes[:start]...), runes[runeOffset:]...)

	text := e.kills.prev()
	if text == "" {
		return
	}
	insertRunes := []rune(text)
	result := make([]rune, 0, len(remaining)+len(insertRunes))
	result = append(result, remaining[:start]...)
	result = append(result, insertRunes...)
	result = append(result, remaining[start:]...)

	e.textarea.SetValue(string(result))
	e.yankLen = len(insertRunes)
	e.navigateTo(string(result), start+len(insertRunes))
}

// handleUndo restores the previous editor state.
func (e *Editor) handleUndo() {
	entry, ok := e.undo.pop()
	if !ok {
		return
	}
	if entry.value == e.textarea.Value() {
		entry, ok = e.undo.pop()
		if !ok {
			return
		}
	}
	e.textarea.SetValue(entry.value)
	lines := strings.Split(entry.value, "\n")
	currentLine := len(lines) - 1
	for i := 0; i < currentLine-entry.row; i++ {
		e.textarea.CursorUp()
	}
	e.textarea.SetCursor(entry.col)
}

// View renders the editor with a styled border and an optional autocomplete
// hint line above it when the user is typing a slash command.
func (e *Editor) View() string {
	style := e.borderStyle()
	style = style.Width(e.width - 2) // account for border chars

	hint := e.fileCompletionHint()
	if hint == "" {
		hint = e.commandHint()
	}
	editor := style.Render(e.textarea.View())
	if hint != "" {
		return hint + "\n" + editor
	}
	return editor
}

// handleTabCompletion attempts @filepath tab completion. Returns true if
// the Tab keypress was consumed (an @ token was found), false otherwise.
func (e *Editor) handleTabCompletion() bool {
	if e.completer != nil {
		// Already in completion mode — cycle to next match.
		current := e.completer.matches[e.completer.idx]
		if strings.HasSuffix(current, "/") {
			// Current completion is a directory — drill into it.
			newMatches := completeFilePath(current)
			if len(newMatches) > 0 {
				e.completer.matches = newMatches
				e.completer.idx = 0
				e.applyCompletion()
				return true
			}
		}
		e.completer.idx = (e.completer.idx + 1) % len(e.completer.matches)
		e.applyCompletion()
		return true
	}

	// Look for an @token at the cursor position.
	runes := []rune(e.textarea.Value())
	cursor := e.cursorRuneOffset()
	partial, atStart, ok := findAtToken(runes, cursor)
	if !ok {
		return false
	}

	matches := completeFilePath(partial)
	if len(matches) == 0 {
		return false
	}

	e.completer = &fileCompletion{
		matches:    matches,
		idx:        0,
		textBefore: string(runes[:atStart]),
		textAfter:  string(runes[cursor:]),
	}
	e.applyCompletion()
	return true
}

// applyCompletion sets the textarea value to reflect the currently selected
// completion and positions the cursor right after the completed path.
func (e *Editor) applyCompletion() {
	c := e.completer
	completion := c.matches[c.idx]
	newText := c.textBefore + "@" + completion + c.textAfter

	e.textarea.SetValue(newText)

	// SetValue puts the cursor at the end. If there's text after the
	// completion we need to reposition the cursor.
	if c.textAfter != "" {
		targetRune := len([]rune(c.textBefore)) + 1 + len([]rune(completion))
		e.navigateTo(newText, targetRune)
	}
}

// cursorRuneOffset returns the cursor position as a rune offset in the
// full textarea value.
func (e *Editor) cursorRuneOffset() int {
	lines := strings.Split(e.textarea.Value(), "\n")
	row := e.textarea.Line()
	col := e.textarea.LineInfo().ColumnOffset

	offset := 0
	for i := 0; i < row && i < len(lines); i++ {
		offset += len([]rune(lines[i])) + 1 // +1 for newline
	}
	offset += col
	return offset
}

// navigateTo moves the textarea cursor to the given rune offset within text.
func (e *Editor) navigateTo(text string, runeOffset int) {
	lines := strings.Split(text, "\n")

	targetLine := 0
	targetCol := 0
	remaining := runeOffset

	for i, line := range lines {
		lineLen := len([]rune(line))
		if remaining <= lineLen {
			targetLine = i
			targetCol = remaining
			break
		}
		remaining -= lineLen + 1
	}

	// After SetValue cursor is on the last line — walk up.
	currentLine := len(lines) - 1
	for i := 0; i < currentLine-targetLine; i++ {
		e.textarea.CursorUp()
	}
	e.textarea.SetCursor(targetCol)
}

// fileCompletionHint returns a hint line showing available file completions
// when actively completing, or empty string otherwise.
func (e *Editor) fileCompletionHint() string {
	if e.completer == nil || len(e.completer.matches) <= 1 {
		return ""
	}

	const maxShow = 10
	matches := e.completer.matches
	truncated := false
	if len(matches) > maxShow {
		matches = matches[:maxShow]
		truncated = true
	}

	parts := make([]string, 0, len(matches))
	for _, m := range matches {
		parts = append(parts, m)
	}
	hint := strings.Join(parts, "  ")
	if truncated {
		hint += fmt.Sprintf("  (+%d more)", len(e.completer.matches)-maxShow)
	}
	return MutedStyle.Render(hint)
}

// commandHint returns an autocomplete hint string when the current input looks
// like an incomplete slash command, or empty string otherwise.
func (e *Editor) commandHint() string {
	if e.commands == nil {
		return ""
	}
	text := e.textarea.Value()
	if !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") {
		return ""
	}

	// Extract the partial command name (everything after "/" up to the first space).
	rest := text[1:]
	if idx := strings.IndexByte(rest, ' '); idx >= 0 {
		// Already has a space — user is typing args, no hint needed.
		return ""
	}

	matches := e.commands.Match(rest)
	if len(matches) == 0 {
		return ""
	}

	// Build a single hint line: "/name — description, /other — description"
	parts := make([]string, 0, len(matches))
	for _, cmd := range matches {
		parts = append(parts, fmt.Sprintf("/%s — %s", cmd.Name, cmd.Description))
	}
	return MutedStyle.Render(strings.Join(parts, "\n"))
}

// borderStyle returns the appropriate border style based on the current state.
func (e *Editor) borderStyle() lipgloss.Style {
	switch e.state {
	case editorRunning:
		return EditorActiveStyle
	case editorThinking:
		return EditorThinkingStyle
	default:
		return EditorStyle
	}
}

// handleTab performs slash-command tab completion.  On a unique match the
// partial is replaced with the full command name followed by a space.  When
// multiple commands match, repeated Tab presses cycle through them.
func (e *Editor) handleTab() tea.Cmd {
	if e.commands == nil {
		return nil
	}
	text := e.textarea.Value()
	if !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") {
		return nil
	}

	rest := text[1:]
	// If the text already contains a space the user is typing args — no
	// completion to offer.
	if strings.ContainsRune(rest, ' ') {
		return nil
	}

	// On first Tab press (no active cycle), compute matches from the typed
	// prefix.  On subsequent presses, reuse the existing match set and
	// advance the index.
	if e.tabMatches == nil {
		matches := e.commands.Match(rest)
		if len(matches) == 0 {
			return nil
		}
		e.tabMatches = matches
		e.tabIndex = 0
	} else {
		e.tabIndex = (e.tabIndex + 1) % len(e.tabMatches)
	}

	chosen := e.tabMatches[e.tabIndex]
	if len(e.tabMatches) == 1 {
		// Unique match — complete with a trailing space so the user can
		// start typing args immediately.
		e.textarea.SetValue("/" + chosen.Name + " ")
		e.textarea.CursorEnd()
		// Clear cycle state since completion is final.
		e.tabMatches = nil
		e.tabIndex = 0
	} else {
		// Multiple matches — show the current candidate without trailing
		// space so the user can keep cycling.
		e.textarea.SetValue("/" + chosen.Name)
		e.textarea.CursorEnd()
	}
	return nil
}

// parseSlashCommand splits "/name some args" into ("name", "some args").
func parseSlashCommand(text string) (name, args string) {
	// Strip the leading "/".
	text = text[1:]
	if idx := strings.IndexByte(text, ' '); idx >= 0 {
		return text[:idx], strings.TrimSpace(text[idx+1:])
	}
	return text, ""
}
