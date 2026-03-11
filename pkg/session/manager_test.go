package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/ai"
)

func TestNewSession(t *testing.T) {
	m := NewManager(t.TempDir())
	id := m.NewSession()

	if id == "" {
		t.Fatal("NewSession returned empty ID")
	}
	if len(id) != 16 {
		t.Errorf("expected 16-char hex ID, got %q (len %d)", id, len(id))
	}
	if m.CurrentID() != id {
		t.Errorf("CurrentID mismatch: got %q, want %q", m.CurrentID(), id)
	}
}

func TestSaveMessageAndGetMessages(t *testing.T) {
	m := NewManager(t.TempDir())
	m.NewSession()

	userMsg := ai.NewTextMessage(ai.RoleUser, "hello")
	assistantMsg := ai.NewTextMessage(ai.RoleAssistant, "hi there")

	if err := m.SaveMessage(userMsg); err != nil {
		t.Fatalf("SaveMessage(user): %v", err)
	}
	if err := m.SaveMessage(assistantMsg); err != nil {
		t.Fatalf("SaveMessage(assistant): %v", err)
	}

	msgs := m.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != ai.RoleUser {
		t.Errorf("expected first message role user, got %s", msgs[0].Role)
	}
	if msgs[0].GetText() != "hello" {
		t.Errorf("expected 'hello', got %q", msgs[0].GetText())
	}
	if msgs[1].Role != ai.RoleAssistant {
		t.Errorf("expected second message role assistant, got %s", msgs[1].Role)
	}
	if msgs[1].GetText() != "hi there" {
		t.Errorf("expected 'hi there', got %q", msgs[1].GetText())
	}
}

func TestAppendEntryWritesToDisk(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	id := m.NewSession()

	entry := Entry{
		ID:        "test-entry-1",
		Timestamp: time.Now().UTC(),
		Type:      "message",
		Data: MessageData{
			Role: ai.RoleUser,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeText, Text: "test"},
			},
		},
	}

	if err := m.AppendEntry(entry); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}

	// Verify file exists and has content.
	path := filepath.Join(dir, id+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		t.Fatal("session file is empty")
	}
	if !strings.Contains(content, "test-entry-1") {
		t.Errorf("expected entry ID in file, got: %s", content)
	}
}

func TestAppendEntryNoActiveSession(t *testing.T) {
	m := NewManager(t.TempDir())
	// No NewSession called.
	err := m.AppendEntry(Entry{ID: "x", Type: "message"})
	if err == nil {
		t.Error("expected error when no active session")
	}
}

func TestLoadSession(t *testing.T) {
	dir := t.TempDir()

	// Create and populate a session.
	m1 := NewManager(dir)
	id := m1.NewSession()
	if err := m1.SaveMessage(ai.NewTextMessage(ai.RoleUser, "saved message")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	if err := m1.SaveMessage(ai.NewTextMessage(ai.RoleAssistant, "saved reply")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	// Load the session in a fresh manager.
	m2 := NewManager(dir)
	if err := m2.LoadSession(id); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	if m2.CurrentID() != id {
		t.Errorf("CurrentID mismatch after load")
	}

	msgs := m2.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after load, got %d", len(msgs))
	}
	if msgs[0].GetText() != "saved message" {
		t.Errorf("expected 'saved message', got %q", msgs[0].GetText())
	}
	if msgs[1].GetText() != "saved reply" {
		t.Errorf("expected 'saved reply', got %q", msgs[1].GetText())
	}
}

func TestLoadSessionNotFound(t *testing.T) {
	m := NewManager(t.TempDir())
	err := m.LoadSession("nonexistent")
	if err == nil {
		t.Error("expected error loading nonexistent session")
	}
}

func TestListSessions(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	// Create two sessions with entries.
	id1 := m.NewSession()
	if err := m.SaveMessage(ai.NewTextMessage(ai.RoleUser, "session 1")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	// Small delay so modification times differ.
	time.Sleep(10 * time.Millisecond)

	id2 := m.NewSession()
	if err := m.SaveMessage(ai.NewTextMessage(ai.RoleUser, "session 2")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	sessions := m.ListSessions()
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	// Most recently updated should be first.
	if sessions[0].ID != id2 {
		t.Errorf("expected most recent session %q first, got %q", id2, sessions[0].ID)
	}
	if sessions[1].ID != id1 {
		t.Errorf("expected older session %q second, got %q", id1, sessions[1].ID)
	}

	// Each should have 1 entry.
	for _, s := range sessions {
		if s.Entries != 1 {
			t.Errorf("session %s: expected 1 entry, got %d", s.ID, s.Entries)
		}
	}
}

func TestListSessionsEmpty(t *testing.T) {
	m := NewManager(t.TempDir())
	sessions := m.ListSessions()
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestMultipleSessions(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	id1 := m.NewSession()
	if err := m.SaveMessage(ai.NewTextMessage(ai.RoleUser, "msg in session 1")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	id2 := m.NewSession()
	if err := m.SaveMessage(ai.NewTextMessage(ai.RoleUser, "msg in session 2")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	// Load session 1 and verify its messages.
	if err := m.LoadSession(id1); err != nil {
		t.Fatalf("LoadSession(%s): %v", id1, err)
	}
	msgs1 := m.GetMessages()
	if len(msgs1) != 1 || msgs1[0].GetText() != "msg in session 1" {
		t.Errorf("session 1 messages incorrect")
	}

	// Load session 2 and verify its messages.
	if err := m.LoadSession(id2); err != nil {
		t.Fatalf("LoadSession(%s): %v", id2, err)
	}
	msgs2 := m.GetMessages()
	if len(msgs2) != 1 || msgs2[0].GetText() != "msg in session 2" {
		t.Errorf("session 2 messages incorrect")
	}
}

func TestGenerateIDUniqueness(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateID()
		if len(id) != 16 {
			t.Errorf("expected 16-char ID, got %q", id)
		}
		if ids[id] {
			t.Errorf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}

func TestNonMessageEntriesSkipped(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	// Append a non-message entry.
	if err := m.AppendEntry(Entry{
		ID:        "info-1",
		Timestamp: time.Now().UTC(),
		Type:      "info",
		Data:      map[string]any{"note": "session started"},
	}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}

	// Append a message entry.
	if err := m.SaveMessage(ai.NewTextMessage(ai.RoleUser, "hello")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	msgs := m.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message (info skipped), got %d", len(msgs))
	}
	if msgs[0].GetText() != "hello" {
		t.Errorf("expected 'hello', got %q", msgs[0].GetText())
	}
}
