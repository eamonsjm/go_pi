package ai

import (
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
	openaiAPIURL = "https://api.openai.com/v1/chat/completions"
)

// Compile-time interface check.
var _ Provider = (*OpenAIProvider)(nil)

// OpenAIProvider implements the Provider interface for OpenAI's Chat Completions API.
type OpenAIProvider struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
}

// NewOpenAIProvider creates a new OpenAI provider.
// If apiKey is empty, it reads from the OPENAI_API_KEY environment variable.
func NewOpenAIProvider(apiKey string) (*OpenAIProvider, error) {
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("openai: API key not set (provide key or set OPENAI_API_KEY)")
	}
	return &OpenAIProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		baseURL:    openaiAPIURL,
	}, nil
}

func (p *OpenAIProvider) Name() string { return "openai" }

// Stream sends a streaming request to the OpenAI Chat Completions API and
// returns a channel of StreamEvents.
func (p *OpenAIProvider) Stream(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error) {
	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("openai: failed to build request: %w", err)
	}

	for attempt := 0; ; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("openai: failed to create HTTP request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

		resp, err := p.httpClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("openai: request failed: %w", err)
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

		apiErr := parseOpenAIError(resp.StatusCode, resp.Header, errBody)
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) && attempt < maxRetries {
			wait := apiErr.RetryAfter
			if wait == 0 {
				wait = (attempt + 1) * 2
			}
			log.Printf("openai: HTTP %d, retrying in %ds (attempt %d/%d)", resp.StatusCode, wait, attempt+1, maxRetries+1)
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

// parseOpenAIError parses a non-200 HTTP response from OpenAI into an APIError.
// OpenAI error format: {"error":{"message":"...","type":"invalid_request_error","param":null,"code":null}}
func parseOpenAIError(statusCode int, header http.Header, body []byte) *APIError {
	apiErr := &APIError{
		StatusCode: statusCode,
		Provider:   "openai",
		AuthMethod: "api-key",
	}

	if ra := header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			apiErr.RetryAfter = secs
		}
	}

	var errResp struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"` // can be string or null
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Type != "" {
		apiErr.ErrorType = errResp.Error.Type
		apiErr.Message = errResp.Error.Message
		return apiErr
	}

	raw := strings.TrimSpace(string(body))
	if raw == "" {
		raw = fmt.Sprintf("empty response body (HTTP %d)", statusCode)
	}
	apiErr.Message = raw
	return apiErr
}

// -- Request construction --

type oaiRequestBody struct {
	Model         string        `json:"model"`
	Messages      []oaiMessage  `json:"messages"`
	Stream        bool          `json:"stream"`
	StreamOptions *oaiStreamOpt `json:"stream_options,omitempty"`
	MaxTokens     int           `json:"max_tokens,omitempty"`
	Temperature   *float64      `json:"temperature,omitempty"`
	Stop          []string      `json:"stop,omitempty"`
	Tools         []oaiTool     `json:"tools,omitempty"`
}

type oaiStreamOpt struct {
	IncludeUsage bool `json:"include_usage"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content,omitempty"` // string, []oaiContentPart, or nil
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

// oaiContentPart represents a content part in OpenAI's multi-modal message format.
type oaiContentPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *oaiImageURL `json:"image_url,omitempty"`
}

// oaiImageURL represents an image URL in OpenAI's vision format.
type oaiImageURL struct {
	URL string `json:"url"`
}

type oaiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function oaiToolFunction `json:"function"`
}

type oaiToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiTool struct {
	Type     string         `json:"type"`
	Function oaiToolFuncDef `json:"function"`
}

type oaiToolFuncDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

