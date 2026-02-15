package channel

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

// AgentManager handles agent CRUD operations.
type AgentManager interface {
	CreateAgent(name, description, model string, tools, skills, triggers []string) error
	ListAgents() ([]AgentInfo, error)
	DeleteAgent(name string) error
	GetAgent(name string) (*AgentInfo, error)
}

// AgentInfo holds basic agent information.
type AgentInfo struct {
	Name        string
	Description string
	Model       string
	Tools       []string
	Skills      []string
	Triggers    []string
}

// ThreadHistory stores conversation history for a thread
type ThreadHistory struct {
	Messages   []ThreadMessage
	LastActive time.Time
}

// ThreadMessage represents a message in thread history
type ThreadMessage struct {
	Role    string // "user" or "assistant"
	Content string
	User    string
}

// SlackChannel integrates with Slack via Socket Mode.
type SlackChannel struct {
	client       *slack.Client
	socketClient *socketmode.Client
	botUserID    string

	messages chan *Message
	done     chan struct{}

	mu      sync.Mutex
	started bool

	// Track conversations
	currentChannel string
	currentTS      string // thread timestamp for current response

	// Track ALL threads where bot was mentioned (channel:thread_ts -> history)
	activeThreads map[string]*ThreadHistory

	// Buffer for streaming
	streamBuffer  strings.Builder
	lastMessageTS string

	// Agent management
	agentManager AgentManager
}

// SlackConfig holds Slack configuration.
type SlackConfig struct {
	BotToken string // xoxb-...
	AppToken string // xapp-...
}

// NewSlackChannel creates a new Slack channel.
func NewSlackChannel(cfg SlackConfig) (*SlackChannel, error) {
	if cfg.BotToken == "" || cfg.AppToken == "" {
		return nil, fmt.Errorf("both bot token and app token are required")
	}

	client := slack.New(
		cfg.BotToken,
		slack.OptionAppLevelToken(cfg.AppToken),
	)

	socketClient := socketmode.New(
		client,
		socketmode.OptionDebug(false),
	)

	// Get bot user ID
	authResp, err := client.AuthTest()
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate: %w", err)
	}

	return &SlackChannel{
		client:        client,
		socketClient:  socketClient,
		botUserID:     authResp.UserID,
		messages:      make(chan *Message, 10),
		done:          make(chan struct{}),
		activeThreads: make(map[string]*ThreadHistory),
	}, nil
}

func (s *SlackChannel) Name() string {
	return "slack"
}

// SetAgentManager sets the agent manager for CRUD operations.
func (s *SlackChannel) SetAgentManager(am AgentManager) {
	s.agentManager = am
}

func (s *SlackChannel) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = true
	s.mu.Unlock()

	// Handle socket events
	go s.handleEvents(ctx)

	// Run socket client
	go func() {
		if err := s.socketClient.Run(); err != nil {
			fmt.Printf("Slack socket error: %v\n", err)
		}
	}()

	// Cleanup old threads periodically (every hour, remove threads older than 24h)
	go s.cleanupOldThreads(ctx)

	return nil
}

func (s *SlackChannel) cleanupOldThreads(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	maxAge := 1 * time.Hour

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now()
			for key, history := range s.activeThreads {
				if now.Sub(history.LastActive) > maxAge {
					delete(s.activeThreads, key)
				}
			}
			s.mu.Unlock()
		}
	}
}

// buildContextFromHistory creates a context string from thread history
func (s *SlackChannel) buildContextFromHistory(history *ThreadHistory) string {
	if len(history.Messages) <= 1 {
		return "" // No previous context needed for first message
	}

	var sb strings.Builder
	sb.WriteString("Previous conversation in this thread:\n\n")

	// Include last 10 messages (excluding the current one which is the last)
	start := 0
	if len(history.Messages) > 11 {
		start = len(history.Messages) - 11
	}

	for i := start; i < len(history.Messages)-1; i++ {
		msg := history.Messages[i]
		if msg.Role == "user" {
			sb.WriteString(fmt.Sprintf("User: %s\n", msg.Content))
		} else {
			sb.WriteString(fmt.Sprintf("Assistant: %s\n", msg.Content))
		}
	}

	sb.WriteString("\nNow the user says:\n")
	return sb.String()
}

// addAssistantResponse adds an assistant response to thread history
func (s *SlackChannel) addAssistantResponse(threadKey, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if history, ok := s.activeThreads[threadKey]; ok {
		history.Messages = append(history.Messages, ThreadMessage{
			Role:    "assistant",
			Content: content,
		})
	}
}

