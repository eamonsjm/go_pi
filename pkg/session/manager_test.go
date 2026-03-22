package session

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

	if err := m.SaveMessage(context.Background(),userMsg); err != nil {
		t.Fatalf("SaveMessage(user): %v", err)
	}
	if err := m.SaveMessage(context.Background(),assistantMsg); err != nil {
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

	if err := m.AppendEntry(context.Background(),entry); err != nil {
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
	err := m.AppendEntry(context.Background(), Entry{ID: "x", Type: "message"})
	if !errors.Is(err, ErrNoActiveSession) {
		t.Errorf("expected ErrNoActiveSession, got %v", err)
	}
}

func TestLoadSession(t *testing.T) {
	dir := t.TempDir()

	// Create and populate a session.
	m1 := NewManager(dir)
	id := m1.NewSession()
	if err := m1.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "saved message")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	if err := m1.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleAssistant, "saved reply")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	// Load the session in a fresh manager.
	m2 := NewManager(dir)
	if err := m2.LoadSession(context.Background(),id); err != nil {
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
	err := m.LoadSession(context.Background(),"nonexistent")
	if err == nil {
		t.Error("expected error loading nonexistent session")
	}
}

func TestListSessions(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	// Create two sessions with entries.
	id1 := m.NewSession()
	if err := m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "session 1")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	// Small delay so modification times differ.
	time.Sleep(10 * time.Millisecond)

	id2 := m.NewSession()
	if err := m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "session 2")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	sessions, _ := m.ListSessions(context.Background())
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
	sessions, _ := m.ListSessions(context.Background())
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestLatestSessionID(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	// No sessions — should return empty.
	if got := m.LatestSessionID(context.Background()); got != "" {
		t.Errorf("expected empty, got %q", got)
	}

	// Create first session.
	m.NewSession()
	if err := m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "first")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	// Create second session — should be the latest.
	id2 := m.NewSession()
	if err := m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "second")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	if got := m.LatestSessionID(context.Background()); got != id2 {
		t.Errorf("expected latest %q, got %q", id2, got)
	}
}

func TestListSessionsPreview(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	m.NewSession()
	if err := m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "hello world")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	if err := m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleAssistant, "hi there")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	if err := m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "tell me more\nwith multiple lines")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	sessions, _ := m.ListSessions(context.Background())
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
	if err := m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "msg in session 1")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	id2 := m.NewSession()
	if err := m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "msg in session 2")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}

	// Load session 1 and verify its messages.
	if err := m.LoadSession(context.Background(),id1); err != nil {
		t.Fatalf("LoadSession(%s): %v", id1, err)
	}
	msgs1 := m.GetMessages()
	if len(msgs1) != 1 || msgs1[0].GetText() != "msg in session 1" {
		t.Errorf("session 1 messages incorrect")
	}

	// Load session 2 and verify its messages.
	if err := m.LoadSession(context.Background(),id2); err != nil {
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
	if err := m.AppendEntry(context.Background(),Entry{
		ID:        "info-1",
		Timestamp: time.Now().UTC(),
		Type:      "info",
		Data:      map[string]any{"note": "session started"},
	}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}

	// Append a message entry.
	if err := m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "hello")); err != nil {
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
				if err := m.AppendEntry(context.Background(),entry); err != nil {
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
				if err := m.SaveMessage(context.Background(),msg); err != nil {
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
	if err := m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "seed")); err != nil {
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
				if err := m.SaveMessage(context.Background(),msg); err != nil {
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
				sessions, _ := m.ListSessions(context.Background())
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
				_ = m.SaveMessage(context.Background(),msg)
			}
		}(w)
	}

	// Readers call GetMessages concurrently.
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < readsPerReader; i++ {
				_ = m.GetMessages()
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
				_ = m.SaveMessage(context.Background(),msg)
			}
		}(g)
	}

	wg.Wait()

	// Reload from disk and verify all entries survived.
	m2 := NewManager(dir)
	if err := m2.LoadSession(context.Background(),id); err != nil {
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

// --- Orphaned tool_use repair tests -----------------------------------------

func TestRepairOrphanedToolUse_NoToolUse(t *testing.T) {
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "hello"),
		ai.NewTextMessage(ai.RoleAssistant, "hi"),
	}
	repaired := RepairOrphanedToolUse(msgs)
	if len(repaired) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(repaired))
	}
}

