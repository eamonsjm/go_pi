package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// --- Mock types for Stream/readEventStream tests ---

// mockBedrockStreamReader implements bedrockruntime.ConverseStreamOutputReader.
type mockBedrockStreamReader struct {
	events chan types.ConverseStreamOutput
	err    error
	mu     sync.Mutex
}

func (m *mockBedrockStreamReader) Events() <-chan types.ConverseStreamOutput {
	return m.events
}

func (m *mockBedrockStreamReader) Close() error { return nil }

func (m *mockBedrockStreamReader) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.err
}

// panicOnErrReader panics when Err() is called — used to test panic recovery.
type panicOnErrReader struct {
	events chan types.ConverseStreamOutput
}

func (p *panicOnErrReader) Events() <-chan types.ConverseStreamOutput { return p.events }
func (p *panicOnErrReader) Close() error                              { return nil }
func (p *panicOnErrReader) Err() error                                { panic("simulated stream panic") }

// mockBedrockClient implements bedrockStreamer for testing Stream().
type mockBedrockClient struct {
	fn func(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

func (m *mockBedrockClient) ConverseStream(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
	return m.fn(ctx, params, optFns...)
}

// --- Helpers ---

func newTestEventStream(reader bedrockruntime.ConverseStreamOutputReader) *bedrockruntime.ConverseStreamEventStream {
	return bedrockruntime.NewConverseStreamEventStream(func(s *bedrockruntime.ConverseStreamEventStream) {
		s.Reader = reader
	})
}

func strPtr(s string) *string { return &s }
func int32Ptr(v int32) *int32 { return &v }

func sendBedrockEvents(events ...types.ConverseStreamOutput) *mockBedrockStreamReader {
	ch := make(chan types.ConverseStreamOutput, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return &mockBedrockStreamReader{events: ch}
}

func TestBedrockProvider_Name(t *testing.T) {
	p := &BedrockProvider{}
	if p.Name() != "bedrock" {
		t.Errorf("expected name %q, got %q", "bedrock", p.Name())
	}
}

func TestMapToBedrockRole(t *testing.T) {
	tests := []struct {
		role Role
		want types.ConversationRole
	}{
		{RoleUser, types.ConversationRoleUser},
		{RoleAssistant, types.ConversationRoleAssistant},
		{RoleSystem, types.ConversationRoleUser}, // system maps to user as fallback
	}
	for _, tt := range tests {
		got := mapToBedrockRole(tt.role)
		if got != tt.want {
			t.Errorf("mapToBedrockRole(%q) = %q, want %q", tt.role, got, tt.want)
		}
	}
}

func TestMapMediaTypeToImageFormat(t *testing.T) {
	tests := []struct {
		mediaType string
		want      types.ImageFormat
	}{
		{"image/png", types.ImageFormatPng},
		{"image/gif", types.ImageFormatGif},
		{"image/webp", types.ImageFormatWebp},
		{"image/jpeg", types.ImageFormatJpeg},
		{"image/unknown", types.ImageFormatJpeg}, // default
	}
	for _, tt := range tests {
		got := mapMediaTypeToImageFormat(tt.mediaType)
		if got != tt.want {
			t.Errorf("mapMediaTypeToImageFormat(%q) = %q, want %q", tt.mediaType, got, tt.want)
		}
	}
}

func TestMapToBedrockMessage_Text(t *testing.T) {
	msg := NewTextMessage(RoleUser, "Hello world")
	bm, err := mapToBedrockMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bm.Role != types.ConversationRoleUser {
		t.Errorf("expected role %q, got %q", types.ConversationRoleUser, bm.Role)
	}
	if len(bm.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(bm.Content))
	}

	textBlock, ok := bm.Content[0].(*types.ContentBlockMemberText)
	if !ok {
		t.Fatalf("expected ContentBlockMemberText, got %T", bm.Content[0])
	}
	if textBlock.Value != "Hello world" {
		t.Errorf("expected text %q, got %q", "Hello world", textBlock.Value)
	}
}

func TestMapToBedrockMessage_ToolUse(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{
				Type:      ContentTypeToolUse,
				ToolUseID: "tool-123",
				ToolName:  "get_weather",
				Input:     map[string]any{"city": "NYC"},
			},
		},
	}

	bm, err := mapToBedrockMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bm.Role != types.ConversationRoleAssistant {
		t.Errorf("expected role %q, got %q", types.ConversationRoleAssistant, bm.Role)
	}
	if len(bm.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(bm.Content))
	}

	toolBlock, ok := bm.Content[0].(*types.ContentBlockMemberToolUse)
	if !ok {
		t.Fatalf("expected ContentBlockMemberToolUse, got %T", bm.Content[0])
	}
	if *toolBlock.Value.ToolUseId != "tool-123" {
		t.Errorf("expected tool use ID %q, got %q", "tool-123", *toolBlock.Value.ToolUseId)
	}
	if *toolBlock.Value.Name != "get_weather" {
		t.Errorf("expected tool name %q, got %q", "get_weather", *toolBlock.Value.Name)
	}
}

