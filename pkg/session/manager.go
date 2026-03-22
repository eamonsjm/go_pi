package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"unicode/utf8"

	"github.com/ejm/go_pi/pkg/ai"
)

// truncatePreview truncates s to at most maxLen bytes on a valid UTF-8 boundary
// and appends "..." if the string was shortened.
func truncatePreview(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	// Back up from the target cut point to avoid splitting a multi-byte rune.
	truncLen := maxLen - 3 // leave room for "..."
	for truncLen > 0 && !utf8.RuneStart(s[truncLen]) {
		truncLen--
	}
	return s[:truncLen] + "..."
}

// Entry represents a single session entry persisted as one line of JSONL.
type Entry struct {
	ID        string    `json:"id"`
	ParentID  string    `json:"parent_id,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"` // "message", "model_change", "thinking_change", "compaction", "info"
	Data      any       `json:"data"`
}

// MessageData is the Data payload for entries of type "message".
type MessageData struct {
	Role    ai.Role           `json:"role"`
	Content []ai.ContentBlock `json:"content"`
}

// SessionInfo provides summary metadata about a session.
type SessionInfo struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Entries   int       `json:"entries"`
	Preview   string    `json:"preview,omitempty"` // first line of last user message
	Branches  int       `json:"branches,omitempty"`
}

// BranchInfo describes a single branch in a session tree.
type BranchInfo struct {
	LeafID    string    // ID of the leaf entry on this branch
	Depth     int       // Number of entries from root to leaf
	Preview   string    // Preview of the last user message on this branch
	UpdatedAt time.Time // Timestamp of the leaf entry
	IsActive  bool      // Whether this is the current active branch
}

// Manager handles session persistence using JSONL files.
// All exported methods are safe for concurrent use.
type Manager struct {
	mu              sync.RWMutex
	dir             string // root directory, e.g. ~/.gi/sessions/
	current         string // active session ID
	entries         []Entry
	activeBranch    string          // leaf entry ID of the current branch
	skippedToolUses map[string]bool // tool_use IDs skipped by dedup (transient, not persisted)
}

// NewManager creates a new session manager rooted at the given directory.
// The directory is created if it does not exist.
func NewManager(dir string) *Manager {
	return &Manager{
		dir:             dir,
		skippedToolUses: make(map[string]bool),
	}
}

// NewSession creates a new session and returns its ID.
func (m *Manager) NewSession() string {
	id := generateID()
	m.mu.Lock()
	m.current = id
	m.entries = nil
	m.activeBranch = ""
	m.mu.Unlock()
	return id
}

// CurrentID returns the ID of the active session, or empty if none.
func (m *Manager) CurrentID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.current
}

// LoadSession loads an existing session from disk by ID.
func (m *Manager) LoadSession(id string) (retErr error) {
	path := m.sessionPath(id)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open session %s: %w", id, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && retErr == nil {
			retErr = fmt.Errorf("close session %s: %w", id, cerr)
		}
	}()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	// Allow large lines for messages with big tool results.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return fmt.Errorf("parse session entry: %w", err)
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read session %s: %w", id, err)
	}

	normalizeParentIDs(entries)

	var branch string
	if len(entries) > 0 {
		branch = entries[len(entries)-1].ID
	}

	m.mu.Lock()
	m.current = id
	m.entries = entries
	m.activeBranch = branch
	m.mu.Unlock()
	return nil
}

// ListSessions returns metadata for all sessions on disk, sorted by
// most recently updated first.
func (m *Manager) ListSessions() []SessionInfo {
	pattern := filepath.Join(m.dir, "*.jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil
	}

	var sessions []SessionInfo
	for _, path := range matches {
		base := filepath.Base(path)
		id := strings.TrimSuffix(base, ".jsonl")
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		si := SessionInfo{
			ID:        id,
			UpdatedAt: info.ModTime(),
		}
		// Read entries for creation time, count, preview, and branch count.
		if f, err := os.Open(path); err == nil {
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
			count := 0
			var allEntries []Entry
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" {
					continue
				}
				count++
				var e Entry
				if err := json.Unmarshal([]byte(line), &e); err != nil {
					continue
				}
				allEntries = append(allEntries, e)
				if count == 1 {
					si.CreatedAt = e.Timestamp
				}
				// Track the last user message for preview.
				if e.Type == "message" {
					if msg, ok := entryToMessage(e); ok && msg.Role == ai.RoleUser {
						text := msg.GetText()
						if first, _, ok := strings.Cut(text, "\n"); ok {
							si.Preview = first
						} else {
							si.Preview = text
						}
						si.Preview = truncatePreview(si.Preview, 80)
					}
				}
			}
			if err := scanner.Err(); err != nil {
				log.Printf("session: read %s: %v", path, err)
			}
			si.Entries = count
			normalizeParentIDs(allEntries)
			si.Branches = len(findLeafEntries(allEntries))
			if err := f.Close(); err != nil {
				log.Printf("session: close %s: %v", path, err)
			}
		}
		sessions = append(sessions, si)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions
}