func TestRepairOrphanedToolUse_CompleteToolUse(t *testing.T) {
	// Assistant has tool_use, followed by matching tool_result — no repair needed.
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "do something"),
		{
			Role: ai.RoleAssistant,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeToolUse, ToolUseID: "tu-1", ToolName: "bash", Input: map[string]any{"cmd": "ls"}},
			},
		},
		ai.NewToolResultMessage("tu-1", "file1 file2", false),
		ai.NewTextMessage(ai.RoleAssistant, "done"),
	}
	repaired := RepairOrphanedToolUse(msgs)
	if len(repaired) != 4 {
		t.Fatalf("expected 4 messages (no repair), got %d", len(repaired))
	}
}

func TestRepairOrphanedToolUse_OrphanedAtEnd(t *testing.T) {
	// Assistant has tool_use as last message — no tool_result follows.
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "do something"),
		{
			Role: ai.RoleAssistant,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeText, Text: "let me run that"},
				{Type: ai.ContentTypeToolUse, ToolUseID: "tu-1", ToolName: "bash", Input: map[string]any{"cmd": "ls"}},
				{Type: ai.ContentTypeToolUse, ToolUseID: "tu-2", ToolName: "read", Input: map[string]any{"path": "/tmp"}},
			},
		},
	}
	repaired := RepairOrphanedToolUse(msgs)
	if len(repaired) != 4 {
		t.Fatalf("expected 4 messages (2 original + 2 synthetic), got %d", len(repaired))
	}
	// Synthetic results should be user messages with tool_result type.
	for _, idx := range []int{2, 3} {
		msg := repaired[idx]
		if msg.Role != ai.RoleUser {
			t.Errorf("repaired[%d]: expected role user, got %s", idx, msg.Role)
		}
		if len(msg.Content) != 1 || msg.Content[0].Type != ai.ContentTypeToolResult {
			t.Errorf("repaired[%d]: expected tool_result content", idx)
		}
		if !msg.Content[0].IsError {
			t.Errorf("repaired[%d]: expected is_error=true", idx)
		}
	}
	if repaired[2].Content[0].ToolResultID != "tu-1" {
		t.Errorf("expected tu-1, got %s", repaired[2].Content[0].ToolResultID)
	}
	if repaired[3].Content[0].ToolResultID != "tu-2" {
		t.Errorf("expected tu-2, got %s", repaired[3].Content[0].ToolResultID)
	}
}

func TestRepairOrphanedToolUse_PartialResults(t *testing.T) {
	// Assistant has 3 tool_use blocks, only the first has a result.
	msgs := []ai.Message{
		ai.NewTextMessage(ai.RoleUser, "do three things"),
		{
			Role: ai.RoleAssistant,
			Content: []ai.ContentBlock{
				{Type: ai.ContentTypeToolUse, ToolUseID: "tu-1", ToolName: "bash", Input: nil},
				{Type: ai.ContentTypeToolUse, ToolUseID: "tu-2", ToolName: "read", Input: nil},
				{Type: ai.ContentTypeToolUse, ToolUseID: "tu-3", ToolName: "write", Input: nil},
			},
		},
		ai.NewToolResultMessage("tu-1", "ok", false),
	}
	repaired := RepairOrphanedToolUse(msgs)
	// Original: user, assistant, tool_result(tu-1). Synthetic: tool_result(tu-2), tool_result(tu-3).
	if len(repaired) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(repaired))
	}
	// Synthetic results should be after the existing tool_result.
	if repaired[3].Content[0].ToolResultID != "tu-2" {
		t.Errorf("expected tu-2, got %s", repaired[3].Content[0].ToolResultID)
	}
	if repaired[4].Content[0].ToolResultID != "tu-3" {
		t.Errorf("expected tu-3, got %s", repaired[4].Content[0].ToolResultID)
	}
}

func TestRepairOrphanedToolUse_Empty(t *testing.T) {
	repaired := RepairOrphanedToolUse(nil)
	if repaired != nil {
		t.Errorf("expected nil for nil input, got %v", repaired)
	}

	repaired = RepairOrphanedToolUse([]ai.Message{})
	if len(repaired) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(repaired))
	}
}

