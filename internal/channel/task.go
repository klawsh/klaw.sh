package channel

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TaskChannel sends a single task and prints output, then exits.
type TaskChannel struct {
	task     string
	messages chan *Message
	done     chan struct{}
	mu       sync.Mutex
	started  bool
	taskSent bool
	closed   bool
}

// NewTaskChannel creates a channel that sends a single task.
func NewTaskChannel(task string) *TaskChannel {
	return &TaskChannel{
		task:     task,
		messages: make(chan *Message, 10),
		done:     make(chan struct{}),
	}
}

func (t *TaskChannel) Name() string {
	return "task"
}

func (t *TaskChannel) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return nil
	}
	t.started = true
	t.mu.Unlock()

	// Send the task after a brief delay
	go func() {
		time.Sleep(100 * time.Millisecond)
		t.messages <- &Message{
			ID:        uuid.New().String(),
			Role:      "user",
			Content:   t.task,
			Timestamp: time.Now(),
		}
		t.mu.Lock()
		t.taskSent = true
		t.mu.Unlock()
	}()

	return nil
}

func (t *TaskChannel) Send(ctx context.Context, msg *Message) error {
	if msg.Role != "assistant" && msg.Role != "error" {
		return nil
	}

	if msg.Role == "error" {
		fmt.Fprintf(os.Stderr, "[ERROR] %s\n", msg.Content)
		return nil
	}

	if msg.IsPartial {
		fmt.Print(msg.Content)
		os.Stdout.Sync()
		return nil
	}

	if msg.IsDone {
		fmt.Println()
		// After task is complete, signal done (only once)
		t.mu.Lock()
		if t.taskSent && !t.closed {
			t.closed = true
			close(t.done)
		}
		t.mu.Unlock()
		return nil
	}

	fmt.Println(msg.Content)
	return nil
}

func (t *TaskChannel) Receive() <-chan *Message {
	return t.messages
}

func (t *TaskChannel) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	select {
	case <-t.done:
	default:
		close(t.done)
	}

	return nil
}

func (t *TaskChannel) Done() <-chan struct{} {
	return t.done
}