func (s *SlackChannel) handleEvents(ctx context.Context) {
	fmt.Println("[slack] Event handler started, waiting for events...")
	for {
		select {
		case <-ctx.Done():
			fmt.Println("[slack] Context done, stopping event handler")
			return
		case <-s.done:
			fmt.Println("[slack] Done signal received, stopping event handler")
			return
		case evt := <-s.socketClient.Events:
			fmt.Printf("[slack] Received event: %s\n", evt.Type)
			switch evt.Type {
			case socketmode.EventTypeEventsAPI:
				eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
				if !ok {
					fmt.Println("[slack] Failed to cast EventsAPI event")
					continue
				}
				fmt.Printf("[slack] EventsAPI: %s\n", eventsAPIEvent.Type)
				s.socketClient.Ack(*evt.Request)
				s.handleEventsAPI(eventsAPIEvent)

			case socketmode.EventTypeSlashCommand:
				cmd, ok := evt.Data.(slack.SlashCommand)
				if !ok {
					fmt.Println("[slack] Failed to cast SlashCommand")
					continue
				}
				fmt.Printf("[slack] SlashCommand: %s %s\n", cmd.Command, cmd.Text)
				s.socketClient.Ack(*evt.Request)
				s.handleSlashCommand(cmd)

			case socketmode.EventTypeInteractive:
				callback, ok := evt.Data.(slack.InteractionCallback)
				if !ok {
					fmt.Println("[slack] Failed to cast InteractionCallback")
					continue
				}
				fmt.Printf("[slack] Interactive: %s\n", callback.Type)
				s.socketClient.Ack(*evt.Request)
				s.handleInteraction(callback)

			case socketmode.EventTypeConnecting:
				fmt.Println("[slack] Connecting to Slack...")

			case socketmode.EventTypeConnected:
				fmt.Println("[slack] Connected to Slack!")

			case socketmode.EventTypeConnectionError:
				fmt.Println("[slack] Connection error!")

			case socketmode.EventTypeHello:
				fmt.Println("[slack] Received hello from Slack")

			default:
				fmt.Printf("[slack] Unknown event type: %s\n", evt.Type)
			}
		}
	}
}

func (s *SlackChannel) handleEventsAPI(event slackevents.EventsAPIEvent) {
	switch event.Type {
	case slackevents.CallbackEvent:
		innerEvent := event.InnerEvent
		fmt.Printf("[slack] InnerEvent type: %T\n", innerEvent.Data)
		switch ev := innerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			fmt.Printf("[slack] AppMentionEvent received\n")
			s.handleMention(ev)
		case *slackevents.MessageEvent:
			fmt.Printf("[slack] MessageEvent received: subtype=%q, threadTS=%q, channel=%q\n", ev.SubType, ev.ThreadTimeStamp, ev.Channel)
			s.handleMessage(ev)
		default:
			fmt.Printf("[slack] Unhandled inner event type: %T\n", innerEvent.Data)
		}
	}
}

func (s *SlackChannel) handleMention(ev *slackevents.AppMentionEvent) {
	// Remove bot mention from text
	text := strings.TrimSpace(ev.Text)
	text = strings.ReplaceAll(text, fmt.Sprintf("<@%s>", s.botUserID), "")
	text = strings.TrimSpace(text)

	if text == "" {
		return
	}

	threadTS := ev.ThreadTimeStamp
	if threadTS == "" {
		threadTS = ev.TimeStamp // Start new thread
	}

	// Track this thread as active
	threadKey := fmt.Sprintf("%s:%s", ev.Channel, threadTS)
	fmt.Printf("[slack] handleMention: creating/updating thread key: %s\n", threadKey)

	s.mu.Lock()
	s.currentChannel = ev.Channel
	s.currentTS = threadTS

	// Create or update thread history
	if s.activeThreads[threadKey] == nil {
		fmt.Printf("[slack] Creating new thread history for: %s\n", threadKey)
		s.activeThreads[threadKey] = &ThreadHistory{
			Messages:   []ThreadMessage{},
			LastActive: time.Now(),
		}
	}
	history := s.activeThreads[threadKey]
	history.LastActive = time.Now()
	history.Messages = append(history.Messages, ThreadMessage{
		Role:    "user",
		Content: text,
		User:    ev.User,
	})

	// Build context from history
	contextMessages := s.buildContextFromHistory(history)
	s.mu.Unlock()

	// Send to agent with history context
	s.messages <- &Message{
		ID:        uuid.New().String(),
		Role:      "user",
		Content:   text,
		Timestamp: time.Now(),
		Metadata: map[string]any{
			"channel":   ev.Channel,
			"thread_ts": threadTS,
			"user":      ev.User,
			"history":   contextMessages,
		},
	}
}

func (s *SlackChannel) handleMessage(ev *slackevents.MessageEvent) {
	// Ignore bot messages
	if ev.BotID != "" || ev.User == s.botUserID {
		return
	}

	// Ignore messages without content
	text := strings.TrimSpace(ev.Text)
	if text == "" {
		return
	}

	fmt.Printf("[slack] handleMessage: text=%q, threadTS=%q, channelType=%q\n", text, ev.ThreadTimeStamp, ev.ChannelType)

	// Handle thread replies in channels (when replying to bot's thread)
	if ev.ThreadTimeStamp != "" && ev.ChannelType != "im" {
		// Check if this thread is one we're tracking
		threadKey := fmt.Sprintf("%s:%s", ev.Channel, ev.ThreadTimeStamp)
		fmt.Printf("[slack] Checking thread key: %s\n", threadKey)

		s.mu.Lock()
		history, isTrackedThread := s.activeThreads[threadKey]
		fmt.Printf("[slack] Thread tracked: %v, active threads: %d\n", isTrackedThread, len(s.activeThreads))
		if isTrackedThread {
			// Update thread activity and add message to history
			history.LastActive = time.Now()
			history.Messages = append(history.Messages, ThreadMessage{
				Role:    "user",
				Content: text,
				User:    ev.User,
			})
			// Set as current for response
			s.currentChannel = ev.Channel
			s.currentTS = ev.ThreadTimeStamp
			fmt.Printf("[slack] Added message to thread history, total messages: %d\n", len(history.Messages))
		}

		var contextMessages string
		if isTrackedThread {
			contextMessages = s.buildContextFromHistory(history)
			fmt.Printf("[slack] Built context: %d chars\n", len(contextMessages))
		}
		s.mu.Unlock()

		if !isTrackedThread {
			// Not a thread we're tracking, ignore
			fmt.Printf("[slack] Ignoring - not a tracked thread\n")
			return
		}

		// This is a reply in a tracked thread - process it with history
		s.messages <- &Message{
			ID:        uuid.New().String(),
			Role:      "user",
			Content:   text,
			Timestamp: time.Now(),
			Metadata: map[string]any{
				"channel":   ev.Channel,
				"thread_ts": ev.ThreadTimeStamp,
				"user":      ev.User,
				"is_reply":  true,
				"history":   contextMessages,
			},
		}
		return
	}

	// Handle DMs
	if ev.ChannelType == "im" {
		threadKey := fmt.Sprintf("%s:dm", ev.Channel)

		s.mu.Lock()
		s.currentChannel = ev.Channel
		s.currentTS = ev.ThreadTimeStamp

		// Track DM conversation
		if s.activeThreads[threadKey] == nil {
			s.activeThreads[threadKey] = &ThreadHistory{
				Messages:   []ThreadMessage{},
				LastActive: time.Now(),
			}
		}
		history := s.activeThreads[threadKey]
		history.LastActive = time.Now()
		history.Messages = append(history.Messages, ThreadMessage{
			Role:    "user",
			Content: text,
			User:    ev.User,
		})
		contextMessages := s.buildContextFromHistory(history)
		s.mu.Unlock()

		s.messages <- &Message{
			ID:        uuid.New().String(),
			Role:      "user",
			Content:   text,
			Timestamp: time.Now(),
			Metadata: map[string]any{
				"channel": ev.Channel,
				"user":    ev.User,
				"history": contextMessages,
			},
		}
	}
}

