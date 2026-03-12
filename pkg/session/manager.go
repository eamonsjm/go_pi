package session

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ejm/go_pi/pkg/ai"
)

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
}

// Manager handles session persistence using JSONL files.
// All exported methods are safe for concurrent use.
type Manager struct {
	mu      sync.RWMutex
	dir     string // root directory, e.g. ~/.gi/sessions/
	current string // active session ID
	entries []Entry
}

// NewManager creates a new session manager rooted at the given directory.
// The directory is created if it does not exist.
func NewManager(dir string) *Manager {
	return &Manager{
		dir: dir,
	}
}

// NewSession creates a new session and returns its ID.
func (m *Manager) NewSession() string {
	id := generateID()
	m.mu.Lock()
	m.current = id
	m.entries = nil
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

	m.mu.Lock()
	m.current = id
	m.entries = entries
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
		// Read entries for creation time, count, and preview.
		if f, err := os.Open(path); err == nil {
			scanner := bufio.NewScanner(f)
			scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
			count := 0
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
						if len(si.Preview) > 80 {
							si.Preview = si.Preview[:77] + "..."
						}
					}
				}
			}
			si.Entries = count
			f.Close()
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
// and the in-memory entry list.
func (m *Manager) AppendEntry(entry Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.current == "" {
		return fmt.Errorf("no active session")
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
		f.Close()
		return fmt.Errorf("write entry: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close session file: %w", err)
	}

	m.entries = append(m.entries, entry)
	return nil
}

// GetMessages reconstructs the conversation messages from the current session's
// message entries, in order. Non-message entries are skipped. Any orphaned
// tool_use blocks (without matching tool_result) are repaired with synthetic
// error results so the conversation is valid for the API.
func (m *Manager) GetMessages() []ai.Message {
	m.mu.RLock()
	entries := make([]Entry, len(m.entries))
	copy(entries, m.entries)
	m.mu.RUnlock()

	var msgs []ai.Message
	for _, e := range entries {
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
// and appends it.
func (m *Manager) SaveMessage(msg ai.Message) error {
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

// --- internal ---------------------------------------------------------------

func (m *Manager) sessionPath(id string) string {
	return filepath.Join(m.dir, id+".jsonl")
}

// entryToMessage converts an Entry with type "message" into an ai.Message.
// The Data field may be a MessageData struct (in-memory) or a map (from JSON).
func entryToMessage(e Entry) (ai.Message, bool) {
	switch v := e.Data.(type) {
	case MessageData:
		return ai.Message{Role: v.Role, Content: v.Content}, true
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
		return ai.Message{Role: md.Role, Content: md.Content}, true
	}
	return ai.Message{}, false
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
