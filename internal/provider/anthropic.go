package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Anthropic implements the Provider interface for Claude models.
type Anthropic struct {
	client *anthropic.Client
	model  string
}

// AnthropicConfig holds configuration for the Anthropic provider.
type AnthropicConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// NewAnthropic creates a new Anthropic provider.
func NewAnthropic(cfg AnthropicConfig) (*Anthropic, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("anthropic api key is required")
	}

	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}

	client := anthropic.NewClient(opts...)

	model := cfg.Model
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	return &Anthropic{
		client: client,
		model:  model,
	}, nil
}

func (a *Anthropic) Name() string {
	return "anthropic"
}

func (a *Anthropic) Models() []string {
	return []string{
		"claude-sonnet-4-20250514",
		"claude-opus-4-20250514",
		"claude-3-5-sonnet-20241022",
		"claude-3-5-haiku-20241022",
	}
}

// Chat sends a non-streaming request.
func (a *Anthropic) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	messages := a.buildMessages(req.Messages)
	tools := a.buildTools(req.Tools)

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.F(anthropic.Model(a.model)),
		MaxTokens: anthropic.F(int64(maxTokens)),
		Messages:  anthropic.F(messages),
	}

	if req.System != "" {
		params.System = anthropic.F([]anthropic.TextBlockParam{
			anthropic.NewTextBlock(req.System),
		})
	}

	if len(tools) > 0 {
		params.Tools = anthropic.F(tools)
	}

	resp, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic chat failed: %w", err)
	}

	return a.parseResponse(resp), nil
}

// Stream sends a streaming request.
func (a *Anthropic) Stream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
	messages := a.buildMessages(req.Messages)
	tools := a.buildTools(req.Tools)

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.F(anthropic.Model(a.model)),
		MaxTokens: anthropic.F(int64(maxTokens)),
		Messages:  anthropic.F(messages),
	}

	if req.System != "" {
		params.System = anthropic.F([]anthropic.TextBlockParam{
			anthropic.NewTextBlock(req.System),
		})
	}

	if len(tools) > 0 {
		params.Tools = anthropic.F(tools)
	}

	stream := a.client.Messages.NewStreaming(ctx, params)

	events := make(chan StreamEvent, 100)

	go func() {
		defer close(events)
		defer stream.Close()

		var currentToolUse *ToolCall
		var toolInputBuffer string

		for stream.Next() {
			event := stream.Current()

			switch event.Type {
			case anthropic.MessageStreamEventTypeContentBlockStart:
				// ContentBlock is ContentBlockStartEventContentBlock with Type, ID, Name fields
				if cb, ok := event.ContentBlock.(anthropic.ContentBlockStartEventContentBlock); ok {
					if cb.Type == anthropic.ContentBlockStartEventContentBlockTypeToolUse {
						currentToolUse = &ToolCall{
							ID:   cb.ID,
							Name: cb.Name,
						}
						toolInputBuffer = ""
					}
				}

			case anthropic.MessageStreamEventTypeContentBlockDelta:
				// Handle ContentBlockDeltaEventDelta which has Type, Text, and PartialJSON fields
				if delta, ok := event.Delta.(anthropic.ContentBlockDeltaEventDelta); ok {
					if delta.Type == "text_delta" && delta.Text != "" {
						events <- StreamEvent{
							Type: "text",
							Text: delta.Text,
						}
					} else if delta.Type == "input_json_delta" && delta.PartialJSON != "" {
						toolInputBuffer += delta.PartialJSON
					}
				}

			case anthropic.MessageStreamEventTypeContentBlockStop:
				if currentToolUse != nil {
					currentToolUse.Input = json.RawMessage(toolInputBuffer)
					events <- StreamEvent{
						Type:    "tool_use",
						ToolUse: currentToolUse,
					}
					currentToolUse = nil
					toolInputBuffer = ""
				}

			case anthropic.MessageStreamEventTypeMessageStop:
				events <- StreamEvent{Type: "stop"}
			}
		}

		if err := stream.Err(); err != nil {
			events <- StreamEvent{
				Type:  "error",
				Error: err,
			}
		}
	}()

	return events, nil
}

