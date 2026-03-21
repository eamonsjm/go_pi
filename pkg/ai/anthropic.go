package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	anthropicAPIURL     = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion = "2023-06-01"
)

// AnthropicProvider implements the Provider interface for Anthropic's Messages API.
type AnthropicProvider struct {
	apiKey     string
	useBearer  bool // true when using an OAuth access token instead of API key
	httpClient *http.Client
	baseURL    string
}

// NewAnthropicProvider creates a new Anthropic provider.
// If apiKey is empty, it reads from the ANTHROPIC_API_KEY environment variable.
func NewAnthropicProvider(apiKey string) (*AnthropicProvider, error) {
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic: API key not set (provide key or set ANTHROPIC_API_KEY)")
	}
	return &AnthropicProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		baseURL:    anthropicAPIURL,
	}, nil
}

// NewAnthropicProviderWithToken creates a provider that uses an OAuth access
// token with Authorization: Bearer header instead of x-api-key.
func NewAnthropicProviderWithToken(token string) (*AnthropicProvider, error) {
	if token == "" {
		return nil, fmt.Errorf("anthropic: OAuth token is empty")
	}
	return &AnthropicProvider{
		apiKey:     token,
		useBearer:  true,
		httpClient: &http.Client{},
		baseURL:    anthropicAPIURL,
	}, nil
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

// authMethod returns "oauth" or "api-key" for error context.
func (p *AnthropicProvider) authMethod() string {
	if p.useBearer {
		return "oauth"
	}
	return "api-key"
}

const maxRetries = 2 // up to 3 attempts total

// Stream sends a streaming request to the Anthropic Messages API and returns
// a channel of StreamEvents. Retries automatically on rate limit (429) and
// overloaded (529) errors.
func (p *AnthropicProvider) Stream(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error) {
	// OAuth tokens require a system prompt; use default if none provided
	if p.useBearer && req.SystemPrompt == "" {
		req.SystemPrompt = "You are Claude, an AI assistant created by Anthropic."
	}

	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: failed to build request: %w", err)
	}

	// OAuth requires system to be a string, not an array. Convert if needed.
	if p.useBearer && req.SystemPrompt != "" {
		var bodyObj map[string]interface{}
		if err := json.Unmarshal(body, &bodyObj); err != nil {
			return nil, fmt.Errorf("anthropic: failed to unmarshal request body for OAuth rewrite: %w", err)
		}
		if systemArray, ok := bodyObj["system"].([]interface{}); ok && len(systemArray) > 0 {
			if systemBlock, ok := systemArray[0].(map[string]interface{}); ok {
				if text, ok := systemBlock["text"].(string); ok {
					bodyObj["system"] = text
					newBody, err := json.Marshal(bodyObj)
					if err != nil {
						return nil, fmt.Errorf("anthropic: failed to re-marshal request body for OAuth rewrite: %w", err)
					}
					body = newBody
				}
			}
		}
	}

	for attempt := 0; ; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("anthropic: failed to create HTTP request: %w", err)
		}

		httpReq.Header.Set("Content-Type", "application/json")
		if p.useBearer {
			httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
			httpReq.Header.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20")
			httpReq.Header.Set("user-agent", "claude-cli/2.1.75")
			httpReq.Header.Set("x-app", "cli")
		} else {
			httpReq.Header.Set("x-api-key", p.apiKey)
		}
		httpReq.Header.Set("anthropic-version", anthropicAPIVersion)

		resp, err := p.httpClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("anthropic: request failed: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			ch := make(chan StreamEvent, 64)
			go p.readSSEStream(ctx, resp.Body, ch)
			return ch, nil
		}

		errBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			errBody = []byte(fmt.Sprintf("failed to read response body: %v", readErr))
		}
		apiErr := parseHTTPError(resp.StatusCode, resp.Header, errBody, p.authMethod())
		if apiErr.IsRetryable() && attempt < maxRetries {
			wait := apiErr.RetryAfter
			if wait == 0 {
				wait = (attempt + 1) * 2 // 2s, 4s
			}
			log.Printf("anthropic: %s, retrying in %ds (attempt %d/%d)", apiErr.ErrorType, wait, attempt+1, maxRetries+1)
			select {
			case <-time.After(time.Duration(wait) * time.Second):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return nil, apiErr
	}
}

