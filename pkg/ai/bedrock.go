package ai

import (
	"context"
	"encoding/base64"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// bedrockStreamer abstracts the Bedrock ConverseStream API call for testability.
type bedrockStreamer interface {
	ConverseStream(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

// BedrockProvider implements the Provider interface for AWS Bedrock's Converse Stream API.
// It supports the standard AWS credential chain (environment variables, shared config,
// IAM roles, instance profiles).
type BedrockProvider struct {
	client bedrockStreamer
}

// NewBedrockProvider creates a new AWS Bedrock provider.
// The ctx parameter controls cancellation of the AWS config loading (e.g. IMDS fetches).
// The region parameter is optional; if empty, it uses the AWS_REGION environment variable
// or the default region from the AWS config. Authentication uses the standard AWS
// credential chain (AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY, ~/.aws/credentials,
// IAM roles, etc.).
func NewBedrockProvider(ctx context.Context, region string) (*BedrockProvider, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("bedrock: failed to load AWS config: %w", err)
	}

	client := bedrockruntime.NewFromConfig(cfg)
	return &BedrockProvider{
		client: client,
	}, nil
}

func (p *BedrockProvider) Name() string { return "bedrock" }

// Stream sends a streaming request via the Bedrock Converse Stream API.
func (p *BedrockProvider) Stream(ctx context.Context, req StreamRequest) (<-chan StreamEvent, error) {
	input, err := p.buildConverseInput(req)
	if err != nil {
		return nil, fmt.Errorf("bedrock: failed to build request: %w", err)
	}

	output, err := p.client.ConverseStream(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("bedrock: request failed: %w", err)
	}

	ch := make(chan StreamEvent, 64)
	go p.readEventStream(ctx, output.GetStream(), ch)
	return ch, nil
}

func (p *BedrockProvider) buildConverseInput(req StreamRequest) (*bedrockruntime.ConverseStreamInput, error) {
	maxTokens := int32(req.MaxTokens)
	if maxTokens == 0 {
		maxTokens = BedrockDefaultMaxTokens
	}

	input := &bedrockruntime.ConverseStreamInput{
		ModelId: &req.Model,
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens: &maxTokens,
		},
	}

	if req.Temperature != nil {
		temp := float32(*req.Temperature)
		input.InferenceConfig.Temperature = &temp
	}
	if len(req.StopSequences) > 0 {
		input.InferenceConfig.StopSequences = req.StopSequences
	}

	// System prompt.
	if req.SystemPrompt != "" {
		input.System = []types.SystemContentBlock{
			&types.SystemContentBlockMemberText{Value: req.SystemPrompt},
		}
	}

	// Messages.
	msgs := make([]types.Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		bm, err := mapToBedrockMessage(m)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, bm)
	}
	input.Messages = msgs

	// Tools.
	if len(req.Tools) > 0 {
		tools := make([]types.Tool, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, &types.ToolMemberToolSpec{
				Value: types.ToolSpecification{
					Name:        &t.Name,
					Description: &t.Description,
					InputSchema: &types.ToolInputSchemaMemberJson{
						Value: document.NewLazyDocument(t.InputSchema),
					},
				},
			})
		}
		input.ToolConfig = &types.ToolConfiguration{Tools: tools}
	}

	return input, nil
}

func mapToBedrockMessage(m Message) (types.Message, error) {
	role := mapToBedrockRole(m.Role)
	blocks := make([]types.ContentBlock, 0, len(m.Content))

	for _, cb := range m.Content {
		switch cb.Type {
		case ContentTypeText:
			blocks = append(blocks, &types.ContentBlockMemberText{Value: cb.Text})

		case ContentTypeImage:
			imgBytes, err := base64.StdEncoding.DecodeString(cb.ImageData)
			if err != nil {
				return types.Message{}, fmt.Errorf("bedrock: failed to decode image: %w", err)
			}
			blocks = append(blocks, &types.ContentBlockMemberImage{
				Value: types.ImageBlock{
					Format: mapMediaTypeToImageFormat(cb.MediaType),
					Source: &types.ImageSourceMemberBytes{Value: imgBytes},
				},
			})

		case ContentTypeToolUse:
			blocks = append(blocks, &types.ContentBlockMemberToolUse{
				Value: types.ToolUseBlock{
					ToolUseId: &cb.ToolUseID,
					Name:      &cb.ToolName,
					Input:     document.NewLazyDocument(cb.Input),
				},
			})

		case ContentTypeToolResult:
			var resultContent []types.ToolResultContentBlock
			if len(cb.ContentBlocks) > 0 {
				for _, sub := range cb.ContentBlocks {
					switch sub.Type {
					case ContentTypeText:
						resultContent = append(resultContent, &types.ToolResultContentBlockMemberText{Value: sub.Text})
					case ContentTypeImage:
						imgBytes, imgErr := base64.StdEncoding.DecodeString(sub.ImageData)
						if imgErr == nil {
							resultContent = append(resultContent, &types.ToolResultContentBlockMemberImage{
								Value: types.ImageBlock{
									Format: mapMediaTypeToImageFormat(sub.MediaType),
									Source: &types.ImageSourceMemberBytes{Value: imgBytes},
								},
							})
						}
					}
				}
			}
			if len(resultContent) == 0 {
				resultContent = []types.ToolResultContentBlock{
					&types.ToolResultContentBlockMemberText{Value: cb.Content},
				}
			}
			status := types.ToolResultStatusSuccess
			if cb.IsError {
				status = types.ToolResultStatusError
			}
			blocks = append(blocks, &types.ContentBlockMemberToolResult{
				Value: types.ToolResultBlock{
					ToolUseId: &cb.ToolResultID,
					Content:   resultContent,
					Status:    status,
				},
			})
		}
	}

	return types.Message{
		Role:    role,
		Content: blocks,
	}, nil
}

