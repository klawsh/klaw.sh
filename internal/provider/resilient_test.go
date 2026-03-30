package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// mockProvider is a test provider that can be configured to fail.
type mockProvider struct {
	name      string
	chatErr   error
	chatResp  *ChatResponse
	callCount int
}

func (m *mockProvider) Name() string    { return m.name }
func (m *mockProvider) Models() []string { return []string{"test-model"} }

func (m *mockProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	m.callCount++
	if m.chatErr != nil {
		return nil, m.chatErr
	}
	return m.chatResp, nil
}

func (m *mockProvider) Stream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
	m.callCount++
	if m.chatErr != nil {
		return nil, m.chatErr
	}
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Type: "stop"}
	close(ch)
	return ch, nil
}

func TestResilientProviderSuccess(t *testing.T) {
	primary := &mockProvider{
		name:     "primary",
		chatResp: &ChatResponse{Content: []ContentBlock{{Type: "text", Text: "hello"}}},
	}

	rp := NewResilientProvider(ResilientConfig{
		Primary: primary,
		Retry:   RetryConfig{MaxRetries: 3},
	})

	resp, err := rp.Chat(context.Background(), &ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content[0].Text != "hello" {
		t.Errorf("expected 'hello', got %q", resp.Content[0].Text)
	}
	if primary.callCount != 1 {
		t.Errorf("expected 1 call, got %d", primary.callCount)
	}
}

func TestResilientProviderRetry(t *testing.T) {
	// Provider that fails with retryable error
	callCount := 0
	primary := &mockProvider{
		name:    "primary",
		chatErr: errors.New("HTTP 429: rate limit exceeded"),
	}

	// Override Chat to succeed on 3rd attempt
	origChat := primary.chatErr
	rp := NewResilientProvider(ResilientConfig{
		Primary: primary,
		Retry: RetryConfig{
			MaxRetries:     3,
			InitialBackoff: 10 * time.Millisecond,
			MaxBackoff:     50 * time.Millisecond,
			BackoffFactor:  2.0,
		},
	})

	// Wrap to track and succeed on attempt 3
	wrapper := &retryTestProvider{
		inner:          primary,
		succeedAttempt: 3,
		err:            origChat,
		resp:           &ChatResponse{Content: []ContentBlock{{Type: "text", Text: "ok"}}},
	}
	_ = callCount
	rp.primary = wrapper

	resp, err := rp.Chat(context.Background(), &ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content[0].Text != "ok" {
		t.Errorf("expected 'ok', got %q", resp.Content[0].Text)
	}
	if wrapper.callCount != 3 {
		t.Errorf("expected 3 calls, got %d", wrapper.callCount)
	}
}

type retryTestProvider struct {
	inner          *mockProvider
	succeedAttempt int
	callCount      int
	err            error
	resp           *ChatResponse
}

func (r *retryTestProvider) Name() string    { return r.inner.Name() }
func (r *retryTestProvider) Models() []string { return r.inner.Models() }

func (r *retryTestProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	r.callCount++
	if r.callCount >= r.succeedAttempt {
		return r.resp, nil
	}
	return nil, r.err
}

func (r *retryTestProvider) Stream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
	r.callCount++
	if r.callCount >= r.succeedAttempt {
		ch := make(chan StreamEvent, 1)
		ch <- StreamEvent{Type: "stop"}
		close(ch)
		return ch, nil
	}
	return nil, r.err
}

func TestResilientProviderFallback(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: errors.New("HTTP 500: internal server error"),
	}
	fallback := &mockProvider{
		name:     "fallback",
		chatResp: &ChatResponse{Content: []ContentBlock{{Type: "text", Text: "fallback ok"}}},
	}

	rp := NewResilientProvider(ResilientConfig{
		Primary:   primary,
		Fallbacks: []Provider{fallback},
		Retry: RetryConfig{
			MaxRetries:     1,
			InitialBackoff: 10 * time.Millisecond,
			MaxBackoff:     50 * time.Millisecond,
			BackoffFactor:  2.0,
		},
	})

	resp, err := rp.Chat(context.Background(), &ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content[0].Text != "fallback ok" {
		t.Errorf("expected 'fallback ok', got %q", resp.Content[0].Text)
	}
}

func TestResilientProviderNonRetryable(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: errors.New("invalid API key"),
	}

	rp := NewResilientProvider(ResilientConfig{
		Primary: primary,
		Retry: RetryConfig{
			MaxRetries:     3,
			InitialBackoff: 10 * time.Millisecond,
		},
	})

	_, err := rp.Chat(context.Background(), &ChatRequest{})
	if err == nil {
		t.Fatal("expected error for non-retryable failure")
	}
	// Should not retry — only 1 call
	if primary.callCount != 1 {
		t.Errorf("expected 1 call for non-retryable error, got %d", primary.callCount)
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		err       string
		retryable bool
	}{
		{"HTTP 429: rate limit exceeded", true},
		{"HTTP 500: internal server error", true},
		{"HTTP 502: bad gateway", true},
		{"HTTP 503: service unavailable", true},
		{"HTTP 529: overloaded", true},
		{"connection reset by peer", true},
		{"connection refused", true},
		{"request timed out", true},
		{"timeout waiting for response", true},
		{"unexpected EOF", true},
		{"invalid API key", false},
		{"model not found", false},
		{"content policy violation", false},
	}

	for _, tt := range tests {
		got := isRetryable(errors.New(tt.err))
		if got != tt.retryable {
			t.Errorf("isRetryable(%q) = %v, want %v", tt.err, got, tt.retryable)
		}
	}
}

