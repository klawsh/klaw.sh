package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/eachlabs/klaw/internal/channel"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/tool"
)

// ─── Errors ────────────────────────────────────────────────────────────

func TestAgentError_Error(t *testing.T) {
	t.Run("without cause", func(t *testing.T) {
		err := &AgentError{Code: ErrMaxIterations, Message: "hit limit"}
		got := err.Error()
		if got != "[max_iterations] hit limit" {
			t.Errorf("unexpected: %q", got)
		}
	})

	t.Run("with cause", func(t *testing.T) {
		cause := fmt.Errorf("upstream broke")
		err := &AgentError{Code: ErrProvider, Message: "API error", Cause: cause}
		got := err.Error()
		if !strings.Contains(got, "provider_error") || !strings.Contains(got, "upstream broke") {
			t.Errorf("unexpected: %q", got)
		}
	})
}

func TestAgentError_Unwrap(t *testing.T) {
	cause := fmt.Errorf("root cause")
	err := &AgentError{Code: ErrToolExec, Message: "tool failed", Cause: cause}

	unwrapped := errors.Unwrap(err)
	if unwrapped != cause {
		t.Errorf("Unwrap returned %v, want %v", unwrapped, cause)
	}

	noCause := &AgentError{Code: ErrContextLimit, Message: "no cause"}
	if errors.Unwrap(noCause) != nil {
		t.Error("Unwrap should return nil when no cause")
	}
}

func TestAgentError_Is(t *testing.T) {
	cause := fmt.Errorf("underlying")
	err := &AgentError{Code: ErrBudgetExceed, Message: "over budget", Cause: cause}

	if !errors.Is(err, cause) {
		t.Error("errors.Is should find wrapped cause")
	}
}

// ─── Cost Tracker ──────────────────────────────────────────────────────

func TestCostTracker_Record(t *testing.T) {
	ct := NewCostTracker(CostConfig{})

	t.Run("known model", func(t *testing.T) {
		cost := ct.Record("claude-sonnet-4-20250514", 1_000_000, 100_000)
		// 1M * 3.0/1M + 100k * 15.0/1M = 3.0 + 1.5 = 4.5
		if cost < 4.4 || cost > 4.6 {
			t.Errorf("expected ~4.5, got %f", cost)
		}
	})

	t.Run("unknown model uses default pricing", func(t *testing.T) {
		ct2 := NewCostTracker(CostConfig{})
		cost := ct2.Record("unknown-model-xyz", 1_000_000, 0)
		// Should use conservative 3.0/M
		if cost < 2.9 || cost > 3.1 {
			t.Errorf("expected ~3.0 for unknown model, got %f", cost)
		}
	})

	t.Run("accumulates across calls", func(t *testing.T) {
		ct2 := NewCostTracker(CostConfig{})
		ct2.Record("claude-sonnet-4-20250514", 500_000, 50_000)
		ct2.Record("claude-sonnet-4-20250514", 500_000, 50_000)
		total := ct2.SessionCost()
		expected := 2 * (500_000.0/1_000_000*3.0 + 50_000.0/1_000_000*15.0)
		if total < expected-0.01 || total > expected+0.01 {
			t.Errorf("expected %f, got %f", expected, total)
		}
	})
}

func TestCostTracker_CheckBudget(t *testing.T) {
	t.Run("unlimited budget", func(t *testing.T) {
		ct := NewCostTracker(CostConfig{MaxSessionCost: 0})
		ct.Record("claude-sonnet-4-20250514", 10_000_000, 10_000_000)
		if err := ct.CheckBudget(); err != nil {
			t.Errorf("unlimited budget should not error: %v", err)
		}
	})

	t.Run("under budget", func(t *testing.T) {
		ct := NewCostTracker(CostConfig{MaxSessionCost: 10.0})
		ct.Record("claude-sonnet-4-20250514", 100_000, 10_000)
		if err := ct.CheckBudget(); err != nil {
			t.Errorf("under budget should not error: %v", err)
		}
	})

	t.Run("over budget", func(t *testing.T) {
		ct := NewCostTracker(CostConfig{MaxSessionCost: 0.01})
		ct.Record("claude-sonnet-4-20250514", 1_000_000, 1_000_000)
		err := ct.CheckBudget()
		if err == nil {
			t.Fatal("expected budget error")
		}
		var agentErr *AgentError
		if !errors.As(err, &agentErr) {
			t.Fatal("expected AgentError type")
		}
		if agentErr.Code != ErrBudgetExceed {
			t.Errorf("expected ErrBudgetExceed, got %s", agentErr.Code)
		}
	})
}

