// Package observe provides structured logging and metrics for klaw.
package observe

import (
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Logger wraps slog.Logger for structured logging.
type Logger struct {
	*slog.Logger
}

// NewLogger creates a structured JSON logger.
func NewLogger(level string, output io.Writer) *Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level: lvl,
	})

	return &Logger{Logger: slog.New(handler)}
}

// Nop returns a no-op logger that discards all output.
func Nop() *Logger {
	return &Logger{Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
}

// SessionMetrics tracks per-session stats.
type SessionMetrics struct {
	InputTokens  int64
	OutputTokens int64
	Requests     int64
	Errors       int64
	ToolCalls    map[string]int64
	StartedAt    time.Time
	mu           sync.Mutex
}

// Metrics tracks global and per-session metrics.
type Metrics struct {
	TotalInputTokens  atomic.Int64
	TotalOutputTokens atomic.Int64
	TotalRequests     atomic.Int64
	TotalErrors       atomic.Int64
	TotalToolCalls    atomic.Int64
	sessions          map[string]*SessionMetrics
	mu                sync.RWMutex
}

// NewMetrics creates a new metrics collector.
func NewMetrics() *Metrics {
	return &Metrics{
		sessions: make(map[string]*SessionMetrics),
	}
}

func (m *Metrics) getSession(sessionID string) *SessionMetrics {
	m.mu.RLock()
	sm, ok := m.sessions[sessionID]
	m.mu.RUnlock()
	if ok {
		return sm
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Double-check
	if sm, ok = m.sessions[sessionID]; ok {
		return sm
	}
	sm = &SessionMetrics{
		ToolCalls: make(map[string]int64),
		StartedAt: time.Now(),
	}
	m.sessions[sessionID] = sm
	return sm
}

// RecordRequest records a provider request with token usage.
func (m *Metrics) RecordRequest(sessionID string, input, output int) {
	m.TotalInputTokens.Add(int64(input))
	m.TotalOutputTokens.Add(int64(output))
	m.TotalRequests.Add(1)

	sm := m.getSession(sessionID)
	sm.mu.Lock()
	sm.InputTokens += int64(input)
	sm.OutputTokens += int64(output)
	sm.Requests++
	sm.mu.Unlock()
}

// RecordToolCall records a tool invocation.
func (m *Metrics) RecordToolCall(sessionID, toolName string) {
	m.TotalToolCalls.Add(1)

	sm := m.getSession(sessionID)
	sm.mu.Lock()
	sm.ToolCalls[toolName]++
	sm.mu.Unlock()
}

// RecordError records an error occurrence.
func (m *Metrics) RecordError(sessionID, errCode string) {
	m.TotalErrors.Add(1)

	sm := m.getSession(sessionID)
	sm.mu.Lock()
	sm.Errors++
	sm.mu.Unlock()
}

// Summary returns a snapshot of global metrics.
func (m *Metrics) Summary() map[string]any {
	return map[string]any{
		"total_input_tokens":  m.TotalInputTokens.Load(),
		"total_output_tokens": m.TotalOutputTokens.Load(),
		"total_requests":      m.TotalRequests.Load(),
		"total_errors":        m.TotalErrors.Load(),
		"total_tool_calls":    m.TotalToolCalls.Load(),
	}
}
