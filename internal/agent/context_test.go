package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/eachlabs/klaw/internal/provider"
)

func TestEstimateTokens(t *testing.T) {
	cm := NewContextManager(ContextConfig{})

	t.Run("text messages", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: "Hello, world! This is a test message."},
			{Role: "assistant", Content: "Hi! How can I help you today?"},
		}
		tokens := cm.EstimateTokens(msgs)
		if tokens == 0 {
			t.Error("expected non-zero token estimate")
		}
		if tokens < 10 || tokens > 25 {
			t.Errorf("unexpected token estimate: %d", tokens)
		}
	})

	t.Run("empty messages", func(t *testing.T) {
		tokens := cm.EstimateTokens(nil)
		if tokens != 0 {
			t.Errorf("expected 0 for nil messages, got %d", tokens)
		}
	})

	t.Run("tool result messages", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", ToolResult: &provider.ToolResult{
				ToolUseID: "id1",
				Content:   "file contents here with lots of text to count",
			}},
		}
		tokens := cm.EstimateTokens(msgs)
		if tokens == 0 {
			t.Error("expected non-zero for tool result")
		}
	})

	t.Run("tool call messages", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "assistant", ToolCalls: []provider.ToolCall{
				{ID: "id1", Name: "bash", Input: json.RawMessage(`{"command": "ls -la /tmp/something"}`)},
			}},
		}
		tokens := cm.EstimateTokens(msgs)
		if tokens == 0 {
			t.Error("expected non-zero for tool calls")
		}
	})
}

func TestNeedsCompaction(t *testing.T) {
	t.Run("small conversation no compaction", func(t *testing.T) {
		cm := NewContextManager(ContextConfig{
			MaxContextTokens:    100,
			CompactionThreshold: 0.75,
			ReserveTokens:       10,
		})
		small := []provider.Message{
			{Role: "user", Content: "hi"},
		}
		if cm.NeedsCompaction(small) {
			t.Error("should not need compaction for small conversation")
		}
	})

	t.Run("large conversation needs compaction", func(t *testing.T) {
		cm := NewContextManager(ContextConfig{
			MaxContextTokens:    100,
			CompactionThreshold: 0.75,
			ReserveTokens:       10,
		})
		// Threshold = (100-10) * 0.75 = 67.5 tokens → ~270 chars
		big := []provider.Message{
			{Role: "user", Content: string(make([]byte, 300))},
		}
		if !cm.NeedsCompaction(big) {
			t.Error("should need compaction for large conversation")
		}
	})

	t.Run("exactly at threshold", func(t *testing.T) {
		cm := NewContextManager(ContextConfig{
			MaxContextTokens:    110,
			CompactionThreshold: 0.5,
			ReserveTokens:       10,
		})
		// threshold = (110-10) * 0.5 = 50 tokens → 200 chars
		msgs := []provider.Message{
			{Role: "user", Content: string(make([]byte, 200))},
		}
		// EstimateTokens = 200/4 = 50, threshold = 50 → not strictly greater
		if cm.NeedsCompaction(msgs) {
			t.Error("at threshold should not trigger compaction")
		}
	})
}

func TestRecordUsage(t *testing.T) {
	cm := NewContextManager(ContextConfig{})

	cm.RecordUsage(provider.Usage{InputTokens: 100, OutputTokens: 50})
	cm.RecordUsage(provider.Usage{InputTokens: 200, OutputTokens: 100})

	input, output, turns := cm.Usage()
	if input != 300 {
		t.Errorf("expected 300 input tokens, got %d", input)
	}
	if output != 150 {
		t.Errorf("expected 150 output tokens, got %d", output)
	}
	if turns != 2 {
		t.Errorf("expected 2 turns, got %d", turns)
	}
}

func TestContextManagerDefaults(t *testing.T) {
	cm := NewContextManager(ContextConfig{})
	if cm.config.MaxContextTokens != 200000 {
		t.Errorf("expected default 200000, got %d", cm.config.MaxContextTokens)
	}
	if cm.config.CompactionThreshold != 0.75 {
		t.Errorf("expected default 0.75, got %f", cm.config.CompactionThreshold)
	}
	if cm.config.ReserveTokens != 8192 {
		t.Errorf("expected default 8192, got %d", cm.config.ReserveTokens)
	}
}