func TestCostTracker_IsNearBudget(t *testing.T) {
	t.Run("no budget", func(t *testing.T) {
		ct := NewCostTracker(CostConfig{})
		if ct.IsNearBudget() {
			t.Error("should be false with no budget")
		}
	})

	t.Run("below threshold", func(t *testing.T) {
		ct := NewCostTracker(CostConfig{MaxSessionCost: 10.0, WarnThreshold: 0.8})
		ct.Record("claude-sonnet-4-20250514", 100_000, 10_000) // tiny cost
		if ct.IsNearBudget() {
			t.Error("should not be near budget")
		}
	})

	t.Run("above threshold", func(t *testing.T) {
		ct := NewCostTracker(CostConfig{MaxSessionCost: 1.0, WarnThreshold: 0.5})
		ct.Record("claude-sonnet-4-20250514", 1_000_000, 100_000) // ~4.5 → way over 0.5
		if !ct.IsNearBudget() {
			t.Error("should be near budget")
		}
	})
}

func TestCostTracker_Summary(t *testing.T) {
	ct := NewCostTracker(CostConfig{})
	ct.Record("claude-sonnet-4-20250514", 4200, 1100)
	s := ct.Summary()
	if !strings.Contains(s, "$") || !strings.Contains(s, "in") || !strings.Contains(s, "out") {
		t.Errorf("unexpected summary format: %q", s)
	}
}

func TestDefaultCostTable(t *testing.T) {
	table := DefaultCostTable()
	if len(table) == 0 {
		t.Fatal("cost table is empty")
	}

	// Verify a few known models
	models := []string{
		"claude-sonnet-4-20250514",
		"claude-opus-4-20250514",
		"anthropic/claude-sonnet-4",
		"openai/gpt-4o",
	}
	for _, m := range models {
		if _, ok := table[m]; !ok {
			t.Errorf("missing model %q in cost table", m)
		}
	}
}

// ─── Reflection ────────────────────────────────────────────────────────

func TestShouldReflect(t *testing.T) {
	tests := []struct {
		name     string
		cfg      ReflectionConfig
		calls    int
		expected bool
	}{
		{"disabled", ReflectionConfig{Enabled: false}, 100, false},
		{"below threshold", ReflectionConfig{Enabled: true, ReflectAfterTools: 5}, 3, false},
		{"at threshold", ReflectionConfig{Enabled: true, ReflectAfterTools: 3}, 3, true},
		{"above threshold", ReflectionConfig{Enabled: true, ReflectAfterTools: 3}, 5, true},
		{"default threshold", ReflectionConfig{Enabled: true}, 3, true},
		{"default threshold below", ReflectionConfig{Enabled: true}, 2, false},
		{"zero threshold uses default 3", ReflectionConfig{Enabled: true, ReflectAfterTools: 0}, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldReflect(tt.cfg, tt.calls)
			if got != tt.expected {
				t.Errorf("ShouldReflect() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestInjectReflection(t *testing.T) {
	t.Run("default prompt", func(t *testing.T) {
		history := []provider.Message{
			{Role: "user", Content: "hello"},
		}
		result := InjectReflection(history, "")
		if len(result) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(result))
		}
		if result[1].Role != "user" {
			t.Error("reflection should be user role")
		}
		if !strings.Contains(result[1].Content, "reflect") {
			t.Error("should contain default reflection prompt")
		}
	})

	t.Run("custom prompt", func(t *testing.T) {
		history := []provider.Message{
			{Role: "user", Content: "do stuff"},
		}
		result := InjectReflection(history, "Think about what you did.")
		if result[1].Content != "Think about what you did." {
			t.Errorf("unexpected content: %q", result[1].Content)
		}
	})

	t.Run("does not modify original", func(t *testing.T) {
		original := []provider.Message{
			{Role: "user", Content: "test"},
		}
		_ = InjectReflection(original, "reflect")
		if len(original) != 1 {
			t.Error("original slice should not be modified in length")
		}
	})
}

// ─── Planner ───────────────────────────────────────────────────────────

func TestInjectPlanRequest(t *testing.T) {
	t.Run("empty messages", func(t *testing.T) {
		result := InjectPlanRequest(nil, "plan")
		if len(result) != 0 {
			t.Errorf("expected empty, got %d", len(result))
		}
	})

	t.Run("augments first user message", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: "fix the bug"},
		}
		result := InjectPlanRequest(msgs, "")
		if !strings.Contains(result[0].Content, "fix the bug") {
			t.Error("should contain original content")
		}
		if !strings.Contains(result[0].Content, "plan") {
			t.Error("should contain planning prompt")
		}
	})

	t.Run("custom prompt", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "user", Content: "task"},
		}
		result := InjectPlanRequest(msgs, "CUSTOM PLAN PROMPT")
		if !strings.Contains(result[0].Content, "CUSTOM PLAN PROMPT") {
			t.Error("should use custom prompt")
		}
	})

	t.Run("does not modify original", func(t *testing.T) {
		original := []provider.Message{
			{Role: "user", Content: "original"},
		}
		result := InjectPlanRequest(original, "plan")
		if original[0].Content != "original" {
			t.Error("original should not be modified")
		}
		if result[0].Content == "original" {
			t.Error("result should be different from original")
		}
	})

	t.Run("only modifies first user message", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "first user msg"},
			{Role: "user", Content: "second user msg"},
		}
		result := InjectPlanRequest(msgs, "PLAN")
		if !strings.Contains(result[1].Content, "PLAN") {
			t.Error("should modify first user message")
		}
		if strings.Contains(result[2].Content, "PLAN") {
			t.Error("should not modify second user message")
		}
	})
}