func TestMapToBedrockMessage_ToolResult(t *testing.T) {
	msg := NewToolResultMessage("tool-123", "sunny, 72°F", false)

	bm, err := mapToBedrockMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(bm.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(bm.Content))
	}

	resultBlock, ok := bm.Content[0].(*types.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("expected ContentBlockMemberToolResult, got %T", bm.Content[0])
	}
	if *resultBlock.Value.ToolUseId != "tool-123" {
		t.Errorf("expected tool use ID %q, got %q", "tool-123", *resultBlock.Value.ToolUseId)
	}
	if resultBlock.Value.Status != types.ToolResultStatusSuccess {
		t.Errorf("expected status %q, got %q", types.ToolResultStatusSuccess, resultBlock.Value.Status)
	}
}

func TestMapToBedrockMessage_ToolResultError(t *testing.T) {
	msg := NewToolResultMessage("tool-456", "command failed", true)

	bm, err := mapToBedrockMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resultBlock := bm.Content[0].(*types.ContentBlockMemberToolResult)
	if resultBlock.Value.Status != types.ToolResultStatusError {
		t.Errorf("expected status %q, got %q", types.ToolResultStatusError, resultBlock.Value.Status)
	}
}

func TestMapToBedrockMessage_RichToolResult(t *testing.T) {
	msg := NewRichToolResultMessage("tool-rich", []ContentBlock{
		{Type: ContentTypeText, Text: "first block"},
		{Type: ContentTypeText, Text: "second block"},
	}, false)

	bm, err := mapToBedrockMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(bm.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(bm.Content))
	}

	resultBlock, ok := bm.Content[0].(*types.ContentBlockMemberToolResult)
	if !ok {
		t.Fatalf("expected ContentBlockMemberToolResult, got %T", bm.Content[0])
	}
	if *resultBlock.Value.ToolUseId != "tool-rich" {
		t.Errorf("expected tool use ID %q, got %q", "tool-rich", *resultBlock.Value.ToolUseId)
	}
	if len(resultBlock.Value.Content) != 2 {
		t.Fatalf("expected 2 tool result content blocks, got %d", len(resultBlock.Value.Content))
	}
	text0, ok := resultBlock.Value.Content[0].(*types.ToolResultContentBlockMemberText)
	if !ok {
		t.Fatalf("expected ToolResultContentBlockMemberText at [0], got %T", resultBlock.Value.Content[0])
	}
	if text0.Value != "first block" {
		t.Errorf("expected text %q, got %q", "first block", text0.Value)
	}
	text1, ok := resultBlock.Value.Content[1].(*types.ToolResultContentBlockMemberText)
	if !ok {
		t.Fatalf("expected ToolResultContentBlockMemberText at [1], got %T", resultBlock.Value.Content[1])
	}
	if text1.Value != "second block" {
		t.Errorf("expected text %q, got %q", "second block", text1.Value)
	}
	if resultBlock.Value.Status != types.ToolResultStatusSuccess {
		t.Errorf("expected status %q, got %q", types.ToolResultStatusSuccess, resultBlock.Value.Status)
	}
}

