package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// OpenAICompatProvider implements the Provider interface for any OpenAI-compatible API.
// This works with Ollama, LM Studio, vLLM, GLM, Minimax, Together AI, and any other
// service that exposes an OpenAI-compatible /v1/chat/completions endpoint.
type OpenAICompatProvider struct {
	client *openai.Client
	model  string
	name   string
}

// OpenAICompatConfig holds configuration for a generic OpenAI-compatible provider.
type OpenAICompatConfig struct {
	Name    string // provider name for display (e.g., "ollama", "openai")
	APIKey  string // optional — some local providers don't require auth
	BaseURL string // required — e.g., "http://localhost:11434/v1"
	Model   string // required — e.g., "llama3.2", "gpt-4o"
}

// NewOpenAICompat creates a new OpenAI-compatible provider.
func NewOpenAICompat(cfg OpenAICompatConfig) (*OpenAICompatProvider, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("base_url is required for provider %q", cfg.Name)
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("model is required for provider %q", cfg.Name)
	}

	name := cfg.Name
	if name == "" {
		name = "openai-compatible"
	}

	opts := []option.RequestOption{
		option.WithBaseURL(cfg.BaseURL),
	}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	} else {
		// Some local providers (Ollama, LM Studio) don't need an API key
		opts = append(opts, option.WithAPIKey("not-needed"))
	}

	client := openai.NewClient(opts...)

	return &OpenAICompatProvider{
		client: &client,
		model:  cfg.Model,
		name:   name,
	}, nil
}

func (p *OpenAICompatProvider) Name() string {
	return p.name
}

func (p *OpenAICompatProvider) Models() []string {
	return []string{p.model}
}

// Chat sends a non-streaming request.
func (p *OpenAICompatProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	messages := p.buildMessages(req)
	tools := p.buildTools(req.Tools)

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	params := openai.ChatCompletionNewParams{
		Model:    p.model,
		Messages: messages,
	}
	params.MaxTokens = openai.Int(int64(maxTokens))

	if len(tools) > 0 {
		params.Tools = tools
	}

	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("%s chat failed: %w", p.name, err)
	}

	return p.parseResponse(resp), nil
}

// Stream sends a request and emits events.
func (p *OpenAICompatProvider) Stream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
	events := make(chan StreamEvent, 100)

	go func() {
		defer close(events)

		resp, err := p.Chat(ctx, req)
		if err != nil {
			events <- StreamEvent{
				Type:  "error",
				Error: err,
			}
			return
		}

		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				events <- StreamEvent{
					Type: "text",
					Text: block.Text,
				}
			case "tool_use":
				events <- StreamEvent{
					Type:    "tool_use",
					ToolUse: block.ToolUse,
				}
			}
		}

		events <- StreamEvent{Type: "stop"}
	}()

	return events, nil
}

func (p *OpenAICompatProvider) buildMessages(req *ChatRequest) []openai.ChatCompletionMessageParamUnion {
	var messages []openai.ChatCompletionMessageParamUnion

	if req.System != "" {
		messages = append(messages, openai.SystemMessage(req.System))
	}

	for _, msg := range req.Messages {
		switch msg.Role {
		case "user":
			if msg.ToolResult != nil {
				messages = append(messages, openai.ToolMessage(msg.ToolResult.ToolUseID, msg.ToolResult.Content))
			} else {
				messages = append(messages, openai.UserMessage(msg.Content))
			}
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				toolCalls := make([]openai.ChatCompletionMessageToolCallParam, len(msg.ToolCalls))
				for i, tc := range msg.ToolCalls {
					toolCalls[i] = openai.ChatCompletionMessageToolCallParam{
						ID: tc.ID,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: string(tc.Input),
						},
					}
				}
				assistant := openai.ChatCompletionAssistantMessageParam{
					ToolCalls: toolCalls,
				}
				assistant.Content.OfString = openai.String(msg.Content)
				messages = append(messages, openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant})
			} else {
				messages = append(messages, openai.AssistantMessage(msg.Content))
			}
		}
	}

	return messages
}

func (p *OpenAICompatProvider) buildTools(tools []ToolDefinition) []openai.ChatCompletionToolParam {
	var result []openai.ChatCompletionToolParam

	for _, t := range tools {
		var schema openai.FunctionParameters
		_ = json.Unmarshal(t.InputSchema, &schema)

		param := openai.ChatCompletionToolParam{
			Type: "function",
			Function: openai.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  schema,
			},
		}
		result = append(result, param)
	}

	return result
}

func (p *OpenAICompatProvider) parseResponse(resp *openai.ChatCompletion) *ChatResponse {
	result := &ChatResponse{
		StopReason: "end_turn",
	}

	if len(resp.Choices) == 0 {
		return result
	}

	choice := resp.Choices[0]
	msg := choice.Message

	if msg.Content != "" {
		result.Content = append(result.Content, ContentBlock{
			Type: "text",
			Text: msg.Content,
		})
	}

	for _, tc := range msg.ToolCalls {
		result.Content = append(result.Content, ContentBlock{
			Type: "tool_use",
			ToolUse: &ToolCall{
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			},
		})
	}

	if choice.FinishReason == "tool_calls" {
		result.StopReason = "tool_use"
	}

	return result
}
