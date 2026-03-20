package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAzureOpenAIProvider_NoKey(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_KEY", "")
	_, err := NewAzureOpenAIProvider("", "https://test.openai.azure.com", "gpt-4o")
	if err == nil {
		t.Fatal("expected error when no API key provided")
	}
	if !strings.Contains(err.Error(), "API key not set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewAzureOpenAIProvider_NoEndpoint(t *testing.T) {
	t.Setenv("AZURE_OPENAI_ENDPOINT", "")
	_, err := NewAzureOpenAIProvider("test-key", "", "gpt-4o")
	if err == nil {
		t.Fatal("expected error when no endpoint provided")
	}
	if !strings.Contains(err.Error(), "endpoint not set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewAzureOpenAIProvider_NoDeployment(t *testing.T) {
	t.Setenv("AZURE_OPENAI_DEPLOYMENT", "")
	_, err := NewAzureOpenAIProvider("test-key", "https://test.openai.azure.com", "")
	if err == nil {
		t.Fatal("expected error when no deployment provided")
	}
	if !strings.Contains(err.Error(), "deployment not set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestNewAzureOpenAIProvider_ExplicitConfig(t *testing.T) {
	p, err := NewAzureOpenAIProvider("test-key", "https://myresource.openai.azure.com", "gpt-4o-deploy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.apiKey != "test-key" {
		t.Errorf("expected key %q, got %q", "test-key", p.apiKey)
	}
	if p.endpoint != "https://myresource.openai.azure.com" {
		t.Errorf("expected endpoint %q, got %q", "https://myresource.openai.azure.com", p.endpoint)
	}
	if p.deployment != "gpt-4o-deploy" {
		t.Errorf("expected deployment %q, got %q", "gpt-4o-deploy", p.deployment)
	}
	if p.Name() != "azure" {
		t.Errorf("expected name %q, got %q", "azure", p.Name())
	}
}

func TestNewAzureOpenAIProvider_EnvVars(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_KEY", "env-key")
	t.Setenv("AZURE_OPENAI_ENDPOINT", "https://env.openai.azure.com")
	t.Setenv("AZURE_OPENAI_DEPLOYMENT", "env-deploy")
	t.Setenv("AZURE_OPENAI_API_VERSION", "2024-06-01")

	p, err := NewAzureOpenAIProvider("", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.apiKey != "env-key" {
		t.Errorf("expected key %q, got %q", "env-key", p.apiKey)
	}
	if p.endpoint != "https://env.openai.azure.com" {
		t.Errorf("expected endpoint %q, got %q", "https://env.openai.azure.com", p.endpoint)
	}
	if p.deployment != "env-deploy" {
		t.Errorf("expected deployment %q, got %q", "env-deploy", p.deployment)
	}
	if p.apiVersion != "2024-06-01" {
		t.Errorf("expected apiVersion %q, got %q", "2024-06-01", p.apiVersion)
	}
}

func TestAzureOpenAIProvider_RequestURL(t *testing.T) {
	p := &AzureOpenAIProvider{
		endpoint:   "https://myresource.openai.azure.com",
		deployment: "gpt-4o",
		apiVersion: "2024-10-21",
	}
	want := "https://myresource.openai.azure.com/openai/deployments/gpt-4o/chat/completions?api-version=2024-10-21"
	got := p.requestURL()
	if got != want {
		t.Errorf("requestURL() = %q, want %q", got, want)
	}
}

func TestAzureOpenAIProvider_EndpointTrailingSlash(t *testing.T) {
	p, err := NewAzureOpenAIProvider("key", "https://myresource.openai.azure.com/", "deploy")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.HasSuffix(p.endpoint, "/") {
		t.Errorf("endpoint should not have trailing slash: %q", p.endpoint)
	}
	// Verify requestURL doesn't produce double slashes in the path.
	url := p.requestURL()
	pathStart := strings.Index(url, "//") + 2
	pathPortion := url[pathStart:]
	if afterHost := strings.Index(pathPortion, "/"); afterHost >= 0 {
		pathOnly := pathPortion[afterHost:]
		if strings.Contains(pathOnly, "//") {
			t.Errorf("URL path should not have double slashes: %q", url)
		}
	}
}

func TestAzureOpenAIProvider_StreamAuth(t *testing.T) {
	var receivedAPIKey string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAPIKey = r.Header.Get("api-key")
		// Verify no Bearer auth is used.
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("Azure should not use Authorization header, got %q", auth)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	p := &AzureOpenAIProvider{
		apiKey:     "test-azure-key",
		endpoint:   server.URL,
		deployment: "gpt-4o",
		apiVersion: azureDefaultAPIVersion,
		httpClient: server.Client(),
		inner:      &OpenAIProvider{apiKey: "test-azure-key"},
	}

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:    "gpt-4o",
		Messages: []Message{NewTextMessage(RoleUser, "Hello")},
	})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	// Drain the channel.
	for range ch {
	}

	if receivedAPIKey != "test-azure-key" {
		t.Errorf("expected api-key header %q, got %q", "test-azure-key", receivedAPIKey)
	}
}

func TestAzureOpenAIProvider_StreamText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request path includes deployment.
		if !strings.Contains(r.URL.Path, "/openai/deployments/") {
			t.Errorf("expected deployment in path, got %s", r.URL.Path)
		}
		// Verify api-version query param.
		if v := r.URL.Query().Get("api-version"); v == "" {
			t.Errorf("expected api-version query param")
		}

		// Verify request body is valid.
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to parse request body: %v", err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hello \"}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"world\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"id\":\"1\",\"choices\":[{\"index\":0,\"finish_reason\":\"stop\",\"delta\":{}}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	p := &AzureOpenAIProvider{
		apiKey:     "test-key",
		endpoint:   server.URL,
		deployment: "gpt-4o",
		apiVersion: azureDefaultAPIVersion,
		httpClient: server.Client(),
		inner:      &OpenAIProvider{apiKey: "test-key"},
	}

	ch, err := p.Stream(context.Background(), StreamRequest{
		Model:        "gpt-4o",
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
}
