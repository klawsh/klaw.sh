package channel

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Terminal is an interactive terminal channel.
type Terminal struct {
	messages    chan *Message
	done        chan struct{}
	mu          sync.Mutex
	started     bool
	waitingResp bool

	// For streaming output
	currentLine strings.Builder
}

// NewTerminal creates a new terminal channel.
func NewTerminal() *Terminal {
	return &Terminal{
		messages: make(chan *Message, 10),
		done:     make(chan struct{}),
	}
}

func (t *Terminal) Name() string {
	return "terminal"
}

func (t *Terminal) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return nil
	}
	t.started = true
	t.mu.Unlock()

	go t.readLoop(ctx)
	return nil
}

func (t *Terminal) readLoop(ctx context.Context) {
	reader := bufio.NewReader(os.Stdin)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.done:
			return
		default:
		}

		// Only print prompt if not waiting for response
		t.mu.Lock()
		waiting := t.waitingResp
		t.mu.Unlock()

		if !waiting {
			fmt.Print("\n> ")
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Check for exit commands
		if line == "exit" || line == "quit" || line == "/exit" || line == "/quit" {
			close(t.done)
			return
		}

		// Check for special commands
		if strings.HasPrefix(line, "/") {
			t.handleCommand(line)
			continue
		}

		// Mark as waiting for response
		t.mu.Lock()
		t.waitingResp = true
		t.mu.Unlock()

		t.messages <- &Message{
			ID:        uuid.New().String(),
			Role:      "user",
			Content:   line,
			Timestamp: time.Now(),
		}
	}
}

func (t *Terminal) handleCommand(cmd string) {
	switch cmd {
	case "/help":
		fmt.Println("\nCommands:")
		fmt.Println("  /help    - Show this help")
		fmt.Println("  /clear   - Clear screen")
		fmt.Println("  /exit    - Exit klaw")
	case "/clear":
		fmt.Print("\033[H\033[2J")
	default:
		fmt.Printf("Unknown command: %s (try /help)\n", cmd)
	}
}

func (t *Terminal) Send(ctx context.Context, msg *Message) error {
	if msg.Role != "assistant" && msg.Role != "error" {
		return nil
	}

	if msg.Role == "error" {
		fmt.Printf("\n[ERROR] %s\n", msg.Content)
		t.mu.Lock()
		t.waitingResp = false
		t.mu.Unlock()
		return nil
	}

	if msg.IsPartial {
		// Streaming: print without newline
		fmt.Print(msg.Content)
		os.Stdout.Sync() // Force flush
		t.currentLine.WriteString(msg.Content)
		return nil
	}

	if msg.IsDone {
		// End of stream
		if t.currentLine.Len() > 0 {
			fmt.Println()
			t.currentLine.Reset()
		}
		// Mark as done waiting
		t.mu.Lock()
		t.waitingResp = false
		t.mu.Unlock()
		return nil
	}

	// Complete message
	fmt.Println(msg.Content)
	t.mu.Lock()
	t.waitingResp = false
	t.mu.Unlock()
	return nil
}

func (t *Terminal) Receive() <-chan *Message {
	return t.messages
}

func (t *Terminal) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.started {
		return nil
	}

	select {
	case <-t.done:
		// Already closed
	default:
		close(t.done)
	}

	return nil
}

// Done returns a channel that's closed when the terminal exits.
func (t *Terminal) Done() <-chan struct{} {
	return t.done
}
