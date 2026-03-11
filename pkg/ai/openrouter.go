package ai

import (
	"context"
	"fmt"
	"net/http"
	"os"
)

const (
	openrouterAPIURL = "https://openrouter.ai/api/v1/chat/completions"
)

// OpenRouterProvider implements the Provider interface using OpenRouter's
// OpenAI-compatible API. It delegates all streaming and request construction
// to OpenAIProvider with a different base URL.
type OpenRouterProvider struct {
	inner *OpenAIProvider
}

// NewOpenRouterProvider creates a new OpenRouter provider.
// If apiKey is empty, it reads from the OPENROUTER_API_KEY environment variable.
func NewOpenRouterProvider(apiKey string) (*OpenRouterProvider, error) {
	if apiKey == "" {
		apiKey = os.Getenv("OPENROUTER_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("openrouter: API key not set (provide key or set OPENROUTER_API_KEY)")
	}
	inner := &OpenAIProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		baseURL:    openrouterAPIURL,
	}
	return &OpenRouterProvider{inner: inner}, nil
}

func (p *OpenRouterProvider) Name() string { return "openrouter" }

// Stream sends a streaming request via OpenRouter.
func (p *OpenRouterProvider) Stream(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error) {
	return p.inner.Stream(ctx, req)
}