func mapToBedrockRole(role Role) types.ConversationRole {
	switch role {
	case RoleAssistant:
		return types.ConversationRoleAssistant
	default:
		return types.ConversationRoleUser
	}
}

func mapMediaTypeToImageFormat(mediaType string) types.ImageFormat {
	switch mediaType {
	case "image/png":
		return types.ImageFormatPng
	case "image/gif":
		return types.ImageFormatGif
	case "image/webp":
		return types.ImageFormatWebp
	default:
		return types.ImageFormatJpeg
	}
}

// bedrockToolState tracks tool call metadata across stream events.
type bedrockToolState struct {
	id   string
	name string
}

func (p *BedrockProvider) readEventStream(ctx context.Context, stream *bedrockruntime.ConverseStreamEventStream, ch chan<- StreamEvent) {
	defer close(ch)
	defer func() {
		if r := recover(); r != nil {
			select {
			case ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("bedrock: stream goroutine panicked: %v", r)}:
			default:
			}
		}
	}()

	defer func() { _ = stream.Close() }()

	var usage Usage
	// Track active tool calls by content block index for emitting ToolUseEnd.
	activeTools := make(map[int32]*bedrockToolState)

	for event := range stream.Events() {
		select {
		case <-ctx.Done():
			trySend(ctx, ch, StreamEvent{Type: EventError, Error: ctx.Err()})
			return
		default:
		}

		switch ev := event.(type) {
		case *types.ConverseStreamOutputMemberMessageStart:
			if !trySend(ctx, ch, StreamEvent{Type: EventMessageStart}) {
				return
			}

		case *types.ConverseStreamOutputMemberContentBlockStart:
			if start, ok := ev.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
				idx := derefInt32(ev.Value.ContentBlockIndex)
				id := derefStr(start.Value.ToolUseId)
				name := derefStr(start.Value.Name)
				activeTools[idx] = &bedrockToolState{id: id, name: name}
				if !trySend(ctx, ch, StreamEvent{
					Type:       EventToolUseStart,
					ToolCallID: id,
					ToolName:   name,
				}) {
					return
				}
			}

		case *types.ConverseStreamOutputMemberContentBlockDelta:
			switch delta := ev.Value.Delta.(type) {
			case *types.ContentBlockDeltaMemberText:
				if !trySend(ctx, ch, StreamEvent{Type: EventTextDelta, Delta: delta.Value}) {
					return
				}
			case *types.ContentBlockDeltaMemberToolUse:
				idx := derefInt32(ev.Value.ContentBlockIndex)
				if state := activeTools[idx]; state != nil {
					if !trySend(ctx, ch, StreamEvent{
						Type:         EventToolUseDelta,
						ToolCallID:   state.id,
						ToolName:     state.name,
						PartialInput: derefStr(delta.Value.Input),
					}) {
						return
					}
				}
			}

		case *types.ConverseStreamOutputMemberContentBlockStop:
			idx := derefInt32(ev.Value.ContentBlockIndex)
			if state := activeTools[idx]; state != nil {
				if !trySend(ctx, ch, StreamEvent{
					Type:       EventToolUseEnd,
					ToolCallID: state.id,
					ToolName:   state.name,
				}) {
					return
				}
				delete(activeTools, idx)
			}

		case *types.ConverseStreamOutputMemberMessageStop:
			trySend(ctx, ch, StreamEvent{
				Type:  EventMessageEnd,
				Usage: &usage,
			})
			return

		case *types.ConverseStreamOutputMemberMetadata:
			if ev.Value.Usage != nil {
				usage.InputTokens = int(derefInt32(ev.Value.Usage.InputTokens))
				usage.OutputTokens = int(derefInt32(ev.Value.Usage.OutputTokens))
			}
		}
	}

	if err := stream.Err(); err != nil {
		trySend(ctx, ch, StreamEvent{Type: EventError, Error: fmt.Errorf("bedrock: stream error: %w", err)})
	}
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func derefInt32(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}
