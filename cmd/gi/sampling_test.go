package main

import (
	"context"
	"testing"

	"github.com/ejm/go_pi/pkg/ai"
	"github.com/ejm/go_pi/pkg/mcp"
)

// mockProvider is a minimal ai.Provider for testing executeSampling.
type mockProvider struct {
	events []ai.StreamEvent
	err    error
}

func (m *mockProvider) Stream(_ context.Context, _ ai.StreamRequest) (<-chan ai.StreamEvent, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan ai.StreamEvent, len(m.events))
	for _, e := range m.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (m *mockProvider) Name() string { return "mock" }

func TestExecuteSampling(t *testing.T) {
	provider := &mockProvider{
		events: []ai.StreamEvent{
			{Type: ai.EventTextDelta, Delta: "Hello "},
			{Type: ai.EventTextDelta, Delta: "world"},
			{Type: ai.EventMessageEnd},
		},
	}

	req := mcp.SamplingRequest{
		Messages: []mcp.SamplingMessage{
			{Role: "user", Content: mcp.ContentItem{Type: "text", Text: "say hello"}},
		},
		MaxTokens: 100,
	}

	resp, err := executeSampling(context.Background(), provider, "test-model", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Role != "assistant" {
		t.Errorf("expected role=assistant, got %q", resp.Role)
	}
	if resp.Content.Text != "Hello world" {
		t.Errorf("expected text=%q, got %q", "Hello world", resp.Content.Text)
	}
	if resp.Model != "test-model" {
		t.Errorf("expected model=test-model, got %q", resp.Model)
	}
}

func TestExecuteSampling_StreamError(t *testing.T) {
	provider := &mockProvider{
		events: []ai.StreamEvent{
			{Type: ai.EventTextDelta, Delta: "partial"},
			{Type: ai.EventError, Error: context.DeadlineExceeded},
		},
	}

	req := mcp.SamplingRequest{
		Messages:  []mcp.SamplingMessage{{Role: "user", Content: mcp.ContentItem{Type: "text", Text: "hello"}}},
		MaxTokens: 100,
	}

	_, err := executeSampling(context.Background(), provider, "test-model", req)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSamplingBridge_HandleNilProvider(t *testing.T) {
	sb := &samplingBridge{}
	_, err := sb.Handle(context.Background(), "server", mcp.SamplingRequest{})
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
}

func TestSamplingBridge_ConfirmNilProgram(t *testing.T) {
	sb := &samplingBridge{}
	approved, err := sb.Confirm("server", mcp.SamplingRequest{})
	if err == nil {
		t.Fatal("expected error for nil program")
	}
	if approved {
		t.Error("expected approved=false")
	}
}
