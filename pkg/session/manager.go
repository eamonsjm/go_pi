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
}

// Manager handles session persistence using JSONL files.
// All exported methods are safe for concurrent use.
type Manager struct {
	mu      sync.RWMutex
	dir     string // root directory, e.g. ~/.pi/sessions/
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
func (m *Manager) LoadSession(id string) error {
	path := m.sessionPath(id)
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open session %s: %w", id, err)
	}
	defer f.Close()

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
		// Read first entry for creation time and count entries.
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
				if count == 1 {
					var e Entry
					if err := json.Unmarshal([]byte(line), &e); err == nil {
						si.CreatedAt = e.Timestamp
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
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}

	m.entries = append(m.entries, entry)
	return nil
}

// GetMessages reconstructs the conversation messages from the current session's
// message entries, in order. Non-message entries are skipped.
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
	return msgs
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