func TestMapToBedrockMessage_RichToolResultWithImage(t *testing.T) {
	imgData := base64.StdEncoding.EncodeToString([]byte("fake-image"))
	msg := NewRichToolResultMessage("tool-img", []ContentBlock{
		{Type: ContentTypeText, Text: "description"},
		{Type: ContentTypeImage, MediaType: "image/png", ImageData: imgData},
	}, false)

	bm, err := mapToBedrockMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resultBlock := bm.Content[0].(*types.ContentBlockMemberToolResult)
	if len(resultBlock.Value.Content) != 2 {
		t.Fatalf("expected 2 tool result content blocks, got %d", len(resultBlock.Value.Content))
	}
	if _, ok := resultBlock.Value.Content[0].(*types.ToolResultContentBlockMemberText); !ok {
		t.Errorf("expected text block at [0], got %T", resultBlock.Value.Content[0])
	}
	imgBlock, ok := resultBlock.Value.Content[1].(*types.ToolResultContentBlockMemberImage)
	if !ok {
		t.Fatalf("expected image block at [1], got %T", resultBlock.Value.Content[1])
	}
	if imgBlock.Value.Format != types.ImageFormatPng {
		t.Errorf("expected format %q, got %q", types.ImageFormatPng, imgBlock.Value.Format)
	}
}

func TestMapToBedrockMessage_Image(t *testing.T) {
	imgData := base64.StdEncoding.EncodeToString([]byte("fake-image-data"))
	msg := Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{
				Type:      ContentTypeImage,
				MediaType: "image/png",
				ImageData: imgData,
			},
		},
	}

	bm, err := mapToBedrockMessage(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	imgBlock, ok := bm.Content[0].(*types.ContentBlockMemberImage)
	if !ok {
		t.Fatalf("expected ContentBlockMemberImage, got %T", bm.Content[0])
	}
	if imgBlock.Value.Format != types.ImageFormatPng {
		t.Errorf("expected format %q, got %q", types.ImageFormatPng, imgBlock.Value.Format)
	}
}

func TestBuildConverseInput_Basic(t *testing.T) {
	p := &BedrockProvider{}
	model := "anthropic.claude-3-5-sonnet-20241022-v2:0"
	req := StreamRequest{
		Model:        model,
		SystemPrompt: "You are helpful.",
		Messages: []Message{
			NewTextMessage(RoleUser, "Hello"),
		},
		MaxTokens: 2048,
	}

	input, err := p.buildConverseInput(req)
	if err != nil {
		t.Fatalf("buildConverseInput failed: %v", err)
	}

	if *input.ModelId != model {
		t.Errorf("expected model %q, got %q", model, *input.ModelId)
	}
	if *input.InferenceConfig.MaxTokens != 2048 {
		t.Errorf("expected max tokens 2048, got %d", *input.InferenceConfig.MaxTokens)
	}
	if len(input.System) != 1 {
		t.Fatalf("expected 1 system block, got %d", len(input.System))
	}
	if len(input.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(input.Messages))
	}
}

func TestBuildConverseInput_DefaultMaxTokens(t *testing.T) {
	p := &BedrockProvider{}
	model := "test-model"
	req := StreamRequest{
		Model:    model,
		Messages: []Message{NewTextMessage(RoleUser, "Hello")},
	}

	input, err := p.buildConverseInput(req)
	if err != nil {
		t.Fatalf("buildConverseInput failed: %v", err)
	}

	if *input.InferenceConfig.MaxTokens != BedrockDefaultMaxTokens {
		t.Errorf("expected default max tokens %d, got %d", BedrockDefaultMaxTokens, *input.InferenceConfig.MaxTokens)
	}
}

