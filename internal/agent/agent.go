// Package agent implements the main agent loop.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/eachlabs/klaw/internal/channel"
	"github.com/eachlabs/klaw/internal/memory"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/tool"
)

// Agent coordinates the conversation between user, LLM, and tools.
type Agent struct {
	provider provider.Provider
	channel  channel.Channel
	tools    *tool.Registry
	memory   memory.Memory

	systemPrompt string
	history      []provider.Message                // Default history for single-conversation channels
	histories    map[string][]provider.Message     // Per-conversation histories (for multi-thread channels like Slack)
	maxTokens    int
}

// Config holds agent configuration.
type Config struct {
	Provider     provider.Provider
	Channel      channel.Channel
	Tools        *tool.Registry
	Memory       memory.Memory
	SystemPrompt string
	MaxTokens    int
}

// New creates a new agent.
func New(cfg Config) *Agent {
	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	return &Agent{
		provider:     cfg.Provider,
		channel:      cfg.Channel,
		tools:        cfg.Tools,
		memory:       cfg.Memory,
		systemPrompt: cfg.SystemPrompt,
		history:      make([]provider.Message, 0),
		histories:    make(map[string][]provider.Message),
		maxTokens:    maxTokens,
	}
}

// Run starts the agent loop.
func (a *Agent) Run(ctx context.Context) error {
	// Start receiving messages
	if err := a.channel.Start(ctx); err != nil {
		return fmt.Errorf("failed to start channel: %w", err)
	}

	// Print welcome
	a.channel.Send(ctx, &channel.Message{
		Role:    "assistant",
		Content: "klaw ready. Type /help for commands, /exit to quit.\n",
	})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-a.channel.Receive():
			if !ok {
				return nil
			}
			if err := a.handleMessage(ctx, msg); err != nil {
				// Send error to channel so user sees it
				a.channel.Send(ctx, &channel.Message{
					Role:    "error",
					Content: err.Error(),
				})
			}
		}
	}
}