// LatestSessionID returns the ID of the most recently updated session, or
// empty string if no sessions exist on disk.
func (m *Manager) LatestSessionID() string {
	sessions := m.ListSessions()
	if len(sessions) == 0 {
		return ""
	}
	return sessions[0].ID
}

// AppendEntry appends a single entry to the current session's JSONL file
// and the in-memory entry list. The entry's ParentID is automatically set
// to the current active branch leaf if not already specified.
func (m *Manager) AppendEntry(entry Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current == "" {
		return fmt.Errorf("no active session")
	}

	// Link to active branch if no explicit parent.
	if entry.ParentID == "" && m.activeBranch != "" {
		entry.ParentID = m.activeBranch
	}

	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal entry: %w", err)
	}

	path := m.sessionPath(m.current)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open session file: %w", err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return fmt.Errorf("write entry: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close session file: %w", err)
	}

	m.entries = append(m.entries, entry)
	m.activeBranch = entry.ID
	return nil
}

// GetMessages reconstructs the conversation messages for the active branch
// from the current session's entries. In a tree-structured session, only
// entries on the path from root to the active branch leaf are included.
// Non-message entries are skipped. Any orphaned tool_use blocks (without
// matching tool_result) are repaired with synthetic error results.
func (m *Manager) GetMessages() []ai.Message {
	m.mu.RLock()
	entries := make([]Entry, len(m.entries))
	copy(entries, m.entries)
	branch := m.activeBranch
	m.mu.RUnlock()

	branchEntries := getEntriesOnBranch(entries, branch)

	var msgs []ai.Message
	for _, e := range branchEntries {
		if e.Type != "message" {
			continue
		}
		msg, ok := entryToMessage(e)
		if ok {
			msgs = append(msgs, msg)
		}
	}
	return RepairOrphanedToolUse(msgs)
}

// RepairOrphanedToolUse scans messages for assistant tool_use blocks that lack
// corresponding tool_result responses. For each orphan, a synthetic error
// tool_result is injected so the conversation is valid for the API.
// Clean conversations (no orphans) are returned unmodified.
func RepairOrphanedToolUse(msgs []ai.Message) []ai.Message {
	if len(msgs) == 0 {
		return msgs
	}

	// First pass: find orphaned tool_use IDs and where to insert synthetic results.
	type orphanGroup struct {
		afterIndex int      // insert synthetic results after this message index
		toolUseIDs []string // IDs needing synthetic results, in original order
	}
	var orphans []orphanGroup

	for i, msg := range msgs {
		if msg.Role != ai.RoleAssistant {
			continue
		}
		toolCalls := msg.GetToolCalls()
		if len(toolCalls) == 0 {
			continue
		}

		// Collect tool_use IDs from this assistant message.
		needed := make(map[string]bool, len(toolCalls))
		for _, tc := range toolCalls {
			needed[tc.ToolUseID] = true
		}

		// Scan subsequent non-assistant messages for matching tool_results.
		lastIdx := i
		for j := i + 1; j < len(msgs); j++ {
			if msgs[j].Role == ai.RoleAssistant {
				break
			}
			lastIdx = j
			for _, cb := range msgs[j].Content {
				if cb.Type == ai.ContentTypeToolResult {
					delete(needed, cb.ToolResultID)
				}
			}
		}

		if len(needed) == 0 {
			continue
		}

		// Collect orphan IDs in original tool_use order.
		var ids []string
		for _, tc := range toolCalls {
			if needed[tc.ToolUseID] {
				ids = append(ids, tc.ToolUseID)
			}
		}
		orphans = append(orphans, orphanGroup{afterIndex: lastIdx, toolUseIDs: ids})
	}

	if len(orphans) == 0 {
		return msgs
	}

	// Second pass: build result with synthetic tool_results inserted.
	insertAfter := make(map[int][]string, len(orphans))
	for _, og := range orphans {
		insertAfter[og.afterIndex] = og.toolUseIDs
	}

	result := make([]ai.Message, 0, len(msgs)+len(orphans)*2)
	for i, msg := range msgs {
		result = append(result, msg)
		if ids, ok := insertAfter[i]; ok {
			for _, id := range ids {
				result = append(result, ai.NewToolResultMessage(
					id,
					"Tool execution interrupted — session was resumed",
					true,
				))
			}
		}
	}

	return result
}

