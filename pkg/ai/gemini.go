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
	geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"
)

// Compile-time interface check.
var _ Provider = (*GeminiProvider)(nil)

// GeminiProvider implements the Provider interface for Google's Generative AI API.
type GeminiProvider struct {
	apiKey     string
	httpClient *http.Client
	baseURL    string
}

// NewGeminiProvider creates a new Gemini provider.
// If apiKey is empty, it reads from the GEMINI_API_KEY environment variable.
func NewGeminiProvider(apiKey string) (*GeminiProvider, error) {
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("gemini: API key not set (provide key or set GEMINI_API_KEY)")
	}
	return &GeminiProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		baseURL:    geminiBaseURL,
	}, nil
}

func (p *GeminiProvider) Name() string { return "gemini" }

// Stream sends a streaming request to the Gemini API and returns a channel of StreamEvents.
func (p *GeminiProvider) Stream(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error) {
	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("gemini: failed to build request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", p.baseURL, req.Model)

	for attempt := 0; ; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("gemini: failed to create HTTP request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-goog-api-key", p.apiKey)

		resp, err := p.httpClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("gemini: request failed: %w", err)
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

		apiErr := parseGeminiError(resp.StatusCode, resp.Header, errBody)

		// Retry on 429 (rate limit) and 503 (overloaded).
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) && attempt < maxRetries {
			wait := apiErr.RetryAfter
			if wait == 0 {
				wait = (attempt + 1) * 2
			}
			log.Printf("gemini: HTTP %d, retrying in %ds (attempt %d/%d)", resp.StatusCode, wait, attempt+1, maxRetries+1)
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

// parseGeminiError parses a non-200 HTTP response from Gemini into an APIError.
// Gemini error format: {"error":{"code":429,"message":"...","status":"RESOURCE_EXHAUSTED"}}
func parseGeminiError(statusCode int, header http.Header, body []byte) *APIError {
	apiErr := &APIError{
		StatusCode: statusCode,
		Provider:   "gemini",
		AuthMethod: "api-key",
	}

	if ra := header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			apiErr.RetryAfter = secs
		}
	}

	var errResp struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Status != "" {
		apiErr.ErrorType = geminiStatusToErrorType(errResp.Error.Status, statusCode)
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

// geminiStatusToErrorType maps Gemini status strings and HTTP codes to
// canonical error types matching the APIError.ErrorType convention.
func geminiStatusToErrorType(status string, statusCode int) string {
	switch status {
	case "RESOURCE_EXHAUSTED":
		return "rate_limit_error"
	case "UNAVAILABLE":
		return "overloaded_error"
	case "INVALID_ARGUMENT":
		return "invalid_request_error"
	case "NOT_FOUND":
		return "not_found_error"
	case "PERMISSION_DENIED":
		return "permission_error"
	case "UNAUTHENTICATED":
		return "authentication_error"
	default:
		// Fall back to HTTP status code mapping.
		switch statusCode {
		case http.StatusTooManyRequests:
			return "rate_limit_error"
		case http.StatusServiceUnavailable:
			return "overloaded_error"
		case http.StatusUnauthorized:
			return "authentication_error"
		case http.StatusForbidden:
			return "permission_error"
		case http.StatusNotFound:
			return "not_found_error"
		default:
			return status
		}
	}
}

// -- Request construction --

type gemRequestBody struct {
	Contents          []gemContent  `json:"contents"`
	SystemInstruction *gemContent   `json:"systemInstruction,omitempty"`
	Tools             []gemToolSet  `json:"tools,omitempty"`
	GenerationConfig  *gemGenConfig `json:"generationConfig,omitempty"`
}

type gemContent struct {
	Role  string    `json:"role,omitempty"`
	Parts []gemPart `json:"parts"`
}

type gemPart struct {
	Text             string           `json:"text,omitempty"`
	InlineData       *gemInlineData   `json:"inlineData,omitempty"`
	FunctionCall     *gemFunctionCall `json:"functionCall,omitempty"`
	FunctionResponse *gemFuncResponse `json:"functionResponse,omitempty"`
}

// gemInlineData represents inline binary data (images) in Gemini's format.
type gemInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type gemFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}

type gemFuncResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type gemToolSet struct {
	FunctionDeclarations []gemFuncDecl `json:"functionDeclarations,omitempty"`
}

type gemFuncDecl struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

