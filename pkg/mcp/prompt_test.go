package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ejm/go_pi/pkg/skill"
)

func TestBridgePrompt(t *testing.T) {
	info := PromptInfo{
		Name:        "summarize",
		Description: "Summarize a topic",
		Arguments: []PromptArgument{
			{Name: "topic", Description: "The topic", Required: true},
			{Name: "length", Description: "Output length", Required: false},
		},
	}

	s := BridgePrompt("myserver", info, nil)

	if s.Name != "mcp__myserver__summarize" {
		t.Errorf("Name = %q, want %q", s.Name, "mcp__myserver__summarize")
	}
	if s.Description != "Summarize a topic" {
		t.Errorf("Description = %q, want %q", s.Description, "Summarize a topic")
	}
	if !s.UserInvocable {
		t.Error("UserInvocable should be true")
	}
	if s.Source != "mcp" {
		t.Errorf("Source = %q, want %q", s.Source, "mcp")
	}
	if len(s.Arguments) != 2 {
		t.Fatalf("len(Arguments) = %d, want 2", len(s.Arguments))
	}
	if !s.Arguments[0].Required {
		t.Error("first argument should be required")
	}
	if s.Arguments[1].Required {
		t.Error("second argument should not be required")
	}
}

func TestBridgePrompt_NoArgs(t *testing.T) {
	info := PromptInfo{
		Name:        "greet",
		Description: "A greeting",
	}

	s := BridgePrompt("srv", info, nil)
	if len(s.Arguments) != 0 {
		t.Errorf("len(Arguments) = %d, want 0", len(s.Arguments))
	}
}