func TestRepairOrphanedToolUse_ViaGetMessages(t *testing.T) {
	// End-to-end: save messages with orphaned tool_use, reload, verify repair.
	dir := t.TempDir()
	m := NewManager(dir)
	id := m.NewSession()

	// Save user message.
	if err := m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "run this")); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	// Save assistant message with tool_use.
	assistantMsg := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-orphan", ToolName: "bash", Input: map[string]any{"cmd": "ls"}},
		},
	}
	if err := m.SaveMessage(context.Background(),assistantMsg); err != nil {
		t.Fatalf("SaveMessage: %v", err)
	}
	// No tool_result saved — simulates user quit mid-execution.

	// Reload in a fresh manager.
	m2 := NewManager(dir)
	if err := m2.LoadSession(context.Background(),id); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	msgs := m2.GetMessages()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages (2 original + 1 synthetic), got %d", len(msgs))
	}
	synth := msgs[2]
	if synth.Role != ai.RoleUser {
		t.Errorf("synthetic message: expected role user, got %s", synth.Role)
	}
	if synth.Content[0].ToolResultID != "tu-orphan" {
		t.Errorf("synthetic message: expected tool_use_id tu-orphan, got %s", synth.Content[0].ToolResultID)
	}
	if !synth.Content[0].IsError {
		t.Error("synthetic message: expected is_error=true")
	}
	if !strings.Contains(synth.Content[0].Content, "interrupted") {
		t.Errorf("synthetic message: expected 'interrupted' in content, got %q", synth.Content[0].Content)
	}
}

// --- Tree/branching tests ---------------------------------------------------

func TestNormalizeParentIDs(t *testing.T) {
	entries := []Entry{
		{ID: "a"},
		{ID: "b"},
		{ID: "c"},
	}
	normalizeParentIDs(entries)
	if entries[0].ParentID != "" {
		t.Errorf("root entry should have no parent, got %q", entries[0].ParentID)
	}
	if entries[1].ParentID != "a" {
		t.Errorf("expected parent 'a', got %q", entries[1].ParentID)
	}
	if entries[2].ParentID != "b" {
		t.Errorf("expected parent 'b', got %q", entries[2].ParentID)
	}
}

func TestNormalizeParentIDs_PreservesExisting(t *testing.T) {
	entries := []Entry{
		{ID: "a"},
		{ID: "b", ParentID: "a"},
		{ID: "c", ParentID: "a"}, // fork from a
	}
	normalizeParentIDs(entries)
	if entries[1].ParentID != "a" {
		t.Errorf("expected existing parent 'a' preserved, got %q", entries[1].ParentID)
	}
	if entries[2].ParentID != "a" {
		t.Errorf("expected existing parent 'a' preserved, got %q", entries[2].ParentID)
	}
}

func TestGetEntriesOnBranch(t *testing.T) {
	// Tree:
	//   a -> b -> c (branch 1)
	//        b -> d -> e (branch 2)
	entries := []Entry{
		{ID: "a"},
		{ID: "b", ParentID: "a"},
		{ID: "c", ParentID: "b"},
		{ID: "d", ParentID: "b"},
		{ID: "e", ParentID: "d"},
	}

	// Branch 1: a -> b -> c
	branch1 := getEntriesOnBranch(entries, "c")
	if len(branch1) != 3 {
		t.Fatalf("expected 3 entries on branch 1, got %d", len(branch1))
	}
	if branch1[0].ID != "a" || branch1[1].ID != "b" || branch1[2].ID != "c" {
		t.Errorf("unexpected branch 1: %v", ids(branch1))
	}

	// Branch 2: a -> b -> d -> e
	branch2 := getEntriesOnBranch(entries, "e")
	if len(branch2) != 4 {
		t.Fatalf("expected 4 entries on branch 2, got %d", len(branch2))
	}
	if branch2[0].ID != "a" || branch2[3].ID != "e" {
		t.Errorf("unexpected branch 2: %v", ids(branch2))
	}
}

func TestGetEntriesOnBranch_EmptyLeaf(t *testing.T) {
	entries := []Entry{{ID: "a"}, {ID: "b"}}
	result := getEntriesOnBranch(entries, "")
	if len(result) != 2 {
		t.Fatalf("expected all entries returned for empty leaf, got %d", len(result))
	}
}

func TestFindLeafEntries(t *testing.T) {
	entries := []Entry{
		{ID: "a"},
		{ID: "b", ParentID: "a"},
		{ID: "c", ParentID: "b"},
		{ID: "d", ParentID: "b"}, // fork: d is also a child of b
	}
	leaves := findLeafEntries(entries)
	if len(leaves) != 2 {
		t.Fatalf("expected 2 leaves, got %d", len(leaves))
	}
	leafIDs := make(map[string]bool)
	for _, l := range leaves {
		leafIDs[l.ID] = true
	}
	if !leafIDs["c"] || !leafIDs["d"] {
		t.Errorf("expected leaves c and d, got %v", leafIDs)
	}
}