func (s *SlackChannel) handleSlashCommand(cmd slack.SlashCommand) {
	// Handle /klaw command
	if cmd.Command != "/klaw" {
		return
	}

	text := strings.TrimSpace(cmd.Text)
	parts := strings.Fields(text)

	// Handle management commands
	if len(parts) > 0 {
		switch parts[0] {
		case "help", "":
			s.sendHelp(cmd.ChannelID)
			return

		case "agents":
			s.listAgents(cmd.ChannelID)
			return

		case "spawn":
			// Quick command to create a new agent
			s.openCreateAgentModal(cmd.TriggerID)
			return

		case "create":
			if len(parts) > 1 && parts[1] == "agent" {
				s.openCreateAgentModal(cmd.TriggerID)
				return
			}

		case "delete":
			if len(parts) > 2 && parts[1] == "agent" {
				s.deleteAgent(cmd.ChannelID, parts[2])
				return
			}
		}
	}

	if text == "" {
		s.sendHelp(cmd.ChannelID)
		return
	}

	s.mu.Lock()
	s.currentChannel = cmd.ChannelID
	s.currentTS = ""
	s.mu.Unlock()

	s.messages <- &Message{
		ID:        uuid.New().String(),
		Role:      "user",
		Content:   text,
		Timestamp: time.Now(),
		Metadata: map[string]any{
			"channel": cmd.ChannelID,
			"user":    cmd.UserID,
		},
	}
}

func (s *SlackChannel) sendHelp(channelID string) {
	blocks := []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", "🤖 Klaw - AI Employee", true, false),
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "*Talk to agents:*\n`/klaw <message>` - Auto-route to best agent\n`/klaw @coder fix this bug` - Direct to specific agent", false, false),
			nil, nil,
		),
		slack.NewDividerBlock(),
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", "*Manage agents:*\n`/klaw spawn` - Create new agent (quick)\n`/klaw agents` - List all agents\n`/klaw delete agent <name>` - Delete agent", false, false),
			nil, nil,
		),
		slack.NewDividerBlock(),
		slack.NewActionBlock(
			"help_actions",
			slack.NewButtonBlockElement("create_agent_btn", "create_agent", slack.NewTextBlockObject("plain_text", "➕ Spawn Agent", true, false)).WithStyle(slack.StylePrimary),
			slack.NewButtonBlockElement("list_agents_btn", "list_agents", slack.NewTextBlockObject("plain_text", "📋 List Agents", true, false)),
		),
	}

	s.client.PostMessage(channelID, slack.MsgOptionBlocks(blocks...))
}

func (s *SlackChannel) listAgents(channelID string) {
	if s.agentManager == nil {
		s.client.PostMessage(channelID, slack.MsgOptionText("❌ Agent management not configured", false))
		return
	}

	agents, err := s.agentManager.ListAgents()
	if err != nil {
		s.client.PostMessage(channelID, slack.MsgOptionText(fmt.Sprintf("❌ Error: %v", err), false))
		return
	}

	if len(agents) == 0 {
		blocks := []slack.Block{
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", "No agents configured yet.", false, false),
				nil, nil,
			),
			slack.NewActionBlock(
				"no_agents_actions",
				slack.NewButtonBlockElement("create_agent_btn", "create_agent", slack.NewTextBlockObject("plain_text", "➕ Create Your First Agent", true, false)).WithStyle(slack.StylePrimary),
			),
		}
		s.client.PostMessage(channelID, slack.MsgOptionBlocks(blocks...))
		return
	}

	blocks := []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", "🤖 Your Agents", true, false),
		),
	}

	for _, ag := range agents {
		triggers := ""
		if len(ag.Triggers) > 0 {
			triggers = fmt.Sprintf("\n📌 Triggers: `%s`", strings.Join(ag.Triggers, "`, `"))
		}

		agentBlock := slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("*%s*\n%s%s\n_Model: %s_", ag.Name, ag.Description, triggers, ag.Model), false, false),
			nil,
			slack.NewAccessory(
				slack.NewOverflowBlockElement(
					fmt.Sprintf("agent_overflow_%s", ag.Name),
					slack.NewOptionBlockObject(
						fmt.Sprintf("edit_%s", ag.Name),
						slack.NewTextBlockObject("plain_text", "✏️ Edit", true, false),
						nil,
					),
					slack.NewOptionBlockObject(
						fmt.Sprintf("delete_%s", ag.Name),
						slack.NewTextBlockObject("plain_text", "🗑️ Delete", true, false),
						nil,
					),
				),
			),
		)
		blocks = append(blocks, agentBlock, slack.NewDividerBlock())
	}

	// Add create button at bottom
	blocks = append(blocks, slack.NewActionBlock(
		"agents_actions",
		slack.NewButtonBlockElement("create_agent_btn", "create_agent", slack.NewTextBlockObject("plain_text", "➕ Create Agent", true, false)).WithStyle(slack.StylePrimary),
	))

	s.client.PostMessage(channelID, slack.MsgOptionBlocks(blocks...))
}