func (p *OpenAIProvider) buildRequestBody(req StreamRequest) ([]byte, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = OpenAIDefaultMaxTokens
	}

	body := oaiRequestBody{
		Model:         req.Model,
		Stream:        true,
		StreamOptions: &oaiStreamOpt{IncludeUsage: true},
		MaxTokens:     maxTokens,
	}

	if req.Temperature != nil {
		body.Temperature = req.Temperature
	}
	if len(req.StopSequences) > 0 {
		body.Stop = req.StopSequences
	}

	// Build messages.
	msgs := make([]oaiMessage, 0, len(req.Messages)+1)

	// System prompt goes as a system message.
	if req.SystemPrompt != "" {
		msgs = append(msgs, oaiMessage{Role: "system", Content: req.SystemPrompt})
	}

	for _, m := range req.Messages {
		om, err := mapToOpenAIMessage(m)
		if err != nil {
			return nil, fmt.Errorf("openai: %w", err)
		}
		msgs = append(msgs, om)
	}
	body.Messages = msgs

	// Tools.
	if len(req.Tools) > 0 {
		body.Tools = make([]oaiTool, len(req.Tools))
		for i, t := range req.Tools {
			body.Tools[i] = oaiTool{
				Type: "function",
				Function: oaiToolFuncDef{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			}
		}
	}

	return json.Marshal(body)
}

func mapToOpenAIMessage(m Message) (oaiMessage, error) {
	om := oaiMessage{Role: string(m.Role)}

	// Check for tool result messages.
	if len(m.Content) == 1 && m.Content[0].Type == ContentTypeToolResult {
		cb := m.Content[0]
		om.Role = "tool"
		om.ToolCallID = cb.ToolResultID
		if len(cb.ContentBlocks) > 0 {
			// Rich tool result: serialize as array of content parts.
			om.Content = mapContentBlocksToOAI(cb.ContentBlocks)
		} else {
			om.Content = cb.Content
		}
		return om, nil
	}

	// Build text content, image content, and tool calls.
	textParts := make([]string, 0, len(m.Content))
	contentParts := make([]oaiContentPart, 0, len(m.Content))
	toolCalls := make([]oaiToolCall, 0, len(m.Content))
	hasImages := false

	for _, cb := range m.Content {
		switch cb.Type {
		case ContentTypeText:
			textParts = append(textParts, cb.Text)
			contentParts = append(contentParts, oaiContentPart{
				Type: "text",
				Text: cb.Text,
			})
		case ContentTypeImage:
			hasImages = true
			contentParts = append(contentParts, oaiContentPart{
				Type: "image_url",
				ImageURL: &oaiImageURL{
					URL: "data:" + cb.MediaType + ";base64," + cb.ImageData,
				},
			})
		case ContentTypeToolUse:
			inputJSON, err := json.Marshal(cb.Input)
			if err != nil {
				return oaiMessage{}, fmt.Errorf("failed to marshal tool call input: %w", err)
			}
			toolCalls = append(toolCalls, oaiToolCall{
				ID:   cb.ToolUseID,
				Type: "function",
				Function: oaiToolFunction{
					Name:      cb.ToolName,
					Arguments: string(inputJSON),
				},
			})
		}
	}

	if hasImages {
		// Use array content format for multi-modal messages.
		om.Content = contentParts
	} else if len(textParts) > 0 {
		om.Content = strings.Join(textParts, "")
	}
	if len(toolCalls) > 0 {
		om.Role = "assistant"
		om.ToolCalls = toolCalls
	}

	return om, nil
}

// mapContentBlocksToOAI converts ContentBlocks to OpenAI content parts.
func mapContentBlocksToOAI(blocks []ContentBlock) []oaiContentPart {
	parts := make([]oaiContentPart, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case ContentTypeText:
			parts = append(parts, oaiContentPart{Type: "text", Text: b.Text})
		case ContentTypeImage:
			parts = append(parts, oaiContentPart{
				Type: "image_url",
				ImageURL: &oaiImageURL{
					URL: "data:" + b.MediaType + ";base64," + b.ImageData,
				},
			})
		}
	}
	return parts
}

// -- SSE stream parsing --

// OpenAI streaming chunk structure.
type oaiChunk struct {
	ID      string      `json:"id"`
	Choices []oaiChoice `json:"choices"`
	Usage   *oaiUsage   `json:"usage,omitempty"`
}

