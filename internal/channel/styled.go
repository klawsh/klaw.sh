package channel

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
)

// Styles for Claude Code-like appearance
var (
	// Colors
	purple     = lipgloss.Color("#A855F7")
	green      = lipgloss.Color("#22C55E")
	yellow     = lipgloss.Color("#EAB308")
	red        = lipgloss.Color("#EF4444")
	gray       = lipgloss.Color("#6B7280")
	darkGray   = lipgloss.Color("#374151")
	white      = lipgloss.Color("#F9FAFB")

	// Logo/Brand
	logoStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(purple)

	// User prompt
	userPromptStyle = lipgloss.NewStyle().
		Bold(true).
		Foreground(green)

	// Assistant response
	assistantStyle = lipgloss.NewStyle().
		Foreground(white)

	// Tool styles
	toolHeaderStyle = lipgloss.NewStyle().
		Foreground(yellow).
		Bold(true)

	toolOutputStyle = lipgloss.NewStyle().
		Foreground(gray)

	toolBoxStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(darkGray).
		Padding(0, 1)

	// Error style
	errorStyle = lipgloss.NewStyle().
		Foreground(red).
		Bold(true)

	// Muted text
	mutedStyle = lipgloss.NewStyle().
		Foreground(gray)

	// Spinner frames
	spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
)

// StyledTerminal is a Claude Code-like terminal channel.
type StyledTerminal struct {
	messages    chan *Message
	done        chan struct{}
	mu          sync.Mutex
	started     bool
	waitingResp bool

	// Streaming state
	inToolCall    bool
	currentTool   string
	toolOutput    strings.Builder
	spinnerIdx    int
	spinnerActive bool
	spinnerDone   chan struct{}
}

// NewStyledTerminal creates a new styled terminal channel.
func NewStyledTerminal() *StyledTerminal {
	return &StyledTerminal{
		messages:    make(chan *Message, 10),
		done:        make(chan struct{}),
		spinnerDone: make(chan struct{}),
	}
}

func (t *StyledTerminal) Name() string {
	return "styled-terminal"
}

func (t *StyledTerminal) Start(ctx context.Context) error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return nil
	}
	t.started = true
	t.mu.Unlock()

	// Print header
	t.printHeader()

	go t.readLoop(ctx)
	return nil
}

func (t *StyledTerminal) printHeader() {
	fmt.Println()
	fmt.Println(logoStyle.Render("  ╭─────────────────────────────────────╮"))
	fmt.Println(logoStyle.Render("  │") + "            " + logoStyle.Render("klaw") + "                    " + logoStyle.Render("│"))
	fmt.Println(logoStyle.Render("  │") + mutedStyle.Render("       AI Employee for Everyone     ") + logoStyle.Render("│"))
	fmt.Println(logoStyle.Render("  ╰─────────────────────────────────────╯"))
	fmt.Println()
	fmt.Println(mutedStyle.Render("  /help for commands • Ctrl+C to exit"))
	fmt.Println(mutedStyle.Render("  ─────────────────────────────────────"))
}

func (t *StyledTerminal) readLoop(ctx context.Context) {
	reader := bufio.NewReader(os.Stdin)

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.done:
			return
		default:
		}

		// Print prompt
		t.mu.Lock()
		waiting := t.waitingResp
		t.mu.Unlock()

		if !waiting {
			// Clear prompt indicator
			fmt.Print("\n")
			fmt.Print(userPromptStyle.Render("  ❯ "))
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Exit commands
		if line == "exit" || line == "quit" || line == "/exit" || line == "/quit" {
			fmt.Println(mutedStyle.Render("\n  Goodbye! 👋\n"))
			close(t.done)
			return
		}

		// Help command
		if line == "/help" {
			t.printHelp()
			continue
		}

		// Clear command
		if line == "/clear" {
			fmt.Print("\033[H\033[2J")
			t.printHeader()
			continue
		}

		// Mark as waiting
		t.mu.Lock()
		t.waitingResp = true
		t.mu.Unlock()

		// Start thinking spinner
		t.startSpinner()

		t.messages <- &Message{
			ID:        uuid.New().String(),
			Role:      "user",
			Content:   line,
			Timestamp: time.Now(),
		}
	}
}