func TestBuildConverseInput_WithTools(t *testing.T) {
	p := &BedrockProvider{}
	req := StreamRequest{
		Model:    "test-model",
		Messages: []Message{NewTextMessage(RoleUser, "What's the weather?")},
		Tools: []ToolDef{
			{
				Name:        "get_weather",
				Description: "Get weather for a city",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
				},
			},
		},
	}

	input, err := p.buildConverseInput(req)
	if err != nil {
		t.Fatalf("buildConverseInput failed: %v", err)
	}

	if input.ToolConfig == nil {
		t.Fatal("expected tool config, got nil")
	}
	if len(input.ToolConfig.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(input.ToolConfig.Tools))
	}

	toolSpec, ok := input.ToolConfig.Tools[0].(*types.ToolMemberToolSpec)
	if !ok {
		t.Fatalf("expected ToolMemberToolSpec, got %T", input.ToolConfig.Tools[0])
	}
	if *toolSpec.Value.Name != "get_weather" {
		t.Errorf("expected tool name %q, got %q", "get_weather", *toolSpec.Value.Name)
	}
}

func TestBuildConverseInput_Temperature(t *testing.T) {
	p := &BedrockProvider{}
	temp := 0.7
	req := StreamRequest{
		Model:       "test-model",
		Messages:    []Message{NewTextMessage(RoleUser, "Hello")},
		Temperature: &temp,
	}

	input, err := p.buildConverseInput(req)
	if err != nil {
		t.Fatalf("buildConverseInput failed: %v", err)
	}

	if input.InferenceConfig.Temperature == nil {
		t.Fatal("expected temperature, got nil")
	}
	if *input.InferenceConfig.Temperature != float32(0.7) {
		t.Errorf("expected temperature 0.7, got %f", *input.InferenceConfig.Temperature)
	}
}

func TestBuildConverseInput_StopSequences(t *testing.T) {
	p := &BedrockProvider{}
	req := StreamRequest{
		Model:         "test-model",
		Messages:      []Message{NewTextMessage(RoleUser, "Hello")},
		StopSequences: []string{"STOP", "END"},
	}

	input, err := p.buildConverseInput(req)
	if err != nil {
		t.Fatalf("buildConverseInput failed: %v", err)
	}

	if len(input.InferenceConfig.StopSequences) != 2 {
		t.Errorf("expected 2 stop sequences, got %d", len(input.InferenceConfig.StopSequences))
	}
}

func TestDerefStr(t *testing.T) {
	s := "hello"
	if got := derefStr(&s); got != "hello" {
		t.Errorf("derefStr(&%q) = %q, want %q", s, got, "hello")
	}
	if got := derefStr(nil); got != "" {
		t.Errorf("derefStr(nil) = %q, want %q", got, "")
	}
}

func TestDerefInt32(t *testing.T) {
	v := int32(42)
	if got := derefInt32(&v); got != 42 {
		t.Errorf("derefInt32(&42) = %d, want 42", got)
	}
	if got := derefInt32(nil); got != 0 {
		t.Errorf("derefInt32(nil) = %d, want 0", got)
	}
}

// --- readEventStream tests ---

func TestReadEventStream_TextStream(t *testing.T) {
	reader := sendBedrockEvents(
		&types.ConverseStreamOutputMemberMessageStart{
			Value: types.MessageStartEvent{Role: types.ConversationRoleAssistant},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: int32Ptr(0),
				Delta:             &types.ContentBlockDeltaMemberText{Value: "Hello"},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: int32Ptr(0),
				Delta:             &types.ContentBlockDeltaMemberText{Value: " world"},
			},
		},
		&types.ConverseStreamOutputMemberMetadata{
			Value: types.ConverseStreamMetadataEvent{
				Usage: &types.TokenUsage{
					InputTokens:  int32Ptr(10),
					OutputTokens: int32Ptr(5),
				},
			},
		},
		&types.ConverseStreamOutputMemberMessageStop{
			Value: types.MessageStopEvent{StopReason: types.StopReasonEndTurn},
		},
	)
	stream := newTestEventStream(reader)
	ch := make(chan StreamEvent, 64)
	p := &BedrockProvider{}

	go p.readEventStream(context.Background(), stream, ch)
	events := collectEvents(ch)

	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d: %+v", len(events), events)
	}

	if events[0].Type != EventMessageStart {
		t.Errorf("events[0]: expected %s, got %s", EventMessageStart, events[0].Type)
	}
	if events[1].Type != EventTextDelta || events[1].Delta != "Hello" {
		t.Errorf("events[1]: expected text delta 'Hello', got %s %q", events[1].Type, events[1].Delta)
	}
	if events[2].Type != EventTextDelta || events[2].Delta != " world" {
		t.Errorf("events[2]: expected text delta ' world', got %s %q", events[2].Type, events[2].Delta)
	}
	if events[3].Type != EventMessageEnd {
		t.Errorf("events[3]: expected %s, got %s", EventMessageEnd, events[3].Type)
	}
	if events[3].Usage == nil {
		t.Fatal("events[3]: expected usage, got nil")
	}
	if events[3].Usage.InputTokens != 10 || events[3].Usage.OutputTokens != 5 {
		t.Errorf("usage: expected 10/5, got %d/%d", events[3].Usage.InputTokens, events[3].Usage.OutputTokens)
	}
}

