package session

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestLatestSessionID(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	// No sessions — should return empty.
	if got := m.LatestSessionID(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}

	// Create first session.
	m.NewSession()
	if err := m.SaveMessage(ai.NewTextMessage(ai.RoleUser, "first")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	// Create second session — should be the latest.
	id2 := m.NewSession()
	if err := m.SaveMessage(ai.NewTextMessage(ai.RoleUser, "second")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	if got := m.LatestSessionID(); got != id2 {
		t.Errorf("expected latest %q, got %q", id2, got)
	}
}

func TestListSessionsPreview(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	m.NewSession()
	if err := m.SaveMessage(ai.NewTextMessage(ai.RoleUser, "hello world")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	if err := m.SaveMessage(ai.NewTextMessage(ai.RoleAssistant, "hi there")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	if err := m.SaveMessage(ai.NewTextMessage(ai.RoleUser, "tell me more\nwith multiple lines")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	sessions := m.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	// Preview should be the first line of the last user message.
	if sessions[0].Preview != "tell me more" {
		t.Errorf("expected preview %q, got %q", "tell me more", sessions[0].Preview)
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

// --- Concurrency tests ------------------------------------------------------

func TestConcurrentAppendEntry(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	id := m.NewSession()

	const goroutines = 10
	const entriesPerGoroutine = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines*entriesPerGoroutine)

	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < entriesPerGoroutine; i++ {
				entry := Entry{
					ID:        fmt.Sprintf("g%d-e%d", gid, i),
					Timestamp: time.Now().UTC(),
					Type:      "message",
					Data: MessageData{
						Role:    ai.RoleUser,
						Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: fmt.Sprintf("msg-%d-%d", gid, i)}},
					},
				}
				if err := m.AppendEntry(entry); err != nil {
					errs <- err
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("AppendEntry error: %v", err)
	}

	// Verify in-memory entry count.
	msgs := m.GetMessages()
	expected := goroutines * entriesPerGoroutine
	if len(msgs) != expected {
		t.Errorf("in-memory: expected %d messages, got %d", expected, len(msgs))
	}

	// Verify on-disk entry count by reading the JSONL file.
	path := filepath.Join(dir, id+".jsonl")
	diskCount := countJSONLLines(t, path)
	if diskCount != expected {
		t.Errorf("on-disk: expected %d lines, got %d", expected, diskCount)
	}
}

func TestConcurrentSaveMessage(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	id := m.NewSession()

	const goroutines = 8
	const msgsPerGoroutine = 25

	var wg sync.WaitGroup
	wg.Add(goroutines)
	errs := make(chan error, goroutines*msgsPerGoroutine)

	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < msgsPerGoroutine; i++ {
				msg := ai.NewTextMessage(ai.RoleUser, fmt.Sprintf("concurrent-%d-%d", gid, i))
				if err := m.SaveMessage(msg); err != nil {
					errs <- err
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("SaveMessage error: %v", err)
	}

	expected := goroutines * msgsPerGoroutine
	msgs := m.GetMessages()
	if len(msgs) != expected {
		t.Errorf("expected %d messages, got %d", expected, len(msgs))
	}

	// Verify each message text is present on disk.
	path := filepath.Join(dir, id+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	for g := 0; g < goroutines; g++ {
		for i := 0; i < msgsPerGoroutine; i++ {
			needle := fmt.Sprintf("concurrent-%d-%d", g, i)
			if !strings.Contains(content, needle) {
				t.Errorf("missing entry on disk: %s", needle)
			}
		}
	}
}

func TestListSessionsDuringConcurrentWrites(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	// Seed one entry so the session file exists on disk before concurrent reads.
	if err := m.SaveMessage(ai.NewTextMessage(ai.RoleUser, "seed")); err != nil {
		t.Fatalf("seed SaveMessage: %v", err)
	}

	const writers = 4
	const writesPerWriter = 30
	const readers = 4
	const readsPerReader = 20

	var wg sync.WaitGroup
	wg.Add(writers + readers)
	writeErrs := make(chan error, writers*writesPerWriter)

	// Writers append entries concurrently.
	for w := 0; w < writers; w++ {
		go func(wid int) {
			defer wg.Done()
			for i := 0; i < writesPerWriter; i++ {
				msg := ai.NewTextMessage(ai.RoleUser, fmt.Sprintf("w%d-%d", wid, i))
				if err := m.SaveMessage(msg); err != nil {
					writeErrs <- err
				}
			}
		}(w)
	}

	// Readers list sessions concurrently with the writes.
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < readsPerReader; i++ {
				sessions := m.ListSessions()
				// Should always see at least 1 session (the one we created).
				if len(sessions) < 1 {
					t.Errorf("ListSessions returned %d sessions, expected >= 1", len(sessions))
				}
			}
		}()
	}

	wg.Wait()
	close(writeErrs)

	for err := range writeErrs {
		t.Errorf("write error: %v", err)
	}
}

func TestConcurrentGetMessagesDuringAppend(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	const writers = 4
	const writesPerWriter = 50
	const readers = 4
	const readsPerReader = 50

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	// Writers.
	for w := 0; w < writers; w++ {
		go func(wid int) {
			defer wg.Done()
			for i := 0; i < writesPerWriter; i++ {
				msg := ai.NewTextMessage(ai.RoleUser, fmt.Sprintf("r%d-%d", wid, i))
				_ = m.SaveMessage(msg)
			}
		}(w)
	}

	// Readers call GetMessages concurrently.
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < readsPerReader; i++ {
				msgs := m.GetMessages()
				// Message count should be monotonically non-decreasing within
				// a single reader, but across readers we just check sanity.
				if len(msgs) < 0 {
					t.Errorf("negative message count: %d", len(msgs))
				}
			}
		}()
	}

	wg.Wait()

	// Final count must match total writes.
	expected := writers * writesPerWriter
	msgs := m.GetMessages()
	if len(msgs) != expected {
		t.Errorf("expected %d messages after concurrent access, got %d", expected, len(msgs))
	}
}

func TestConcurrentAppendEntryDataIntegrity(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	id := m.NewSession()

	const goroutines = 10
	const entriesPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < entriesPerGoroutine; i++ {
				msg := ai.NewTextMessage(ai.RoleUser, fmt.Sprintf("integrity-%d-%d", gid, i))
				_ = m.SaveMessage(msg)
			}
		}(g)
	}

	wg.Wait()

	// Reload from disk and verify all entries survived.
	m2 := NewManager(dir)
	if err := m2.LoadSession(id); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	msgs := m2.GetMessages()
	expected := goroutines * entriesPerGoroutine
	if len(msgs) != expected {
		t.Fatalf("expected %d messages after reload, got %d", expected, len(msgs))
	}

	// Build a set of expected texts and verify each is present.
	seen := make(map[string]bool)
	for _, msg := range msgs {
		seen[msg.GetText()] = true
	}
	for g := 0; g < goroutines; g++ {
		for i := 0; i < entriesPerGoroutine; i++ {
			key := fmt.Sprintf("integrity-%d-%d", g, i)
			if !seen[key] {
				t.Errorf("missing message after reload: %s", key)
			}
		}
	}
}

func TestConcurrentCurrentID(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	const goroutines = 10
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := m.CurrentID()
				if id == "" {
					t.Errorf("CurrentID returned empty during concurrent access")
				}
			}
		}()
	}

	wg.Wait()
}

// countJSONLLines counts non-empty lines in a JSONL file.
func countJSONLLines(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) != "" {
			count++
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}
	return count
}