func (a *Agent) handleMessage(ctx context.Context, msg *channel.Message) error {
	// Get conversation ID from metadata (for per-thread history)
	conversationID := a.getConversationID(msg)

	// Get or create history for this conversation
	history := a.getHistory(conversationID)

	// Build message content with context
	content := msg.Content
	if msg.Metadata != nil {
		// Add context info so LLM knows the current channel
		if channelID, ok := msg.Metadata["channel"].(string); ok && channelID != "" {
			content = fmt.Sprintf("[Context: channel=%s]\n\n%s", channelID, content)
		}
	}

	// Add user message to history
	history = append(history, provider.Message{
		Role:    "user",
		Content: content,
	})
	a.setHistory(conversationID, history)

	// Build tool definitions
	toolDefs := a.buildToolDefinitions()

	// Keep processing until we get a final response (no tool calls)
	for {
		// Get latest history for this conversation
		history = a.getHistory(conversationID)

		req := &provider.ChatRequest{
			System:    a.systemPrompt,
			Messages:  history,
			Tools:     toolDefs,
			MaxTokens: a.maxTokens,
		}

		// Stream response
		events, err := a.provider.Stream(ctx, req)
		if err != nil {
			return fmt.Errorf("API error: %w", err)
		}

		// Collect response
		var textContent strings.Builder
		var toolCalls []provider.ToolCall
		var streamErr error

		for event := range events {
			switch event.Type {
			case "text":
				textContent.WriteString(event.Text)
				a.channel.Send(ctx, &channel.Message{
					Role:      "assistant",
					Content:   event.Text,
					IsPartial: true,
				})

			case "tool_use":
				toolCalls = append(toolCalls, *event.ToolUse)

			case "error":
				streamErr = event.Error

			case "stop":
				a.channel.Send(ctx, &channel.Message{
					Role:   "assistant",
					IsDone: true,
				})
			}
		}

		if streamErr != nil {
			return fmt.Errorf("stream error: %w", streamErr)
		}

		// Add assistant response to history
		assistantMsg := provider.Message{
			Role:      "assistant",
			Content:   textContent.String(),
			ToolCalls: toolCalls,
		}
		history = append(history, assistantMsg)
		a.setHistory(conversationID, history)

		// If no tool calls, we're done
		if len(toolCalls) == 0 {
			return nil
		}

		// Execute tools and add results
		for _, tc := range toolCalls {
			// Show tool being called with input preview
			toolDesc := tc.Name
			if tc.Name == "bash" {
				var params struct{ Command string `json:"command"` }
				_ = json.Unmarshal(tc.Input, &params)
				if params.Command != "" {
					toolDesc = fmt.Sprintf("bash: %s", truncate(params.Command, 60))
				}
			}
			a.channel.Send(ctx, &channel.Message{
				Role:      "assistant",
				Content:   fmt.Sprintf("\n╭─ %s\n", toolDesc),
				IsPartial: true,
			})

			result := a.executeTool(ctx, tc)

			// Show tool result
			if result.IsError {
				a.channel.Send(ctx, &channel.Message{
					Role:      "assistant",
					Content:   fmt.Sprintf("│ ERROR: %s\n╰─\n", truncate(result.Content, 500)),
					IsPartial: true,
				})
			} else {
				// Format output with box drawing
				lines := strings.Split(result.Content, "\n")
				var output strings.Builder
				for i, line := range lines {
					if i >= 20 {
						output.WriteString(fmt.Sprintf("│ ... (%d more lines)\n", len(lines)-20))
						break
					}
					output.WriteString(fmt.Sprintf("│ %s\n", line))
				}
				output.WriteString("╰─\n")
				a.channel.Send(ctx, &channel.Message{
					Role:      "assistant",
					Content:   output.String(),
					IsPartial: true,
				})
			}

			// Add tool result to history
			history = append(history, provider.Message{
				Role: "user",
				ToolResult: &provider.ToolResult{
					ToolUseID: tc.ID,
					Content:   result.Content,
					IsError:   result.IsError,
				},
			})
			a.setHistory(conversationID, history)
		}

		// Continue loop to get next response after tool results
	}
}

// getConversationID extracts a unique conversation identifier from message metadata
func (a *Agent) getConversationID(msg *channel.Message) string {
	if msg.Metadata == nil {
		return "default"
	}

	// For Slack: use channel:thread_ts as conversation ID
	channelID, _ := msg.Metadata["channel"].(string)
	threadTS, _ := msg.Metadata["thread_ts"].(string)

	if channelID != "" && threadTS != "" {
		return fmt.Sprintf("%s:%s", channelID, threadTS)
	}
	if channelID != "" {
		return channelID
	}

	return "default"
}

// getHistory returns the history for a specific conversation
func (a *Agent) getHistory(conversationID string) []provider.Message {
	if conversationID == "default" {
		return a.history
	}
	if history, ok := a.histories[conversationID]; ok {
		return history
	}
	return make([]provider.Message, 0)
}

// setHistory sets the history for a specific conversation
func (a *Agent) setHistory(conversationID string, history []provider.Message) {
	if conversationID == "default" {
		a.history = history
		return
	}
	a.histories[conversationID] = history
}

func (a *Agent) executeTool(ctx context.Context, tc provider.ToolCall) *tool.Result {
	t, ok := a.tools.Get(tc.Name)
	if !ok {
		return &tool.Result{
			Content: fmt.Sprintf("unknown tool: %s", tc.Name),
			IsError: true,
		}
	}

	// Execute with timeout
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	result, err := t.Execute(ctx, tc.Input)
	if err != nil {
		return &tool.Result{
			Content: fmt.Sprintf("tool execution failed: %v", err),
			IsError: true,
		}
	}

	return result
}

