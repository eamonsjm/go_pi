package ai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	azureDefaultAPIVersion = "2024-10-21"
	azureDefaultMaxToks    = 4096
)

// AzureOpenAIProvider implements the Provider interface for Azure OpenAI deployments.
// It uses the same request/response format as OpenAI but with deployment-based URLs
// and api-key authentication.
type AzureOpenAIProvider struct {
	apiKey     string
	endpoint   string // e.g. "https://my-resource.openai.azure.com"
	deployment string // e.g. "gpt-4o"
	apiVersion string
	httpClient *http.Client
	// inner is used to access shared request body building and SSE parsing.
	inner *OpenAIProvider
}

// NewAzureOpenAIProvider creates a new Azure OpenAI provider.
// Config is read from environment variables if not provided:
//   - AZURE_OPENAI_API_KEY: the API key
//   - AZURE_OPENAI_ENDPOINT: the resource endpoint (e.g. https://my-resource.openai.azure.com)
//   - AZURE_OPENAI_DEPLOYMENT: the deployment name
//   - AZURE_OPENAI_API_VERSION: optional, defaults to 2024-10-21
func NewAzureOpenAIProvider(apiKey, endpoint, deployment string) (*AzureOpenAIProvider, error) {
	if apiKey == "" {
		apiKey = os.Getenv("AZURE_OPENAI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("azure openai: API key not set (provide key or set AZURE_OPENAI_API_KEY)")
	}

	if endpoint == "" {
		endpoint = os.Getenv("AZURE_OPENAI_ENDPOINT")
	}
	if endpoint == "" {
		return nil, fmt.Errorf("azure openai: endpoint not set (provide endpoint or set AZURE_OPENAI_ENDPOINT)")
	}
	endpoint = strings.TrimRight(endpoint, "/")

	if deployment == "" {
		deployment = os.Getenv("AZURE_OPENAI_DEPLOYMENT")
	}
	if deployment == "" {
		return nil, fmt.Errorf("azure openai: deployment not set (provide deployment or set AZURE_OPENAI_DEPLOYMENT)")
	}

	apiVersion := os.Getenv("AZURE_OPENAI_API_VERSION")
	if apiVersion == "" {
		apiVersion = azureDefaultAPIVersion
	}

	return &AzureOpenAIProvider{
		apiKey:     apiKey,
		endpoint:   endpoint,
		deployment: deployment,
		apiVersion: apiVersion,
		httpClient: &http.Client{},
		inner:      &OpenAIProvider{apiKey: apiKey},
	}, nil
}

func (p *AzureOpenAIProvider) Name() string { return "azure" }

// requestURL builds the Azure OpenAI deployment URL.
func (p *AzureOpenAIProvider) requestURL() string {
	return fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		p.endpoint, p.deployment, p.apiVersion)
}

// Stream sends a streaming request to Azure OpenAI and returns a channel of StreamEvents.
func (p *AzureOpenAIProvider) Stream(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error) {
	body, err := p.inner.buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("azure openai: failed to build request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.requestURL(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("azure openai: failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("api-key", p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("azure openai: request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("azure openai: API returned status %d: %s", resp.StatusCode, string(errBody))
	}

	ch := make(chan StreamEvent, 64)
	go p.inner.readSSEStream(ctx, resp.Body, ch)
	return ch, nil
}