// SaveMessage is a convenience method that wraps an ai.Message as an Entry
// and appends it. Duplicate assistant tool_use messages are detected and
// silently skipped to prevent session bloat from model retries (e.g. after
// crash recovery where RepairOrphanedToolUse injects synthetic errors).
func (m *Manager) SaveMessage(msg ai.Message) error {
	// Skip tool_result messages that reference deduplicated tool_use IDs.
	if msg.Role == ai.RoleUser && m.shouldSkipToolResult(msg) {
		return nil
	}

	// Skip assistant messages whose tool_use blocks are content-identical
	// to those in the most recent assistant entry.
	if msg.Role == ai.RoleAssistant {
		if skippedIDs := m.findDuplicateToolUseIDs(msg); len(skippedIDs) > 0 {
			m.mu.Lock()
			for _, id := range skippedIDs {
				m.skippedToolUses[id] = true
			}
			m.mu.Unlock()
			return nil
		}
	}

	entry := Entry{
		ID:        generateID(),
		Timestamp: time.Now().UTC(),
		Type:      "message",
		Data: MessageData{
			Role:    msg.Role,
			Content: msg.Content,
		},
	}
	return m.AppendEntry(entry)
}

// findDuplicateToolUseIDs checks if an assistant message's tool_use blocks
// are content-identical (same name and input, ignoring API-generated IDs) to
// those in the most recent assistant entry. Returns the new message's tool_use
// IDs if duplicate, nil otherwise.
func (m *Manager) findDuplicateToolUseIDs(msg ai.Message) []string {
	newCalls := msg.GetToolCalls()
	if len(newCalls) == 0 {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Walk backwards to find the last assistant message with tool_use blocks.
	for i := len(m.entries) - 1; i >= 0 && i >= len(m.entries)-20; i-- {
		e := m.entries[i]
		if e.Type != "message" {
			continue
		}
		prevMsg, ok := entryToMessage(e)
		if !ok || prevMsg.Role != ai.RoleAssistant {
			continue
		}
		prevCalls := prevMsg.GetToolCalls()
		if len(prevCalls) == 0 {
			return nil // last assistant had no tool calls — not a dup
		}
		if toolCallsContentEqual(newCalls, prevCalls) {
			ids := make([]string, len(newCalls))
			for j, tc := range newCalls {
				ids[j] = tc.ToolUseID
			}
			return ids
		}
		return nil // last assistant had different tool calls — not a dup
	}

	return nil
}

// shouldSkipToolResult returns true if every tool_result block in msg
// references a tool_use ID that was previously skipped by dedup. When true,
// the matched IDs are removed from the skip set.
func (m *Manager) shouldSkipToolResult(msg ai.Message) bool {
	var resultIDs []string
	for _, cb := range msg.Content {
		if cb.Type == ai.ContentTypeToolResult {
			resultIDs = append(resultIDs, cb.ToolResultID)
		}
	}
	if len(resultIDs) == 0 {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.skippedToolUses) == 0 {
		return false
	}
	for _, id := range resultIDs {
		if !m.skippedToolUses[id] {
			return false
		}
	}

	// All tool_results reference skipped IDs — clean up and skip.
	for _, id := range resultIDs {
		delete(m.skippedToolUses, id)
	}
	return true
}

// toolCallsContentEqual returns true if two sets of tool_use blocks have
// identical tool names and inputs, ignoring API-generated tool_use IDs.
func toolCallsContentEqual(a, b []ai.ContentBlock) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ToolName != b[i].ToolName {
			return false
		}
		if !jsonEqual(a[i].Input, b[i].Input) {
			return false
		}
	}
	return true
}

// jsonEqual compares two values by their JSON representation.
func jsonEqual(a, b any) bool {
	aj, aerr := json.Marshal(a)
	bj, berr := json.Marshal(b)
	if aerr != nil || berr != nil {
		return false
	}
	return string(aj) == string(bj)
}