// parseHTTPError parses a non-200 HTTP response into an APIError.
// authMethod should be "oauth" or "api-key" to aid error diagnosis.
func parseHTTPError(statusCode int, header http.Header, body []byte, authMethod string) *APIError {
	apiErr := &APIError{StatusCode: statusCode, Provider: "anthropic", AuthMethod: authMethod}

	// Try to parse retry-after header.
	if ra := header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			apiErr.RetryAfter = secs
		}
	}

	// Try to parse structured Anthropic error JSON: {"error":{"type":"...","message":"..."}}
	var errResp struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Type != "" {
		apiErr.ErrorType = errResp.Error.Type
		apiErr.Message = errResp.Error.Message
		return apiErr
	}

	// Try flat OAuth error format: {"error":"error_code","error_description":"..."}
	var oauthErr struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &oauthErr); err == nil && oauthErr.Error != "" {
		apiErr.ErrorType = oauthErr.Error
		if oauthErr.Description != "" {
			apiErr.Message = oauthErr.Description
		} else {
			apiErr.Message = oauthErr.Error
		}
		return apiErr
	}

	// Fallback: use raw body as message.
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		raw = fmt.Sprintf("empty response body (HTTP %d)", statusCode)
	}
	apiErr.Message = raw
	return apiErr
}

// -- Request construction --

// anthRequestBody is the JSON body sent to the Anthropic Messages API.
type anthRequestBody struct {
	Model         string            `json:"model"`
	MaxTokens     int               `json:"max_tokens"`
	Stream        bool              `json:"stream"`
	System        []anthSystemBlock `json:"system,omitempty"`
	Messages      []anthMessage     `json:"messages"`
	Tools         []anthTool        `json:"tools,omitempty"`
	Thinking      *anthThinking     `json:"thinking,omitempty"`
	Temperature   *float64          `json:"temperature,omitempty"`
	StopSequences []string          `json:"stop_sequences,omitempty"`
}

type anthSystemBlock struct {
	Type         string            `json:"type"`
	Text         string            `json:"text"`
	CacheControl *anthCacheControl `json:"cache_control,omitempty"`
}

type anthCacheControl struct {
	Type string `json:"type"`
}

type anthMessage struct {
	Role    string        `json:"role"`
	Content []anthContent `json:"content"`
}

type anthContent struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// tool_use
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"` // string or []anthContent
	IsError   bool   `json:"is_error,omitempty"`

	// thinking
	Thinking string `json:"thinking,omitempty"`

	// image
	Source *anthImageSource `json:"source,omitempty"`
}

type anthImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

type anthThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

func (p *AnthropicProvider) buildRequestBody(req StreamRequest) ([]byte, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = AnthropicDefaultMaxTokens
	}

	body := anthRequestBody{
		Model:     req.Model,
		MaxTokens: maxTokens,
		Stream:    true,
	}

	// System prompt with cache_control for prompt caching.
	if req.SystemPrompt != "" {
		body.System = []anthSystemBlock{
			{
				Type:         "text",
				Text:         req.SystemPrompt,
				CacheControl: &anthCacheControl{Type: "ephemeral"},
			},
		}
	}

	// Messages. Ensure every message has a non-nil content list — the
	// Anthropic API rejects content:null with "should be a valid list".
	body.Messages = make([]anthMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		am := anthMessage{Role: string(m.Role)}
		if len(m.Content) == 0 {
			am.Content = []anthContent{}
		} else {
			am.Content = make([]anthContent, 0, len(m.Content))
			for _, cb := range m.Content {
				am.Content = append(am.Content, mapContentBlock(cb))
			}
		}
		body.Messages = append(body.Messages, am)
	}

	// Tools.
	if len(req.Tools) > 0 {
		body.Tools = make([]anthTool, len(req.Tools))
		for i, t := range req.Tools {
			body.Tools[i] = anthTool(t)
		}
	}

	// Thinking.
	if req.ThinkingLevel != "" && req.ThinkingLevel != ThinkingOff {
		budget := thinkingBudget(req.ThinkingLevel)
		// Anthropic requires budget_tokens < max_tokens.
		if budget >= maxTokens {
			maxTokens = budget + 1
			body.MaxTokens = maxTokens
		}
		body.Thinking = &anthThinking{
			Type:         "enabled",
			BudgetTokens: budget,
		}
		// Anthropic requires temperature to be unset (or 1) when thinking is enabled.
		body.Temperature = nil
	} else if req.Temperature != nil {
		body.Temperature = req.Temperature
	}

	if len(req.StopSequences) > 0 {
		body.StopSequences = req.StopSequences
	}

	return json.Marshal(body)
}