// ─── Approval ──────────────────────────────────────────────────────────

func TestApprovalConfig_NeedsApproval(t *testing.T) {
	tests := []struct {
		name     string
		cfg      ApprovalConfig
		tool     string
		expected bool
	}{
		{"disabled", ApprovalConfig{Enabled: false, RequireApproval: []string{"bash"}}, "bash", false},
		{"enabled match", ApprovalConfig{Enabled: true, RequireApproval: []string{"bash", "write"}}, "bash", true},
		{"enabled no match", ApprovalConfig{Enabled: true, RequireApproval: []string{"bash"}}, "read", false},
		{"enabled empty list", ApprovalConfig{Enabled: true, RequireApproval: nil}, "bash", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.NeedsApproval(tt.tool)
			if got != tt.expected {
				t.Errorf("NeedsApproval(%q) = %v, want %v", tt.tool, got, tt.expected)
			}
		})
	}
}

func TestRequestApproval_Approved(t *testing.T) {
	ch := newTestChannel()
	tc := provider.ToolCall{ID: "t1", Name: "bash"}

	// Simulate user responding "y"
	go func() {
		// Wait for the prompt to be sent
		<-ch.sent
		ch.incoming <- &channel.Message{Role: "user", Content: "y"}
	}()

	approved, err := RequestApproval(context.Background(), ch, tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approved {
		t.Error("expected approval")
	}
}

func TestRequestApproval_Denied(t *testing.T) {
	ch := newTestChannel()
	tc := provider.ToolCall{ID: "t1", Name: "bash"}

	go func() {
		<-ch.sent
		ch.incoming <- &channel.Message{Role: "user", Content: "n"}
	}()

	approved, err := RequestApproval(context.Background(), ch, tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approved {
		t.Error("expected denial")
	}
}

func TestRequestApproval_Yes(t *testing.T) {
	ch := newTestChannel()
	tc := provider.ToolCall{ID: "t1", Name: "bash"}

	go func() {
		<-ch.sent
		ch.incoming <- &channel.Message{Role: "user", Content: "  YES  "}
	}()

	approved, err := RequestApproval(context.Background(), ch, tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approved {
		t.Error("expected approval for 'YES'")
	}
}

func TestRequestApproval_ContextCancelled(t *testing.T) {
	ch := newTestChannel()
	tc := provider.ToolCall{ID: "t1", Name: "bash"}

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-ch.sent
		cancel()
	}()

	_, err := RequestApproval(ctx, ch, tc)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

// ─── Agent New / Defaults ──────────────────────────────────────────────

func TestNew_Defaults(t *testing.T) {
	ch := newTestChannel()
	reg := tool.NewRegistry()
	ag := New(Config{
		Provider: &mockChatProvider{},
		Channel:  ch,
		Tools:    reg,
	})

	if ag.maxTokens != 8192 {
		t.Errorf("default maxTokens = %d, want 8192", ag.maxTokens)
	}
	if ag.maxIterations != 50 {
		t.Errorf("default maxIterations = %d, want 50", ag.maxIterations)
	}
	if ag.logger == nil {
		t.Error("logger should not be nil")
	}
	if ag.metrics == nil {
		t.Error("metrics should not be nil")
	}
}

func TestNew_CustomValues(t *testing.T) {
	ch := newTestChannel()
	ag := New(Config{
		Provider:      &mockChatProvider{},
		Channel:       ch,
		Tools:         tool.NewRegistry(),
		MaxTokens:     4096,
		MaxIterations: 10,
		Model:         "test-model",
	})

	if ag.maxTokens != 4096 {
		t.Errorf("maxTokens = %d, want 4096", ag.maxTokens)
	}
	if ag.maxIterations != 10 {
		t.Errorf("maxIterations = %d, want 10", ag.maxIterations)
	}
	if ag.model != "test-model" {
		t.Errorf("model = %q, want 'test-model'", ag.model)
	}
}

func TestClearHistory(t *testing.T) {
	ag := New(Config{
		Provider:       &mockChatProvider{},
		Channel:        newTestChannel(),
		Tools:          tool.NewRegistry(),
		InitialHistory: []provider.Message{{Role: "user", Content: "old"}},
	})

	if len(ag.History()) != 1 {
		t.Fatalf("expected 1 message, got %d", len(ag.History()))
	}
	ag.ClearHistory()
	if len(ag.History()) != 0 {
		t.Errorf("expected 0 messages after clear, got %d", len(ag.History()))
	}
}

func TestHistoryJSON(t *testing.T) {
	ag := New(Config{
		Provider:       &mockChatProvider{},
		Channel:        newTestChannel(),
		Tools:          tool.NewRegistry(),
		InitialHistory: []provider.Message{{Role: "user", Content: "hello"}},
	})

	data, err := ag.HistoryJSON()
	if err != nil {
		t.Fatalf("HistoryJSON error: %v", err)
	}
	if !strings.Contains(string(data), "hello") {
		t.Error("JSON should contain message content")
	}
}

// ─── Agent Integration: handleMessage ──────────────────────────────────

func TestHandleMessage_SimpleResponse(t *testing.T) {
	ch := newTestChannel()
	prov := &mockChatProvider{
		resp: &provider.ChatResponse{
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Hello!"},
			},
		},
	}

	ag := New(Config{
		Provider: prov,
		Channel:  ch,
		Tools:    tool.NewRegistry(),
	})

	msg := &channel.Message{Role: "user", Content: "hi"}
	err := ag.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage error: %v", err)
	}

	// Should have user + assistant in history
	if len(ag.History()) != 2 {
		t.Errorf("expected 2 messages in history, got %d", len(ag.History()))
	}
	if ag.History()[1].Role != "assistant" {
		t.Error("second message should be assistant")
	}
}

func TestHandleMessage_ToolExecution(t *testing.T) {
	ch := newTestChannel()

	callCount := 0
	prov := &sequentialProvider{
		responses: []*provider.ChatResponse{
			// First: tool call
			{Content: []provider.ContentBlock{
				{Type: "text", Text: "Let me check."},
				{Type: "tool_use", ToolUse: &provider.ToolCall{
					ID: "tc1", Name: "echo", Input: json.RawMessage(`{"msg":"test"}`),
				}},
			}},
			// Second: final text response
			{Content: []provider.ContentBlock{
				{Type: "text", Text: "Done!"},
			}},
		},
		callCount: &callCount,
	}

	reg := tool.NewRegistry()
	reg.Register(&echoTool{})

	ag := New(Config{
		Provider: prov,
		Channel:  ch,
		Tools:    reg,
	})

	msg := &channel.Message{Role: "user", Content: "run echo"}
	err := ag.handleMessage(context.Background(), msg)
	if err != nil {
		t.Fatalf("handleMessage error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 provider calls, got %d", callCount)
	}

	// History: user, assistant (tool call), tool result, assistant (final)
	if len(ag.History()) != 4 {
		t.Errorf("expected 4 messages in history, got %d", len(ag.History()))
	}
}

func TestHandleMessage_MaxIterations(t *testing.T) {
	ch := newTestChannel()

	// Provider always returns a tool call — infinite loop
	prov := &infiniteToolProvider{}

	reg := tool.NewRegistry()
	reg.Register(&echoTool{})

	ag := New(Config{
		Provider:      prov,
		Channel:       ch,
		Tools:         reg,
		MaxIterations: 3,
	})

	msg := &channel.Message{Role: "user", Content: "loop forever"}
	err := ag.handleMessage(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error from max iterations")
	}

	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("expected AgentError, got %T: %v", err, err)
	}
	if agentErr.Code != ErrMaxIterations {
		t.Errorf("expected ErrMaxIterations, got %s", agentErr.Code)
	}
}

