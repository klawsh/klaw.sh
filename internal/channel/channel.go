// Package channel defines the messaging channel interface.
package channel

import (
	"context"
	"time"
)

// Channel is any messaging surface (terminal, telegram, discord, etc).
type Channel interface {
	// Start initializes the channel and begins receiving messages.
	Start(ctx context.Context) error

	// Send sends a message through the channel.
	Send(ctx context.Context, msg *Message) error

	// Receive returns a channel for incoming messages.
	Receive() <-chan *Message

	// Stop gracefully shuts down the channel.
	Stop() error

	// Name returns the channel identifier.
	Name() string
}

// Message represents a chat message.
type Message struct {
	ID        string
	Role      string // "user", "assistant", "system"
	Content   string
	Timestamp time.Time
	Metadata  map[string]any

	// For streaming assistant responses
	IsPartial bool
	IsDone    bool
}
