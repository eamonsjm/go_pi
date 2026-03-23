package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/session"
)

// ---------------------------------------------------------------------------
// countMessageTypes
// ---------------------------------------------------------------------------

func TestCountMessageTypes(t *testing.T) {
	tests := []struct {
		name                                       string
		msgs                                       []ai.Message
		wantUser, wantAssistant, wantTool, wantRes int
	}{
		{
			name: "empty",
			msgs: nil,
		},
		{
			name: "single user text",
			msgs: []ai.Message{
				ai.NewTextMessage(ai.RoleUser, "hello"),
			},
			wantUser: 1,
		},
		{
			name: "single assistant text",
			msgs: []ai.Message{
				ai.NewTextMessage(ai.RoleAssistant, "hi"),
			},
			wantAssistant: 1,
		},
		{
			name: "assistant with tool use",
			msgs: []ai.Message{
				{Role: ai.RoleAssistant, Content: []ai.ContentBlock{
					{Type: ai.ContentTypeText, Text: "let me check"},
					{Type: ai.ContentTypeToolUse, ToolName: "bash", ToolUseID: "t1"},
				}},
			},
			wantAssistant: 1,
			wantTool:      1,
		},
		{
			name: "user message with tool result only",
			msgs: []ai.Message{
				{Role: ai.RoleUser, Content: []ai.ContentBlock{
					{Type: ai.ContentTypeToolResult, ToolResultID: "t1", Content: "ok"},
				}},
			},
			wantRes: 1,
			// No user count because there's no text block.
		},
		{
			name: "user message with text and tool result",
			msgs: []ai.Message{
				{Role: ai.RoleUser, Content: []ai.ContentBlock{
					{Type: ai.ContentTypeText, Text: "here is the result"},
					{Type: ai.ContentTypeToolResult, ToolResultID: "t1", Content: "ok"},
				}},
			},
			wantUser: 1,
			wantRes:  1,
		},
		{
			name: "mixed conversation",
			msgs: []ai.Message{
				ai.NewTextMessage(ai.RoleUser, "hello"),
				{Role: ai.RoleAssistant, Content: []ai.ContentBlock{
					{Type: ai.ContentTypeText, Text: "checking"},
					{Type: ai.ContentTypeToolUse, ToolName: "bash", ToolUseID: "t1"},
					{Type: ai.ContentTypeToolUse, ToolName: "read", ToolUseID: "t2"},
				}},
				{Role: ai.RoleUser, Content: []ai.ContentBlock{
					{Type: ai.ContentTypeToolResult, ToolResultID: "t1", Content: "ok"},
					{Type: ai.ContentTypeToolResult, ToolResultID: "t2", Content: "data"},
				}},
				ai.NewTextMessage(ai.RoleAssistant, "done"),
			},
			wantUser:      1,
			wantAssistant: 2,
			wantTool:      2,
			wantRes:       2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, a, tc, tr := countMessageTypes(tt.msgs)
			if u != tt.wantUser {
				t.Errorf("user = %d, want %d", u, tt.wantUser)
			}
			if a != tt.wantAssistant {
				t.Errorf("assistant = %d, want %d", a, tt.wantAssistant)
			}
			if tc != tt.wantTool {
				t.Errorf("toolCalls = %d, want %d", tc, tt.wantTool)
			}
			if tr != tt.wantRes {
				t.Errorf("toolResults = %d, want %d", tr, tt.wantRes)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// pricingForModel
// ---------------------------------------------------------------------------

func TestPricingForModel(t *testing.T) {
	tests := []struct {
		model      string
		wantInput  float64
		wantOutput float64
	}{
		{"claude-opus-4-6", 15.0, 75.0},
		{"claude-opus-4-20260301", 15.0, 75.0},
		{"claude-sonnet-4-6", 3.0, 15.0},
		{"claude-haiku-4-5", 0.80, 4.0},
		{"gpt-4o-2024-05-13", 2.50, 10.0},
		{"gpt-4-turbo-preview", 10.0, 30.0},
		{"gemini-2.0-flash", 0.10, 0.40},
		{"gemini-1.5-pro-latest", 1.25, 5.0},
		// Unknown model falls back to Sonnet rates.
		{"unknown-model", 3.0, 15.0},
		{"", 3.0, 15.0},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			p := pricingForModel(tt.model)
			if p.inputPerM != tt.wantInput {
				t.Errorf("inputPerM = %v, want %v", p.inputPerM, tt.wantInput)
			}
			if p.outputPerM != tt.wantOutput {
				t.Errorf("outputPerM = %v, want %v", p.outputPerM, tt.wantOutput)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// calculateCost
// ---------------------------------------------------------------------------

func TestCalculateCost(t *testing.T) {
	tests := []struct {
		name  string
		usage ai.Usage
		model string
		want  float64
	}{
		{
			name:  "zero usage",
			usage: ai.Usage{},
			model: "claude-sonnet-4-6",
			want:  0,
		},
		{
			name: "input only",
			usage: ai.Usage{
				InputTokens: 1_000_000,
			},
			model: "claude-sonnet-4-6",
			want:  3.0, // 1M * 3.0 / 1M
		},
		{
			name: "output only",
			usage: ai.Usage{
				OutputTokens: 1_000_000,
			},
			model: "claude-sonnet-4-6",
			want:  15.0, // 1M * 15.0 / 1M
		},
		{
			name: "with cache read",
			usage: ai.Usage{
				InputTokens: 1_000_000,
				CacheRead:   800_000,
			},
			model: "claude-sonnet-4-6",
			// nonCacheInput = 200_000 → 200k * 3.0 / 1M = 0.6
			// cacheRead = 800k * 0.30 / 1M = 0.24
			want: 0.84,
		},
		{
			name: "with cache write",
			usage: ai.Usage{
				InputTokens: 500_000,
				CacheWrite:  500_000,
			},
			model: "claude-sonnet-4-6",
			// nonCacheInput = 500k * 3.0 / 1M = 1.5
			// cacheWrite = 500k * 3.75 / 1M = 1.875
			want: 3.375,
		},
		{
			name: "opus pricing",
			usage: ai.Usage{
				InputTokens:  100_000,
				OutputTokens: 50_000,
			},
			model: "claude-opus-4-6",
			// input: 100k * 15 / 1M = 1.5
			// output: 50k * 75 / 1M = 3.75
			want: 5.25,
		},
		{
			name: "cache read exceeds input clamps to zero",
			usage: ai.Usage{
				InputTokens: 100,
				CacheRead:   200,
			},
			model: "claude-sonnet-4-6",
			// nonCacheInput clamped to 0
			// cacheRead = 200 * 0.30 / 1M ≈ 0.00000006
			want: 0.00000006,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateCost(tt.usage, tt.model)
			diff := got - tt.want
			if diff < 0 {
				diff = -diff
			}
			if diff > 0.0001 {
				t.Errorf("calculateCost() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// providerAPIType
// ---------------------------------------------------------------------------

func TestProviderAPIType(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"anthropic", "anthropic-messages"},
		{"openai", "openai-chat"},
		{"azure", "openai-chat"},
		{"openrouter", "openai-chat"},
		{"gemini", "gemini-stream"},
		{"bedrock", "bedrock-converse"},
		{"unknown", "unknown"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := providerAPIType(tt.name)
			if got != tt.want {
				t.Errorf("providerAPIType(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// providerEndpoint
// ---------------------------------------------------------------------------

func TestProviderEndpoint(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"anthropic", "https://api.anthropic.com/"},
		{"openai", "https://api.openai.com/"},
		{"openrouter", "https://openrouter.ai/"},
		{"gemini", "https://generativelanguage.googleapis.com/"},
		{"azure", "(Azure deployment)"},
		{"bedrock", "(AWS Bedrock)"},
		{"unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := providerEndpoint(tt.name)
			if got != tt.want {
				t.Errorf("providerEndpoint(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// shortID
// ---------------------------------------------------------------------------

func TestShortID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"abcdef123456789", "abcdef123456"},
		{"abcdef123456", "abcdef123456"},
		{"short", "short"},
		{"", ""},
		{"exactly12ch", "exactly12ch"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := shortID(tt.input)
			if got != tt.want {
				t.Errorf("shortID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// formatSessionList
// ---------------------------------------------------------------------------

func TestFormatSessionList(t *testing.T) {
	ts := time.Date(2026, 3, 22, 14, 30, 0, 0, time.UTC)

	t.Run("single session", func(t *testing.T) {
		sessions := []session.SessionInfo{
			{ID: "abc123def456xyz", UpdatedAt: ts, Entries: 5},
		}
		got := formatSessionList(sessions)

		if !strings.Contains(got, "Available sessions:") {
			t.Error("missing header")
		}
		if !strings.Contains(got, "[1]") {
			t.Error("missing index")
		}
		if !strings.Contains(got, "abc123def456") {
			t.Error("missing short ID")
		}
		if !strings.Contains(got, "5 entries") {
			t.Error("missing entry count")
		}
		if !strings.Contains(got, "2026-03-22 14:30") {
			t.Error("missing date")
		}
		if !strings.Contains(got, "/resume") {
			t.Error("missing usage hint")
		}
	})

	t.Run("with branches", func(t *testing.T) {
		sessions := []session.SessionInfo{
			{ID: "abc123def456xyz", UpdatedAt: ts, Entries: 3, Branches: 3},
		}
		got := formatSessionList(sessions)
		if !strings.Contains(got, "[3 branches]") {
			t.Error("missing branches info")
		}
	})

	t.Run("single branch not shown", func(t *testing.T) {
		sessions := []session.SessionInfo{
			{ID: "abc123def456xyz", UpdatedAt: ts, Entries: 3, Branches: 1},
		}
		got := formatSessionList(sessions)
		if strings.Contains(got, "branch") {
			t.Error("single branch should not be shown")
		}
	})

	t.Run("with preview", func(t *testing.T) {
		sessions := []session.SessionInfo{
			{ID: "abc123def456xyz", UpdatedAt: ts, Entries: 2, Preview: "What is Go?"},
		}
		got := formatSessionList(sessions)
		if !strings.Contains(got, `"What is Go?"`) {
			t.Error("missing preview")
		}
	})

	t.Run("multiple sessions numbered sequentially", func(t *testing.T) {
		sessions := []session.SessionInfo{
			{ID: "session-aaa-xxx", UpdatedAt: ts, Entries: 1},
			{ID: "session-bbb-xxx", UpdatedAt: ts, Entries: 2},
			{ID: "session-ccc-xxx", UpdatedAt: ts, Entries: 3},
		}
		got := formatSessionList(sessions)
		if !strings.Contains(got, "[1]") || !strings.Contains(got, "[2]") || !strings.Contains(got, "[3]") {
			t.Error("missing sequential indices")
		}
	})
}

// ---------------------------------------------------------------------------
// resolveSessionArg
// ---------------------------------------------------------------------------

func TestResolveSessionArg(t *testing.T) {
	all := []session.SessionInfo{
		{ID: "session-aaa-111"},
		{ID: "session-bbb-222"},
		{ID: "session-ccc-333"},
	}

	t.Run("exact match", func(t *testing.T) {
		got := resolveSessionArg("session-bbb-222", nil, all)
		if got != "session-bbb-222" {
			t.Errorf("got %q, want session-bbb-222", got)
		}
	})

	t.Run("prefix match unique", func(t *testing.T) {
		got := resolveSessionArg("session-aaa", nil, all)
		if got != "session-aaa-111" {
			t.Errorf("got %q, want session-aaa-111", got)
		}
	})

	t.Run("prefix match ambiguous", func(t *testing.T) {
		got := resolveSessionArg("session-", nil, all)
		if got != "" {
			t.Errorf("ambiguous prefix should return empty, got %q", got)
		}
	})

	t.Run("numeric index from lastListed", func(t *testing.T) {
		listed := []session.SessionInfo{
			{ID: "listed-xxx"},
			{ID: "listed-yyy"},
		}
		got := resolveSessionArg("2", listed, all)
		if got != "listed-yyy" {
			t.Errorf("got %q, want listed-yyy", got)
		}
	})

	t.Run("numeric index falls back to allSessions when no lastListed", func(t *testing.T) {
		got := resolveSessionArg("1", nil, all)
		if got != "session-aaa-111" {
			t.Errorf("got %q, want session-aaa-111", got)
		}
	})

	t.Run("numeric index out of range", func(t *testing.T) {
		got := resolveSessionArg("99", nil, all)
		if got != "" {
			t.Errorf("out-of-range index should return empty, got %q", got)
		}
	})

	t.Run("zero index", func(t *testing.T) {
		got := resolveSessionArg("0", nil, all)
		// 0 is not >= 1, so numeric path skipped; falls through to prefix match.
		if got != "" {
			t.Errorf("zero index should return empty, got %q", got)
		}
	})

	t.Run("negative index", func(t *testing.T) {
		got := resolveSessionArg("-1", nil, all)
		if got != "" {
			t.Errorf("negative index should return empty, got %q", got)
		}
	})

	t.Run("no match", func(t *testing.T) {
		got := resolveSessionArg("nonexistent", nil, all)
		if got != "" {
			t.Errorf("expected empty for no match, got %q", got)
		}
	})

	t.Run("empty all sessions", func(t *testing.T) {
		got := resolveSessionArg("anything", nil, nil)
		if got != "" {
			t.Errorf("expected empty with no sessions, got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// NewNameCommand
// ---------------------------------------------------------------------------

func TestNewNameCommand_Metadata(t *testing.T) {
	cmd := NewNameCommand(session.NewManager(t.TempDir()), &Header{}, &ChatView{})

	if cmd.Name != "name" {
		t.Errorf("Name = %q, want %q", cmd.Name, "name")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

// ---------------------------------------------------------------------------
// NewNewSessionCommand
// ---------------------------------------------------------------------------

func TestNewNewSessionCommand_Metadata(t *testing.T) {
	mgr := session.NewManager(t.TempDir())
	cv := &ChatView{}
	h := &Header{}

	cmd := NewNewSessionCommand(nil, mgr, cv, h)

	if cmd.Name != "new" {
		t.Errorf("Name = %q, want %q", cmd.Name, "new")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

// ---------------------------------------------------------------------------
// NewSessionInfoCommand
// ---------------------------------------------------------------------------

func TestNewSessionInfoCommand_Metadata(t *testing.T) {
	mgr := session.NewManager(t.TempDir())

	cmd := NewSessionInfoCommand(mgr, &ChatView{}, func() ProviderInfo {
		return ProviderInfo{}
	}, func() ai.Usage {
		return ai.Usage{}
	})

	if cmd.Name != "session" {
		t.Errorf("Name = %q, want %q", cmd.Name, "session")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}

// ---------------------------------------------------------------------------
// NewResumeCommand
// ---------------------------------------------------------------------------

func TestNewResumeCommand_Metadata(t *testing.T) {
	mgr := session.NewManager(t.TempDir())
	cv := &ChatView{}
	h := &Header{}

	cmd := NewResumeCommand(t.Context(), nil, mgr, cv, h)

	if cmd.Name != "resume" {
		t.Errorf("Name = %q, want %q", cmd.Name, "resume")
	}
	if cmd.Description == "" {
		t.Error("Description should not be empty")
	}
	if cmd.Execute == nil {
		t.Fatal("Execute should not be nil")
	}
}