func TestHandleMessage_StreamError(t *testing.T) {
	ch := newTestChannel()
	prov := &errorStreamProvider{err: fmt.Errorf("connection reset")}

	ag := New(Config{
		Provider: prov,
		Channel:  ch,
		Tools:    tool.NewRegistry(),
	})

	msg := &channel.Message{Role: "user", Content: "test"}
	err := ag.handleMessage(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("expected AgentError, got %T", err)
	}
	if agentErr.Code != ErrProvider {
		t.Errorf("expected ErrProvider, got %s", agentErr.Code)
	}
}

func TestHandleMessage_BudgetExceeded(t *testing.T) {
	ch := newTestChannel()

	prov := &mockChatProvider{
		resp: &provider.ChatResponse{
			Content: []provider.ContentBlock{
				{Type: "text", Text: "Hello!"},
			},
		},
	}

	ag := New(Config{
		Provider: prov,
		Channel:  ch,
		Tools:    tool.NewRegistry(),
		Cost:     CostConfig{MaxSessionCost: 0.0001},
	})

	// Record some cost to exceed budget
	ag.costTracker.Record("claude-sonnet-4-20250514", 1_000_000, 1_000_000)

	msg := &channel.Message{Role: "user", Content: "test"}
	err := ag.handleMessage(context.Background(), msg)
	if err == nil {
		t.Fatal("expected budget error")
	}
	var agentErr *AgentError
	if errors.As(err, &agentErr) {
		if agentErr.Code != ErrBudgetExceed {
			t.Errorf("expected ErrBudgetExceed, got %s", agentErr.Code)
		}
	}
}