func TestIsRetryable_Nil(t *testing.T) {
	if isRetryable(nil) {
		t.Error("nil error should not be retryable")
	}
}

func TestResilientProviderStreamSuccess(t *testing.T) {
	primary := &mockProvider{
		name:     "primary",
		chatResp: &ChatResponse{Content: []ContentBlock{{Type: "text", Text: "ok"}}},
	}

	rp := NewResilientProvider(ResilientConfig{
		Primary: primary,
		Retry:   RetryConfig{MaxRetries: 3},
	})

	events, err := rp.Stream(context.Background(), &ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Drain the channel
	for range events {
	}
	if primary.callCount != 1 {
		t.Errorf("expected 1 call, got %d", primary.callCount)
	}
}

func TestResilientProviderStreamRetry(t *testing.T) {
	wrapper := &retryTestProvider{
		inner:          &mockProvider{name: "primary"},
		succeedAttempt: 2,
		err:            errors.New("HTTP 503: service unavailable"),
	}

	rp := NewResilientProvider(ResilientConfig{
		Primary: wrapper,
		Retry: RetryConfig{
			MaxRetries:     3,
			InitialBackoff: 5 * time.Millisecond,
			MaxBackoff:     20 * time.Millisecond,
			BackoffFactor:  2.0,
		},
	})

	events, err := rp.Stream(context.Background(), &ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range events {
	}
	if wrapper.callCount != 2 {
		t.Errorf("expected 2 calls, got %d", wrapper.callCount)
	}
}

func TestResilientProviderStreamFallback(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: errors.New("HTTP 500: internal server error"),
	}
	fallback := &mockProvider{
		name:     "fallback",
		chatResp: &ChatResponse{Content: []ContentBlock{{Type: "text", Text: "ok"}}},
	}

	rp := NewResilientProvider(ResilientConfig{
		Primary:   primary,
		Fallbacks: []Provider{fallback},
		Retry: RetryConfig{
			MaxRetries:     1,
			InitialBackoff: 5 * time.Millisecond,
			MaxBackoff:     20 * time.Millisecond,
			BackoffFactor:  2.0,
		},
	})

	events, err := rp.Stream(context.Background(), &ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for range events {
	}
}

func TestResilientProviderContextCancellation(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: errors.New("HTTP 429: rate limit"),
	}

	rp := NewResilientProvider(ResilientConfig{
		Primary: primary,
		Retry: RetryConfig{
			MaxRetries:     5,
			InitialBackoff: 1 * time.Second, // long backoff
			MaxBackoff:     5 * time.Second,
			BackoffFactor:  2.0,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := rp.Chat(ctx, &ChatRequest{})
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
}

func TestResilientProviderNameAndModels(t *testing.T) {
	primary := &mockProvider{name: "test-provider"}
	rp := NewResilientProvider(ResilientConfig{Primary: primary})

	if rp.Name() != "test-provider" {
		t.Errorf("Name() = %q, want 'test-provider'", rp.Name())
	}
	models := rp.Models()
	if len(models) != 1 || models[0] != "test-model" {
		t.Errorf("Models() = %v, want [test-model]", models)
	}
}

func TestResilientProviderDefaultConfig(t *testing.T) {
	primary := &mockProvider{name: "test"}
	rp := NewResilientProvider(ResilientConfig{Primary: primary})

	// Should use default retry config
	if rp.retry.MaxRetries != 3 {
		t.Errorf("default MaxRetries = %d, want 3", rp.retry.MaxRetries)
	}
	if rp.retry.InitialBackoff != 1*time.Second {
		t.Errorf("default InitialBackoff = %v, want 1s", rp.retry.InitialBackoff)
	}
	if rp.retry.MaxBackoff != 30*time.Second {
		t.Errorf("default MaxBackoff = %v, want 30s", rp.retry.MaxBackoff)
	}
	if rp.retry.BackoffFactor != 2.0 {
		t.Errorf("default BackoffFactor = %f, want 2.0", rp.retry.BackoffFactor)
	}
}

func TestResilientProviderAllFail(t *testing.T) {
	primary := &mockProvider{
		name:    "primary",
		chatErr: errors.New("HTTP 500: fail"),
	}
	fb1 := &mockProvider{
		name:    "fallback1",
		chatErr: errors.New("HTTP 500: also fail"),
	}
	fb2 := &mockProvider{
		name:    "fallback2",
		chatErr: errors.New("HTTP 500: still fail"),
	}

	rp := NewResilientProvider(ResilientConfig{
		Primary:   primary,
		Fallbacks: []Provider{fb1, fb2},
		Retry: RetryConfig{
			MaxRetries:     1,
			InitialBackoff: 5 * time.Millisecond,
			MaxBackoff:     20 * time.Millisecond,
			BackoffFactor:  2.0,
		},
	})

	_, err := rp.Chat(context.Background(), &ChatRequest{})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if !strings.Contains(err.Error(), "all providers failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCalcBackoff(t *testing.T) {
	rp := NewResilientProvider(ResilientConfig{
		Primary: &mockProvider{name: "test"},
		Retry: RetryConfig{
			MaxRetries:     5,
			InitialBackoff: 100 * time.Millisecond,
			MaxBackoff:     1 * time.Second,
			BackoffFactor:  2.0,
		},
	})

	// Attempt 1: ~100ms (+ up to 25% jitter)
	b1 := rp.calcBackoff(1)
	if b1 < 100*time.Millisecond || b1 > 150*time.Millisecond {
		t.Errorf("attempt 1 backoff = %v, expected 100-150ms", b1)
	}

	// Attempt 2: ~200ms (+ up to 25% jitter)
	b2 := rp.calcBackoff(2)
	if b2 < 200*time.Millisecond || b2 > 300*time.Millisecond {
		t.Errorf("attempt 2 backoff = %v, expected 200-300ms", b2)
	}

	// Large attempt: should be capped at max
	b10 := rp.calcBackoff(10)
	if b10 > 1250*time.Millisecond { // max + 25% jitter
		t.Errorf("attempt 10 backoff = %v, should be capped at ~1.25s", b10)
	}
}