func TestCompactTooShort(t *testing.T) {
	cm := NewContextManager(ContextConfig{})
	msgs := []provider.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	result, err := cm.Compact(context.Background(), nil, "", msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(msgs) {
		t.Error("should return original messages when too short")
	}
}

func TestCompactWithMockProvider(t *testing.T) {
	cm := NewContextManager(ContextConfig{})

	// Build a history of 12 messages (> 8)
	msgs := make([]provider.Message, 12)
	msgs[0] = provider.Message{Role: "user", Content: "initial question"}
	for i := 1; i < 12; i++ {
		if i%2 == 0 {
			msgs[i] = provider.Message{Role: "user", Content: "follow up"}
		} else {
			msgs[i] = provider.Message{Role: "assistant", Content: "response"}
		}
	}

	mock := &mockChatProvider{
		resp: &provider.ChatResponse{
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Summary of conversation."},
			},
		},
	}

	result, err := cm.Compact(context.Background(), mock, "sys prompt", msgs)
	if err != nil {
		t.Fatalf("compact failed: %v", err)
	}

	// Should have: first msg + summary msg + last 6 = 8
	if len(result) != 8 {
		t.Errorf("expected 8 messages after compaction, got %d", len(result))
	}

	// First message preserved
	if result[0].Content != "initial question" {
		t.Errorf("first message not preserved: %q", result[0].Content)
	}

	// Second message should be the summary
	if result[1].Role != "user" || !contains(result[1].Content, "Summary of conversation") {
		t.Errorf("expected summary message, got: %q", result[1].Content)
	}
}

func TestCompactWithToolResults(t *testing.T) {
	cm := NewContextManager(ContextConfig{})

	msgs := make([]provider.Message, 0, 12)
	msgs = append(msgs, provider.Message{Role: "user", Content: "do something"})
	// Add tool-related messages in the middle
	msgs = append(msgs, provider.Message{
		Role:      "assistant",
		Content:   "Let me run that",
		ToolCalls: []provider.ToolCall{{ID: "t1", Name: "bash", Input: json.RawMessage(`{}`)}},
	})
	msgs = append(msgs, provider.Message{
		Role:       "user",
		ToolResult: &provider.ToolResult{ToolUseID: "t1", Content: "output here"},
	})
	// Fill remaining
	for i := 0; i < 9; i++ {
		msgs = append(msgs, provider.Message{Role: "user", Content: "more"})
	}

	mock := &mockChatProvider{
		resp: &provider.ChatResponse{
			Content: []provider.ContentBlock{{Type: "text", Text: "summary"}},
		},
	}

	result, err := cm.Compact(context.Background(), mock, "", msgs)
	if err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if len(result) < 3 {
		t.Errorf("expected at least 3 messages, got %d", len(result))
	}
}

func TestTruncateForSummary(t *testing.T) {
	short := "short text"
	if truncateForSummary(short, 100) != short {
		t.Error("should not truncate short text")
	}

	long := string(make([]byte, 300))
	result := truncateForSummary(long, 100)
	if len(result) != 103 { // 100 + "..."
		t.Errorf("expected length 103, got %d", len(result))
	}
}

// mockChatProvider is a simple provider that returns fixed responses.
type mockChatProvider struct {
	resp    *provider.ChatResponse
	chatErr error
}

func (m *mockChatProvider) Name() string    { return "mock" }
func (m *mockChatProvider) Models() []string { return []string{"mock"} }
func (m *mockChatProvider) Chat(_ context.Context, _ *provider.ChatRequest) (*provider.ChatResponse, error) {
	if m.chatErr != nil {
		return nil, m.chatErr
	}
	return m.resp, nil
}
func (m *mockChatProvider) Stream(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 10)
	if m.chatErr != nil {
		ch <- provider.StreamEvent{Type: "error", Error: m.chatErr}
		close(ch)
		return ch, nil
	}
	for _, block := range m.resp.Content {
		if block.Type == "text" {
			ch <- provider.StreamEvent{Type: "text", Text: block.Text}
		}
		if block.ToolUse != nil {
			ch <- provider.StreamEvent{Type: "tool_use", ToolUse: block.ToolUse}
		}
	}
	ch <- provider.StreamEvent{Type: "stop", Usage: &provider.Usage{InputTokens: 100, OutputTokens: 50}}
	close(ch)
	return ch, nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