func (s *SlackChannel) openCreateAgentModal(triggerID string) {
	// Model options
	modelOptions := []*slack.OptionBlockObject{
		slack.NewOptionBlockObject("claude-sonnet-4-20250514", slack.NewTextBlockObject("plain_text", "Claude Sonnet 4 (Recommended)", true, false), nil),
		slack.NewOptionBlockObject("claude-opus-4-20250514", slack.NewTextBlockObject("plain_text", "Claude Opus 4 (Most capable)", true, false), nil),
		slack.NewOptionBlockObject("claude-haiku-4-20250514", slack.NewTextBlockObject("plain_text", "Claude Haiku 4 (Fastest)", true, false), nil),
	}

	// Tool options
	toolOptions := []*slack.OptionBlockObject{
		slack.NewOptionBlockObject("bash", slack.NewTextBlockObject("plain_text", "🖥️ Bash - Run commands", true, false), nil),
		slack.NewOptionBlockObject("read", slack.NewTextBlockObject("plain_text", "📖 Read - Read files", true, false), nil),
		slack.NewOptionBlockObject("write", slack.NewTextBlockObject("plain_text", "✍️ Write - Write files", true, false), nil),
		slack.NewOptionBlockObject("edit", slack.NewTextBlockObject("plain_text", "📝 Edit - Edit files", true, false), nil),
		slack.NewOptionBlockObject("glob", slack.NewTextBlockObject("plain_text", "🔍 Glob - Find files", true, false), nil),
		slack.NewOptionBlockObject("grep", slack.NewTextBlockObject("plain_text", "🔎 Grep - Search content", true, false), nil),
	}

	// Default selected tools
	defaultTools := []*slack.OptionBlockObject{
		slack.NewOptionBlockObject("bash", slack.NewTextBlockObject("plain_text", "🖥️ Bash - Run commands", true, false), nil),
		slack.NewOptionBlockObject("read", slack.NewTextBlockObject("plain_text", "📖 Read - Read files", true, false), nil),
		slack.NewOptionBlockObject("write", slack.NewTextBlockObject("plain_text", "✍️ Write - Write files", true, false), nil),
		slack.NewOptionBlockObject("edit", slack.NewTextBlockObject("plain_text", "📝 Edit - Edit files", true, false), nil),
	}

	// Skill options
	skillOptions := []*slack.OptionBlockObject{
		slack.NewOptionBlockObject("web-search", slack.NewTextBlockObject("plain_text", "🔍 Web Search", true, false), nil),
		slack.NewOptionBlockObject("browser", slack.NewTextBlockObject("plain_text", "🌐 Browser", true, false), nil),
		slack.NewOptionBlockObject("code-exec", slack.NewTextBlockObject("plain_text", "💻 Code Execution", true, false), nil),
		slack.NewOptionBlockObject("git", slack.NewTextBlockObject("plain_text", "📦 Git", true, false), nil),
		slack.NewOptionBlockObject("docker", slack.NewTextBlockObject("plain_text", "🐳 Docker", true, false), nil),
		slack.NewOptionBlockObject("api", slack.NewTextBlockObject("plain_text", "🔌 API Requests", true, false), nil),
		slack.NewOptionBlockObject("database", slack.NewTextBlockObject("plain_text", "🗄️ Database", true, false), nil),
		slack.NewOptionBlockObject("slack", slack.NewTextBlockObject("plain_text", "💬 Slack", true, false), nil),
		slack.NewOptionBlockObject("email", slack.NewTextBlockObject("plain_text", "📧 Email", true, false), nil),
		slack.NewOptionBlockObject("calendar", slack.NewTextBlockObject("plain_text", "📅 Calendar", true, false), nil),
	}

	modalRequest := slack.ModalViewRequest{
		Type:       slack.ViewType("modal"),
		CallbackID: "create_agent_modal",
		Title:      slack.NewTextBlockObject("plain_text", "Spawn Agent", true, false),
		Submit:     slack.NewTextBlockObject("plain_text", "Create", true, false),
		Close:      slack.NewTextBlockObject("plain_text", "Cancel", true, false),
		Blocks: slack.Blocks{
			BlockSet: []slack.Block{
				slack.NewSectionBlock(
					slack.NewTextBlockObject("mrkdwn", "🤖 *Spawn a new AI agent*\nConfigure your agent with skills and capabilities.", false, false),
					nil, nil,
				),
				slack.NewDividerBlock(),
				slack.NewInputBlock(
					"agent_name",
					slack.NewTextBlockObject("plain_text", "Agent Name", true, false),
					slack.NewTextBlockObject("plain_text", "e.g., coder, researcher, writer", true, false),
					slack.NewPlainTextInputBlockElement(slack.NewTextBlockObject("plain_text", "coder", true, false), "name_input"),
				),
				slack.NewInputBlock(
					"agent_description",
					slack.NewTextBlockObject("plain_text", "Description", true, false),
					slack.NewTextBlockObject("plain_text", "What does this agent do?", true, false),
					slack.NewPlainTextInputBlockElement(slack.NewTextBlockObject("plain_text", "Writes and reviews code", true, false), "description_input"),
				),
				slack.NewInputBlock(
					"agent_triggers",
					slack.NewTextBlockObject("plain_text", "Trigger Keywords (optional)", true, false),
					slack.NewTextBlockObject("plain_text", "Keywords that auto-route messages to this agent", true, false),
					slack.NewPlainTextInputBlockElement(slack.NewTextBlockObject("plain_text", "code, fix, bug, implement", true, false), "triggers_input"),
				).WithOptional(true),
				slack.NewDividerBlock(),
				slack.NewSectionBlock(
					slack.NewTextBlockObject("mrkdwn", "*⚡ Skills*\nSpecial capabilities for your agent", false, false),
					nil, nil,
				),
				&slack.InputBlock{
					Type:     slack.MBTInput,
					BlockID:  "agent_skills",
					Label:    slack.NewTextBlockObject("plain_text", "Select Skills", true, false),
					Optional: true,
					Element: &slack.CheckboxGroupsBlockElement{
						Type:     slack.METCheckboxGroups,
						ActionID: "skills_select",
						Options:  skillOptions,
					},
				},
				slack.NewDividerBlock(),
				slack.NewSectionBlock(
					slack.NewTextBlockObject("mrkdwn", "*⚙️ Configuration*", false, false),
					nil, nil,
				),
				slack.NewInputBlock(
					"agent_model",
					slack.NewTextBlockObject("plain_text", "Model", true, false),
					nil,
					slack.NewOptionsSelectBlockElement(
						slack.OptTypeStatic,
						slack.NewTextBlockObject("plain_text", "Select model", true, false),
						"model_select",
						modelOptions...,
					).WithInitialOption(modelOptions[0]),
				),
				&slack.InputBlock{
					Type:    slack.MBTInput,
					BlockID: "agent_tools",
					Label:   slack.NewTextBlockObject("plain_text", "Base Tools", true, false),
					Hint:    slack.NewTextBlockObject("plain_text", "Core tools the agent can use", true, false),
					Element: &slack.CheckboxGroupsBlockElement{
						Type:           slack.METCheckboxGroups,
						ActionID:       "tools_select",
						Options:        toolOptions,
						InitialOptions: defaultTools,
					},
				},
			},
		},
	}

	_, err := s.client.OpenView(triggerID, modalRequest)
	if err != nil {
		fmt.Printf("Error opening modal: %v\n", err)
	}
}