func (a *Agent) buildToolDefinitions() []provider.ToolDefinition {
	tools := a.tools.All()
	defs := make([]provider.ToolDefinition, len(tools))

	for i, t := range tools {
		defs[i] = provider.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
		}
	}

	return defs
}

// RunOnce processes a single message from the channel and returns.
func (a *Agent) RunOnce(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case msg, ok := <-a.channel.Receive():
		if !ok {
			return nil
		}
		return a.handleMessage(ctx, msg)
	}
}

// ClearHistory clears the conversation history.
func (a *Agent) ClearHistory() {
	a.history = make([]provider.Message, 0)
}

// History returns the current conversation history.
func (a *Agent) History() []provider.Message {
	return a.history
}

// HistoryJSON returns the conversation history as JSON.
func (a *Agent) HistoryJSON() ([]byte, error) {
	return json.MarshalIndent(a.history, "", "  ")
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

// RunOnceConfig holds configuration for a single agent run.
type RunOnceConfig struct {
	Provider     provider.Provider
	Tools        *tool.Registry
	SystemPrompt string
	Prompt       string
	MaxTokens    int
}

// RunOnce runs an agent with a single prompt and returns the result.
func RunOnce(ctx context.Context, cfg RunOnceConfig) (string, error) {
	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	// Build tool definitions
	tools := cfg.Tools.All()
	toolDefs := make([]provider.ToolDefinition, len(tools))
	for i, t := range tools {
		toolDefs[i] = provider.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
		}
	}

	// Build messages
	messages := []provider.Message{
		{Role: "user", Content: cfg.Prompt},
	}

	var result strings.Builder
	maxIterations := 20

	for i := 0; i < maxIterations; i++ {
		// Call provider
		resp, err := cfg.Provider.Chat(ctx, &provider.ChatRequest{
			System:    cfg.SystemPrompt,
			Messages:  messages,
			Tools:     toolDefs,
			MaxTokens: maxTokens,
		})
		if err != nil {
			return "", fmt.Errorf("chat failed: %w", err)
		}

		// Process response
		var textContent strings.Builder
		var toolCalls []provider.ToolCall

		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				textContent.WriteString(block.Text)
			case "tool_use":
				if block.ToolUse != nil {
					toolCalls = append(toolCalls, *block.ToolUse)
				}
			}
		}

		// Add assistant message
		messages = append(messages, provider.Message{
			Role:    "assistant",
			Content: textContent.String(),
		})

		// If no tool calls, we're done
		if len(toolCalls) == 0 {
			result.WriteString(textContent.String())
			break
		}

		// Execute tools
		var toolResults []provider.ToolResult
		for _, tc := range toolCalls {
			t, ok := cfg.Tools.Get(tc.Name)
			if !ok {
				toolResults = append(toolResults, provider.ToolResult{
					ToolUseID: tc.ID,
					Content:   fmt.Sprintf("Tool not found: %s", tc.Name),
					IsError:   true,
				})
				continue
			}

			toolResult, err := t.Execute(ctx, tc.Input)
			if err != nil {
				toolResults = append(toolResults, provider.ToolResult{
					ToolUseID: tc.ID,
					Content:   fmt.Sprintf("Error: %v", err),
					IsError:   true,
				})
			} else {
				toolResults = append(toolResults, provider.ToolResult{
					ToolUseID: tc.ID,
					Content:   toolResult.Content,
					IsError:   toolResult.IsError,
				})
			}
		}

		// Add tool results to messages
		var toolResultContent strings.Builder
		for _, tr := range toolResults {
			toolResultContent.WriteString(fmt.Sprintf("[Tool Result: %s]\n%s\n", tr.ToolUseID, tr.Content))
		}
		messages = append(messages, provider.Message{
			Role:    "user",
			Content: toolResultContent.String(),
		})

		// Check stop reason
		if resp.StopReason == "end_turn" {
			result.WriteString(textContent.String())
			break
		}
	}

	return result.String(), nil
}