func mapContentBlock(cb ContentBlock) anthContent {
	switch cb.Type {
	case ContentTypeText:
		return anthContent{Type: "text", Text: cb.Text}
	case ContentTypeToolUse:
		return anthContent{Type: "tool_use", ID: cb.ToolUseID, Name: cb.ToolName, Input: cb.Input}
	case ContentTypeToolResult:
		ac := anthContent{Type: "tool_result", ToolUseID: cb.ToolResultID, IsError: cb.IsError}
		if len(cb.ContentBlocks) > 0 {
			// Rich tool result: serialize content as array of sub-blocks.
			subBlocks := make([]anthContent, len(cb.ContentBlocks))
			for i, sub := range cb.ContentBlocks {
				subBlocks[i] = mapContentBlock(sub)
			}
			ac.Content = subBlocks
		} else if cb.Content != "" {
			ac.Content = cb.Content
		}
		return ac
	case ContentTypeThinking:
		return anthContent{Type: "thinking", Thinking: cb.Thinking}
	case ContentTypeImage:
		return anthContent{
			Type: "image",
			Source: &anthImageSource{
				Type:      "base64",
				MediaType: cb.MediaType,
				Data:      cb.ImageData,
			},
		}
	default:
		return anthContent{Type: string(cb.Type), Text: cb.Text}
	}
}

func thinkingBudget(level ThinkingLevel) int {
	switch level {
	case ThinkingLow:
		return 5000
	case ThinkingMedium:
		return 10000
	case ThinkingHigh:
		return 32000
	default:
		return 10000
	}
}

// -- SSE stream parsing --

// SSE event types from the Anthropic API.
type anthSSEEvent struct {
	Type string
	Data json.RawMessage
}

// Anthropic SSE data structures.
type anthMessageStartData struct {
	Type    string `json:"type"`
	Message struct {
		ID    string    `json:"id"`
		Role  string    `json:"role"`
		Usage anthUsage `json:"usage"`
	} `json:"message"`
}

type anthContentBlockStartData struct {
	Index        int `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id,omitempty"`
		Name string `json:"name,omitempty"`
		Text string `json:"text,omitempty"`
	} `json:"content_block"`
}

