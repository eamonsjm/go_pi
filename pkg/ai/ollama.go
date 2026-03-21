package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// OllamaProvider implements the Provider interface for Ollama's local inference API.
// Ollama runs at localhost:11434 by default and exposes a streaming /api/chat endpoint.
type OllamaProvider struct {
	baseURL    string
	httpClient *http.Client
}

// NewOllamaProvider creates a new Ollama provider.
// If baseURL is empty, it reads from the OLLAMA_HOST environment variable,
// falling back to http://localhost:11434.
func NewOllamaProvider(baseURL string) (*OllamaProvider, error) {
	if baseURL == "" {
		baseURL = os.Getenv("OLLAMA_HOST")
	}
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &OllamaProvider{
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}, nil
}

func (p *OllamaProvider) Name() string { return "ollama" }

// Stream sends a streaming chat request to Ollama's /api/chat endpoint.
func (p *OllamaProvider) Stream(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error) {
	body, err := p.buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: failed to build request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer func() { _ = resp.Body.Close() }()
		errBody, _ := io.ReadAll(resp.Body)
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Message:    string(errBody),
			Provider:   "ollama",
		}
	}

	ch := make(chan StreamEvent, 64)
	go p.readStream(ctx, resp.Body, ch)
	return ch, nil
}

// -- Request construction --

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  *ollamaOptions  `json:"options,omitempty"`
	Tools    []ollamaTool    `json:"tools,omitempty"`
}

type ollamaMessage struct {
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Images    []string        `json:"images,omitempty"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

type ollamaOptions struct {
	NumPredict  int      `json:"num_predict,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

type ollamaTool struct {
	Type     string             `json:"type"`
	Function ollamaToolFuncDef  `json:"function"`
}

type ollamaToolFuncDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type ollamaToolCall struct {
	Function ollamaToolCallFunc `json:"function"`
}

type ollamaToolCallFunc struct {
	Name      string `json:"name"`
	Arguments any    `json:"arguments"`
}

func (p *OllamaProvider) buildRequestBody(req StreamRequest) ([]byte, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = OllamaDefaultMaxTokens
	}

	body := ollamaRequest{
		Model:  req.Model,
		Stream: true,
		Options: &ollamaOptions{
			NumPredict: maxTokens,
		},
	}

	if req.Temperature != nil {
		body.Options.Temperature = req.Temperature
	}
	if len(req.StopSequences) > 0 {
		body.Options.Stop = req.StopSequences
	}

	// Build messages.
	msgs := make([]ollamaMessage, 0, len(req.Messages)+1)

	if req.SystemPrompt != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: req.SystemPrompt})
	}

	for _, m := range req.Messages {
		msgs = append(msgs, mapToOllamaMessages(m)...)
	}
	body.Messages = msgs

	// Tools.
	if len(req.Tools) > 0 {
		body.Tools = make([]ollamaTool, len(req.Tools))
		for i, t := range req.Tools {
			body.Tools[i] = ollamaTool{
				Type: "function",
				Function: ollamaToolFuncDef{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.InputSchema,
				},
			}
		}
	}

	return json.Marshal(body)
}

func mapToOllamaMessages(m Message) []ollamaMessage {
	// Tool result messages: Ollama expects role "tool" with content.
	if len(m.Content) == 1 && m.Content[0].Type == ContentTypeToolResult {
		cb := m.Content[0]
		content := cb.Content
		if len(cb.ContentBlocks) > 0 {
			// Flatten rich tool results to text.
			var b strings.Builder
			for _, bl := range cb.ContentBlocks {
				if bl.Type == ContentTypeText {
					b.WriteString(bl.Text)
				}
			}
			if b.Len() > 0 {
				content = b.String()
			}
		}
		return []ollamaMessage{{Role: "tool", Content: content}}
	}

	om := ollamaMessage{Role: string(m.Role)}
	textParts := make([]string, 0, len(m.Content))
	images := make([]string, 0)
	toolCalls := make([]ollamaToolCall, 0)

	for _, cb := range m.Content {
		switch cb.Type {
		case ContentTypeText:
			textParts = append(textParts, cb.Text)
		case ContentTypeImage:
			// Ollama expects raw base64 image data (no data URI prefix).
			images = append(images, cb.ImageData)
		case ContentTypeToolUse:
			toolCalls = append(toolCalls, ollamaToolCall{
				Function: ollamaToolCallFunc{
					Name:      cb.ToolName,
					Arguments: cb.Input,
				},
			})
		case ContentTypeThinking:
			// Skip thinking blocks — Ollama doesn't support them.
		}
	}

	// If we have tool calls, emit them as an assistant message, potentially
	// splitting text and tool calls into separate messages since Ollama's
	// tool_calls appear on the assistant message.
	if len(toolCalls) > 0 {
		om.Role = "assistant"
		om.ToolCalls = toolCalls
		if len(textParts) > 0 {
			om.Content = strings.Join(textParts, "")
		}
		if len(images) > 0 {
			om.Images = images
		}
		return []ollamaMessage{om}
	}

	om.Content = strings.Join(textParts, "")
	if len(images) > 0 {
		om.Images = images
	}
	return []ollamaMessage{om}
}

// -- Stream parsing --

// Ollama streams newline-delimited JSON objects, one per line.
type ollamaStreamChunk struct {
	Model           string         `json:"model"`
	CreatedAt       string         `json:"created_at"`
	Message         ollamaChunkMsg `json:"message"`
	Done            bool           `json:"done"`
	TotalDuration   int64          `json:"total_duration,omitempty"`
	PromptEvalCount int            `json:"prompt_eval_count,omitempty"`
	EvalCount       int            `json:"eval_count,omitempty"`
}

type ollamaChunkMsg struct {
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
}

func (p *OllamaProvider) readStream(ctx context.Context, body io.ReadCloser, ch chan<- StreamEvent) {
	defer close(ch)
	defer func() { _ = body.Close() }()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	started := false
	toolCallIdx := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		select {
		case <-ctx.Done():
			ch <- StreamEvent{Type: EventError, Error: ctx.Err()}
			return
		default:
		}

		var chunk ollamaStreamChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("ollama: failed to parse chunk: %w", err)}
			return
		}

		if !started {
			started = true
			ch <- StreamEvent{Type: EventMessageStart}
		}

		// Text delta.
		if chunk.Message.Content != "" {
			ch <- StreamEvent{Type: EventTextDelta, Delta: chunk.Message.Content}
		}

		// Tool calls — Ollama emits complete tool calls in a single chunk.
		for _, tc := range chunk.Message.ToolCalls {
			callID := fmt.Sprintf("ollama_call_%d", toolCallIdx)
			toolCallIdx++

			ch <- StreamEvent{
				Type:       EventToolUseStart,
				ToolCallID: callID,
				ToolName:   tc.Function.Name,
			}

			// Serialize the arguments as a single delta.
			argsJSON, _ := json.Marshal(tc.Function.Arguments)
			ch <- StreamEvent{
				Type:         EventToolUseDelta,
				ToolCallID:   callID,
				ToolName:     tc.Function.Name,
				PartialInput: string(argsJSON),
			}

			ch <- StreamEvent{
				Type:       EventToolUseEnd,
				ToolCallID: callID,
				ToolName:   tc.Function.Name,
			}
		}

		// Final chunk.
		if chunk.Done {
			ch <- StreamEvent{
				Type: EventMessageEnd,
				Usage: &Usage{
					InputTokens:  chunk.PromptEvalCount,
					OutputTokens: chunk.EvalCount,
				},
			}
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("ollama: stream read error: %w", err)}
	}
}