func TestForkAt(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	// Add linear entries.
	for _, id := range []string{"e1", "e2", "e3"} {
		err := m.AppendEntry(context.Background(),Entry{
			ID:        id,
			Timestamp: time.Now().UTC(),
			Type:      "message",
			Data:      MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: id}}},
		})
		if err != nil {
			t.Fatalf("AppendEntry(%s): %v", id, err)
		}
	}

	// Active branch should be e3.
	if m.ActiveBranch() != "e3" {
		t.Fatalf("expected active branch e3, got %s", m.ActiveBranch())
	}

	// Fork at e1.
	if err := m.ForkAt("e1"); err != nil {
		t.Fatalf("ForkAt: %v", err)
	}
	if m.ActiveBranch() != "e1" {
		t.Errorf("expected active branch e1 after fork, got %s", m.ActiveBranch())
	}

	// New entry on forked branch.
	err := m.AppendEntry(context.Background(),Entry{
		ID:        "e4",
		Timestamp: time.Now().UTC(),
		Type:      "message",
		Data:      MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "forked"}}},
	})
	if err != nil {
		t.Fatalf("AppendEntry(e4): %v", err)
	}
	if m.ActiveBranch() != "e4" {
		t.Errorf("expected active branch e4, got %s", m.ActiveBranch())
	}

	// GetMessages should return only the forked branch: e1 -> e4.
	msgs := m.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages on forked branch, got %d", len(msgs))
	}
	if msgs[0].GetText() != "e1" || msgs[1].GetText() != "forked" {
		t.Errorf("unexpected messages: %q, %q", msgs[0].GetText(), msgs[1].GetText())
	}
}

func TestForkAtEntryNotFound(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	err := m.ForkAt("nonexistent")
	if !errors.Is(err, ErrEntryNotFound) {
		t.Errorf("expected ErrEntryNotFound, got %v", err)
	}
	var enf *EntryNotFoundError
	if !errors.As(err, &enf) {
		t.Fatalf("expected *EntryNotFoundError, got %T", err)
	}
	if enf.EntryID != "nonexistent" {
		t.Errorf("expected EntryID %q, got %q", "nonexistent", enf.EntryID)
	}
}

func TestGetBranches(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	// Build a tree: e1 -> e2 -> e3 (main), e2 -> e4 (fork)
	m.AppendEntry(context.Background(),Entry{ID: "e1", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "start"}}}})
	m.AppendEntry(context.Background(),Entry{ID: "e2", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleAssistant, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "response"}}}})
	m.AppendEntry(context.Background(),Entry{ID: "e3", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "continue main"}}}})

	// Fork at e2.
	m.ForkAt("e2")
	m.AppendEntry(context.Background(),Entry{ID: "e4", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "fork path"}}}})

	branches := m.GetBranches()
	if len(branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(branches))
	}

	// Active branch (e4) should be first.
	if branches[0].LeafID != "e4" {
		t.Errorf("expected active branch e4 first, got %s", branches[0].LeafID)
	}
	if !branches[0].IsActive {
		t.Error("expected first branch to be active")
	}
	if branches[1].LeafID != "e3" {
		t.Errorf("expected branch e3 second, got %s", branches[1].LeafID)
	}
}

func TestSwitchBranch(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	m.AppendEntry(context.Background(),Entry{ID: "e1", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "root"}}}})
	m.AppendEntry(context.Background(),Entry{ID: "e2", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "main"}}}})

	// Fork from e1.
	m.ForkAt("e1")
	m.AppendEntry(context.Background(),Entry{ID: "e3", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "alt"}}}})

	// We're on e3 branch now. Switch to e2 branch.
	m.SwitchBranch("e2")
	msgs := m.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after switch, got %d", len(msgs))
	}
	if msgs[1].GetText() != "main" {
		t.Errorf("expected 'main', got %q", msgs[1].GetText())
	}

	// Switch back to e3 branch.
	m.SwitchBranch("e3")
	msgs = m.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after second switch, got %d", len(msgs))
	}
	if msgs[1].GetText() != "alt" {
		t.Errorf("expected 'alt', got %q", msgs[1].GetText())
	}
}

func TestHasBranches(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	m.AppendEntry(context.Background(),Entry{ID: "e1", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "a"}}}})

	if m.HasBranches() {
		t.Error("linear session should not have branches")
	}

	m.AppendEntry(context.Background(),Entry{ID: "e2", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "b"}}}})

	m.ForkAt("e1")
	m.AppendEntry(context.Background(),Entry{ID: "e3", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "c"}}}})

	if !m.HasBranches() {
		t.Error("forked session should have branches")
	}
}