// FilePath returns the file path of the current session, or empty if none.
func (m *Manager) FilePath() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == "" {
		return ""
	}
	return m.sessionPath(m.current)
}

// ActiveBranch returns the leaf entry ID of the current branch.
func (m *Manager) ActiveBranch() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.activeBranch
}

// ForkAt sets the active branch to the given entry ID, so that subsequent
// appends create a new branch from that point. Returns an error if the
// entry ID is not found in the current session.
func (m *Manager) ForkAt(entryID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, e := range m.entries {
		if e.ID == entryID {
			m.activeBranch = entryID
			return nil
		}
	}
	return fmt.Errorf("entry %s not found in session", entryID)
}

// SwitchBranch changes the active branch to the one identified by the given
// leaf entry ID.
func (m *Manager) SwitchBranch(leafID string) error {
	return m.ForkAt(leafID)
}

// GetBranches returns information about all branches in the current session.
// A branch is identified by its leaf entry (an entry that is not any other
// entry's parent).
func (m *Manager) GetBranches() []BranchInfo {
	m.mu.RLock()
	entries := make([]Entry, len(m.entries))
	copy(entries, m.entries)
	branch := m.activeBranch
	m.mu.RUnlock()

	leaves := findLeafEntries(entries)
	byID := buildEntryIndex(entries)

	var branches []BranchInfo
	for _, leaf := range leaves {
		bi := BranchInfo{
			LeafID:    leaf.ID,
			UpdatedAt: leaf.Timestamp,
			IsActive:  leaf.ID == branch,
		}

		// Walk back to count depth and find preview.
		depth := 0
		currentID := leaf.ID
		visited := make(map[string]bool)
		for currentID != "" && !visited[currentID] {
			visited[currentID] = true
			e, ok := byID[currentID]
			if !ok {
				break
			}
			depth++
			if bi.Preview == "" && e.Type == "message" {
				if msg, ok := entryToMessage(e); ok && msg.Role == ai.RoleUser {
					text := msg.GetText()
					if first, _, ok := strings.Cut(text, "\n"); ok {
						bi.Preview = first
					} else {
						bi.Preview = text
					}
					bi.Preview = truncatePreview(bi.Preview, 60)
				}
			}
			currentID = e.ParentID
		}
		bi.Depth = depth
		branches = append(branches, bi)
	}

	// Sort: active branch first, then by most recently updated.
	sort.Slice(branches, func(i, j int) bool {
		if branches[i].IsActive != branches[j].IsActive {
			return branches[i].IsActive
		}
		return branches[i].UpdatedAt.After(branches[j].UpdatedAt)
	})

	return branches
}

// HasBranches returns true if the session has more than one branch.
func (m *Manager) HasBranches() bool {
	m.mu.RLock()
	entries := make([]Entry, len(m.entries))
	copy(entries, m.entries)
	m.mu.RUnlock()

	return len(findLeafEntries(entries)) > 1
}

// GetEntries returns a copy of all entries in the current session.
func (m *Manager) GetEntries() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := make([]Entry, len(m.entries))
	copy(cp, m.entries)
	return cp
}

// GetBranchMessages returns the messages for a specific branch identified
// by its leaf entry ID.
func (m *Manager) GetBranchMessages(leafID string) []ai.Message {
	m.mu.RLock()
	entries := make([]Entry, len(m.entries))
	copy(entries, m.entries)
	m.mu.RUnlock()

	branchEntries := getEntriesOnBranch(entries, leafID)

	var msgs []ai.Message
	for _, e := range branchEntries {
		if e.Type != "message" {
			continue
		}
		msg, ok := entryToMessage(e)
		if ok {
			msgs = append(msgs, msg)
		}
	}
	return RepairOrphanedToolUse(msgs)
}

