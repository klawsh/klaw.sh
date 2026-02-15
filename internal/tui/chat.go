package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	// Colors for chat
	chatPurple    = lipgloss.Color("#A855F7")
	chatGreen     = lipgloss.Color("#22C55E")
	chatYellow    = lipgloss.Color("#FBBF24")
	chatRed       = lipgloss.Color("#EF4444")
	chatGray      = lipgloss.Color("#6B7280")
	chatDarkGray  = lipgloss.Color("#374151")
	chatLightGray = lipgloss.Color("#9CA3AF")
	chatWhite     = lipgloss.Color("#F9FAFB")

	// Styles for chat
	chatTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(chatPurple).
			MarginBottom(1)

	chatUserMsgStyle = lipgloss.NewStyle().
			Foreground(chatWhite).
			Background(chatPurple).
			Padding(0, 1).
			MarginTop(1)

	chatUserLabelStyle = lipgloss.NewStyle().
			Foreground(chatPurple).
			Bold(true)

	chatAssistantLabelStyle = lipgloss.NewStyle().
				Foreground(chatGreen).
				Bold(true)

	chatAssistantMsgStyle = lipgloss.NewStyle().
				Foreground(chatWhite).
				MarginTop(1)

	chatToolStyle = lipgloss.NewStyle().
			Foreground(chatYellow).
			Bold(true)

	chatToolOutputStyle = lipgloss.NewStyle().
			Foreground(chatLightGray).
			Background(chatDarkGray).
			Padding(0, 1)

	chatErrorMsgStyle = lipgloss.NewStyle().
			Foreground(chatRed).
			Bold(true)

	chatInputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(chatPurple).
			Padding(0, 1)

	chatInputBoxFocusedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(chatGreen).
				Padding(0, 1)

	chatStatusStyle = lipgloss.NewStyle().
			Foreground(chatGray).
			MarginTop(1)

	chatHelpStyle = lipgloss.NewStyle().
			Foreground(chatGray)
)

// Message types for chat
type ChatMessage struct {
	Role    string // "user", "assistant", "tool", "error"
	Content string
	Tool    string // tool name if Role == "tool"
}

// ChatModel is the bubbletea model for chat UI
type ChatModel struct {
	// UI components
	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model

	// State
	messages    []ChatMessage
	thinking    bool
	width       int
	height      int
	ready       bool
	err         error

	// Channel for sending user input
	inputChan chan<- string
	// Channel for receiving assistant output
	outputChan <-chan ChatMessage
	// Context for cancellation
	ctx    context.Context
	cancel context.CancelFunc
}

// Messages
type thinkingMsg struct{}
type responseMsg ChatMessage
type chatDoneMsg struct{}
type chatErrMsg struct{ err error }

// NewChatModel creates a new chat TUI model
func NewChatModel(inputChan chan<- string, outputChan <-chan ChatMessage) ChatModel {
	// Text area for input
	ta := textarea.New()
	ta.Placeholder = "Type your message..."
	ta.Focus()
	ta.CharLimit = 4000
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false) // Enter sends message

	// Spinner for thinking
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(chatPurple)

	// Viewport for messages
	vp := viewport.New(80, 20)

	ctx, cancel := context.WithCancel(context.Background())

	return ChatModel{
		textarea:   ta,
		viewport:   vp,
		spinner:    sp,
		messages:   []ChatMessage{},
		inputChan:  inputChan,
		outputChan: outputChan,
		ctx:        ctx,
		cancel:     cancel,
	}
}

func (m ChatModel) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		m.waitForResponse(),
	)
}

func (m ChatModel) waitForResponse() tea.Cmd {
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
			return chatDoneMsg{}
		case msg, ok := <-m.outputChan:
			if !ok {
				return chatDoneMsg{}
			}
			return responseMsg(msg)
		}
	}
}

func (m ChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.cancel()
			return m, tea.Quit

		case tea.KeyEnter:
			if !m.thinking {
				input := strings.TrimSpace(m.textarea.Value())
				if input != "" {
					// Add user message
					m.messages = append(m.messages, ChatMessage{
						Role:    "user",
						Content: input,
					})
					m.textarea.Reset()
					m.thinking = true
					m.updateViewport()

					// Send to channel
					go func() {
						m.inputChan <- input
					}()
				}
			}
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		headerHeight := 3
		inputHeight := 5
		helpHeight := 2
		viewportHeight := m.height - headerHeight - inputHeight - helpHeight - 2

		if !m.ready {
			m.viewport = viewport.New(m.width-2, viewportHeight)
			m.viewport.YPosition = headerHeight
			m.ready = true
		} else {
			m.viewport.Width = m.width - 2
			m.viewport.Height = viewportHeight
		}

		m.textarea.SetWidth(m.width - 4)
		m.updateViewport()

	case responseMsg:
		chatMsg := ChatMessage(msg)
		m.messages = append(m.messages, chatMsg)

		if chatMsg.Role == "done" {
			m.thinking = false
		}

		m.updateViewport()
		cmds = append(cmds, m.waitForResponse())

	case chatDoneMsg:
		m.thinking = false

	case spinner.TickMsg:
		if m.thinking {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case chatErrMsg:
		m.err = msg.err
		m.thinking = false
	}

	// Update textarea
	if !m.thinking {
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *ChatModel) updateViewport() {
	var content strings.Builder

	for _, msg := range m.messages {
		switch msg.Role {
		case "user":
			content.WriteString(chatUserLabelStyle.Render("You") + "\n")
			content.WriteString(chatUserMsgStyle.Render(msg.Content) + "\n\n")

		case "assistant":
			content.WriteString(chatAssistantLabelStyle.Render("klaw") + "\n")
			content.WriteString(chatAssistantMsgStyle.Render(msg.Content) + "\n\n")

		case "tool":
			content.WriteString(chatToolStyle.Render("⚡ "+msg.Tool) + "\n")
			if msg.Content != "" {
				lines := strings.Split(msg.Content, "\n")
				for _, line := range lines {
					content.WriteString(chatToolOutputStyle.Render(line) + "\n")
				}
			}
			content.WriteString("\n")

		case "error":
			content.WriteString(chatErrorMsgStyle.Render("Error: "+msg.Content) + "\n\n")

		case "done":
			// Just marks end of response, don't show
		}
	}

	m.viewport.SetContent(content.String())
	m.viewport.GotoBottom()
}

func (m ChatModel) View() string {
	if !m.ready {
		return "Loading..."
	}

	var b strings.Builder

	// Header
	header := chatTitleStyle.Render("klaw") + "  " + chatStatusStyle.Render("AI Employee for Everyone")
	b.WriteString(header + "\n")
	b.WriteString(strings.Repeat("─", m.width-2) + "\n")

	// Messages viewport
	b.WriteString(m.viewport.View() + "\n")

	// Thinking indicator
	if m.thinking {
		b.WriteString(m.spinner.View() + " " + chatStatusStyle.Render("Thinking...") + "\n")
	} else {
		b.WriteString("\n")
	}

	// Input area
	b.WriteString(strings.Repeat("─", m.width-2) + "\n")

	inputStyle := chatInputBoxStyle
	if !m.thinking {
		inputStyle = chatInputBoxFocusedStyle
	}
	b.WriteString(inputStyle.Render(m.textarea.View()) + "\n")

	// Help
	help := chatHelpStyle.Render("Enter to send • Esc to quit")
	b.WriteString(help)

	return b.String()
}

// RunChat starts the chat TUI
func RunChat(inputChan chan<- string, outputChan <-chan ChatMessage) error {
	model := NewChatModel(inputChan, outputChan)
	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
