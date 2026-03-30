// Package server implements the OpenAI-compatible HTTP gateway.
package server

import (
	"context"

	"github.com/eachlabs/klaw/internal/channel"
)

// HTTPChannel implements channel.Channel for HTTP request/response lifecycle.
// Each HTTP request creates one HTTPChannel. Agent writes to outgoing,
// HTTP handler reads from outgoing and converts to SSE chunks.
type HTTPChannel struct {
	incoming  chan *channel.Message
	outgoing  chan *channel.Message
	requestID string
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewHTTPChannel creates a channel for a single HTTP request.
func NewHTTPChannel(ctx context.Context, requestID string) *HTTPChannel {
	ctx, cancel := context.WithCancel(ctx)
	return &HTTPChannel{
		incoming:  make(chan *channel.Message, 1),
		outgoing:  make(chan *channel.Message, 100),
		requestID: requestID,
		ctx:       ctx,
		cancel:    cancel,
	}
}

func (c *HTTPChannel) Start(ctx context.Context) error { return nil }

func (c *HTTPChannel) Receive() <-chan *channel.Message { return c.incoming }

func (c *HTTPChannel) Send(ctx context.Context, msg *channel.Message) error {
	select {
	case c.outgoing <- msg:
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	}
}

func (c *HTTPChannel) Stop() error {
	c.cancel()
	// Close outgoing so the HTTP handler's range loop exits.
	// Safe because only one goroutine (agent) writes, and Stop is called once.
	close(c.outgoing)
	return nil
}

func (c *HTTPChannel) Name() string { return "openai-http" }

// PushUserMessage sends the parsed user message into the channel for the agent.
// No metadata is set so the agent uses "default" conversation ID,
// which maps to agent.history (accessible via agent.History()).
func (c *HTTPChannel) PushUserMessage(msg *channel.Message) {
	c.incoming <- msg
	close(c.incoming)
}