func (s *SlackChannel) handleInteraction(callback slack.InteractionCallback) {
	switch callback.Type {
	case slack.InteractionTypeBlockActions:
		s.handleBlockActions(callback)
	case slack.InteractionTypeViewSubmission:
		s.handleViewSubmission(callback)
	}
}

func (s *SlackChannel) handleBlockActions(callback slack.InteractionCallback) {
	for _, action := range callback.ActionCallback.BlockActions {
		switch action.ActionID {
		case "create_agent_btn":
			s.openCreateAgentModal(callback.TriggerID)

		case "list_agents_btn":
			s.listAgents(callback.Channel.ID)

		default:
			// Handle overflow menu actions
			if strings.HasPrefix(action.ActionID, "agent_overflow_") {
				selectedOption := action.SelectedOption.Value
				if strings.HasPrefix(selectedOption, "delete_") {
					agentName := strings.TrimPrefix(selectedOption, "delete_")
					s.confirmDeleteAgent(callback.TriggerID, agentName)
				} else if strings.HasPrefix(selectedOption, "edit_") {
					agentName := strings.TrimPrefix(selectedOption, "edit_")
					s.openEditAgentModal(callback.TriggerID, agentName)
				}
			} else if strings.HasPrefix(action.ActionID, "confirm_delete_") {
				agentName := strings.TrimPrefix(action.ActionID, "confirm_delete_")
				s.deleteAgent(callback.Channel.ID, agentName)
			}
		}
	}
}

func (s *SlackChannel) handleViewSubmission(callback slack.InteractionCallback) {
	switch callback.View.CallbackID {
	case "create_agent_modal":
		s.handleCreateAgentSubmission(callback)
	case "edit_agent_modal":
		s.handleEditAgentSubmission(callback)
	}
}

