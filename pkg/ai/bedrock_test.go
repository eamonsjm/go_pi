package ai

import (
	"encoding/base64"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

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

	if *input.InferenceConfig.MaxTokens != bedrockDefaultMaxToks {
		t.Errorf("expected default max tokens %d, got %d", bedrockDefaultMaxToks, *input.InferenceConfig.MaxTokens)
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