func TestReadEventStream_ToolUseFlow(t *testing.T) {
	reader := sendBedrockEvents(
		&types.ConverseStreamOutputMemberMessageStart{
			Value: types.MessageStartEvent{Role: types.ConversationRoleAssistant},
		},
		&types.ConverseStreamOutputMemberContentBlockStart{
			Value: types.ContentBlockStartEvent{
				ContentBlockIndex: int32Ptr(0),
				Start: &types.ContentBlockStartMemberToolUse{
					Value: types.ToolUseBlockStart{
						ToolUseId: strPtr("tool-abc"),
						Name:      strPtr("get_weather"),
					},
				},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: int32Ptr(0),
				Delta: &types.ContentBlockDeltaMemberToolUse{
					Value: types.ToolUseBlockDelta{Input: strPtr(`{"city":`)},
				},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: int32Ptr(0),
				Delta: &types.ContentBlockDeltaMemberToolUse{
					Value: types.ToolUseBlockDelta{Input: strPtr(`"NYC"}`)},
				},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockStop{
			Value: types.ContentBlockStopEvent{ContentBlockIndex: int32Ptr(0)},
		},
		&types.ConverseStreamOutputMemberMessageStop{
			Value: types.MessageStopEvent{StopReason: types.StopReasonToolUse},
		},
	)
	stream := newTestEventStream(reader)
	ch := make(chan StreamEvent, 64)
	p := &BedrockProvider{}

	go p.readEventStream(context.Background(), stream, ch)
	events := collectEvents(ch)

	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d: %+v", len(events), events)
	}

	// MessageStart
	if events[0].Type != EventMessageStart {
		t.Errorf("events[0]: expected %s, got %s", EventMessageStart, events[0].Type)
	}

	// ToolUseStart
	if events[1].Type != EventToolUseStart {
		t.Errorf("events[1]: expected %s, got %s", EventToolUseStart, events[1].Type)
	}
	if events[1].ToolCallID != "tool-abc" || events[1].ToolName != "get_weather" {
		t.Errorf("events[1]: expected tool-abc/get_weather, got %s/%s", events[1].ToolCallID, events[1].ToolName)
	}

	// ToolUseDeltas
	if events[2].Type != EventToolUseDelta || events[2].PartialInput != `{"city":` {
		t.Errorf("events[2]: expected tool delta with partial input, got %s %q", events[2].Type, events[2].PartialInput)
	}
	if events[2].ToolCallID != "tool-abc" || events[2].ToolName != "get_weather" {
		t.Errorf("events[2]: expected tool metadata propagated, got %s/%s", events[2].ToolCallID, events[2].ToolName)
	}
	if events[3].Type != EventToolUseDelta || events[3].PartialInput != `"NYC"}` {
		t.Errorf("events[3]: expected tool delta, got %s %q", events[3].Type, events[3].PartialInput)
	}

	// ToolUseEnd
	if events[4].Type != EventToolUseEnd {
		t.Errorf("events[4]: expected %s, got %s", EventToolUseEnd, events[4].Type)
	}
	if events[4].ToolCallID != "tool-abc" || events[4].ToolName != "get_weather" {
		t.Errorf("events[4]: expected tool metadata on end, got %s/%s", events[4].ToolCallID, events[4].ToolName)
	}

	// MessageEnd
	if events[5].Type != EventMessageEnd {
		t.Errorf("events[5]: expected %s, got %s", EventMessageEnd, events[5].Type)
	}
}

func TestReadEventStream_MultipleToolCalls(t *testing.T) {
	reader := sendBedrockEvents(
		&types.ConverseStreamOutputMemberMessageStart{
			Value: types.MessageStartEvent{},
		},
		// First tool at content block index 0
		&types.ConverseStreamOutputMemberContentBlockStart{
			Value: types.ContentBlockStartEvent{
				ContentBlockIndex: int32Ptr(0),
				Start: &types.ContentBlockStartMemberToolUse{
					Value: types.ToolUseBlockStart{
						ToolUseId: strPtr("tool-1"),
						Name:      strPtr("read_file"),
					},
				},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockStop{
			Value: types.ContentBlockStopEvent{ContentBlockIndex: int32Ptr(0)},
		},
		// Second tool at content block index 1
		&types.ConverseStreamOutputMemberContentBlockStart{
			Value: types.ContentBlockStartEvent{
				ContentBlockIndex: int32Ptr(1),
				Start: &types.ContentBlockStartMemberToolUse{
					Value: types.ToolUseBlockStart{
						ToolUseId: strPtr("tool-2"),
						Name:      strPtr("write_file"),
					},
				},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockStop{
			Value: types.ContentBlockStopEvent{ContentBlockIndex: int32Ptr(1)},
		},
		&types.ConverseStreamOutputMemberMessageStop{
			Value: types.MessageStopEvent{},
		},
	)
	stream := newTestEventStream(reader)
	ch := make(chan StreamEvent, 64)
	p := &BedrockProvider{}

	go p.readEventStream(context.Background(), stream, ch)
	events := collectEvents(ch)

	// MessageStart, ToolStart1, ToolEnd1, ToolStart2, ToolEnd2, MessageEnd
	if len(events) != 6 {
		t.Fatalf("expected 6 events, got %d", len(events))
	}
	if events[1].ToolCallID != "tool-1" || events[1].ToolName != "read_file" {
		t.Errorf("first tool: expected tool-1/read_file, got %s/%s", events[1].ToolCallID, events[1].ToolName)
	}
	if events[2].ToolCallID != "tool-1" {
		t.Errorf("first tool end: expected tool-1, got %s", events[2].ToolCallID)
	}
	if events[3].ToolCallID != "tool-2" || events[3].ToolName != "write_file" {
		t.Errorf("second tool: expected tool-2/write_file, got %s/%s", events[3].ToolCallID, events[3].ToolName)
	}
	if events[4].ToolCallID != "tool-2" {
		t.Errorf("second tool end: expected tool-2, got %s", events[4].ToolCallID)
	}
}

func TestReadEventStream_ContentBlockStartNonToolUse(t *testing.T) {
	// ContentBlockStart without ToolUse start should not emit any event.
	reader := sendBedrockEvents(
		&types.ConverseStreamOutputMemberMessageStart{
			Value: types.MessageStartEvent{},
		},
		&types.ConverseStreamOutputMemberContentBlockStart{
			Value: types.ContentBlockStartEvent{
				ContentBlockIndex: int32Ptr(0),
				Start:             nil, // no tool use start
			},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: int32Ptr(0),
				Delta:             &types.ContentBlockDeltaMemberText{Value: "text"},
			},
		},
		&types.ConverseStreamOutputMemberMessageStop{
			Value: types.MessageStopEvent{},
		},
	)
	stream := newTestEventStream(reader)
	ch := make(chan StreamEvent, 64)
	p := &BedrockProvider{}

	go p.readEventStream(context.Background(), stream, ch)
	events := collectEvents(ch)

	// MessageStart, TextDelta, MessageEnd — no ToolUseStart
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d: %+v", len(events), events)
	}
	if events[1].Type != EventTextDelta {
		t.Errorf("expected text delta, got %s", events[1].Type)
	}
}

func TestReadEventStream_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventsCh := make(chan types.ConverseStreamOutput, 2)
	reader := &mockBedrockStreamReader{events: eventsCh}
	stream := newTestEventStream(reader)
	ch := make(chan StreamEvent, 64)
	p := &BedrockProvider{}

	go p.readEventStream(ctx, stream, ch)

	// Send message start and wait for it to be processed.
	eventsCh <- &types.ConverseStreamOutputMemberMessageStart{
		Value: types.MessageStartEvent{},
	}
	evt := <-ch
	if evt.Type != EventMessageStart {
		t.Fatalf("expected MessageStart, got %s", evt.Type)
	}

	// Cancel context, then send another event to unblock the range loop.
	cancel()
	eventsCh <- &types.ConverseStreamOutputMemberContentBlockDelta{
		Value: types.ContentBlockDeltaEvent{
			Delta: &types.ContentBlockDeltaMemberText{Value: "ignored"},
		},
	}

	// Collect remaining events — should get an error event.
	var remaining []StreamEvent
	for evt := range ch {
		remaining = append(remaining, evt)
	}

	if len(remaining) != 1 {
		t.Fatalf("expected 1 error event, got %d: %+v", len(remaining), remaining)
	}
	if remaining[0].Type != EventError {
		t.Errorf("expected error event, got %s", remaining[0].Type)
	}
	if !errors.Is(remaining[0].Error, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", remaining[0].Error)
	}
}

func TestReadEventStream_StreamError(t *testing.T) {
	reader := &mockBedrockStreamReader{
		events: make(chan types.ConverseStreamOutput),
		err:    errors.New("connection reset"),
	}
	close(reader.events) // empty stream, range ends immediately

	stream := newTestEventStream(reader)
	ch := make(chan StreamEvent, 64)
	p := &BedrockProvider{}

	go p.readEventStream(context.Background(), stream, ch)
	events := collectEvents(ch)

	if len(events) != 1 {
		t.Fatalf("expected 1 error event, got %d: %+v", len(events), events)
	}
	if events[0].Type != EventError {
		t.Errorf("expected error event, got %s", events[0].Type)
	}
	if !strings.Contains(events[0].Error.Error(), "connection reset") {
		t.Errorf("expected 'connection reset' in error, got: %v", events[0].Error)
	}
}

func TestReadEventStream_PanicRecovery(t *testing.T) {
	eventsCh := make(chan types.ConverseStreamOutput)
	close(eventsCh) // empty stream — Err() is called after range, which panics
	reader := &panicOnErrReader{events: eventsCh}

	stream := newTestEventStream(reader)
	ch := make(chan StreamEvent, 64)
	p := &BedrockProvider{}

	go p.readEventStream(context.Background(), stream, ch)
	events := collectEvents(ch)

	if len(events) != 1 {
		t.Fatalf("expected 1 panic error event, got %d: %+v", len(events), events)
	}
	if events[0].Type != EventError {
		t.Errorf("expected error event, got %s", events[0].Type)
	}
	if !strings.Contains(events[0].Error.Error(), "panicked") {
		t.Errorf("expected 'panicked' in error message, got: %v", events[0].Error)
	}
}

func TestReadEventStream_MetadataBeforeMessageStop(t *testing.T) {
	reader := sendBedrockEvents(
		&types.ConverseStreamOutputMemberMessageStart{
			Value: types.MessageStartEvent{},
		},
		&types.ConverseStreamOutputMemberMetadata{
			Value: types.ConverseStreamMetadataEvent{
				Usage: &types.TokenUsage{
					InputTokens:  int32Ptr(100),
					OutputTokens: int32Ptr(42),
				},
			},
		},
		&types.ConverseStreamOutputMemberMessageStop{
			Value: types.MessageStopEvent{},
		},
	)
	stream := newTestEventStream(reader)
	ch := make(chan StreamEvent, 64)
	p := &BedrockProvider{}

	go p.readEventStream(context.Background(), stream, ch)
	events := collectEvents(ch)

	if len(events) != 2 {
		t.Fatalf("expected 2 events (start + end), got %d", len(events))
	}
	if events[1].Usage == nil {
		t.Fatal("expected usage on message end")
	}
	if events[1].Usage.InputTokens != 100 || events[1].Usage.OutputTokens != 42 {
		t.Errorf("expected 100/42 tokens, got %d/%d", events[1].Usage.InputTokens, events[1].Usage.OutputTokens)
	}
}

func TestReadEventStream_MetadataNilUsage(t *testing.T) {
	reader := sendBedrockEvents(
		&types.ConverseStreamOutputMemberMessageStart{
			Value: types.MessageStartEvent{},
		},
		&types.ConverseStreamOutputMemberMetadata{
			Value: types.ConverseStreamMetadataEvent{
				Usage: nil, // nil usage should be safely ignored
			},
		},
		&types.ConverseStreamOutputMemberMessageStop{
			Value: types.MessageStopEvent{},
		},
	)
	stream := newTestEventStream(reader)
	ch := make(chan StreamEvent, 64)
	p := &BedrockProvider{}

	go p.readEventStream(context.Background(), stream, ch)
	events := collectEvents(ch)

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[1].Usage == nil {
		t.Fatal("expected non-nil usage (zero values)")
	}
	if events[1].Usage.InputTokens != 0 || events[1].Usage.OutputTokens != 0 {
		t.Errorf("expected 0/0 tokens, got %d/%d", events[1].Usage.InputTokens, events[1].Usage.OutputTokens)
	}
}

func TestReadEventStream_ToolUseDeltaWithoutStart(t *testing.T) {
	// ToolUse delta for an index that was never started should be silently ignored.
	reader := sendBedrockEvents(
		&types.ConverseStreamOutputMemberMessageStart{
			Value: types.MessageStartEvent{},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: int32Ptr(99), // no tool started at this index
				Delta: &types.ContentBlockDeltaMemberToolUse{
					Value: types.ToolUseBlockDelta{Input: strPtr(`{"x":1}`)},
				},
			},
		},
		&types.ConverseStreamOutputMemberMessageStop{
			Value: types.MessageStopEvent{},
		},
	)
	stream := newTestEventStream(reader)
	ch := make(chan StreamEvent, 64)
	p := &BedrockProvider{}

	go p.readEventStream(context.Background(), stream, ch)
	events := collectEvents(ch)

	// MessageStart + MessageEnd — the orphaned tool delta is silently dropped
	if len(events) != 2 {
		t.Fatalf("expected 2 events (no orphaned tool delta), got %d: %+v", len(events), events)
	}
}

// --- Stream() tests ---

func TestStream_ClientError(t *testing.T) {
	p := &BedrockProvider{
		client: &mockBedrockClient{
			fn: func(_ context.Context, _ *bedrockruntime.ConverseStreamInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
				return nil, errors.New("access denied")
			},
		},
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model:    "test-model",
		Messages: []Message{NewTextMessage(RoleUser, "Hello")},
	})
	if err == nil {
		t.Fatal("expected error from Stream()")
	}
	if !strings.Contains(err.Error(), "request failed") {
		t.Errorf("expected 'request failed' in error, got: %v", err)
	}
}

func TestStream_BuildInputError(t *testing.T) {
	p := &BedrockProvider{
		client: &mockBedrockClient{
			fn: func(_ context.Context, _ *bedrockruntime.ConverseStreamInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
				t.Fatal("ConverseStream should not be called when buildConverseInput fails")
				return nil, nil
			},
		},
	}

	_, err := p.Stream(context.Background(), StreamRequest{
		Model: "test-model",
		Messages: []Message{{
			Role: RoleUser,
			Content: []ContentBlock{{
				Type:      ContentTypeImage,
				ImageData: "not-valid-base64!!!",
			}},
		}},
	})
	if err == nil {
		t.Fatal("expected error from Stream()")
	}
	if !strings.Contains(err.Error(), "failed to build request") {
		t.Errorf("expected 'failed to build request' in error, got: %v", err)
	}
}
