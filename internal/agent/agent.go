// Package agent implements the main agent loop.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/eachlabs/klaw/internal/channel"
	"github.com/eachlabs/klaw/internal/memory"
	"github.com/eachlabs/klaw/internal/observe"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/session"
	"github.com/eachlabs/klaw/internal/tool"
)

// Agent coordinates the conversation between user, LLM, and tools.
type Agent struct {
	provider       provider.Provider
	channel        channel.Channel
	tools          *tool.Registry
	memory         memory.Memory
	sessionManager *session.Manager

	systemPrompt  string
	history       []provider.Message            // Default history for single-conversation channels
	histories     map[string][]provider.Message  // Per-conversation histories (for multi-thread channels like Slack)
	maxTokens     int
	maxIterations int
	model         string
	contextMgr    *ContextManager
	costTracker   *CostTracker
	reflection    ReflectionConfig
	planner       PlannerConfig
	approval      ApprovalConfig
	logger        *observe.Logger
	metrics       *observe.Metrics
}

// Config holds agent configuration.
type Config struct {
	Provider       provider.Provider
	Channel        channel.Channel
	Tools          *tool.Registry
	Memory         memory.Memory
	SessionManager *session.Manager
	InitialHistory []provider.Message
	SystemPrompt   string
	MaxTokens      int
	MaxIterations  int
	Model          string
	Context        ContextConfig
	Cost           CostConfig
	Reflection     ReflectionConfig
	Planner        PlannerConfig
	Approval       ApprovalConfig
	Logger         *observe.Logger
	Metrics        *observe.Metrics
}

// New creates a new agent.
func New(cfg Config) *Agent {
	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}

	maxIterations := cfg.MaxIterations
	if maxIterations == 0 {
		maxIterations = 50
	}

	logger := cfg.Logger
	if logger == nil {
		logger = observe.Nop()
	}
	metrics := cfg.Metrics
	if metrics == nil {
		metrics = observe.NewMetrics()
	}

	// Use initial history if provided (for session resume)
	history := cfg.InitialHistory
	if history == nil {
		history = make([]provider.Message, 0)
	}

	return &Agent{
		provider:       cfg.Provider,
		channel:        cfg.Channel,
		tools:          cfg.Tools,
		memory:         cfg.Memory,
		sessionManager: cfg.SessionManager,
		systemPrompt:   cfg.SystemPrompt,
		history:        history,
		histories:      make(map[string][]provider.Message),
		maxTokens:      maxTokens,
		maxIterations:  maxIterations,
		model:          cfg.Model,
		contextMgr:     NewContextManager(cfg.Context),
		costTracker:    NewCostTracker(cfg.Cost),
		reflection:     cfg.Reflection,
		planner:        cfg.Planner,
		approval:       cfg.Approval,
		logger:         logger,
		metrics:        metrics,
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

	// Inject planning prompt on first message if enabled
	if a.planner.Enabled {
		history = InjectPlanRequest(history, a.planner.PlanPrompt)
		a.setHistory(conversationID, history)
	}

	toolCallsSinceReflection := 0

	// Keep processing until we get a final response (no tool calls)
	for iteration := 0; iteration < a.maxIterations; iteration++ {
		// Get latest history for this conversation
		history = a.getHistory(conversationID)

		// Check if context needs compaction
		if a.contextMgr.NeedsCompaction(history) {
			a.channel.Send(ctx, &channel.Message{
				Role:      "assistant",
				Content:   "Compacting context...\n",
				IsPartial: true,
			})
			compacted, err := a.contextMgr.Compact(ctx, a.provider, a.systemPrompt, history)
			if err == nil {
				history = compacted
				a.setHistory(conversationID, history)
			}
		}

		// Check budget before making a provider call
		if err := a.costTracker.CheckBudget(); err != nil {
			return err
		}

		req := &provider.ChatRequest{
			System:    a.systemPrompt,
			Messages:  history,
			Tools:     toolDefs,
			MaxTokens: a.maxTokens,
		}

		// Stream response
		events, err := a.provider.Stream(ctx, req)
		if err != nil {
			return &AgentError{Code: ErrProvider, Message: "API error", Cause: err}
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
				if event.Usage != nil {
					a.contextMgr.RecordUsage(*event.Usage)
					a.costTracker.Record(a.model, event.Usage.InputTokens, event.Usage.OutputTokens)
					a.metrics.RecordRequest("default", event.Usage.InputTokens, event.Usage.OutputTokens)
					a.logger.Debug("provider response",
						"model", a.model,
						"input_tokens", event.Usage.InputTokens,
						"output_tokens", event.Usage.OutputTokens,
						"cost", a.costTracker.Summary(),
					)
				}
				a.channel.Send(ctx, &channel.Message{
					Role:   "assistant",
					IsDone: true,
				})
			}
		}

		if streamErr != nil {
			return &AgentError{Code: ErrProvider, Message: "stream error", Cause: streamErr}
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
			// Update session with cost data and force save
			if a.sessionManager != nil {
				if sess := a.sessionManager.Session(); sess != nil {
					input, output, _ := a.contextMgr.Usage()
					sess.TotalInputTokens = input
					sess.TotalOutputTokens = output
					sess.TotalCost = a.costTracker.SessionCost()
				}
				_ = a.sessionManager.ForceSave()
			}
			return nil
		}

		// Phase 1: Approval — sequentially handle tools needing approval
		type toolState struct {
			tc       provider.ToolCall
			approved bool
			result   *tool.Result
		}
		states := make([]toolState, len(toolCalls))
		for i, tc := range toolCalls {
			states[i] = toolState{tc: tc, approved: true}

			// Show tool being called
			a.showToolStart(ctx, tc)

			if a.approval.NeedsApproval(tc.Name) {
				approved, err := RequestApproval(ctx, a.channel, tc)
				if err != nil {
					return &AgentError{Code: ErrToolExec, Message: "approval request failed", Cause: err}
				}
				if !approved {
					states[i].approved = false
					states[i].result = &tool.Result{Content: "Denied by user", IsError: true}
				}
			}
		}

		// Phase 2: Parallel execution of approved tools
		var wg sync.WaitGroup
		for i := range states {
			if !states[i].approved {
				continue
			}
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				toolStart := time.Now()
				states[idx].result = a.executeTool(ctx, states[idx].tc)
				toolDuration := time.Since(toolStart)
				a.metrics.RecordToolCall("default", states[idx].tc.Name)
				a.logger.Debug("tool executed",
					"tool", states[idx].tc.Name,
					"duration_ms", toolDuration.Milliseconds(),
					"is_error", states[idx].result.IsError,
				)
			}(i)
		}
		wg.Wait()

		// Phase 3: Collect results in original order
		for _, s := range states {
			a.showToolResult(ctx, s.result)
			history = append(history, provider.Message{
				Role: "user",
				ToolResult: &provider.ToolResult{
					ToolUseID: s.tc.ID,
					Content:   s.result.Content,
					IsError:   s.result.IsError,
				},
			})
			toolCallsSinceReflection++
		}
		a.setHistory(conversationID, history)

		// Inject reflection if enough tool calls have accumulated
		if ShouldReflect(a.reflection, toolCallsSinceReflection) {
			history = InjectReflection(history, a.reflection.Prompt)
			a.setHistory(conversationID, history)
			toolCallsSinceReflection = 0
		}

		// Continue loop to get next response after tool results
	}

	return &AgentError{
		Code:    ErrMaxIterations,
		Message: fmt.Sprintf("reached maximum iterations (%d)", a.maxIterations),
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
		// Sync to session manager if available
		if a.sessionManager != nil {
			a.sessionManager.SetMessages(history)
			_ = a.sessionManager.Save() // Debounced save
		}
		return
	}
	a.histories[conversationID] = history
}