// FormatTree returns a text representation of the session's branch structure.
func (m *Manager) FormatTree() string {
	m.mu.RLock()
	entries := make([]Entry, len(m.entries))
	copy(entries, m.entries)
	branch := m.activeBranch
	m.mu.RUnlock()

	if len(entries) == 0 {
		return "No entries in session."
	}

	leaves := findLeafEntries(entries)
	if len(leaves) <= 1 {
		return "Session is linear (no branches)."
	}

	byID := buildEntryIndex(entries)

	// Sort: active first, then by timestamp.
	sort.Slice(leaves, func(i, j int) bool {
		iActive := leaves[i].ID == branch
		jActive := leaves[j].ID == branch
		if iActive != jActive {
			return iActive
		}
		return leaves[i].Timestamp.After(leaves[j].Timestamp)
	})

	var sb strings.Builder
	fmt.Fprintf(&sb, "Session branches (%d):\n", len(leaves))
	for i, leaf := range leaves {
		// Count messages on this branch.
		chain := getEntriesOnBranch(entries, leaf.ID)
		msgCount := 0
		var lastUserMsg string
		for _, e := range chain {
			if e.Type == "message" {
				msgCount++
				if msg, ok := entryToMessage(e); ok && msg.Role == ai.RoleUser {
					text := msg.GetText()
					if first, _, ok := strings.Cut(text, "\n"); ok {
						lastUserMsg = first
					} else {
						lastUserMsg = text
					}
				}
			}
		}
		lastUserMsg = truncatePreview(lastUserMsg, 50)

		// Find fork point (first entry on this branch that differs from other branches).
		forkDepth := findForkDepth(entries, byID, leaf.ID, leaves)

		prefix := "├──"
		if i == len(leaves)-1 {
			prefix = "└──"
		}

		active := ""
		if leaf.ID == branch {
			active = " ← active"
		}

		line := fmt.Sprintf("  %s [%d] %d msgs", prefix, i+1, msgCount)
		if forkDepth > 0 {
			line += fmt.Sprintf(" (forked at depth %d)", forkDepth)
		}
		if lastUserMsg != "" {
			line += fmt.Sprintf("  %q", lastUserMsg)
		}
		line += active
		sb.WriteString(line + "\n")
	}
	sb.WriteString("\nUse /tree <number> to switch branches.")
	return sb.String()
}

// GetUserEntries returns message entries with role "user" on the active branch,
// ordered from root to leaf. This is useful for the /fork command to let users
// select a fork point by user message index.
func (m *Manager) GetUserEntries() []Entry {
	m.mu.RLock()
	entries := make([]Entry, len(m.entries))
	copy(entries, m.entries)
	branch := m.activeBranch
	m.mu.RUnlock()

	branchEntries := getEntriesOnBranch(entries, branch)

	var userEntries []Entry
	for _, e := range branchEntries {
		if e.Type != "message" {
			continue
		}
		if msg, ok := entryToMessage(e); ok && msg.Role == ai.RoleUser {
			userEntries = append(userEntries, e)
		}
	}
	return userEntries
}

// CollectUserPrompts reads all session files and extracts unique user prompt
// texts, returned most-recent-first. The result is deduplicated and limited
// to at most maxPrompts entries. This is used for reverse-search (ctrl+r).
func (m *Manager) CollectUserPrompts(maxPrompts int) []string {
	pattern := filepath.Join(m.dir, "*.jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil
	}

	// Collect (text, timestamp) pairs from all sessions.
	type promptEntry struct {
		text string
		ts   time.Time
	}
	seen := make(map[string]bool)
	var prompts []promptEntry

	for _, path := range matches {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var e Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				continue
			}
			if e.Type != "message" {
				continue
			}
			msg, ok := entryToMessage(e)
			if !ok || msg.Role != ai.RoleUser {
				continue
			}
			text := strings.TrimSpace(msg.GetText())
			if text == "" || seen[text] {
				continue
			}
			seen[text] = true
			prompts = append(prompts, promptEntry{text: text, ts: e.Timestamp})
		}
		if err := scanner.Err(); err != nil {
			log.Printf("session: read %s: %v", path, err)
		}
		if err := f.Close(); err != nil {
			log.Printf("session: close %s: %v", path, err)
		}
	}

	// Sort most-recent-first.
	sort.Slice(prompts, func(i, j int) bool {
		return prompts[i].ts.After(prompts[j].ts)
	})

	if len(prompts) > maxPrompts {
		prompts = prompts[:maxPrompts]
	}

	result := make([]string, len(prompts))
	for i, p := range prompts {
		result[i] = p.text
	}
	return result
}

// --- internal ---------------------------------------------------------------

func (m *Manager) sessionPath(id string) string {
	return filepath.Join(m.dir, id+".jsonl")
}