type gemGenConfig struct {
	MaxOutputTokens int             `json:"maxOutputTokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	StopSequences   []string        `json:"stopSequences,omitempty"`
	ThinkingConfig  *gemThinkConfig `json:"thinkingConfig,omitempty"`
}

type gemThinkConfig struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

func (p *GeminiProvider) buildRequestBody(req StreamRequest) ([]byte, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = GeminiDefaultMaxTokens
	}

	body := gemRequestBody{
		GenerationConfig: &gemGenConfig{
			MaxOutputTokens: maxTokens,
		},
	}

	// System instruction.
	if req.SystemPrompt != "" {
		body.SystemInstruction = &gemContent{
			Parts: []gemPart{{Text: req.SystemPrompt}},
		}
	}

	// Temperature.
	if req.Temperature != nil {
		body.GenerationConfig.Temperature = req.Temperature
	}

	// Stop sequences.
	if len(req.StopSequences) > 0 {
		body.GenerationConfig.StopSequences = req.StopSequences
	}

	// Thinking.
	if req.ThinkingLevel != "" && req.ThinkingLevel != ThinkingOff {
		budget := thinkingBudget(req.ThinkingLevel)
		body.GenerationConfig.ThinkingConfig = &gemThinkConfig{
			ThinkingBudget: budget,
		}
	}

	// Build a lookup from ToolUseID → ToolName so that tool result blocks
	// can resolve the original function name for Gemini's functionResponse.
	toolNames := make(map[string]string)
	for _, m := range req.Messages {
		for _, cb := range m.Content {
			if cb.Type == ContentTypeToolUse && cb.ToolUseID != "" {
				toolNames[cb.ToolUseID] = cb.ToolName
			}
		}
	}

	// Messages.
	body.Contents = make([]gemContent, 0, len(req.Messages))
	for _, m := range req.Messages {
		gc, err := mapToGeminiContent(m, toolNames)
		if err != nil {
			return nil, fmt.Errorf("converting message to Gemini content: %w", err)
		}
		body.Contents = append(body.Contents, gc)
	}

	// Tools.
	if len(req.Tools) > 0 {
		decls := make([]gemFuncDecl, len(req.Tools))
		for i, t := range req.Tools {
			decls[i] = gemFuncDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			}
		}
		body.Tools = []gemToolSet{{FunctionDeclarations: decls}}
	}

	return json.Marshal(body)
}

func mapToGeminiContent(m Message, toolNames map[string]string) (gemContent, error) {
	gc := gemContent{}

	switch m.Role {
	case RoleUser:
		gc.Role = "user"
	case RoleAssistant:
		gc.Role = "model"
	case RoleSystem:
		gc.Role = "user" // Gemini doesn't have a system role in contents
	}

	gc.Parts = make([]gemPart, 0, len(m.Content))

	for _, cb := range m.Content {
		switch cb.Type {
		case ContentTypeText:
			gc.Parts = append(gc.Parts, gemPart{Text: cb.Text})

		case ContentTypeImage:
			gc.Parts = append(gc.Parts, gemPart{
				InlineData: &gemInlineData{
					MimeType: cb.MediaType,
					Data:     cb.ImageData,
				},
			})

		case ContentTypeToolUse:
			var args map[string]any
			if cb.Input != nil {
				if m, ok := cb.Input.(map[string]any); ok {
					args = m
				} else if data, marshalErr := json.Marshal(cb.Input); marshalErr != nil {
					return gemContent{}, fmt.Errorf("marshal tool call %q input: %w", cb.ToolName, marshalErr)
				} else if unmarshalErr := json.Unmarshal(data, &args); unmarshalErr != nil {
					return gemContent{}, fmt.Errorf("unmarshal tool call %q input to map: %w", cb.ToolName, unmarshalErr)
				}
			}
			gc.Parts = append(gc.Parts, gemPart{
				FunctionCall: &gemFunctionCall{
					Name: cb.ToolName,
					Args: args,
				},
			})

		case ContentTypeToolResult:
			gc.Role = "user"
			funcName := toolNames[cb.ToolResultID]
			if funcName == "" {
				funcName = cb.ToolResultID // fallback to ID if name not found
			}
			if len(cb.ContentBlocks) > 0 {
				// Rich tool result: emit functionResponse then inline_data parts.
				gc.Parts = append(gc.Parts, gemPart{
					FunctionResponse: &gemFuncResponse{
						Name:     funcName,
						Response: map[string]any{"result": ""},
					},
				})
				for _, sub := range cb.ContentBlocks {
					switch sub.Type {
					case ContentTypeText:
						gc.Parts = append(gc.Parts, gemPart{Text: sub.Text})
					case ContentTypeImage:
						gc.Parts = append(gc.Parts, gemPart{
							InlineData: &gemInlineData{
								MimeType: sub.MediaType,
								Data:     sub.ImageData,
							},
						})
					}
				}
			} else {
				gc.Parts = append(gc.Parts, gemPart{
					FunctionResponse: &gemFuncResponse{
						Name: funcName,
						Response: map[string]any{
							"result": cb.Content,
						},
					},
				})
			}

		case ContentTypeThinking:
			// Thinking content is not sent back to Gemini.
		}
	}

	return gc, nil
}

// -- SSE stream parsing --

// Response-only structures (separate from request types to handle fields like "thought").
type gemSSEResponse struct {
	Candidates    []gemSSECandidate `json:"candidates"`
	UsageMetadata *gemUsage         `json:"usageMetadata,omitempty"`
}

type gemSSECandidate struct {
	Content      gemSSEContent `json:"content"`
	FinishReason string        `json:"finishReason,omitempty"`
	Index        int           `json:"index"`
}

type gemSSEContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []gemSSEPart `json:"parts"`
}

type gemSSEPart struct {
	Text         string           `json:"text,omitempty"`
	Thought      *bool            `json:"thought,omitempty"`
	FunctionCall *gemFunctionCall `json:"functionCall,omitempty"`
}

type gemUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

func (p *GeminiProvider) readSSEStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer func() {
		if r := recover(); r != nil {
			select {
			case ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("gemini: stream goroutine panicked: %v", r)}:
			default:
			}
		}
	}()
	defer func() { _ = body.Close() }()

	scanner, returnBuf := newSSEScanner(body)
	defer returnBuf()

	var (
		started bool
		usage   Usage
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

		var resp gemSSEResponse
		if err := json.Unmarshal([]byte(data), &resp); err != nil {
			trySend(ctx, ch, StreamEvent{Type: EventError, Error: fmt.Errorf("gemini: failed to parse SSE chunk: %w", err)})
			return
		}

		// Emit message_start on first chunk.
		if !started {
			started = true
			if !trySend(ctx, ch, StreamEvent{Type: EventMessageStart}) {
				return
			}
		}

		// Track usage.
		if resp.UsageMetadata != nil {
			usage.InputTokens = resp.UsageMetadata.PromptTokenCount
			usage.OutputTokens = resp.UsageMetadata.CandidatesTokenCount
		}

		if len(resp.Candidates) == 0 {
			continue
		}

		candidate := resp.Candidates[0]

		// Process each part in the candidate content.
		for _, part := range candidate.Content.Parts {
			if part.FunctionCall != nil {
				// Generate a tool call ID since Gemini doesn't provide one.
				toolID := nextToolCallID("gemini_call")

				if !trySend(ctx, ch, StreamEvent{
					Type:       EventToolUseStart,
					ToolCallID: toolID,
					ToolName:   part.FunctionCall.Name,
				}) {
					return
				}

				// Emit the full args as a single delta.
				if part.FunctionCall.Args != nil {
					argsJSON, err := json.Marshal(part.FunctionCall.Args)
					if err != nil {
						trySend(ctx, ch, StreamEvent{Type: EventError, Error: fmt.Errorf("gemini: failed to marshal tool call arguments: %w", err)})
						return
					}
					if !trySend(ctx, ch, StreamEvent{
						Type:         EventToolUseDelta,
						ToolCallID:   toolID,
						ToolName:     part.FunctionCall.Name,
						PartialInput: string(argsJSON),
					}) {
						return
					}
				}

				if !trySend(ctx, ch, StreamEvent{
					Type:       EventToolUseEnd,
					ToolCallID: toolID,
					ToolName:   part.FunctionCall.Name,
				}) {
					return
				}
				continue
			}

			if part.Text != "" {
				if part.Thought != nil && *part.Thought {
					if !trySend(ctx, ch, StreamEvent{Type: EventThinkingDelta, Delta: part.Text}) {
						return
					}
				} else {
					if !trySend(ctx, ch, StreamEvent{Type: EventTextDelta, Delta: part.Text}) {
						return
					}
				}
			}
		}

		// Check for finish reason.
		if candidate.FinishReason != "" && candidate.FinishReason != "FINISH_REASON_UNSPECIFIED" {
			trySend(ctx, ch, StreamEvent{
				Type:  EventMessageEnd,
				Usage: &usage,
			})
			return
		}
	}

	if err := scanner.Err(); err != nil {
		trySend(ctx, ch, StreamEvent{Type: EventError, Error: fmt.Errorf("gemini: stream read error: %w", err)})
		return
	}

	// If we got here without a finish reason, still end gracefully.
	if started {
		trySend(ctx, ch, StreamEvent{
			Type:  EventMessageEnd,
			Usage: &usage,
		})
	}
}