func TestLoadSessionPreservesTree(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	id := m.NewSession()

	// Build tree and reload.
	m.AppendEntry(context.Background(),Entry{ID: "e1", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "root"}}}})
	m.AppendEntry(context.Background(),Entry{ID: "e2", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "main"}}}})
	m.ForkAt("e1")
	m.AppendEntry(context.Background(),Entry{ID: "e3", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "fork"}}}})

	// Reload in fresh manager — last entry is e3 so active branch should be e3.
	m2 := NewManager(dir)
	if err := m2.LoadSession(context.Background(),id); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	msgs := m2.GetMessages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (e1 -> e3), got %d", len(msgs))
	}
	if msgs[1].GetText() != "fork" {
		t.Errorf("expected fork branch loaded, got %q", msgs[1].GetText())
	}

	// Should detect branches.
	if !m2.HasBranches() {
		t.Error("reloaded session should have branches")
	}
}

func TestGetBranchMessages(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	m.AppendEntry(context.Background(),Entry{ID: "e1", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "root"}}}})
	m.AppendEntry(context.Background(),Entry{ID: "e2", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "main"}}}})

	m.ForkAt("e1")
	m.AppendEntry(context.Background(),Entry{ID: "e3", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "alt"}}}})

	// Get main branch messages.
	mainMsgs := m.GetBranchMessages("e2")
	if len(mainMsgs) != 2 {
		t.Fatalf("expected 2 messages on main branch, got %d", len(mainMsgs))
	}
	if mainMsgs[1].GetText() != "main" {
		t.Errorf("expected 'main', got %q", mainMsgs[1].GetText())
	}

	// Get fork branch messages.
	forkMsgs := m.GetBranchMessages("e3")
	if len(forkMsgs) != 2 {
		t.Fatalf("expected 2 messages on fork branch, got %d", len(forkMsgs))
	}
	if forkMsgs[1].GetText() != "alt" {
		t.Errorf("expected 'alt', got %q", forkMsgs[1].GetText())
	}
}

func TestFormatTree(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	// Linear session — no tree.
	m.AppendEntry(context.Background(),Entry{ID: "e1", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "hello"}}}})
	tree := m.FormatTree()
	if !strings.Contains(tree, "linear") {
		t.Errorf("expected 'linear' in tree output for non-branched session, got %q", tree)
	}

	// Add a branch.
	m.AppendEntry(context.Background(),Entry{ID: "e2", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "main path"}}}})
	m.ForkAt("e1")
	m.AppendEntry(context.Background(),Entry{ID: "e3", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "fork path"}}}})

	tree = m.FormatTree()
	if !strings.Contains(tree, "2") {
		t.Errorf("expected branch count in tree, got %q", tree)
	}
	if !strings.Contains(tree, "active") {
		t.Errorf("expected 'active' marker in tree, got %q", tree)
	}
}

func TestGetUserEntries(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	m.AppendEntry(context.Background(),Entry{ID: "e1", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "q1"}}}})
	m.AppendEntry(context.Background(),Entry{ID: "e2", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleAssistant, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "a1"}}}})
	m.AppendEntry(context.Background(),Entry{ID: "e3", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "q2"}}}})

	userEntries := m.GetUserEntries()
	if len(userEntries) != 2 {
		t.Fatalf("expected 2 user entries, got %d", len(userEntries))
	}
	if userEntries[0].ID != "e1" || userEntries[1].ID != "e3" {
		t.Errorf("unexpected user entries: %s, %s", userEntries[0].ID, userEntries[1].ID)
	}
}

func TestAppendEntryAutoSetsParentID(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	m.AppendEntry(context.Background(),Entry{ID: "e1", Timestamp: time.Now().UTC(), Type: "info"})
	m.AppendEntry(context.Background(),Entry{ID: "e2", Timestamp: time.Now().UTC(), Type: "info"})

	entries := m.GetEntries()
	if entries[0].ParentID != "" {
		t.Errorf("first entry should have no parent, got %q", entries[0].ParentID)
	}
	if entries[1].ParentID != "e1" {
		t.Errorf("second entry should have parent e1, got %q", entries[1].ParentID)
	}
}

func TestListSessionsShowsBranches(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	m.AppendEntry(context.Background(),Entry{ID: "e1", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "a"}}}})
	m.AppendEntry(context.Background(),Entry{ID: "e2", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "b"}}}})
	m.ForkAt("e1")
	m.AppendEntry(context.Background(),Entry{ID: "e3", Timestamp: time.Now().UTC(), Type: "message",
		Data: MessageData{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "c"}}}})

	sessions, _ := m.ListSessions(context.Background())
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].Branches != 2 {
		t.Errorf("expected 2 branches, got %d", sessions[0].Branches)
	}
}

