// Package provider defines the LLM provider interface and types.
package provider

import (
	"context"
	"encoding/json"
)

// Provider is any LLM backend that can generate chat completions.
type Provider interface {
	// Chat sends a request and returns the complete response.
	Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)

	// Stream sends a request and returns a channel of streaming events.
	Stream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error)

	// Name returns the provider name (e.g., "anthropic", "openai").
	Name() string

	// Models returns the list of available model IDs.
	Models() []string
}

// ChatRequest represents a chat completion request.
type ChatRequest struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []ToolDefinition
	MaxTokens   int
	Temperature float64
}

// Message represents a single message in the conversation.
type Message struct {
	Role       string         // "user", "assistant"
	Content    string         // Text content
	ToolCalls  []ToolCall     // Tool calls made by assistant
	ToolResult *ToolResult    // Result of tool execution
}

// ContentBlock represents a block of content in a response.
type ContentBlock struct {
	Type    string          // "text", "tool_use"
	Text    string          // For text blocks
	ToolUse *ToolCall       // For tool_use blocks
}

// ToolDefinition defines a tool that the model can use.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolCall represents a tool invocation by the model.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult represents the result of executing a tool.
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
	IsError   bool   `json:"is_error,omitempty"`
}

// ChatResponse represents a complete chat response.
type ChatResponse struct {
	ID         string
	Content    []ContentBlock
	StopReason string
	Usage      Usage
}

// Usage tracks token usage.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// StreamEvent represents an event in a streaming response.
type StreamEvent struct {
	Type    string    // "text", "tool_use", "stop", "error"
	Text    string    // For text events
	ToolUse *ToolCall // For tool_use events
	Error   error     // For error events
}
