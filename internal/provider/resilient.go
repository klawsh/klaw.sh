package provider

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"time"
)

// RetryConfig controls retry behavior.
type RetryConfig struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64
}

// DefaultRetryConfig returns sensible retry defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
	}
}

// ResilientConfig configures the resilient provider wrapper.
type ResilientConfig struct {
	Primary   Provider
	Fallbacks []Provider
	Retry     RetryConfig
}

// ResilientProvider wraps a primary provider with retry logic and optional fallbacks.
type ResilientProvider struct {
	primary   Provider
	fallbacks []Provider
	retry     RetryConfig
}

// NewResilientProvider creates a provider with retry and fallback support.
func NewResilientProvider(cfg ResilientConfig) *ResilientProvider {
	retry := cfg.Retry
	if retry.MaxRetries == 0 {
		retry = DefaultRetryConfig()
	}
	if retry.InitialBackoff == 0 {
		retry.InitialBackoff = 1 * time.Second
	}
	if retry.MaxBackoff == 0 {
		retry.MaxBackoff = 30 * time.Second
	}
	if retry.BackoffFactor == 0 {
		retry.BackoffFactor = 2.0
	}
	return &ResilientProvider{
		primary:   cfg.Primary,
		fallbacks: cfg.Fallbacks,
		retry:     retry,
	}
}

func (r *ResilientProvider) Name() string {
	return r.primary.Name()
}

func (r *ResilientProvider) Models() []string {
	return r.primary.Models()
}

// Chat sends a request with retry and fallback.
func (r *ResilientProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	// Try primary with retries
	resp, err := r.withRetry(ctx, func(ctx context.Context) (*ChatResponse, error) {
		return r.primary.Chat(ctx, req)
	})
	if err == nil {
		return resp, nil
	}

	// Try fallbacks
	for _, fb := range r.fallbacks {
		resp, fbErr := fb.Chat(ctx, req)
		if fbErr == nil {
			return resp, nil
		}
	}

	return nil, fmt.Errorf("all providers failed, last error: %w", err)
}

// Stream sends a streaming request with retry and fallback.
func (r *ResilientProvider) Stream(ctx context.Context, req *ChatRequest) (<-chan StreamEvent, error) {
	// Try primary with retries
	events, err := r.withRetryStream(ctx, func(ctx context.Context) (<-chan StreamEvent, error) {
		return r.primary.Stream(ctx, req)
	})
	if err == nil {
		return events, nil
	}

	// Try fallbacks
	for _, fb := range r.fallbacks {
		events, fbErr := fb.Stream(ctx, req)
		if fbErr == nil {
			return events, nil
		}
	}

	return nil, fmt.Errorf("all providers failed, last error: %w", err)
}

func (r *ResilientProvider) withRetry(ctx context.Context, fn func(context.Context) (*ChatResponse, error)) (*ChatResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= r.retry.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := r.calcBackoff(attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		resp, err := fn(ctx)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		if !isRetryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (r *ResilientProvider) withRetryStream(ctx context.Context, fn func(context.Context) (<-chan StreamEvent, error)) (<-chan StreamEvent, error) {
	var lastErr error
	for attempt := 0; attempt <= r.retry.MaxRetries; attempt++ {
		if attempt > 0 {
			backoff := r.calcBackoff(attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		events, err := fn(ctx)
		if err == nil {
			return events, nil
		}
		lastErr = err

		if !isRetryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (r *ResilientProvider) calcBackoff(attempt int) time.Duration {
	backoff := float64(r.retry.InitialBackoff) * math.Pow(r.retry.BackoffFactor, float64(attempt-1))
	if backoff > float64(r.retry.MaxBackoff) {
		backoff = float64(r.retry.MaxBackoff)
	}
	// Add jitter: 0-25% of backoff
	jitter := backoff * 0.25 * rand.Float64()
	return time.Duration(backoff + jitter)
}

// isRetryable checks if an error is transient and worth retrying.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Check for retryable HTTP status codes in error messages
	retryablePatterns := []string{
		"429", "rate limit",
		"500", "internal server error",
		"502", "bad gateway",
		"503", "service unavailable",
		"529", "overloaded",
		"timeout", "timed out",
		"connection reset", "connection refused",
		"EOF",
	}
	lower := strings.ToLower(msg)
	for _, pattern := range retryablePatterns {
		if strings.Contains(lower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}