func TestEntryToMessage_NilContentFromJSON(t *testing.T) {
	// Simulate a JSON-deserialized entry where content was null.
	e := Entry{
		ID:   "test-nil",
		Type: "message",
		Data: map[string]any{
			"role":    "assistant",
			"content": nil, // null in JSON
		},
	}
	msg, ok := entryToMessage(e)
	if !ok {
		t.Fatal("entryToMessage should succeed even with nil content")
	}
	if msg.Content == nil {
		t.Fatal("Content should not be nil after entryToMessage — nil marshals to JSON null which the API rejects")
	}
	if len(msg.Content) != 0 {
		t.Errorf("expected empty Content slice, got %d elements", len(msg.Content))
	}
}

func TestEntryToMessage_NilContentFromMessageData(t *testing.T) {
	// In-memory MessageData with nil Content.
	e := Entry{
		ID:   "test-nil-md",
		Type: "message",
		Data: MessageData{
			Role:    ai.RoleAssistant,
			Content: nil,
		},
	}
	msg, ok := entryToMessage(e)
	if !ok {
		t.Fatal("entryToMessage should succeed")
	}
	if msg.Content == nil {
		t.Fatal("Content should not be nil after entryToMessage")
	}
}

// --- Duplicate tool_use dedup tests ------------------------------------------

func TestSaveMessage_DedupAssistantToolUse(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	id := m.NewSession()

	// Save user message.
	m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "write a file"))

	// Save assistant message with text + tool_use.
	original := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeText, Text: "I'll write that file for you."},
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-A", ToolName: "write",
				Input: map[string]any{"path": "/tmp/foo.md", "content": "hello world"}},
		},
	}
	if err := m.SaveMessage(context.Background(),original); err != nil {
		t.Fatalf("SaveMessage(original): %v", err)
	}

	// Save tool_result for original.
	m.SaveMessage(context.Background(),ai.NewToolResultMessage("tu-A", "File written", false))

	// Save duplicate assistant message — same tool name and input, different ID.
	dup := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-B", ToolName: "write",
				Input: map[string]any{"path": "/tmp/foo.md", "content": "hello world"}},
		},
	}
	if err := m.SaveMessage(context.Background(),dup); err != nil {
		t.Fatalf("SaveMessage(dup): %v", err)
	}

	// The duplicate's tool_result should also be skipped.
	m.SaveMessage(context.Background(),ai.NewToolResultMessage("tu-B", "File written", false))

	// Verify: only 3 entries on disk (user, assistant, tool_result for tu-A).
	path := filepath.Join(dir, id+".jsonl")
	diskCount := countJSONLLines(t, path)
	if diskCount != 3 {
		t.Errorf("expected 3 entries on disk (dup skipped), got %d", diskCount)
	}

	// In-memory should also have 3.
	entries := m.GetEntries()
	if len(entries) != 3 {
		t.Errorf("expected 3 in-memory entries, got %d", len(entries))
	}
}

func TestSaveMessage_DedupMultipleToolUse(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "do two things"))

	// Original with two tool_use blocks.
	original := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-1", ToolName: "read",
				Input: map[string]any{"path": "/a"}},
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-2", ToolName: "read",
				Input: map[string]any{"path": "/b"}},
		},
	}
	m.SaveMessage(context.Background(),original)

	// Duplicate with same names and inputs, different IDs.
	dup := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-3", ToolName: "read",
				Input: map[string]any{"path": "/a"}},
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-4", ToolName: "read",
				Input: map[string]any{"path": "/b"}},
		},
	}
	m.SaveMessage(context.Background(),dup)

	// Both tool_results for the dup should be skipped.
	m.SaveMessage(context.Background(),ai.NewToolResultMessage("tu-3", "content-a", false))
	m.SaveMessage(context.Background(),ai.NewToolResultMessage("tu-4", "content-b", false))

	entries := m.GetEntries()
	if len(entries) != 2 { // user + original assistant
		t.Errorf("expected 2 entries (dup + results skipped), got %d", len(entries))
	}
}

