package agent

import (
	"context"
	"fmt"

	"github.com/eachlabs/klaw/internal/provider"
)

// ContextConfig controls context window management.
type ContextConfig struct {
	MaxContextTokens    int     // default: 200000 (Claude)
	CompactionThreshold float64 // default: 0.75 — compact when estimated usage exceeds this ratio
	ReserveTokens       int     // default: 8192 — tokens to reserve for the response
}

// ContextManager tracks token usage and triggers compaction.
type ContextManager struct {
	config      ContextConfig
	totalInput  int
	totalOutput int
	turnCount   int
}

// NewContextManager creates a context manager with the given config.
func NewContextManager(cfg ContextConfig) *ContextManager {
	if cfg.MaxContextTokens == 0 {
		cfg.MaxContextTokens = 200000
	}
	if cfg.CompactionThreshold == 0 {
		cfg.CompactionThreshold = 0.75
	}
	if cfg.ReserveTokens == 0 {
		cfg.ReserveTokens = 8192
	}
	return &ContextManager{config: cfg}
}

// EstimateTokens returns a rough token count for a message list (~4 chars/token).
func (cm *ContextManager) EstimateTokens(msgs []provider.Message) int {
	total := 0
	for _, m := range msgs {
		total += len(m.Content) / 4
		if m.ToolResult != nil {
			total += len(m.ToolResult.Content) / 4
		}
		for _, tc := range m.ToolCalls {
			total += len(tc.Input) / 4
		}
	}
	return total
}

// RecordUsage updates cumulative token counts.
func (cm *ContextManager) RecordUsage(u provider.Usage) {
	cm.totalInput += u.InputTokens
	cm.totalOutput += u.OutputTokens
	cm.turnCount++
}

// NeedsCompaction returns true if the estimated context size exceeds the threshold.
func (cm *ContextManager) NeedsCompaction(msgs []provider.Message) bool {
	estimated := cm.EstimateTokens(msgs)
	threshold := int(float64(cm.config.MaxContextTokens-cm.config.ReserveTokens) * cm.config.CompactionThreshold)
	return estimated > threshold
}

// Compact summarizes the middle portion of history to reduce token usage.
// It keeps the first user message and the last 6 messages verbatim,
// then summarizes the middle section via the provider.
func (cm *ContextManager) Compact(ctx context.Context, prov provider.Provider, systemPrompt string, msgs []provider.Message) ([]provider.Message, error) {
	if len(msgs) <= 8 {
		return msgs, nil // too short to compact
	}

	keepStart := 1  // first user message
	keepEnd := 6    // last N messages

	middleStart := keepStart
	middleEnd := len(msgs) - keepEnd

	if middleEnd <= middleStart {
		return msgs, nil
	}

	// Build summary of middle section
	var summaryContent string
	for _, m := range msgs[middleStart:middleEnd] {
		switch {
		case m.ToolResult != nil:
			summaryContent += fmt.Sprintf("[tool result] %s\n", truncateForSummary(m.ToolResult.Content, 200))
		case len(m.ToolCalls) > 0:
			for _, tc := range m.ToolCalls {
				summaryContent += fmt.Sprintf("[tool call: %s]\n", tc.Name)
			}
			if m.Content != "" {
				summaryContent += m.Content + "\n"
			}
		default:
			summaryContent += fmt.Sprintf("[%s] %s\n", m.Role, truncateForSummary(m.Content, 300))
		}
	}

	// Ask the provider to create a concise summary
	summaryReq := &provider.ChatRequest{
		System: "You are a conversation summarizer. Create a concise summary of the conversation so far, preserving key decisions, tool results, and context needed to continue the conversation. Be brief but thorough.",
		Messages: []provider.Message{
			{Role: "user", Content: fmt.Sprintf("Summarize this conversation history:\n\n%s", summaryContent)},
		},
		MaxTokens: 2048,
	}

	resp, err := prov.Chat(ctx, summaryReq)
	if err != nil {
		return msgs, fmt.Errorf("compaction summary failed: %w", err)
	}

	var summaryText string
	for _, block := range resp.Content {
		if block.Type == "text" {
			summaryText += block.Text
		}
	}

	// Build compacted history
	compacted := make([]provider.Message, 0, 2+keepEnd)
	compacted = append(compacted, msgs[0]) // first user message
	compacted = append(compacted, provider.Message{
		Role:    "user",
		Content: fmt.Sprintf("[Previous conversation summary]\n%s", summaryText),
	})
	compacted = append(compacted, msgs[middleEnd:]...) // last N messages

	return compacted, nil
}

// Usage returns cumulative token usage stats.
func (cm *ContextManager) Usage() (input, output, turns int) {
	return cm.totalInput, cm.totalOutput, cm.turnCount
}

func truncateForSummary(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
