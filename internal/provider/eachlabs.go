package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// EachLabsProvider implements the Provider interface using each::labs LLM Router.
// It supports 300+ models through a single API endpoint using OpenAI SDK protocol.
type EachLabsProvider struct {
	client *openai.Client
	model  string
}

// EachLabsConfig holds configuration for the each::labs provider.
type EachLabsConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// NewEachLabs creates a new each::labs provider.
func NewEachLabs(cfg EachLabsConfig) (*EachLabsProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("EACHLABS_API_KEY is required")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.eachlabs.ai/v1"
	}

	client := openai.NewClient(
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(baseURL),
	)

	model := cfg.Model
	if model == "" {
		model = "anthropic/claude-sonnet-4-5" // Default model
	}

	return &EachLabsProvider{
		client: &client,
		model:  model,
	}, nil
}

func (e *EachLabsProvider) Name() string {
	return "eachlabs"
}

func (e *EachLabsProvider) Models() []string {
	return []string{
		// Anthropic Claude
		"anthropic/claude-sonnet-4",
		"anthropic/claude-sonnet-4-5",
		"anthropic/claude-opus-4",
		"anthropic/claude-opus-4-5",
		"anthropic/claude-opus-4-6",
		"anthropic/claude-haiku-4-5",
		"anthropic/claude-3-5-haiku",
		"anthropic/claude-3-7-sonnet",
		// OpenAI
		"openai/gpt-4o",
		"openai/gpt-4o-mini",
		"openai/gpt-5",
		"openai/gpt-5-mini",
		"openai/gpt-5-nano",
		"openai/o3",
		"openai/o3-mini",
		"openai/o4-mini",
		"openai/o1",
		// Google Vertex
		"vertex/gemini-2.5-pro",
		"vertex/gemini-2.5-flash",
		"vertex/gemini-3-pro-preview",
		// DeepSeek
		"deepseek/deepseek-chat",
		"deepseek/deepseek-reasoner",
		// xAI
		"xai/grok-3",
		"xai/grok-4",
		// Mistral
		"mistral/mistral-large-latest",
		"mistral/codestral-latest",
		// Coding optimized
		"coding/claude-sonnet-4-20250514",
		"coding/gemini-2.5-pro",
		// And 300+ more via router
	}
}

// Chat sends a non-streaming request.
func (e *EachLabsProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	messages := e.buildMessages(req)
	tools := e.buildTools(req.Tools)

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	params := openai.ChatCompletionNewParams{
		Model:    e.model,
		Messages: messages,
	}
	params.MaxTokens = openai.Int(int64(maxTokens))

	if len(tools) > 0 {
		params.Tools = tools
	}

	resp, err := e.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("eachlabs chat failed: %w", err)
	}

	return e.parseResponse(resp), nil
}

// Stream sends a request and emits events (uses non-streaming API internally).
func (e *EachLabsProvider) Stream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
	events := make(chan StreamEvent, 100)

	go func() {
		defer close(events)

		// Use non-streaming chat
		resp, err := e.Chat(ctx, req)
		if err != nil {
			events <- StreamEvent{
				Type:  "error",
				Error: err,
			}
			return
		}

		// Emit events from response
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

func (e *EachLabsProvider) buildMessages(req *ChatRequest) []openai.ChatCompletionMessageParamUnion {
	var messages []openai.ChatCompletionMessageParamUnion

	// Add system message
	if req.System != "" {
		messages = append(messages, openai.SystemMessage(req.System))
	}

	// Add conversation messages
	for _, msg := range req.Messages {
		switch msg.Role {
		case "user":
			if msg.ToolResult != nil {
				// Tool result message
				messages = append(messages, openai.ToolMessage(msg.ToolResult.ToolUseID, msg.ToolResult.Content))
			} else {
				messages = append(messages, openai.UserMessage(msg.Content))
			}
		case "assistant":
			if len(msg.ToolCalls) > 0 {
				// Assistant message with tool calls - for now just add the text
				// TODO: properly handle tool calls in assistant messages
				messages = append(messages, openai.AssistantMessage(msg.Content))
			} else {
				messages = append(messages, openai.AssistantMessage(msg.Content))
			}
		}
	}

	return messages
}

func (e *EachLabsProvider) buildTools(tools []ToolDefinition) []openai.ChatCompletionToolParam {
	var result []openai.ChatCompletionToolParam

	for _, t := range tools {
		var schema openai.FunctionParameters
		json.Unmarshal(t.InputSchema, &schema)

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

func (e *EachLabsProvider) parseResponse(resp *openai.ChatCompletion) *ChatResponse {
	result := &ChatResponse{
		StopReason: "end_turn",
	}

	if len(resp.Choices) == 0 {
		return result
	}

	choice := resp.Choices[0]
	msg := choice.Message

	// Add text content
	if msg.Content != "" {
		result.Content = append(result.Content, ContentBlock{
			Type: "text",
			Text: msg.Content,
		})
	}

	// Add tool calls
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