func TestHandleMessage_ConversationID(t *testing.T) {
	ch := newTestChannel()
	prov := &mockChatProvider{
		resp: &provider.ChatResponse{
			Content: []provider.ContentBlock{
				{Type: "text", Text: "ok"},
			},
		},
	}

	ag := New(Config{
		Provider: prov,
		Channel:  ch,
		Tools:    tool.NewRegistry(),
	})

	// Default conversation
	msg1 := &channel.Message{Role: "user", Content: "hello"}
	_ = ag.handleMessage(context.Background(), msg1)

	// Slack-style conversation
	msg2 := &channel.Message{
		Role:    "user",
		Content: "thread msg",
		Metadata: map[string]any{
			"channel":   "C123",
			"thread_ts": "1234.5678",
		},
	}
	_ = ag.handleMessage(context.Background(), msg2)

	// Default should have 2 messages, slack thread should have 2 messages
	defaultHistory := ag.getHistory("default")
	slackHistory := ag.getHistory("C123:1234.5678")

	if len(defaultHistory) != 2 {
		t.Errorf("expected 2 in default history, got %d", len(defaultHistory))
	}
	if len(slackHistory) != 2 {
		t.Errorf("expected 2 in slack history, got %d", len(slackHistory))
	}
}

func TestGetConversationID(t *testing.T) {
	ag := New(Config{
		Provider: &mockChatProvider{},
		Channel:  newTestChannel(),
		Tools:    tool.NewRegistry(),
	})

	tests := []struct {
		name     string
		msg      *channel.Message
		expected string
	}{
		{"nil metadata", &channel.Message{}, "default"},
		{"empty metadata", &channel.Message{Metadata: map[string]any{}}, "default"},
		{"channel only", &channel.Message{Metadata: map[string]any{"channel": "C123"}}, "C123"},
		{"channel+thread", &channel.Message{Metadata: map[string]any{"channel": "C123", "thread_ts": "ts1"}}, "C123:ts1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ag.getConversationID(tt.msg)
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestExecuteTool_UnknownTool(t *testing.T) {
	ag := New(Config{
		Provider: &mockChatProvider{},
		Channel:  newTestChannel(),
		Tools:    tool.NewRegistry(),
	})

	result := ag.executeTool(context.Background(), provider.ToolCall{
		ID: "t1", Name: "nonexistent",
	})
	if !result.IsError {
		t.Error("expected error for unknown tool")
	}
	if !strings.Contains(result.Content, "unknown tool") {
		t.Errorf("unexpected error: %q", result.Content)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		max      int
		expected string
	}{
		{"short", 10, "short"},
		{"a long string here", 5, "a lon..."},
		{"has\nnewlines", 20, "has newlines"},
		{"  spaces  ", 20, "spaces"},
	}

	for _, tt := range tests {
		got := truncate(tt.input, tt.max)
		if got != tt.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.expected)
		}
	}
}

// ─── Test Helpers ──────────────────────────────────────────────────────

// testChannel is a mock channel for testing.
type testChannel struct {
	incoming chan *channel.Message
	sent     chan *channel.Message
}

func newTestChannel() *testChannel {
	return &testChannel{
		incoming: make(chan *channel.Message, 100),
		sent:     make(chan *channel.Message, 100),
	}
}

func (c *testChannel) Start(_ context.Context) error         { return nil }
func (c *testChannel) Stop() error                           { return nil }
func (c *testChannel) Name() string                          { return "test" }
func (c *testChannel) Receive() <-chan *channel.Message       { return c.incoming }
func (c *testChannel) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
func (c *testChannel) Send(_ context.Context, msg *channel.Message) error {
	c.sent <- msg
	return nil
}

// echoTool is a simple tool that returns its input.
type echoTool struct{}

func (e *echoTool) Name() string                 { return "echo" }
func (e *echoTool) Description() string          { return "echoes input" }
func (e *echoTool) Schema() json.RawMessage      { return json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}}}`) }
func (e *echoTool) Execute(_ context.Context, params json.RawMessage) (*tool.Result, error) {
	var p struct{ Msg string `json:"msg"` }
	json.Unmarshal(params, &p)
	return &tool.Result{Content: p.Msg}, nil
}

// sequentialProvider returns different responses on each call.
type sequentialProvider struct {
	responses []*provider.ChatResponse
	callCount *int
}

func (p *sequentialProvider) Name() string    { return "sequential" }
func (p *sequentialProvider) Models() []string { return []string{"test"} }
func (p *sequentialProvider) Chat(_ context.Context, _ *provider.ChatRequest) (*provider.ChatResponse, error) {
	idx := *p.callCount
	*p.callCount++
	if idx < len(p.responses) {
		return p.responses[idx], nil
	}
	return &provider.ChatResponse{}, nil
}
func (p *sequentialProvider) Stream(_ context.Context, req *provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	resp, err := p.Chat(context.Background(), req)
	if err != nil {
		return nil, err
	}
	ch := make(chan provider.StreamEvent, 10)
	go func() {
		defer close(ch)
		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				ch <- provider.StreamEvent{Type: "text", Text: block.Text}
			case "tool_use":
				ch <- provider.StreamEvent{Type: "tool_use", ToolUse: block.ToolUse}
			}
		}
		ch <- provider.StreamEvent{Type: "stop", Usage: &provider.Usage{InputTokens: 10, OutputTokens: 5}}
	}()
	return ch, nil
}

// infiniteToolProvider always returns a tool call.
type infiniteToolProvider struct{}

func (p *infiniteToolProvider) Name() string    { return "infinite" }
func (p *infiniteToolProvider) Models() []string { return []string{"test"} }
func (p *infiniteToolProvider) Chat(_ context.Context, _ *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{
		Content: []provider.ContentBlock{
			{Type: "tool_use", ToolUse: &provider.ToolCall{ID: "t1", Name: "echo", Input: json.RawMessage(`{"msg":"loop"}`)}},
		},
	}, nil
}
func (p *infiniteToolProvider) Stream(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 5)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{
			Type:    "tool_use",
			ToolUse: &provider.ToolCall{ID: "t1", Name: "echo", Input: json.RawMessage(`{"msg":"loop"}`)},
		}
		ch <- provider.StreamEvent{Type: "stop", Usage: &provider.Usage{InputTokens: 10, OutputTokens: 5}}
	}()
	return ch, nil
}

// errorStreamProvider always returns a stream error.
type errorStreamProvider struct {
	err error
}

func (p *errorStreamProvider) Name() string    { return "error" }
func (p *errorStreamProvider) Models() []string { return []string{"test"} }
func (p *errorStreamProvider) Chat(_ context.Context, _ *provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, p.err
}
func (p *errorStreamProvider) Stream(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamEvent{Type: "error", Error: p.err}
	}()
	return ch, nil
}