// entryToMessage converts an Entry with type "message" into an ai.Message.
// The Data field may be a MessageData struct (in-memory) or a map (from JSON).
func entryToMessage(e Entry) (ai.Message, bool) {
	var msg ai.Message
	switch v := e.Data.(type) {
	case MessageData:
		msg = ai.Message{Role: v.Role, Content: v.Content}
	case map[string]any:
		// Re-marshal and unmarshal for entries loaded from JSON.
		raw, err := json.Marshal(v)
		if err != nil {
			return ai.Message{}, false
		}
		var md MessageData
		if err := json.Unmarshal(raw, &md); err != nil {
			return ai.Message{}, false
		}
		msg = ai.Message{Role: md.Role, Content: md.Content}
	default:
		return ai.Message{}, false
	}
	// Ensure Content is never nil — nil marshals to JSON null, which the
	// Anthropic API rejects with "should be a valid list".
	msg.EnsureContent()
	return msg, true
}

// generateID creates a random 16-character hex ID.
func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID — should never happen.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// normalizeParentIDs fills in missing ParentIDs for legacy sessions that
// were created before tree support. Entries that already have a ParentID
// are left unchanged. Entries without one are linked sequentially to the
// preceding entry.
func normalizeParentIDs(entries []Entry) {
	for i := 1; i < len(entries); i++ {
		if entries[i].ParentID == "" {
			entries[i].ParentID = entries[i-1].ID
		}
	}
}

// getEntriesOnBranch returns entries on the path from the root to the given
// leaf entry, in chronological order. If leafID is empty, all entries are
// returned in their original order (legacy linear behavior).
func getEntriesOnBranch(entries []Entry, leafID string) []Entry {
	if len(entries) == 0 {
		return nil
	}
	if leafID == "" {
		return entries
	}

	byID := buildEntryIndex(entries)

	// Walk from leaf back to root.
	var chain []Entry
	currentID := leafID
	visited := make(map[string]bool)
	for currentID != "" && !visited[currentID] {
		visited[currentID] = true
		e, ok := byID[currentID]
		if !ok {
			break
		}
		chain = append(chain, e)
		currentID = e.ParentID
	}

	// Reverse to get chronological order.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// findLeafEntries returns entries that are not referenced as any other
// entry's parent — i.e., the tips of branches.
func findLeafEntries(entries []Entry) []Entry {
	hasChildren := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.ParentID != "" {
			hasChildren[e.ParentID] = true
		}
	}
	var leaves []Entry
	for _, e := range entries {
		if !hasChildren[e.ID] {
			leaves = append(leaves, e)
		}
	}
	return leaves
}

// buildEntryIndex creates a map from entry ID to entry for fast lookups.
func buildEntryIndex(entries []Entry) map[string]Entry {
	idx := make(map[string]Entry, len(entries))
	for _, e := range entries {
		idx[e.ID] = e
	}
	return idx
}

// findForkDepth finds the depth at which a branch diverges from the other
// branches. Returns 0 if the branch shares the root with all others.
func findForkDepth(entries []Entry, byID map[string]Entry, leafID string, allLeaves []Entry) int {
	if len(allLeaves) <= 1 {
		return 0
	}

	// Build the ancestor set for this branch.
	ancestors := make(map[string]bool)
	currentID := leafID
	for currentID != "" {
		if ancestors[currentID] {
			break
		}
		ancestors[currentID] = true
		e, ok := byID[currentID]
		if !ok {
			break
		}
		currentID = e.ParentID
	}

	// Find the fork point: the deepest ancestor that is shared with at
	// least one other branch. Walk each other branch and find the deepest
	// shared entry.
	var forkEntryID string
	for _, other := range allLeaves {
		if other.ID == leafID {
			continue
		}
		cid := other.ID
		visited := make(map[string]bool)
		for cid != "" && !visited[cid] {
			visited[cid] = true
			if ancestors[cid] {
				// This is a shared ancestor — is it deeper than what we found?
				if forkEntryID == "" {
					forkEntryID = cid
				}
				break
			}
			e, ok := byID[cid]
			if !ok {
				break
			}
			cid = e.ParentID
		}
	}

	if forkEntryID == "" {
		return 0
	}

	// Count depth of fork point from root.
	depth := 0
	cid := forkEntryID
	visited := make(map[string]bool)
	for cid != "" && !visited[cid] {
		visited[cid] = true
		depth++
		e, ok := byID[cid]
		if !ok {
			break
		}
		cid = e.ParentID
	}
	return depth
}