func (s *SlackChannel) handleCreateAgentSubmission(callback slack.InteractionCallback) {
	if s.agentManager == nil {
		return
	}

	values := callback.View.State.Values

	name := values["agent_name"]["name_input"].Value
	description := values["agent_description"]["description_input"].Value
	triggersRaw := values["agent_triggers"]["triggers_input"].Value
	model := values["agent_model"]["model_select"].SelectedOption.Value

	// Parse tools
	var tools []string
	for _, opt := range values["agent_tools"]["tools_select"].SelectedOptions {
		tools = append(tools, opt.Value)
	}

	// Parse skills
	var skills []string
	if skillsBlock, ok := values["agent_skills"]; ok {
		if skillsSelect, ok := skillsBlock["skills_select"]; ok {
			for _, opt := range skillsSelect.SelectedOptions {
				skills = append(skills, opt.Value)
			}
		}
	}

	// Parse triggers
	var triggers []string
	if triggersRaw != "" {
		for _, t := range strings.Split(triggersRaw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				triggers = append(triggers, t)
			}
		}
	}

	// Create agent
	err := s.agentManager.CreateAgent(name, description, model, tools, skills, triggers)

	// Find a channel to post response (use user's DM)
	channel, _, _, _ := s.client.OpenConversation(&slack.OpenConversationParameters{
		Users: []string{callback.User.ID},
	})

	if err != nil {
		if channel != nil {
			s.client.PostMessage(channel.ID, slack.MsgOptionText(fmt.Sprintf("❌ Failed to create agent: %v", err), false))
		}
		return
	}

	// Success message
	triggerText := ""
	if len(triggers) > 0 {
		triggerText = fmt.Sprintf("\n📌 Triggers: `%s`", strings.Join(triggers, "`, `"))
	}
	skillsText := ""
	if len(skills) > 0 {
		skillsText = fmt.Sprintf("\n⚡ Skills: `%s`", strings.Join(skills, "`, `"))
	}

	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("✅ *Agent created successfully!*\n\n*%s*\n%s%s%s\n_Model: %s_\n_Tools: %s_", name, description, skillsText, triggerText, model, strings.Join(tools, ", ")), false, false),
			nil, nil,
		),
		slack.NewDividerBlock(),
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("Try it out:\n`/klaw @%s <your message>`", name), false, false),
			nil, nil,
		),
	}

	if channel != nil {
		s.client.PostMessage(channel.ID, slack.MsgOptionBlocks(blocks...))
	}
}

func (s *SlackChannel) handleEditAgentSubmission(callback slack.InteractionCallback) {
	// Similar to create but updates existing agent
	// TODO: implement edit flow
}

func (s *SlackChannel) confirmDeleteAgent(triggerID, agentName string) {
	modalRequest := slack.ModalViewRequest{
		Type:       slack.ViewType("modal"),
		CallbackID: "confirm_delete_modal",
		Title:      slack.NewTextBlockObject("plain_text", "Delete Agent", true, false),
		Close:      slack.NewTextBlockObject("plain_text", "Cancel", true, false),
		Blocks: slack.Blocks{
			BlockSet: []slack.Block{
				slack.NewSectionBlock(
					slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("Are you sure you want to delete *%s*?\n\nThis action cannot be undone.", agentName), false, false),
					nil, nil,
				),
				slack.NewActionBlock(
					"delete_actions",
					slack.NewButtonBlockElement(
						fmt.Sprintf("confirm_delete_%s", agentName),
						agentName,
						slack.NewTextBlockObject("plain_text", "🗑️ Delete", true, false),
					).WithStyle(slack.StyleDanger),
				),
			},
		},
	}

	s.client.OpenView(triggerID, modalRequest)
}

func (s *SlackChannel) openEditAgentModal(triggerID, agentName string) {
	if s.agentManager == nil {
		return
	}

	agent, err := s.agentManager.GetAgent(agentName)
	if err != nil {
		return
	}

	// Similar to create modal but pre-filled with agent data
	// TODO: implement full edit modal
	_ = agent
}

func (s *SlackChannel) deleteAgent(channelID, agentName string) {
	if s.agentManager == nil {
		s.client.PostMessage(channelID, slack.MsgOptionText("❌ Agent management not configured", false))
		return
	}

	err := s.agentManager.DeleteAgent(agentName)
	if err != nil {
		s.client.PostMessage(channelID, slack.MsgOptionText(fmt.Sprintf("❌ Failed to delete agent: %v", err), false))
		return
	}

	s.client.PostMessage(channelID, slack.MsgOptionText(fmt.Sprintf("✅ Agent *%s* deleted successfully", agentName), false))
}

func (s *SlackChannel) Send(ctx context.Context, msg *Message) error {
	s.mu.Lock()
	channel := s.currentChannel
	threadTS := s.currentTS
	s.mu.Unlock()

	if channel == "" {
		return fmt.Errorf("no channel set")
	}

	if msg.Role == "error" {
		blocks := []slack.Block{
			slack.NewSectionBlock(
				slack.NewTextBlockObject("mrkdwn", fmt.Sprintf(":x: *Error*\n%s", msg.Content), false, false),
				nil, nil,
			),
		}
		s.client.PostMessage(
			channel,
			slack.MsgOptionBlocks(blocks...),
			slack.MsgOptionTS(threadTS),
		)
		return nil
	}

	if msg.Role != "assistant" {
		return nil
	}

	content := msg.Content

	// Skip tool output entirely for Slack - don't show raw tool results
	if strings.HasPrefix(content, "\n╭─ ") || strings.HasPrefix(content, "│ ") || strings.HasPrefix(content, "╰─") {
		// Tool output - skip in Slack (don't clutter the conversation)
		return nil
	}

	if msg.IsPartial {
		// Streaming text - buffer it
		s.mu.Lock()
		s.streamBuffer.WriteString(content)
		s.mu.Unlock()
		return nil
	}

	if msg.IsDone {
		// Send accumulated message
		s.mu.Lock()
		text := s.streamBuffer.String()
		s.streamBuffer.Reset()
		lastTS := s.lastMessageTS
		s.mu.Unlock()

		if text == "" {
			return nil
		}

		// Save to thread history
		threadKey := fmt.Sprintf("%s:%s", channel, threadTS)
		s.addAssistantResponse(threadKey, text)

		// Build blocks for Slack
		blocks := s.buildSlackBlocks(text)

		// Update existing message or post new
		if lastTS != "" {
			s.client.UpdateMessage(
				channel,
				lastTS,
				slack.MsgOptionBlocks(blocks...),
			)
		} else {
			_, ts, err := s.client.PostMessage(
				channel,
				slack.MsgOptionBlocks(blocks...),
				slack.MsgOptionTS(threadTS),
			)
			if err == nil {
				s.mu.Lock()
				s.lastMessageTS = ts
				s.mu.Unlock()
			}
		}

		s.mu.Lock()
		s.lastMessageTS = ""
		s.mu.Unlock()

		return nil
	}

	// Complete message - use blocks
	// Save to thread history
	threadKey := fmt.Sprintf("%s:%s", channel, threadTS)
	s.addAssistantResponse(threadKey, msg.Content)

	blocks := s.buildSlackBlocks(msg.Content)
	_, _, err := s.client.PostMessage(
		channel,
		slack.MsgOptionBlocks(blocks...),
		slack.MsgOptionTS(threadTS),
	)

	return err
}