type oaiChoice struct {
	Index        int      `json:"index"`
	Delta        oaiDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason,omitempty"`
}

type oaiDelta struct {
	Role      string             `json:"role,omitempty"`
	Content   *string            `json:"content,omitempty"`
	ToolCalls []oaiDeltaToolCall `json:"tool_calls,omitempty"`
}

type oaiDeltaToolCall struct {
	Index    int                  `json:"index"`
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type,omitempty"`
	Function oaiDeltaToolFunction `json:"function"`
}

type oaiDeltaToolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// toolCallState tracks the accumulated state of a streaming tool call.
type toolCallState struct {
	id   string
	name string
}

func (p *OpenAIProvider) readSSEStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer func() {
		if r := recover(); r != nil {
			select {
			case ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("openai: stream goroutine panicked: %v", r)}:
			default:
			}
		}
	}()
	defer func() { _ = body.Close() }()

	scanner, returnBuf := newSSEScanner(body)
	defer returnBuf()

	var (
		usage     Usage
		started   bool
		toolCalls = make(map[int]*toolCallState)
	)

	for scanner.Scan() {
		line := scanner.Text()

		select {
		case <-ctx.Done():
			trySend(ctx, ch, StreamEvent{Type: EventError, Error: ctx.Err()})
			return
		default:
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		// End-of-stream sentinel.
		if data == "[DONE]" {
			trySend(ctx, ch, StreamEvent{
				Type:  EventMessageEnd,
				Usage: &usage,
			})
			return
		}

		var chunk oaiChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			trySend(ctx, ch, StreamEvent{Type: EventError, Error: fmt.Errorf("openai: failed to parse chunk: %w", err)})
			return
		}

		// Emit message_start on first chunk.
		if !started {
			started = true
			if !trySend(ctx, ch, StreamEvent{Type: EventMessageStart}) {
				return
			}
		}

		// Usage may arrive in a dedicated final chunk.
		if chunk.Usage != nil {
			usage.InputTokens = chunk.Usage.PromptTokens
			usage.OutputTokens = chunk.Usage.CompletionTokens
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		delta := choice.Delta

		// Text content delta.
		if delta.Content != nil && *delta.Content != "" {
			if !trySend(ctx, ch, StreamEvent{Type: EventTextDelta, Delta: *delta.Content}) {
				return
			}
		}

		// Tool call deltas.
		for _, tc := range delta.ToolCalls {
			state := toolCalls[tc.Index]

			// New tool call starts when we get an ID.
			if tc.ID != "" {
				state = &toolCallState{id: tc.ID, name: tc.Function.Name}
				toolCalls[tc.Index] = state
				if !trySend(ctx, ch, StreamEvent{
					Type:       EventToolUseStart,
					ToolCallID: tc.ID,
					ToolName:   tc.Function.Name,
				}) {
					return
				}
			}

			// Skip deltas that arrive before the tool call ID is known.
			if state == nil {
				continue
			}

			// Argument deltas.
			if tc.Function.Arguments != "" {
				if !trySend(ctx, ch, StreamEvent{
					Type:         EventToolUseDelta,
					ToolCallID:   state.id,
					ToolName:     state.name,
					PartialInput: tc.Function.Arguments,
				}) {
					return
				}
			}
		}

		// Check for finish_reason to close open tool calls.
		if choice.FinishReason != nil {
			reason := *choice.FinishReason
			if reason == "tool_calls" || reason == "stop" {
				for idx, state := range toolCalls {
					if !trySend(ctx, ch, StreamEvent{
						Type:       EventToolUseEnd,
						ToolCallID: state.id,
						ToolName:   state.name,
					}) {
						return
					}
					delete(toolCalls, idx)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		trySend(ctx, ch, StreamEvent{Type: EventError, Error: fmt.Errorf("openai: stream read error: %w", err)})
		return
	}

	// If we got here without [DONE], still end gracefully.
	if started {
		trySend(ctx, ch, StreamEvent{
			Type:  EventMessageEnd,
			Usage: &usage,
		})
	}
}
