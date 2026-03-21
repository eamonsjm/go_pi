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

// BedrockProvider implements the Provider interface for AWS Bedrock's Converse Stream API.
// It supports the standard AWS credential chain (environment variables, shared config,
// IAM roles, instance profiles).
type BedrockProvider struct {
	client *bedrockruntime.Client
	region string
}

// NewBedrockProvider creates a new AWS Bedrock provider.
// The region parameter is optional; if empty, it uses the AWS_REGION environment variable
// or the default region from the AWS config. Authentication uses the standard AWS
// credential chain (AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY, ~/.aws/credentials,
// IAM roles, etc.).
func NewBedrockProvider(region string) (*BedrockProvider, error) {
	var opts []func(*awsconfig.LoadOptions) error
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("bedrock: failed to load AWS config: %w", err)
	}

	client := bedrockruntime.NewFromConfig(cfg)
	return &BedrockProvider{
		client: client,
		region: cfg.Region,
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
	go p.readEventStream(ctx, output, ch)
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
			content := cb.Content
			if len(cb.ContentBlocks) > 0 {
				for _, sub := range cb.ContentBlocks {
					if sub.Type == ContentTypeText {
						content = sub.Text
						break
					}
				}
			}
			status := types.ToolResultStatusSuccess
			if cb.IsError {
				status = types.ToolResultStatusError
			}
			blocks = append(blocks, &types.ContentBlockMemberToolResult{
				Value: types.ToolResultBlock{
					ToolUseId: &cb.ToolResultID,
					Content: []types.ToolResultContentBlock{
						&types.ToolResultContentBlockMemberText{Value: content},
					},
					Status: status,
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

func (p *BedrockProvider) readEventStream(ctx context.Context, output *bedrockruntime.ConverseStreamOutput, ch chan<- StreamEvent) {
	defer close(ch)

	stream := output.GetStream()
	defer func() { _ = stream.Close() }()

	var usage Usage
	// Track active tool calls by content block index for emitting ToolUseEnd.
	activeTools := make(map[int32]*bedrockToolState)

	for event := range stream.Events() {
		select {
		case <-ctx.Done():
			ch <- StreamEvent{Type: EventError, Error: ctx.Err()}
			return
		default:
		}

		switch ev := event.(type) {
		case *types.ConverseStreamOutputMemberMessageStart:
			ch <- StreamEvent{Type: EventMessageStart}

		case *types.ConverseStreamOutputMemberContentBlockStart:
			if start, ok := ev.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
				idx := derefInt32(ev.Value.ContentBlockIndex)
				id := derefStr(start.Value.ToolUseId)
				name := derefStr(start.Value.Name)
				activeTools[idx] = &bedrockToolState{id: id, name: name}
				ch <- StreamEvent{
					Type:       EventToolUseStart,
					ToolCallID: id,
					ToolName:   name,
				}
			}

		case *types.ConverseStreamOutputMemberContentBlockDelta:
			switch delta := ev.Value.Delta.(type) {
			case *types.ContentBlockDeltaMemberText:
				ch <- StreamEvent{Type: EventTextDelta, Delta: delta.Value}
			case *types.ContentBlockDeltaMemberToolUse:
				idx := derefInt32(ev.Value.ContentBlockIndex)
				evt := StreamEvent{
					Type:         EventToolUseDelta,
					PartialInput: derefStr(delta.Value.Input),
				}
				if state := activeTools[idx]; state != nil {
					evt.ToolCallID = state.id
					evt.ToolName = state.name
				}
				ch <- evt
			}

		case *types.ConverseStreamOutputMemberContentBlockStop:
			idx := derefInt32(ev.Value.ContentBlockIndex)
			if state := activeTools[idx]; state != nil {
				ch <- StreamEvent{
					Type:       EventToolUseEnd,
					ToolCallID: state.id,
					ToolName:   state.name,
				}
				delete(activeTools, idx)
			}

		case *types.ConverseStreamOutputMemberMessageStop:
			ch <- StreamEvent{
				Type:  EventMessageEnd,
				Usage: &usage,
			}
			return

		case *types.ConverseStreamOutputMemberMetadata:
			if ev.Value.Usage != nil {
				usage.InputTokens = int(derefInt32(ev.Value.Usage.InputTokens))
				usage.OutputTokens = int(derefInt32(ev.Value.Usage.OutputTokens))
			}
		}
	}

	if err := stream.Err(); err != nil {
		ch <- StreamEvent{Type: EventError, Error: fmt.Errorf("bedrock: stream error: %w", err)}
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
