package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// OpenRouterProvider implements the Provider interface using OpenRouter.
type OpenRouterProvider struct {
	client *openai.Client
	model  string
}

// OpenRouterConfig holds configuration for the OpenRouter provider.
type OpenRouterConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// NewOpenRouter creates a new OpenRouter provider.
func NewOpenRouter(cfg OpenRouterConfig) (*OpenRouterProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY is required")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}

	client := openai.NewClient(
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(baseURL),
	)

	model := cfg.Model
	if model == "" {
		model = "anthropic/claude-sonnet-4" // Default model
	}

	return &OpenRouterProvider{
		client: &client,
		model:  model,
	}, nil
}

func (p *OpenRouterProvider) Name() string {
	return "openrouter"
}

func (p *OpenRouterProvider) Models() []string {
	return []string{
		"anthropic/claude-sonnet-4",
		"anthropic/claude-opus-4",
		"anthropic/claude-3.5-sonnet",
		"openai/gpt-4o",
		"openai/gpt-4o-mini",
		"google/gemini-2.0-flash-exp",
		"deepseek/deepseek-chat",
		"meta-llama/llama-3.3-70b-instruct",
	}
}

// Chat sends a non-streaming request.
func (p *OpenRouterProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Debug: log the request messages
	fmt.Printf("[openrouter] Request has %d messages\n", len(req.Messages))
	for i, msg := range req.Messages {
		if msg.ToolResult != nil {
			fmt.Printf("[openrouter] Message %d: role=%s, tool_result_id=%q (first 50 chars: %q)\n",
				i, msg.Role, msg.ToolResult.ToolUseID, truncateStr(msg.ToolResult.ToolUseID, 50))
		} else if len(msg.ToolCalls) > 0 {
			fmt.Printf("[openrouter] Message %d: role=%s, %d tool_calls\n", i, msg.Role, len(msg.ToolCalls))
			for j, tc := range msg.ToolCalls {
				fmt.Printf("[openrouter]   Tool call %d: id=%q, name=%s\n", j, tc.ID, tc.Name)
			}
		} else {
			fmt.Printf("[openrouter] Message %d: role=%s, content=%d chars\n", i, msg.Role, len(msg.Content))
		}
	}

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
		return nil, fmt.Errorf("openrouter chat failed: %w", err)
	}

	return p.parseResponse(resp), nil
}

// Stream sends a request and emits events.
func (p *OpenRouterProvider) Stream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
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

func (p *OpenRouterProvider) buildMessages(req *ChatRequest) []openai.ChatCompletionMessageParamUnion {
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
				// Build tool calls array for OpenAI format
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
				// Create assistant message with tool calls
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

func (p *OpenRouterProvider) buildTools(tools []ToolDefinition) []openai.ChatCompletionToolParam {
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

func (p *OpenRouterProvider) parseResponse(resp *openai.ChatCompletion) *ChatResponse {
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