func (s *SlackChannel) formatForSlack(text string) string {
	// Convert tool output format to Slack code blocks
	lines := strings.Split(text, "\n")
	var result []string
	inToolBlock := false

	for _, line := range lines {
		if strings.HasPrefix(line, "╭─ ") {
			// Tool header
			toolName := strings.TrimPrefix(line, "╭─ ")
			result = append(result, fmt.Sprintf("*:zap: %s*", toolName))
			result = append(result, "```")
			inToolBlock = true
		} else if strings.HasPrefix(line, "│ ") {
			// Tool output line
			result = append(result, strings.TrimPrefix(line, "│ "))
		} else if strings.HasPrefix(line, "╰─") {
			// Tool end
			if inToolBlock {
				result = append(result, "```")
				inToolBlock = false
			}
		} else {
			result = append(result, line)
		}
	}

	if inToolBlock {
		result = append(result, "```")
	}

	return strings.Join(result, "\n")
}

// buildSlackBlocks creates rich Slack blocks from text content
func (s *SlackChannel) buildSlackBlocks(text string) []slack.Block {
	var blocks []slack.Block

	// Parse the text into sections (tool outputs vs regular text)
	sections := s.parseContentSections(text)

	for _, section := range sections {
		switch section.Type {
		case "tool":
			// Skip tool outputs in Slack - keep it clean
			continue

		case "text":
			// Regular text as section block
			content := strings.TrimSpace(section.Content)
			if content == "" {
				continue
			}
			// Convert markdown-style code blocks
			content = s.convertMarkdownForSlack(content)
			// Truncate if too long
			if len(content) > 2900 {
				content = content[:2900] + "\n... (truncated)"
			}
			blocks = append(blocks,
				slack.NewSectionBlock(
					slack.NewTextBlockObject("mrkdwn", content, false, false),
					nil, nil,
				),
			)
		}
	}

	// If no blocks were created, add a simple text block
	if len(blocks) == 0 {
		content := strings.TrimSpace(text)
		// Strip any tool output markers from plain text too
		content = s.stripToolOutput(content)
		if len(content) > 2900 {
			content = content[:2900] + "\n... (truncated)"
		}
		if content != "" {
			blocks = append(blocks,
				slack.NewSectionBlock(
					slack.NewTextBlockObject("mrkdwn", content, false, false),
					nil, nil,
				),
			)
		}
	}

	return blocks
}

// stripToolOutput removes tool output markers from text
func (s *SlackChannel) stripToolOutput(text string) string {
	lines := strings.Split(text, "\n")
	var result []string
	inTool := false

	for _, line := range lines {
		if strings.HasPrefix(line, "╭─ ") {
			inTool = true
			continue
		}
		if strings.HasPrefix(line, "╰─") {
			inTool = false
			continue
		}
		if strings.HasPrefix(line, "│ ") {
			continue // Skip tool output lines
		}
		if !inTool {
			result = append(result, line)
		}
	}

	return strings.TrimSpace(strings.Join(result, "\n"))
}

type contentSection struct {
	Type     string // "tool" or "text"
	ToolName string
	Content  string
}

func (s *SlackChannel) parseContentSections(text string) []contentSection {
	var sections []contentSection
	lines := strings.Split(text, "\n")

	var currentText strings.Builder
	var currentTool strings.Builder
	var toolName string
	inTool := false

	for _, line := range lines {
		if strings.HasPrefix(line, "╭─ ") {
			// Flush any accumulated text
			if currentText.Len() > 0 {
				sections = append(sections, contentSection{
					Type:    "text",
					Content: currentText.String(),
				})
				currentText.Reset()
			}
			// Start new tool section
			toolName = strings.TrimPrefix(line, "╭─ ")
			inTool = true
			currentTool.Reset()
		} else if strings.HasPrefix(line, "│ ") && inTool {
			currentTool.WriteString(strings.TrimPrefix(line, "│ "))
			currentTool.WriteString("\n")
		} else if strings.HasPrefix(line, "╰─") && inTool {
			// End tool section
			sections = append(sections, contentSection{
				Type:     "tool",
				ToolName: toolName,
				Content:  strings.TrimSpace(currentTool.String()),
			})
			currentTool.Reset()
			inTool = false
			toolName = ""
		} else {
			if inTool {
				currentTool.WriteString(line)
				currentTool.WriteString("\n")
			} else {
				currentText.WriteString(line)
				currentText.WriteString("\n")
			}
		}
	}

	// Flush remaining content
	if currentText.Len() > 0 {
		sections = append(sections, contentSection{
			Type:    "text",
			Content: currentText.String(),
		})
	}
	if inTool && currentTool.Len() > 0 {
		sections = append(sections, contentSection{
			Type:     "tool",
			ToolName: toolName,
			Content:  strings.TrimSpace(currentTool.String()),
		})
	}

	return sections
}

