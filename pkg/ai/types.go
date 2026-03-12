package ai

import "context"

// Role represents a message role in the conversation.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// ContentType identifies the type of a content block.
type ContentType string

const (
	ContentTypeText      ContentType = "text"
	ContentTypeToolUse   ContentType = "tool_use"
	ContentTypeToolResult ContentType = "tool_result"
	ContentTypeThinking  ContentType = "thinking"
	ContentTypeImage     ContentType = "image"
)

// ContentBlock is a single piece of content within a message.
type ContentBlock struct {
	Type ContentType `json:"type"`

	// Text content
	Text string `json:"text,omitempty"`

	// Tool use
	ToolUseID string `json:"id,omitempty"`
	ToolName  string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`

	// Tool result
	ToolResultID string `json:"tool_use_id,omitempty"`
	Content      string `json:"content,omitempty"`
	IsError      bool   `json:"is_error,omitempty"`

	// Thinking
	Thinking string `json:"thinking,omitempty"`

	// Image
	MediaType string `json:"media_type,omitempty"`
	ImageData string `json:"data,omitempty"`

	// Rich tool results: multiple content blocks (text + image) in a single tool result.
	ContentBlocks []ContentBlock `json:"content_blocks,omitempty"`
}

// Message represents a conversation message.
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

// NewTextMessage creates a simple text message.
func NewTextMessage(role Role, text string) Message {
	return Message{
		Role: role,
		Content: []ContentBlock{
			{Type: ContentTypeText, Text: text},
		},
	}
}

// NewToolResultMessage creates a tool result message.
func NewToolResultMessage(toolUseID, content string, isError bool) Message {
	return Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{
				Type:         ContentTypeToolResult,
				ToolResultID: toolUseID,
				Content:      content,
				IsError:      isError,
			},
		},
	}
}

// NewRichToolResultMessage creates a tool result message with multiple content blocks.
func NewRichToolResultMessage(toolUseID string, blocks []ContentBlock, isError bool) Message {
	return Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{
				Type:          ContentTypeToolResult,
				ToolResultID:  toolUseID,
				ContentBlocks: blocks,
				IsError:       isError,
			},
		},
	}
}

// GetText returns the concatenated text content of a message.
func (m Message) GetText() string {
	var text string
	for _, c := range m.Content {
		if c.Type == ContentTypeText {
			text += c.Text
		}
	}
	return text
}

// GetToolCalls returns all tool use blocks from a message.
func (m Message) GetToolCalls() []ContentBlock {
	var calls []ContentBlock
	for _, c := range m.Content {
		if c.Type == ContentTypeToolUse {
			calls = append(calls, c)
		}
	}
	return calls
}

// GetThinking returns concatenated thinking content.
func (m Message) GetThinking() string {
	var text string
	for _, c := range m.Content {
		if c.Type == ContentTypeThinking {
			text += c.Thinking
		}
	}
	return text
}

// ToolDef defines a tool the model can call.
type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// ThinkingLevel controls extended thinking behavior.
type ThinkingLevel string

const (
	ThinkingOff    ThinkingLevel = "off"
	ThinkingLow    ThinkingLevel = "low"
	ThinkingMedium ThinkingLevel = "medium"
	ThinkingHigh   ThinkingLevel = "high"
)

// StreamEvent represents a streaming event from the provider.
type StreamEvent struct {
	Type StreamEventType

	// For text/thinking deltas
	Delta string

	// For tool use deltas
	ToolCallID   string
	ToolName     string
	PartialInput string

	// For message_start
	Message *Message

	// For usage
	Usage *Usage

	// For errors
	Error error
}

// StreamEventType identifies what kind of stream event this is.
type StreamEventType string

const (
	EventMessageStart   StreamEventType = "message_start"
	EventTextDelta      StreamEventType = "text_delta"
	EventThinkingDelta  StreamEventType = "thinking_delta"
	EventToolUseStart   StreamEventType = "tool_use_start"
	EventToolUseDelta   StreamEventType = "tool_use_delta"
	EventToolUseEnd     StreamEventType = "tool_use_end"
	EventMessageEnd     StreamEventType = "message_end"
	EventError          StreamEventType = "error"
)

// Usage tracks token usage.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheRead    int `json:"cache_read_input_tokens,omitempty"`
	CacheWrite   int `json:"cache_creation_input_tokens,omitempty"`
}

// StreamRequest contains all parameters for a streaming LLM call.
type StreamRequest struct {
	Model         string
	SystemPrompt  string
	Messages      []Message
	Tools         []ToolDef
	MaxTokens     int
	ThinkingLevel ThinkingLevel
	Temperature   *float64
	StopSequences []string
}

// Provider is the interface for LLM providers.
type Provider interface {
	// Stream sends a request and returns a channel of stream events.
	Stream(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error)

	// Name returns the provider name.
	Name() string
}
