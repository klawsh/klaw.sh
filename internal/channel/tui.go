package channel

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TUIMessage represents a message for the TUI
type TUIMessage struct {
	Role    string // "user", "assistant", "tool", "error", "done"
	Content string
	Tool    string
}

// TUIChannel bridges the agent with the bubbletea TUI
type TUIChannel struct {
	// Input from user (TUI sends here)
	userInput chan string
	// Output to TUI (agent sends here)
	tuiOutput chan TUIMessage

	// Internal message channel for agent
	messages chan *Message
	done     chan struct{}

	mu      sync.Mutex
	started bool

	// Buffer for streaming
	streamBuffer strings.Builder
	inToolCall   bool
	currentTool  string
}

// NewTUIChannel creates a channel that works with bubbletea TUI
func NewTUIChannel() *TUIChannel {
	return &TUIChannel{
		userInput: make(chan string, 10),
		tuiOutput: make(chan TUIMessage, 100),
		messages:  make(chan *Message, 10),
		done:      make(chan struct{}),
	}
}

// UserInput returns the channel for user input (TUI writes here)
func (t *TUIChannel) UserInput() chan<- string {
	return t.userInput
}

// TUIOutput returns the channel for TUI output (TUI reads from here)
func (t *TUIChannel) TUIOutput() <-chan TUIMessage {
	return t.tuiOutput
}

func (t *TUIChannel) Name() string {
	return "tui"
}

func (t *TUIChannel) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return nil
	}
	t.started = true
	t.mu.Unlock()

	// Forward user input to agent
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.done:
				return
			case input := <-t.userInput:
				t.messages <- &Message{
					ID:        uuid.New().String(),
					Role:      "user",
					Content:   input,
					Timestamp: time.Now(),
				}
			}
		}
	}()

	return nil
}

func (t *TUIChannel) Send(ctx context.Context, msg *Message) error {
	if msg.Role == "error" {
		t.tuiOutput <- TUIMessage{
			Role:    "error",
			Content: msg.Content,
		}
		return nil
	}

	if msg.Role != "assistant" {
		return nil
	}

	content := msg.Content

	// Detect tool call start
	if strings.HasPrefix(content, "\n╭─ ") {
		t.mu.Lock()
		t.inToolCall = true
		t.currentTool = strings.TrimPrefix(strings.Split(content, "\n")[0], "\n╭─ ")
		t.streamBuffer.Reset()
		t.mu.Unlock()

		t.tuiOutput <- TUIMessage{
			Role: "tool",
			Tool: t.currentTool,
		}
		return nil
	}

	// Detect tool output
	if strings.HasPrefix(content, "│ ") {
		t.mu.Lock()
		inTool := t.inToolCall
		t.mu.Unlock()

		if inTool {
			// Collect tool output
			lines := strings.Split(content, "\n")
			for _, line := range lines {
				line = strings.TrimPrefix(line, "│ ")
				if line != "" {
					t.streamBuffer.WriteString(line + "\n")
				}
			}
			return nil
		}
	}

	// Detect tool call end
	if strings.HasPrefix(content, "╰─") {
		t.mu.Lock()
		if t.inToolCall {
			// Send complete tool output
			t.tuiOutput <- TUIMessage{
				Role:    "tool",
				Tool:    t.currentTool,
				Content: t.streamBuffer.String(),
			}
			t.inToolCall = false
			t.currentTool = ""
			t.streamBuffer.Reset()
		}
		t.mu.Unlock()
		return nil
	}

	if msg.IsPartial {
		// Streaming text
		t.mu.Lock()
		if !t.inToolCall {
			t.streamBuffer.WriteString(content)
		}
		t.mu.Unlock()
		return nil
	}

	if msg.IsDone {
		// Send accumulated text
		t.mu.Lock()
		if t.streamBuffer.Len() > 0 {
			t.tuiOutput <- TUIMessage{
				Role:    "assistant",
				Content: t.streamBuffer.String(),
			}
			t.streamBuffer.Reset()
		}
		t.mu.Unlock()

		// Signal done
		t.tuiOutput <- TUIMessage{Role: "done"}
		return nil
	}

	// Complete message
	t.tuiOutput <- TUIMessage{
		Role:    "assistant",
		Content: msg.Content,
	}
	return nil
}

func (t *TUIChannel) Receive() <-chan *Message {
	return t.messages
}

func (t *TUIChannel) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started {
		return nil
	}

	select {
	case <-t.done:
	default:
		close(t.done)
	}

	return nil
}

func (t *TUIChannel) Done() <-chan struct{} {
	return t.done
}
