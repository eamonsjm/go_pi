package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewOpenRouterProvider_NoKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	_, err := NewOpenRouterProvider("")
	if err == nil {
		t.Fatal("expected error when no API key provided")
	}
	if !strings.Contains(err.Error(), "API key not set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewOpenRouterProvider_ExplicitKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	p, err := NewOpenRouterProvider("sk-or-test-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.inner.apiKey != "sk-or-test-key" {
		t.Errorf("expected key %q, got %q", "sk-or-test-key", p.inner.apiKey)
	}
	if p.inner.baseURL != openrouterAPIURL {
		t.Errorf("expected baseURL %q, got %q", openrouterAPIURL, p.inner.baseURL)
	}
	if p.Name() != "openrouter" {
		t.Errorf("expected name %q, got %q", "openrouter", p.Name())
	}
}

func TestNewOpenRouterProvider_EnvKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-env-key")
	p, err := NewOpenRouterProvider("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.inner.apiKey != "sk-or-env-key" {
		t.Errorf("expected key %q, got %q", "sk-or-env-key", p.inner.apiKey)
	}
}

func TestNewOpenRouterProvider_ExplicitOverridesEnv(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-env-key")
	p, err := NewOpenRouterProvider("sk-or-explicit")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.inner.apiKey != "sk-or-explicit" {
		t.Errorf("expected explicit key %q, got %q", "sk-or-explicit", p.inner.apiKey)
	}
}

func TestOpenRouterProvider_StreamText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify auth header is set.
		auth := r.Header.Get("Authorization")
		if auth != "Bearer sk-or-test" {
			t.Errorf("expected Authorization %q, got %q", "Bearer sk-or-test", auth)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello \"}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"world\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"finish_reason\":\"stop\",\"delta\":{}}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	p := &OpenRouterProvider{
		inner: &OpenAIProvider{
			apiKey:     "sk-or-test",
			httpClient: server.Client(),
			baseURL:    server.URL,
		},
	}

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:        "anthropic/claude-3.5-sonnet",
		SystemPrompt: "You are helpful.",
		Messages:     []Message{NewTextMessage(RoleUser, "Hi")},
	})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	var gotText string
	var gotUsage *Usage
	for ev := range ch {
		switch ev.Type {
		case EventTextDelta:
			gotText += ev.Delta
		case EventMessageEnd:
			gotUsage = ev.Usage
		case EventError:
			t.Fatalf("unexpected error event: %v", ev.Error)
		}
	}

	if gotText != "Hello world" {
		t.Errorf("expected text %q, got %q", "Hello world", gotText)
	}
	if gotUsage == nil {
		t.Fatal("expected usage, got nil")
	}
	if gotUsage.InputTokens != 10 {
		t.Errorf("expected input tokens 10, got %d", gotUsage.InputTokens)
	}
	if gotUsage.OutputTokens != 2 {
		t.Errorf("expected output tokens 2, got %d", gotUsage.OutputTokens)
	}
}

func TestOpenRouterProvider_StreamHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"Invalid API key","type":"authentication_error"}}`)
	}))
	defer server.Close()

	p := &OpenRouterProvider{
		inner: &OpenAIProvider{
			apiKey:     "bad-key",
			httpClient: server.Client(),
			baseURL:    server.URL,
		},
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "anthropic/claude-3.5-sonnet",
		Messages: []Message{NewTextMessage(RoleUser, "Hi")},
	})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status %d, got %d", http.StatusUnauthorized, apiErr.StatusCode)
	}
}

func TestOpenRouterProvider_StreamContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until context is canceled.
		<-r.Context().Done()
	}))
	defer server.Close()

	p := &OpenRouterProvider{
		inner: &OpenAIProvider{
			apiKey:     "sk-or-test",
			httpClient: server.Client(),
			baseURL:    server.URL,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := p.Stream(ctx, StreamRequest{
		Model:    "anthropic/claude-3.5-sonnet",
		Messages: []Message{NewTextMessage(RoleUser, "Hi")},
	})
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}