func (a *Anthropic) buildMessages(msgs []Message) []anthropic.MessageParam {
	var result []anthropic.MessageParam

	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			if msg.ToolResult != nil {
				// This is a tool result
				result = append(result, anthropic.MessageParam{
					Role: anthropic.F(anthropic.MessageParamRoleUser),
					Content: anthropic.F([]anthropic.ContentBlockParamUnion{
						anthropic.ToolResultBlockParam{
							Type:      anthropic.F(anthropic.ToolResultBlockParamTypeToolResult),
							ToolUseID: anthropic.F(msg.ToolResult.ToolUseID),
							Content: anthropic.F([]anthropic.ToolResultBlockParamContentUnion{
								anthropic.TextBlockParam{
									Type: anthropic.F(anthropic.TextBlockParamTypeText),
									Text: anthropic.F(msg.ToolResult.Content),
								},
							}),
							IsError: anthropic.F(msg.ToolResult.IsError),
						},
					}),
				})
			} else {
				result = append(result, anthropic.MessageParam{
					Role: anthropic.F(anthropic.MessageParamRoleUser),
					Content: anthropic.F([]anthropic.ContentBlockParamUnion{
						anthropic.TextBlockParam{
							Type: anthropic.F(anthropic.TextBlockParamTypeText),
							Text: anthropic.F(msg.Content),
						},
					}),
				})
			}

		case "assistant":
			var content []anthropic.ContentBlockParamUnion

			if msg.Content != "" {
				content = append(content, anthropic.TextBlockParam{
					Type: anthropic.F(anthropic.TextBlockParamTypeText),
					Text: anthropic.F(msg.Content),
				})
			}

			for _, tc := range msg.ToolCalls {
				var input interface{}
				if len(tc.Input) > 0 {
					_ = json.Unmarshal(tc.Input, &input)
				}
				// Ensure input is never nil - Anthropic requires this field
				if input == nil {
					input = map[string]interface{}{}
				}
				content = append(content, anthropic.ToolUseBlockParam{
					Type:  anthropic.F(anthropic.ToolUseBlockParamTypeToolUse),
					ID:    anthropic.F(tc.ID),
					Name:  anthropic.F(tc.Name),
					Input: anthropic.F(input),
				})
			}

			if len(content) > 0 {
				result = append(result, anthropic.MessageParam{
					Role:    anthropic.F(anthropic.MessageParamRoleAssistant),
					Content: anthropic.F(content),
				})
			}
		}
	}

	return result
}

func (a *Anthropic) buildTools(tools []ToolDefinition) []anthropic.ToolUnionUnionParam {
	var result []anthropic.ToolUnionUnionParam

	for _, t := range tools {
		var schema map[string]interface{}
		if len(t.InputSchema) > 0 {
			_ = json.Unmarshal(t.InputSchema, &schema)
		}
		if schema == nil {
			schema = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		result = append(result, anthropic.ToolParam{
			Name:        anthropic.F(t.Name),
			Description: anthropic.F(t.Description),
			InputSchema: anthropic.F[interface{}](schema),
		})
	}

	return result
}

func (a *Anthropic) parseResponse(resp *anthropic.Message) *ChatResponse {
	var content []ContentBlock

	for _, block := range resp.Content {
		switch block.Type {
		case anthropic.ContentBlockTypeText:
			content = append(content, ContentBlock{
				Type: "text",
				Text: block.Text,
			})
		case anthropic.ContentBlockTypeToolUse:
			content = append(content, ContentBlock{
				Type: "tool_use",
				ToolUse: &ToolCall{
					ID:    block.ID,
					Name:  block.Name,
					Input: block.Input,
				},
			})
		}
	}

	return &ChatResponse{
		ID:         resp.ID,
		Content:    content,
		StopReason: string(resp.StopReason),
		Usage: Usage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
		},
	}
}