type anthContentBlockDeltaData struct {
	Index int `json:"index"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text,omitempty"`
		Thinking    string `json:"thinking,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
	} `json:"delta"`
}

type anthMessageDeltaData struct {
	Delta struct {
		StopReason string `json:"stop_reason,omitempty"`
	} `json:"delta"`
	Usage anthUsage `json:"usage"`
}

type anthUsage struct {
	InputTokens              int `json:"input_tokens,omitempty"`
	OutputTokens             int `json:"output_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

type anthErrorData struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// blockState tracks the state of the current content block being streamed.
type blockState struct {
	blockType string
	toolID    string
	toolName  string
	inputBuf  strings.Builder
}

func (p *AnthropicProvider) readSSEStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer func() {
		if r := recover(); r != nil {
			ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("anthropic: stream goroutine panicked: %v", r)}
		}
	}()
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	// Allow up to 1MB per line for large tool inputs.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		eventType string
		usage     Usage
		blocks    = make(map[int]*blockState)
	)

	for scanner.Scan() {
		line := scanner.Text()

		// Check for context cancellation.
		select {
		case <-ctx.Done():
			ch <- StreamEvent{Type: EventError, Error: ctx.Err()}
			return
		default:
		}

		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := []byte(strings.TrimPrefix(line, "data: "))

		switch eventType {
		case "message_start":
			var d anthMessageStartData
			if err := json.Unmarshal(data, &d); err != nil {
				ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("anthropic: failed to parse message_start: %w", err)}
				return
			}
			usage.InputTokens += d.Message.Usage.InputTokens
			usage.CacheRead += d.Message.Usage.CacheReadInputTokens
			usage.CacheWrite += d.Message.Usage.CacheCreationInputTokens
			ch <- StreamEvent{
				Type: EventMessageStart,
				Usage: &Usage{
					InputTokens:  d.Message.Usage.InputTokens,
					OutputTokens: d.Message.Usage.OutputTokens,
					CacheRead:    d.Message.Usage.CacheReadInputTokens,
					CacheWrite:   d.Message.Usage.CacheCreationInputTokens,
				},
			}

		case "content_block_start":
			var d anthContentBlockStartData
			if err := json.Unmarshal(data, &d); err != nil {
				ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("anthropic: failed to parse content_block_start: %w", err)}
				return
			}
			bs := &blockState{blockType: d.ContentBlock.Type}
			blocks[d.Index] = bs

			switch d.ContentBlock.Type {
			case "tool_use":
				bs.toolID = d.ContentBlock.ID
				bs.toolName = d.ContentBlock.Name
				ch <- StreamEvent{
					Type:       EventToolUseStart,
					ToolCallID: d.ContentBlock.ID,
					ToolName:   d.ContentBlock.Name,
				}
			case "text":
				if d.ContentBlock.Text != "" {
					ch <- StreamEvent{Type: EventTextDelta, Delta: d.ContentBlock.Text}
				}
			}

		case "content_block_delta":
			var d anthContentBlockDeltaData
			if err := json.Unmarshal(data, &d); err != nil {
				ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("anthropic: failed to parse content_block_delta: %w", err)}
				return
			}
			bs := blocks[d.Index]
			if bs == nil {
				continue
			}

			switch d.Delta.Type {
			case "text_delta":
				ch <- StreamEvent{Type: EventTextDelta, Delta: d.Delta.Text}
			case "thinking_delta":
				ch <- StreamEvent{Type: EventThinkingDelta, Delta: d.Delta.Thinking}
			case "input_json_delta":
				bs.inputBuf.WriteString(d.Delta.PartialJSON)
				ch <- StreamEvent{
					Type:         EventToolUseDelta,
					ToolCallID:   bs.toolID,
					ToolName:     bs.toolName,
					PartialInput: d.Delta.PartialJSON,
				}
			}

		case "content_block_stop":
			// Parse the index from the data payload.
			var d struct {
				Index int `json:"index"`
			}
			if err := json.Unmarshal(data, &d); err != nil {
				ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("anthropic: failed to parse content_block_stop: %w", err)}
				return
			}
			bs := blocks[d.Index]
			if bs != nil && bs.blockType == "tool_use" {
				ch <- StreamEvent{
					Type:       EventToolUseEnd,
					ToolCallID: bs.toolID,
					ToolName:   bs.toolName,
				}
			}
			delete(blocks, d.Index)

		case "message_delta":
			var d anthMessageDeltaData
			if err := json.Unmarshal(data, &d); err != nil {
				ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("anthropic: failed to parse message_delta: %w", err)}
				return
			}
			usage.OutputTokens += d.Usage.OutputTokens

		case "message_stop":
			ch <- StreamEvent{
				Type:  EventMessageEnd,
				Usage: &usage,
			}
			return

		case "error":
			var d anthErrorData
			if err := json.Unmarshal(data, &d); err != nil {
				ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("anthropic: unparseable error event: %s", string(data))}
			} else {
				ch <- StreamEvent{Type: EventError, Error: &APIError{
					ErrorType:  d.Error.Type,
					Message:    d.Error.Message,
					Provider:   "anthropic",
					AuthMethod: p.authMethod(),
				}}
			}
			return

		case "ping":
			// Heartbeat, ignore.
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("anthropic: stream read error: %w", err)}
	}
}