func TestSaveMessage_NoDedupDifferentInput(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "write a file"))

	original := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-A", ToolName: "write",
				Input: map[string]any{"path": "/tmp/foo.md", "content": "version 1"}},
		},
	}
	m.SaveMessage(context.Background(),original)

	// Different input — should NOT be deduped.
	different := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-B", ToolName: "write",
				Input: map[string]any{"path": "/tmp/foo.md", "content": "version 2"}},
		},
	}
	m.SaveMessage(context.Background(),different)

	entries := m.GetEntries()
	if len(entries) != 3 { // user + original + different
		t.Errorf("expected 3 entries (different input not deduped), got %d", len(entries))
	}
}

func TestSaveMessage_NoDedupDifferentToolName(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "do something"))

	original := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-A", ToolName: "read",
				Input: map[string]any{"path": "/tmp/foo.md"}},
		},
	}
	m.SaveMessage(context.Background(),original)

	// Same input but different tool name — should NOT be deduped.
	different := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-B", ToolName: "write",
				Input: map[string]any{"path": "/tmp/foo.md"}},
		},
	}
	m.SaveMessage(context.Background(),different)

	entries := m.GetEntries()
	if len(entries) != 3 {
		t.Errorf("expected 3 entries (different tool name not deduped), got %d", len(entries))
	}
}

func TestSaveMessage_NoDedupTextOnly(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	// Text-only assistant messages should never be deduped, even if identical.
	m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "hello"))
	m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleAssistant, "hi"))
	m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleAssistant, "hi"))

	entries := m.GetEntries()
	if len(entries) != 3 {
		t.Errorf("expected 3 entries (text-only never deduped), got %d", len(entries))
	}
}

func TestSaveMessage_NoDedupDifferentCount(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "do things"))

	// Original with one tool_use.
	original := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-1", ToolName: "read",
				Input: map[string]any{"path": "/a"}},
		},
	}
	m.SaveMessage(context.Background(),original)

	// New message with two tool_use blocks (one matching, one new).
	different := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-2", ToolName: "read",
				Input: map[string]any{"path": "/a"}},
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-3", ToolName: "read",
				Input: map[string]any{"path": "/b"}},
		},
	}
	m.SaveMessage(context.Background(),different)

	entries := m.GetEntries()
	if len(entries) != 3 {
		t.Errorf("expected 3 entries (different count not deduped), got %d", len(entries))
	}
}

func TestSaveMessage_DedupToolResultNotSkippedForNonDup(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	m.SaveMessage(context.Background(),ai.NewTextMessage(ai.RoleUser, "run"))

	assistant := ai.Message{
		Role: ai.RoleAssistant,
		Content: []ai.ContentBlock{
			{Type: ai.ContentTypeToolUse, ToolUseID: "tu-1", ToolName: "bash",
				Input: map[string]any{"cmd": "ls"}},
		},
	}
	m.SaveMessage(context.Background(),assistant)

	// Normal tool_result — should be saved (not in skipped set).
	m.SaveMessage(context.Background(),ai.NewToolResultMessage("tu-1", "file1 file2", false))

	entries := m.GetEntries()
	if len(entries) != 3 {
		t.Errorf("expected 3 entries (normal result saved), got %d", len(entries))
	}
}

func TestToolCallsContentEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b []ai.ContentBlock
		want bool
	}{
		{
			name: "identical",
			a: []ai.ContentBlock{
				{ToolName: "write", Input: map[string]any{"path": "/a", "content": "x"}},
			},
			b: []ai.ContentBlock{
				{ToolName: "write", Input: map[string]any{"path": "/a", "content": "x"}},
			},
			want: true,
		},
		{
			name: "different_input",
			a: []ai.ContentBlock{
				{ToolName: "write", Input: map[string]any{"path": "/a"}},
			},
			b: []ai.ContentBlock{
				{ToolName: "write", Input: map[string]any{"path": "/b"}},
			},
			want: false,
		},
		{
			name: "different_name",
			a: []ai.ContentBlock{
				{ToolName: "read", Input: map[string]any{"path": "/a"}},
			},
			b: []ai.ContentBlock{
				{ToolName: "write", Input: map[string]any{"path": "/a"}},
			},
			want: false,
		},
		{
			name: "different_length",
			a: []ai.ContentBlock{
				{ToolName: "read", Input: map[string]any{"path": "/a"}},
			},
			b: []ai.ContentBlock{
				{ToolName: "read", Input: map[string]any{"path": "/a"}},
				{ToolName: "read", Input: map[string]any{"path": "/b"}},
			},
			want: false,
		},
		{
			name: "both_empty",
			a:    []ai.ContentBlock{},
			b:    []ai.ContentBlock{},
			want: true,
		},
		{
			name: "nil_inputs",
			a:    []ai.ContentBlock{{ToolName: "bash", Input: nil}},
			b:    []ai.ContentBlock{{ToolName: "bash", Input: nil}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolCallsContentEqual(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("toolCallsContentEqual = %v, want %v", got, tt.want)
			}
		})
	}
}

