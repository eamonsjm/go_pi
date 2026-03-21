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
	"strings"
	"time"
)

const (
	geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"
)

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

		// Retry on 429 (rate limit) and 503 (overloaded).
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable) && attempt < maxRetries {
			wait := (attempt + 1) * 2
			log.Printf("gemini: HTTP %d, retrying in %ds (attempt %d/%d)", resp.StatusCode, wait, attempt+1, maxRetries+1)
			select {
			case <-time.After(time.Duration(wait) * time.Second):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Message:    strings.TrimSpace(string(errBody)),
			Provider:   "gemini",
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

	// Messages.
	body.Contents = make([]gemContent, 0, len(req.Messages))
	for _, m := range req.Messages {
		body.Contents = append(body.Contents, mapToGeminiContent(m))
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

func mapToGeminiContent(m Message) gemContent {
	gc := gemContent{}

	switch m.Role {
	case RoleUser:
		gc.Role = "user"
	case RoleAssistant:
		gc.Role = "model"
	case RoleSystem:
		gc.Role = "user" // Gemini doesn't have a system role in contents
	}

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
				} else if data, marshalErr := json.Marshal(cb.Input); marshalErr == nil {
					if unmarshalErr := json.Unmarshal(data, &args); unmarshalErr != nil {
						args = nil
					}
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
			if len(cb.ContentBlocks) > 0 {
				// Rich tool result: emit functionResponse then inline_data parts.
				gc.Parts = append(gc.Parts, gemPart{
					FunctionResponse: &gemFuncResponse{
						Name:     cb.ToolResultID,
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
						Name: cb.ToolResultID,
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

	return gc
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
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		started     bool
		usage       Usage
		toolCallIdx int
	)

	for scanner.Scan() {
		line := scanner.Text()

		select {
		case <-ctx.Done():
			ch <- StreamEvent{Type: EventError, Error: ctx.Err()}
			return
		default:
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		var resp gemSSEResponse
		if err := json.Unmarshal([]byte(data), &resp); err != nil {
			ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("gemini: failed to parse SSE chunk: %w", err)}
			return
		}

		// Emit message_start on first chunk.
		if !started {
			started = true
			ch <- StreamEvent{Type: EventMessageStart}
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
				toolCallIdx++
				toolID := fmt.Sprintf("gemini_call_%d", toolCallIdx)

				ch <- StreamEvent{
					Type:       EventToolUseStart,
					ToolCallID: toolID,
					ToolName:   part.FunctionCall.Name,
				}

				// Emit the full args as a single delta.
				if part.FunctionCall.Args != nil {
					argsJSON, _ := json.Marshal(part.FunctionCall.Args)
					ch <- StreamEvent{
						Type:         EventToolUseDelta,
						ToolCallID:   toolID,
						ToolName:     part.FunctionCall.Name,
						PartialInput: string(argsJSON),
					}
				}

				ch <- StreamEvent{
					Type:       EventToolUseEnd,
					ToolCallID: toolID,
					ToolName:   part.FunctionCall.Name,
				}
				continue
			}

			if part.Text != "" {
				if part.Thought != nil && *part.Thought {
					ch <- StreamEvent{Type: EventThinkingDelta, Delta: part.Text}
				} else {
					ch <- StreamEvent{Type: EventTextDelta, Delta: part.Text}
				}
			}
		}

		// Check for finish reason.
		if candidate.FinishReason != "" && candidate.FinishReason != "FINISH_REASON_UNSPECIFIED" {
			ch <- StreamEvent{
				Type:  EventMessageEnd,
				Usage: &usage,
			}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("gemini: stream read error: %w", err)}
		return
	}

	// If we got here without a finish reason, still end gracefully.
	if started {
		ch <- StreamEvent{
			Type:  EventMessageEnd,
			Usage: &usage,
		}
	}
}