func (s *SlackChannel) convertMarkdownForSlack(text string) string {
	// Convert GitHub-flavored markdown to Slack mrkdwn

	// Headers: ## -> *bold*
	text = strings.ReplaceAll(text, "### ", "*")
	text = strings.ReplaceAll(text, "## ", "*")
	text = strings.ReplaceAll(text, "# ", "*")

	// Inline code is the same: `code`
	// Code blocks: ```lang -> ``` (Slack doesn't support language hints)
	// Already compatible

	// Bold: **text** -> *text*
	// Slack uses single asterisks for bold
	// This is a simple replacement that might not handle all edge cases
	boldRe := strings.NewReplacer("**", "*")
	text = boldRe.Replace(text)

	// Italic: _text_ -> _text_ (same)

	// Links: [text](url) -> <url|text>
	// Simple regex replacement
	linkStart := 0
	for {
		start := strings.Index(text[linkStart:], "[")
		if start == -1 {
			break
		}
		start += linkStart
		mid := strings.Index(text[start:], "](")
		if mid == -1 {
			linkStart = start + 1
			continue
		}
		mid += start
		end := strings.Index(text[mid:], ")")
		if end == -1 {
			linkStart = start + 1
			continue
		}
		end += mid

		linkText := text[start+1 : mid]
		linkURL := text[mid+2 : end]
		slackLink := fmt.Sprintf("<%s|%s>", linkURL, linkText)
		text = text[:start] + slackLink + text[end+1:]
		linkStart = start + len(slackLink)
	}

	return text
}

func (s *SlackChannel) Receive() <-chan *Message {
	return s.messages
}

func (s *SlackChannel) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return nil
	}

	select {
	case <-s.done:
	default:
		close(s.done)
	}

	return nil
}

func (s *SlackChannel) Done() <-chan struct{} {
	return s.done
}

// ChannelMessage represents a message from Slack channel history
type ChannelMessage struct {
	User      string
	Text      string
	Timestamp time.Time
	SlackTS   string // Original Slack timestamp for threading
}

// GetChannelHistory retrieves recent messages from a Slack channel
func (s *SlackChannel) GetChannelHistory(channelID string, since time.Time, limit int) ([]ChannelMessage, error) {
	if limit <= 0 {
		limit = 50
	}

	// First try without oldest filter to see if we can read at all
	params := &slack.GetConversationHistoryParameters{
		ChannelID: channelID,
		Limit:     limit,
	}

	fmt.Printf("  [DEBUG] GetChannelHistory: channel=%s, since=%s\n", channelID, since.Format("15:04:05"))

	history, err := s.client.GetConversationHistory(params)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel history: %w", err)
	}

	fmt.Printf("  [DEBUG] Slack API returned %d total messages\n", len(history.Messages))

	var messages []ChannelMessage
	sinceUnix := since.Unix()
	for _, msg := range history.Messages {
		// Parse timestamp
		msgTs, _ := parseSlackTimestamp(msg.Timestamp)
		msgUnix := msgTs.Unix()

		// Debug all messages
		textPreview := msg.Text
		if len(textPreview) > 40 {
			textPreview = textPreview[:40]
		}
		fmt.Printf("  [DEBUG] msg: ts=%d user=%s bot=%q text=%q\n", msgUnix, msg.User, msg.BotID, textPreview)

		// Filter by time
		if msgUnix < sinceUnix {
			fmt.Printf("  [DEBUG]   ^ skipped (before since=%d)\n", sinceUnix)
			continue
		}

		// Skip bot messages
		if msg.BotID != "" || msg.User == s.botUserID {
			fmt.Printf("  [DEBUG]   ^ skipped (bot message)\n")
			continue
		}

		messages = append(messages, ChannelMessage{
			User:      msg.User,
			Text:      msg.Text,
			Timestamp: msgTs,
			SlackTS:   msg.Timestamp,
		})
	}

	// Reverse to get chronological order (oldest first)
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

// PostMessage posts a message to a Slack channel
func (s *SlackChannel) PostMessage(channelID, text string) error {
	_, _, err := s.client.PostMessage(channelID, slack.MsgOptionText(text, false))
	return err
}

// PostThreadReply posts a message as a thread reply
func (s *SlackChannel) PostThreadReply(channelID, threadTS, text string) error {
	_, _, err := s.client.PostMessage(channelID, slack.MsgOptionText(text, false), slack.MsgOptionTS(threadTS))
	return err
}

// HasBotReply checks if a message already has a reply from the bot
func (s *SlackChannel) HasBotReply(channelID, messageTS string) bool {
	// Get thread replies
	msgs, _, _, err := s.client.GetConversationReplies(&slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: messageTS,
		Limit:     50,
	})
	if err != nil {
		return false
	}

	// Check if any reply is from the bot
	for _, msg := range msgs {
		// Skip the parent message itself
		if msg.Timestamp == messageTS {
			continue
		}
		// Check if this is a bot message
		if msg.BotID != "" || msg.User == s.botUserID {
			return true
		}
	}
	return false
}

// parseSlackTimestamp converts Slack's timestamp format to time.Time
func parseSlackTimestamp(ts string) (time.Time, error) {
	parts := strings.Split(ts, ".")
	if len(parts) < 1 {
		return time.Time{}, fmt.Errorf("invalid timestamp")
	}

	secs := int64(0)
	fmt.Sscanf(parts[0], "%d", &secs)
	return time.Unix(secs, 0), nil
}