// ids is a test helper that extracts entry IDs for debugging.
func ids(entries []Entry) []string {
	result := make([]string, len(entries))
	for i, e := range entries {
		result[i] = e.ID
	}
	return result
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

// ---------------------------------------------------------------------------
// CollectUserPrompts tests
// ---------------------------------------------------------------------------

func TestCollectUserPrompts_Basic(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	// Create two sessions with user messages.
	id1 := m.NewSession()
	_ = id1
	_ = m.SaveMessage(context.Background(),ai.Message{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "first prompt"}}})
	_ = m.SaveMessage(context.Background(),ai.Message{Role: ai.RoleAssistant, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "response"}}})
	_ = m.SaveMessage(context.Background(),ai.Message{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "second prompt"}}})

	id2 := m.NewSession()
	_ = id2
	_ = m.SaveMessage(context.Background(),ai.Message{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "third prompt"}}})

	prompts := m.CollectUserPrompts(context.Background(),100)
	if len(prompts) != 3 {
		t.Fatalf("expected 3 prompts, got %d: %v", len(prompts), prompts)
	}

	// Most recent first.
	if prompts[0] != "third prompt" {
		t.Errorf("expected most recent first, got %q", prompts[0])
	}
}

func TestCollectUserPrompts_Deduplicates(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	m.NewSession()
	_ = m.SaveMessage(context.Background(),ai.Message{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "duplicate"}}})
	_ = m.SaveMessage(context.Background(),ai.Message{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "duplicate"}}})
	_ = m.SaveMessage(context.Background(),ai.Message{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: "unique"}}})

	prompts := m.CollectUserPrompts(context.Background(),100)
	if len(prompts) != 2 {
		t.Fatalf("expected 2 unique prompts, got %d: %v", len(prompts), prompts)
	}
}

func TestCollectUserPrompts_RespectsMax(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	m.NewSession()

	for i := 0; i < 20; i++ {
		_ = m.SaveMessage(context.Background(),ai.Message{Role: ai.RoleUser, Content: []ai.ContentBlock{{Type: ai.ContentTypeText, Text: fmt.Sprintf("prompt %d", i)}}})
	}

	prompts := m.CollectUserPrompts(context.Background(),5)
	if len(prompts) != 5 {
		t.Fatalf("expected 5 prompts (capped), got %d", len(prompts))
	}
}

func TestCollectUserPrompts_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)

	prompts := m.CollectUserPrompts(context.Background(),100)
	if len(prompts) != 0 {
		t.Errorf("expected 0 prompts from empty dir, got %d", len(prompts))
	}
}

func TestTruncatePreview(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short ASCII", "hello", 80, "hello"},
		{"exact limit", "abcde", 5, "abcde"},
		{"truncate ASCII", "abcdefghij", 8, "abcde..."},
		{"emoji at boundary", strings.Repeat("a", 75) + "\U0001F600\U0001F601", 80, strings.Repeat("a", 75) + "..."},
		{"multi-byte CJK", strings.Repeat("a", 56) + "\u4e16\u754c", 60, strings.Repeat("a", 56) + "..."},
		{"all emoji", "\U0001F600\U0001F601\U0001F602\U0001F603\U0001F604\U0001F605\U0001F606\U0001F607\U0001F608\U0001F609\U0001F60A\U0001F60B\U0001F60C\U0001F60D", 20, "\U0001F600\U0001F601\U0001F602\U0001F603..."},
		{"empty string", "", 10, ""},
		{"maxLen 0", "hello", 0, ""},
		{"maxLen 1", "hello", 1, "h"},
		{"maxLen 2", "hello", 2, "he"},
		{"maxLen 3", "hello", 3, "hel"},
		{"maxLen 4", "hello", 4, "h..."},
		{"maxLen 3 multi-byte", "\U0001F600\U0001F601", 3, ""},
		{"maxLen 1 multi-byte", "\U0001F600\U0001F601", 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncatePreview(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncatePreview(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("truncatePreview(%q, %d) produced invalid UTF-8: %q", tt.input, tt.maxLen, got)
			}
			if len(got) > tt.maxLen {
				t.Errorf("truncatePreview(%q, %d) result too long: %d bytes", tt.input, tt.maxLen, len(got))
			}
		})
	}
}
