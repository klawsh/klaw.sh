package observe

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestNewLogger(t *testing.T) {
	tests := []struct {
		level    string
		message  string
		shouldLog bool
	}{
		{"debug", "debug msg", true},
		{"info", "info msg", true},
		{"warn", "warn msg", true},
		{"error", "error msg", true},
		{"", "default is info", true},
		{"unknown", "defaults to info", true},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			var buf bytes.Buffer
			logger := NewLogger(tt.level, &buf)
			if logger == nil {
				t.Fatal("logger is nil")
			}
			// Just verify it doesn't panic
			logger.Info("test message", "key", "value")
		})
	}
}

func TestNewLogger_JSONOutput(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("info", &buf)
	logger.Info("hello", "name", "klaw")

	output := buf.String()
	if output == "" {
		t.Fatal("expected log output")
	}

	// Should be valid JSON
	var logEntry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &logEntry); err != nil {
		t.Fatalf("expected valid JSON: %v\nOutput: %s", err, output)
	}

	if logEntry["msg"] != "hello" {
		t.Errorf("expected msg='hello', got %v", logEntry["msg"])
	}
	if logEntry["name"] != "klaw" {
		t.Errorf("expected name='klaw', got %v", logEntry["name"])
	}
}

func TestNewLogger_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger("warn", &buf)

	logger.Info("should not appear")
	if buf.Len() > 0 {
		t.Error("info message should be filtered by warn level")
	}

	logger.Warn("should appear")
	if buf.Len() == 0 {
		t.Error("warn message should not be filtered")
	}
}

func TestNop(t *testing.T) {
	logger := Nop()
	if logger == nil {
		t.Fatal("Nop logger is nil")
	}
	// Should not panic
	logger.Info("this goes nowhere", "key", "value")
	logger.Error("also nowhere")
	logger.Debug("debug nop")
}

func TestMetrics_RecordRequest(t *testing.T) {
	m := NewMetrics()

	m.RecordRequest("sess1", 100, 50)
	m.RecordRequest("sess1", 200, 100)
	m.RecordRequest("sess2", 300, 150)

	if m.TotalInputTokens.Load() != 600 {
		t.Errorf("expected 600 input tokens, got %d", m.TotalInputTokens.Load())
	}
	if m.TotalOutputTokens.Load() != 300 {
		t.Errorf("expected 300 output tokens, got %d", m.TotalOutputTokens.Load())
	}
	if m.TotalRequests.Load() != 3 {
		t.Errorf("expected 3 requests, got %d", m.TotalRequests.Load())
	}
}

func TestMetrics_RecordToolCall(t *testing.T) {
	m := NewMetrics()

	m.RecordToolCall("sess1", "bash")
	m.RecordToolCall("sess1", "bash")
	m.RecordToolCall("sess1", "read")
	m.RecordToolCall("sess2", "bash")

	if m.TotalToolCalls.Load() != 4 {
		t.Errorf("expected 4 tool calls, got %d", m.TotalToolCalls.Load())
	}

	sm := m.getSession("sess1")
	sm.mu.Lock()
	if sm.ToolCalls["bash"] != 2 {
		t.Errorf("expected 2 bash calls in sess1, got %d", sm.ToolCalls["bash"])
	}
	if sm.ToolCalls["read"] != 1 {
		t.Errorf("expected 1 read call in sess1, got %d", sm.ToolCalls["read"])
	}
	sm.mu.Unlock()
}

func TestMetrics_RecordError(t *testing.T) {
	m := NewMetrics()

	m.RecordError("sess1", "provider_error")
	m.RecordError("sess1", "tool_execution")
	m.RecordError("sess2", "budget_exceeded")

	if m.TotalErrors.Load() != 3 {
		t.Errorf("expected 3 errors, got %d", m.TotalErrors.Load())
	}

	sm := m.getSession("sess1")
	sm.mu.Lock()
	if sm.Errors != 2 {
		t.Errorf("expected 2 errors in sess1, got %d", sm.Errors)
	}
	sm.mu.Unlock()
}

func TestMetrics_Summary(t *testing.T) {
	m := NewMetrics()
	m.RecordRequest("sess1", 1000, 500)
	m.RecordToolCall("sess1", "bash")
	m.RecordError("sess1", "test")

	summary := m.Summary()

	if summary["total_input_tokens"].(int64) != 1000 {
		t.Error("wrong input token count in summary")
	}
	if summary["total_output_tokens"].(int64) != 500 {
		t.Error("wrong output token count in summary")
	}
	if summary["total_requests"].(int64) != 1 {
		t.Error("wrong request count in summary")
	}
	if summary["total_tool_calls"].(int64) != 1 {
		t.Error("wrong tool call count in summary")
	}
	if summary["total_errors"].(int64) != 1 {
		t.Error("wrong error count in summary")
	}
}

func TestMetrics_Concurrent(t *testing.T) {
	m := NewMetrics()
	var wg sync.WaitGroup

	// Spawn goroutines that concurrently record metrics
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			m.RecordRequest("sess1", 10, 5)
		}()
		go func() {
			defer wg.Done()
			m.RecordToolCall("sess1", "bash")
		}()
		go func() {
			defer wg.Done()
			m.RecordError("sess1", "test")
		}()
	}

	wg.Wait()

	if m.TotalInputTokens.Load() != 1000 {
		t.Errorf("expected 1000, got %d", m.TotalInputTokens.Load())
	}
	if m.TotalOutputTokens.Load() != 500 {
		t.Errorf("expected 500, got %d", m.TotalOutputTokens.Load())
	}
	if m.TotalRequests.Load() != 100 {
		t.Errorf("expected 100, got %d", m.TotalRequests.Load())
	}
	if m.TotalToolCalls.Load() != 100 {
		t.Errorf("expected 100, got %d", m.TotalToolCalls.Load())
	}
	if m.TotalErrors.Load() != 100 {
		t.Errorf("expected 100, got %d", m.TotalErrors.Load())
	}
}

func TestMetrics_GetSession_CreatesOnDemand(t *testing.T) {
	m := NewMetrics()

	sm1 := m.getSession("new-session")
	if sm1 == nil {
		t.Fatal("expected new session to be created")
	}

	sm2 := m.getSession("new-session")
	if sm1 != sm2 {
		t.Error("expected same session instance on second call")
	}
}