func TestValidatePromptArgs(t *testing.T) {
	args := []PromptArgument{
		{Name: "topic", Required: true},
		{Name: "style", Required: true},
		{Name: "length", Required: false},
	}

	// All required present.
	err := ValidatePromptArgs(args, map[string]string{"topic": "go", "style": "brief"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Missing one required.
	err = ValidatePromptArgs(args, map[string]string{"topic": "go"})
	if err == nil {
		t.Error("expected error for missing required arg")
	}

	// Missing all required.
	err = ValidatePromptArgs(args, map[string]string{"length": "short"})
	if err == nil {
		t.Error("expected error for missing required args")
	}

	// No args required.
	err = ValidatePromptArgs(nil, nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDiscoverPrompts(t *testing.T) {
	pages := []PromptsListPage{
		{
			Prompts: []PromptInfo{
				{Name: "alpha", Description: "Alpha prompt"},
				{Name: "beta", Description: "Beta prompt"},
			},
			NextCursor: "page2",
		},
		{
			Prompts: []PromptInfo{
				{Name: "gamma", Description: "Gamma prompt"},
			},
		},
	}

	callCount := 0
	skills, err := DiscoverPrompts(context.Background(), "testserver",
		func(_ context.Context, cursor string) (*PromptsListPage, error) {
			idx := callCount
			callCount++
			return &pages[idx], nil
		}, nil)
	if err != nil {
		t.Fatalf("DiscoverPrompts error: %v", err)
	}
	if len(skills) != 3 {
		t.Fatalf("len(skills) = %d, want 3", len(skills))
	}
	if skills[0].Name != "mcp__testserver__alpha" {
		t.Errorf("skills[0].Name = %q", skills[0].Name)
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
}

func TestDiffSkillCount(t *testing.T) {
	makeSkills := func(names ...string) []*skill.Skill {
		var out []*skill.Skill
		for _, n := range names {
			out = append(out, BridgePrompt("s", PromptInfo{Name: n}, nil))
		}
		return out
	}

	tests := []struct {
		name    string
		old     []*skill.Skill
		new     []*skill.Skill
		added   int
		removed int
	}{
		{"empty to some", nil, makeSkills("a", "b"), 2, 0},
		{"some to empty", makeSkills("a", "b"), nil, 0, 2},
		{"overlap", makeSkills("a", "b"), makeSkills("b", "c"), 1, 1},
		{"identical", makeSkills("a"), makeSkills("a"), 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, r := diffSkillCount(tt.old, tt.new)
			if a != tt.added || r != tt.removed {
				t.Errorf("added=%d removed=%d, want added=%d removed=%d",
					a, r, tt.added, tt.removed)
			}
		})
	}
}

// mockPromptGetter implements PromptGetter for testing.
type mockPromptGetter struct {
	name   string
	result *PromptsGetResult
	err    error
	// captures the last call
	calledName string
	calledArgs map[string]string
}

func (m *mockPromptGetter) ServerName() string { return m.name }
func (m *mockPromptGetter) GetPrompt(_ context.Context, name string, args map[string]string) (*PromptsGetResult, error) {
	m.calledName = name
	m.calledArgs = args
	return m.result, m.err
}

func TestBridgePrompt_WithGetter(t *testing.T) {
	getter := &mockPromptGetter{
		name: "testserver",
		result: &PromptsGetResult{
			Description: "Summary",
			Messages: []PromptMessage{
				{Role: "user", Content: ContentItem{Type: "text", Text: "Summarize: Go"}},
			},
		},
	}

	info := PromptInfo{
		Name:        "summarize",
		Description: "Summarize a topic",
		Arguments: []PromptArgument{
			{Name: "topic", Description: "The topic", Required: true},
		},
	}

	s := BridgePrompt("testserver", info, getter)

	if s.Executor == nil {
		t.Fatal("Executor should be set when getter is non-nil")
	}

	result, err := s.Executor(context.Background(), map[string]string{"topic": "Go"})
	if err != nil {
		t.Fatalf("Executor error: %v", err)
	}
	if getter.calledName != "summarize" {
		t.Errorf("calledName = %q, want %q", getter.calledName, "summarize")
	}
	if getter.calledArgs["topic"] != "Go" {
		t.Errorf("calledArgs[topic] = %q, want %q", getter.calledArgs["topic"], "Go")
	}
	if result == "" {
		t.Error("result should not be empty")
	}
}

func TestBridgePrompt_ExecutorValidatesArgs(t *testing.T) {
	getter := &mockPromptGetter{name: "srv"}

	info := PromptInfo{
		Name: "needs-args",
		Arguments: []PromptArgument{
			{Name: "topic", Required: true},
		},
	}

	s := BridgePrompt("srv", info, getter)

	// Missing required arg should fail before RPC.
	_, err := s.Executor(context.Background(), map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing required argument")
	}
}

func TestBridgePrompt_NilGetter(t *testing.T) {
	s := BridgePrompt("srv", PromptInfo{Name: "test"}, nil)
	if s.Executor != nil {
		t.Error("Executor should be nil when getter is nil")
	}
}

func TestFormatPromptResult(t *testing.T) {
	result := &PromptsGetResult{
		Description: "A test prompt",
		Messages: []PromptMessage{
			{Role: "user", Content: ContentItem{Type: "text", Text: "Hello"}},
			{Role: "assistant", Content: ContentItem{Type: "text", Text: "Hi there"}},
		},
	}

	got := formatPromptResult(result)

	if got != "A test prompt\n\n[user]\nHello\n\n[assistant]\nHi there" {
		t.Errorf("formatPromptResult = %q", got)
	}
}

func TestFormatPromptResult_NoDescription(t *testing.T) {
	result := &PromptsGetResult{
		Messages: []PromptMessage{
			{Role: "user", Content: ContentItem{Type: "text", Text: "Do the thing"}},
		},
	}

	got := formatPromptResult(result)
	if got != "[user]\nDo the thing" {
		t.Errorf("formatPromptResult = %q", got)
	}
}

func TestListPrompts_RPC(t *testing.T) {
	page := PromptsListPage{
		Prompts: []PromptInfo{
			{Name: "test-prompt", Description: "A test"},
		},
	}
	pageJSON, _ := json.Marshal(page)

	mt := newMockTransport()
	client := NewClient(mt, nil)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		mt.incoming <- json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":` + string(pageJSON) + `}`)
	}()

	result, err := client.ListPrompts(ctx, "")
	if err != nil {
		t.Fatalf("ListPrompts error: %v", err)
	}
	if len(result.Prompts) != 1 {
		t.Fatalf("len(Prompts) = %d, want 1", len(result.Prompts))
	}
	if result.Prompts[0].Name != "test-prompt" {
		t.Errorf("Prompts[0].Name = %q", result.Prompts[0].Name)
	}

	// Verify the request method.
	sent := mt.getSent()
	if len(sent) != 1 {
		t.Fatalf("len(sent) = %d, want 1", len(sent))
	}
	var req JSONRPCRequest
	if err := json.Unmarshal(sent[0], &req); err != nil {
		t.Fatalf("unmarshal sent: %v", err)
	}
	if req.Method != "prompts/list" {
		t.Errorf("method = %q, want %q", req.Method, "prompts/list")
	}
}

func TestGetPrompt_RPC(t *testing.T) {
	promptResult := PromptsGetResult{
		Description: "Summary prompt",
		Messages: []PromptMessage{
			{
				Role: "user",
				Content: ContentItem{
					Type: "text",
					Text: "Please summarize: Go programming",
				},
			},
		},
	}
	resultJSON, _ := json.Marshal(promptResult)

	mt := newMockTransport()
	client := NewClient(mt, nil)
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		mt.incoming <- json.RawMessage(`{"jsonrpc":"2.0","id":1,"result":` + string(resultJSON) + `}`)
	}()

	result, err := client.GetPrompt(ctx, "summarize", map[string]string{"topic": "Go programming"})
	if err != nil {
		t.Fatalf("GetPrompt error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(result.Messages))
	}
	if result.Messages[0].Content.Text != "Please summarize: Go programming" {
		t.Errorf("Messages[0].Content.Text = %q", result.Messages[0].Content.Text)
	}

	// Verify the request.
	sent := mt.getSent()
	var req JSONRPCRequest
	if err := json.Unmarshal(sent[0], &req); err != nil {
		t.Fatalf("unmarshal sent: %v", err)
	}
	if req.Method != "prompts/get" {
		t.Errorf("method = %q, want %q", req.Method, "prompts/get")
	}
}