func (t *StyledTerminal) printHelp() {
	help := `
  ` + logoStyle.Render("Commands:") + `

  ` + mutedStyle.Render("/help") + `     Show this help
  ` + mutedStyle.Render("/clear") + `    Clear screen
  ` + mutedStyle.Render("/exit") + `     Exit klaw

  ` + logoStyle.Render("What I can do:") + `

  • Execute tasks and automate workflows
  • Read, write, and manage files
  • Run shell commands and scripts
  • Search and analyze data
  • Help with any work you need done
`
	fmt.Println(help)
}

func (t *StyledTerminal) startSpinner() {
	t.mu.Lock()
	if t.spinnerActive {
		t.mu.Unlock()
		return
	}
	t.spinnerActive = true
	t.spinnerDone = make(chan struct{})
	t.mu.Unlock()

	go func() {
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()

		fmt.Print("\n  ")

		for {
			select {
			case <-t.spinnerDone:
				// Clear spinner line
				fmt.Print("\r\033[K")
				return
			case <-ticker.C:
				t.mu.Lock()
				idx := t.spinnerIdx
				t.spinnerIdx = (t.spinnerIdx + 1) % len(spinnerFrames)
				t.mu.Unlock()

				fmt.Printf("\r  %s %s",
					logoStyle.Render(spinnerFrames[idx]),
					mutedStyle.Render("Thinking..."))
			}
		}
	}()
}

func (t *StyledTerminal) stopSpinner() {
	t.mu.Lock()
	if !t.spinnerActive {
		t.mu.Unlock()
		return
	}
	t.spinnerActive = false
	close(t.spinnerDone)
	t.mu.Unlock()

	time.Sleep(50 * time.Millisecond) // Let spinner goroutine clean up
	fmt.Print("\r\033[K") // Clear the spinner line
}

func (t *StyledTerminal) Send(ctx context.Context, msg *Message) error {
	if msg.Role != "assistant" && msg.Role != "error" {
		return nil
	}

	// Stop spinner on first content
	t.stopSpinner()

	if msg.Role == "error" {
		fmt.Printf("\n  %s %s\n", errorStyle.Render("Error:"), msg.Content)
		t.mu.Lock()
		t.waitingResp = false
		t.mu.Unlock()
		return nil
	}

	// Check for tool call markers
	content := msg.Content

	// Tool call start
	if strings.HasPrefix(content, "\n╭─ ") {
		t.mu.Lock()
		t.inToolCall = true
		// Extract tool name
		parts := strings.SplitN(content, "\n", 2)
		if len(parts) > 0 {
			t.currentTool = strings.TrimPrefix(parts[0], "\n╭─ ")
		}
		t.toolOutput.Reset()
		t.mu.Unlock()

		fmt.Printf("\n  %s %s\n",
			toolHeaderStyle.Render("⚡"),
			toolHeaderStyle.Render(t.currentTool))
		return nil
	}

	// Tool output lines
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
					fmt.Printf("  %s %s\n",
						mutedStyle.Render("│"),
						toolOutputStyle.Render(line))
				}
			}
			return nil
		}
	}

	// Tool call end
	if strings.HasPrefix(content, "╰─") {
		t.mu.Lock()
		t.inToolCall = false
		t.currentTool = ""
		t.mu.Unlock()

		fmt.Printf("  %s\n", mutedStyle.Render("╰─"))
		return nil
	}

	if msg.IsPartial {
		// Streaming text - print directly
		if !t.inToolCall {
			// First chunk, add newline and indent
			if !strings.HasPrefix(content, "\n") && !strings.HasPrefix(content, " ") {
				fmt.Print("\n  ")
			}
			fmt.Print(assistantStyle.Render(content))
		}
		return nil
	}

	if msg.IsDone {
		fmt.Println()
		fmt.Println()
		// Show clear separator and ready indicator
		fmt.Println(mutedStyle.Render("  ─────────────────────────────────────"))
		fmt.Println()
		t.mu.Lock()
		t.waitingResp = false
		t.mu.Unlock()
		return nil
	}

	// Complete message
	fmt.Printf("\n  %s\n", assistantStyle.Render(msg.Content))
	t.mu.Lock()
	t.waitingResp = false
	t.mu.Unlock()
	return nil
}

func (t *StyledTerminal) Receive() <-chan *Message {
	return t.messages
}

func (t *StyledTerminal) Stop() error {
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

func (t *StyledTerminal) Done() <-chan struct{} {
	return t.done
}