func (a *Agent) showToolStart(ctx context.Context, tc provider.ToolCall) {
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
}

func (a *Agent) showToolResult(ctx context.Context, result *tool.Result) {
	if result.IsError {
		a.channel.Send(ctx, &channel.Message{
			Role:      "assistant",
			Content:   fmt.Sprintf("│ ERROR: %s\n╰─\n", truncate(result.Content, 500)),
			IsPartial: true,
		})
	} else {
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
	Provider      provider.Provider
	Tools         *tool.Registry
	SystemPrompt  string
	Prompt        string
	MaxTokens     int
	MaxIterations int
}

// RunOnce runs an agent with a single prompt and returns the result.
func RunOnce(ctx context.Context, cfg RunOnceConfig) (string, error) {
	maxTokens := cfg.MaxTokens
	if maxTokens == 0 {
		maxTokens = 8192
	}
	maxIterations := cfg.MaxIterations
	if maxIterations == 0 {
		maxIterations = 20
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

		// Add assistant message with tool calls preserved
		messages = append(messages, provider.Message{
			Role:      "assistant",
			Content:   textContent.String(),
			ToolCalls: toolCalls,
		})

		// If no tool calls, we're done
		if len(toolCalls) == 0 {
			result.WriteString(textContent.String())
			break
		}

		// Execute tools in parallel
		type toolExecResult struct {
			toolUseID string
			content   string
			isError   bool
		}
		results := make([]toolExecResult, len(toolCalls))

		var wg sync.WaitGroup
		for j, tc := range toolCalls {
			results[j].toolUseID = tc.ID
			wg.Add(1)
			go func(idx int, tc provider.ToolCall) {
				defer wg.Done()
				t, ok := cfg.Tools.Get(tc.Name)
				if !ok {
					results[idx].content = fmt.Sprintf("Tool not found: %s", tc.Name)
					results[idx].isError = true
					return
				}
				toolResult, err := t.Execute(ctx, tc.Input)
				if err != nil {
					results[idx].content = fmt.Sprintf("Error: %v", err)
					results[idx].isError = true
				} else {
					results[idx].content = toolResult.Content
					results[idx].isError = toolResult.IsError
				}
			}(j, tc)
		}
		wg.Wait()

		// Add each tool result as a separate message
		for _, r := range results {
			messages = append(messages, provider.Message{
				Role: "user",
				ToolResult: &provider.ToolResult{
					ToolUseID: r.toolUseID,
					Content:   r.content,
					IsError:   r.isError,
				},
			})
		}

		// Check stop reason
		if resp.StopReason == "end_turn" {
			result.WriteString(textContent.String())
			break
		}
	}

	return result.String(), nil
}
