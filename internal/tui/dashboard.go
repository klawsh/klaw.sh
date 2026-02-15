// Package tui provides a terminal user interface for klaw.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/controller"
	"github.com/eachlabs/klaw/internal/scheduler"
)

// Colors
var (
	purple    = lipgloss.Color("#7C3AED")
	green     = lipgloss.Color("#10B981")
	red       = lipgloss.Color("#EF4444")
	yellow    = lipgloss.Color("#F59E0B")
	blue      = lipgloss.Color("#3B82F6")
	gray      = lipgloss.Color("#6B7280")
	darkGray  = lipgloss.Color("#374151")
	white     = lipgloss.Color("#F9FAFB")
	black     = lipgloss.Color("#111827")
)

// Styles
var (
	// Logo/Title
	logoStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(white).
			Background(purple).
			Padding(0, 2).
			MarginBottom(1)

	// Sidebar
	sidebarStyle = lipgloss.NewStyle().
			Width(24).
			Height(100).
			Border(lipgloss.RoundedBorder(), false, true, false, false).
			BorderForeground(darkGray).
			Padding(1, 1)

	menuItemStyle = lipgloss.NewStyle().
			Padding(0, 1).
			MarginBottom(0)

	menuItemActiveStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(purple).
				Background(lipgloss.Color("#EDE9FE")).
				Padding(0, 1).
				MarginBottom(0)

	// Main content
	contentStyle = lipgloss.NewStyle().
			Padding(1, 2)

	// Cards
	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(darkGray).
			Padding(1, 2).
			MarginBottom(1)

	cardTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(white).
			MarginBottom(1)

	// Stats
	statValueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(purple).
			Width(6)

	statLabelStyle = lipgloss.NewStyle().
			Foreground(gray)

	// Status badges
	badgeActive = lipgloss.NewStyle().
			Foreground(green).
			Bold(true)

	badgeInactive = lipgloss.NewStyle().
			Foreground(gray)

	badgeError = lipgloss.NewStyle().
			Foreground(red).
			Bold(true)

	// Table
	tableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(gray).
				BorderStyle(lipgloss.NormalBorder()).
				BorderBottom(true).
				BorderForeground(darkGray)

	tableRowStyle = lipgloss.NewStyle().
			Padding(0, 1)

	tableRowSelectedStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#EDE9FE")).
				Foreground(purple).
				Bold(true).
				Padding(0, 1)

	// Help bar
	helpStyle = lipgloss.NewStyle().
			Foreground(gray).
			Background(darkGray).
			Padding(0, 2)

	// Form
	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(gray).
			Padding(0, 1)

	inputFocusedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(purple).
				Padding(0, 1)

	labelStyle = lipgloss.NewStyle().
			Foreground(gray).
			MarginBottom(0)
)

// Tab represents main navigation tabs
type Tab int

const (
	TabOverview Tab = iota
	TabNodes
	TabAgents
	TabJobs
	TabChannels
	TabSettings
)

func (t Tab) String() string {
	return []string{"Overview", "Nodes", "Agents", "Jobs", "Channels", "Settings"}[t]
}

func (t Tab) Icon() string {
	return []string{"📊", "🖥️", "🤖", "⏰", "📡", "⚙️"}[t]
}

// View mode within a tab
type ViewMode int

const (
	ViewList ViewMode = iota
	ViewCreate
	ViewEdit
	ViewDetail
)

// Message represents a conversation message
type ConversationMessage struct {
	Time      time.Time
	User      string
	Content   string
	Agent     string
	RoutedVia string // "manual", "keyword", "ai"
}

// Model is the main TUI model
type Model struct {
	// Navigation
	activeTab Tab
	viewMode  ViewMode

	// Data sources
	store         *cluster.Store
	ctrlStore     controller.Store
	scheduler     *scheduler.Scheduler
	clusterName   string
	namespace     string

	// Cached data
	agents      []*cluster.AgentBinding
	channels    []*cluster.ChannelBinding
	nodes       []*controller.Node
	jobs        []*scheduler.Job
	tasks       []*controller.Task

	// Channel logs for detail view
	channelLogs []*cluster.MessageLog

	// UI State
	width         int
	height        int
	loading       bool
	selectedIndex int
	logScrollPos  int
	err           error

	// Components
	spinner      spinner.Model
	viewport     viewport.Model
	inputs       []textinput.Model
	focusedInput int

	// Create agent form state
	formData map[string]string
}

// Messages
type tickMsg time.Time
type errMsg struct{ err error }
type dataLoadedMsg struct {
	agents   []*cluster.AgentBinding
	channels []*cluster.ChannelBinding
	nodes    []*controller.Node
	jobs     []*scheduler.Job
}

// NewDashboard creates a new dashboard
func NewDashboard(store *cluster.Store, sched *scheduler.Scheduler, clusterName, namespace string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(purple)

	vp := viewport.New(80, 20)

	return Model{
		activeTab:   TabOverview,
		viewMode:    ViewList,
		store:       store,
		scheduler:   sched,
		clusterName: clusterName,
		namespace:   namespace,
		loading:     true,
		spinner:     s,
		viewport:    vp,
		formData:    make(map[string]string),
	}
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.loadData(),
		tickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second*5, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) loadData() tea.Cmd {
	return func() tea.Msg {
		agents, _ := m.store.ListAgentBindings(m.clusterName, m.namespace)
		var nodes []*controller.Node
		var jobs []*scheduler.Job

		// Load nodes from controller if available
		if m.ctrlStore != nil {
			nodes, _ = m.ctrlStore.ListNodes(context.Background())
		}

		// Load jobs from scheduler if available
		if m.scheduler != nil {
			jobs = m.scheduler.ListJobs(m.clusterName, m.namespace)
		}
		channels, _ := m.store.ListChannelBindings(m.clusterName, m.namespace)
		return dataLoadedMsg{agents: agents, channels: channels, nodes: nodes, jobs: jobs}
	}
}

// Update handles messages
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle form mode separately
		if m.viewMode == ViewCreate || m.viewMode == ViewEdit {
			return m.updateForm(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit

		case "1", "2", "3", "4", "5":
			m.activeTab = Tab(int(msg.String()[0] - '1'))
			m.selectedIndex = 0
			m.viewMode = ViewList

		case "tab":
			m.activeTab = (m.activeTab + 1) % 5
			m.selectedIndex = 0
			m.viewMode = ViewList

		case "shift+tab":
			m.activeTab = (m.activeTab + 4) % 5
			m.selectedIndex = 0
			m.viewMode = ViewList

		case "up", "k":
			if m.viewMode == ViewDetail && m.activeTab == TabChannels {
				// Scroll logs up
				if m.logScrollPos > 0 {
					m.logScrollPos--
				}
			} else if m.selectedIndex > 0 {
				m.selectedIndex--
			}

		case "down", "j":
			if m.viewMode == ViewDetail && m.activeTab == TabChannels {
				// Scroll logs down
				maxScroll := len(m.channelLogs) - 15
				if maxScroll < 0 {
					maxScroll = 0
				}
				if m.logScrollPos < maxScroll {
					m.logScrollPos++
				}
			} else {
				m.selectedIndex++
				// Clamp to list size
				maxIdx := m.getMaxIndex()
				if m.selectedIndex >= maxIdx {
					m.selectedIndex = maxIdx - 1
				}
				if m.selectedIndex < 0 {
					m.selectedIndex = 0
				}
			}

		case "enter":
			return m.handleEnter()

		case "n", "c":
			// New/Create
			if m.activeTab == TabAgents {
				m.viewMode = ViewCreate
				m.initCreateAgentForm()
			}

		case "d":
			// Delete
			return m.handleDelete()

		case "s":
			// Toggle status (for channels)
			return m.handleToggleStatus()

		case "r":
			// Refresh
			return m, m.loadData()

		case "esc":
			m.viewMode = ViewList
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width - 30
		m.viewport.Height = msg.Height - 10

	case tickMsg:
		return m, tea.Batch(m.loadData(), tickCmd())

	case dataLoadedMsg:
		m.loading = false
		m.agents = msg.agents
		m.channels = msg.channels
		m.nodes = msg.nodes
		m.jobs = msg.jobs

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	case errMsg:
		m.err = msg.err
	}

	return m, tea.Batch(cmds...)
}

func (m Model) getMaxIndex() int {
	switch m.activeTab {
	case TabAgents:
		return len(m.agents)
	case TabChannels:
		return len(m.channels)
	case TabJobs:
		return len(m.jobs)
	case TabNodes:
		return len(m.nodes)
	default:
		return 0
	}
}

func (m *Model) initCreateAgentForm() {
	m.inputs = make([]textinput.Model, 4)

	// Name
	m.inputs[0] = textinput.New()
	m.inputs[0].Placeholder = "coder"
	m.inputs[0].Focus()
	m.inputs[0].Width = 30

	// Description
	m.inputs[1] = textinput.New()
	m.inputs[1].Placeholder = "Writes and reviews code"
	m.inputs[1].Width = 50

	// Triggers
	m.inputs[2] = textinput.New()
	m.inputs[2].Placeholder = "code, fix, bug, implement"
	m.inputs[2].Width = 50

	// Model
	m.inputs[3] = textinput.New()
	m.inputs[3].Placeholder = "claude-sonnet-4-20250514"
	m.inputs[3].SetValue("claude-sonnet-4-20250514")
	m.inputs[3].Width = 40

	m.focusedInput = 0
}

func (m Model) updateForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		m.viewMode = ViewList
		return m, nil

	case "tab", "down":
		m.focusedInput = (m.focusedInput + 1) % len(m.inputs)
		for i := range m.inputs {
			if i == m.focusedInput {
				m.inputs[i].Focus()
			} else {
				m.inputs[i].Blur()
			}
		}
		return m, nil

	case "shift+tab", "up":
		m.focusedInput = (m.focusedInput + len(m.inputs) - 1) % len(m.inputs)
		for i := range m.inputs {
			if i == m.focusedInput {
				m.inputs[i].Focus()
			} else {
				m.inputs[i].Blur()
			}
		}
		return m, nil

	case "enter":
		if m.focusedInput == len(m.inputs)-1 {
			// Submit form
			return m.submitAgentForm()
		}
		m.focusedInput++
		for i := range m.inputs {
			if i == m.focusedInput {
				m.inputs[i].Focus()
			} else {
				m.inputs[i].Blur()
			}
		}
		return m, nil
	}

	// Update focused input
	var cmd tea.Cmd
	m.inputs[m.focusedInput], cmd = m.inputs[m.focusedInput].Update(msg)
	return m, cmd
}

func (m Model) submitAgentForm() (tea.Model, tea.Cmd) {
	name := m.inputs[0].Value()
	description := m.inputs[1].Value()
	triggersRaw := m.inputs[2].Value()
	model := m.inputs[3].Value()

	if name == "" || description == "" {
		m.err = fmt.Errorf("name and description are required")
		return m, nil
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

	// Build system prompt
	systemPrompt := fmt.Sprintf("You are %s, an AI agent. Your role: %s\n\nBe helpful, concise, and take action when needed.", name, description)

	ab := &cluster.AgentBinding{
		Name:         name,
		Cluster:      m.clusterName,
		Namespace:    m.namespace,
		Description:  description,
		SystemPrompt: systemPrompt,
		Model:        model,
		Tools:        []string{"bash", "read", "write", "edit", "glob", "grep"},
		Triggers:     triggers,
	}

	if err := m.store.CreateAgentBinding(ab); err != nil {
		m.err = err
		return m, nil
	}

	m.viewMode = ViewList
	m.err = nil
	return m, m.loadData()
}

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	switch m.activeTab {
	case TabAgents:
		if m.selectedIndex < len(m.agents) {
			m.viewMode = ViewDetail
		}
	case TabChannels:
		if m.selectedIndex < len(m.channels) {
			ch := m.channels[m.selectedIndex]
			// Load channel logs
			logs, _ := m.store.GetMessageLogs(m.clusterName, m.namespace, ch.Name, 50)
			m.channelLogs = logs
			m.logScrollPos = 0
			m.viewMode = ViewDetail
		}
	}
	return m, nil
}

func (m Model) handleDelete() (tea.Model, tea.Cmd) {
	switch m.activeTab {
	case TabAgents:
		if m.selectedIndex < len(m.agents) {
			agent := m.agents[m.selectedIndex]
			m.store.DeleteAgentBinding(m.clusterName, m.namespace, agent.Name)
			return m, m.loadData()
		}
	case TabChannels:
		if m.selectedIndex < len(m.channels) {
			ch := m.channels[m.selectedIndex]
			m.store.DeleteChannelBinding(m.clusterName, m.namespace, ch.Name)
			return m, m.loadData()
		}
	}
	return m, nil
}

func (m Model) handleToggleStatus() (tea.Model, tea.Cmd) {
	if m.activeTab != TabChannels {
		return m, nil
	}

	if m.selectedIndex >= len(m.channels) {
		return m, nil
	}

	ch := m.channels[m.selectedIndex]
	newStatus := "active"
	if ch.Status == "active" {
		newStatus = "inactive"
	}

	m.store.UpdateChannelBindingStatus(m.clusterName, m.namespace, ch.Name, newStatus)
	return m, m.loadData()
}

// View renders the UI
func (m Model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	// Build layout
	sidebar := m.renderSidebar()
	content := m.renderContent()

	// Join sidebar and content
	main := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, content)

	// Add help bar at bottom
	help := m.renderHelp()

	return lipgloss.JoinVertical(lipgloss.Left, main, help)
}

func (m Model) renderSidebar() string {
	var items []string

	// Logo
	items = append(items, logoStyle.Render("⚡ klaw"))
	items = append(items, "")

	// Cluster info
	clusterInfo := lipgloss.NewStyle().Foreground(gray).Render(
		fmt.Sprintf("📦 %s/%s", m.clusterName, m.namespace))
	items = append(items, clusterInfo)
	items = append(items, "")

	// Menu items
	for i := TabOverview; i <= TabSettings; i++ {
		style := menuItemStyle
		if i == m.activeTab {
			style = menuItemActiveStyle
		}
		label := fmt.Sprintf("%s %s", i.Icon(), i.String())

		// Add counts
		switch i {
		case TabAgents:
			label += fmt.Sprintf(" (%d)", len(m.agents))
		case TabChannels:
			label += fmt.Sprintf(" (%d)", len(m.channels))
		}

		items = append(items, style.Render(label))
	}

	// Fill remaining space
	content := strings.Join(items, "\n")
	return sidebarStyle.Height(m.height - 2).Render(content)
}

func (m Model) renderContent() string {
	contentWidth := m.width - 26 // sidebar width + padding

	var content string
	switch m.viewMode {
	case ViewCreate:
		content = m.renderCreateForm()
	case ViewDetail:
		content = m.renderDetail()
	default:
		switch m.activeTab {
		case TabOverview:
			content = m.renderOverview(contentWidth)
		case TabNodes:
			content = m.renderNodes(contentWidth)
		case TabAgents:
			content = m.renderAgents(contentWidth)
		case TabJobs:
			content = m.renderJobs(contentWidth)
		case TabChannels:
			content = m.renderChannels(contentWidth)
		case TabSettings:
			content = m.renderSettings(contentWidth)
		}
	}

	return contentStyle.Width(contentWidth).Height(m.height - 2).Render(content)
}

func (m Model) renderOverview(width int) string {
	var sections []string

	// Title
	title := lipgloss.NewStyle().Bold(true).Foreground(white).Render("📊 Overview")
	sections = append(sections, title)
	sections = append(sections, "")

	// Stats cards
	activeChannels := 0
	for _, ch := range m.channels {
		if ch.Status == "active" {
			activeChannels++
		}
	}

	stats := []struct {
		value string
		label string
		color lipgloss.Color
	}{
		{fmt.Sprintf("%d", len(m.nodes)), "Nodes", purple},
		{fmt.Sprintf("%d", len(m.agents)), "Agents", blue},
		{fmt.Sprintf("%d", len(m.jobs)), "Jobs", green},
		{fmt.Sprintf("%d", len(m.channels)), "Channels", yellow},
	}

	var statCards []string
	for _, s := range stats {
		card := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(s.color).
			Padding(1, 3).
			Render(
				lipgloss.JoinVertical(lipgloss.Center,
					lipgloss.NewStyle().Bold(true).Foreground(s.color).Render(s.value),
					lipgloss.NewStyle().Foreground(gray).Render(s.label),
				),
			)
		statCards = append(statCards, card)
	}
	sections = append(sections, lipgloss.JoinHorizontal(lipgloss.Top, statCards...))
	sections = append(sections, "")

	// Agents quick list
	if len(m.agents) > 0 {
		agentSection := cardTitleStyle.Render("🤖 Agents")
		var agentList []string
		for i, ag := range m.agents {
			if i >= 5 {
				agentList = append(agentList, lipgloss.NewStyle().Foreground(gray).Render(fmt.Sprintf("  ... and %d more", len(m.agents)-5)))
				break
			}
			triggers := ""
			if len(ag.Triggers) > 0 {
				triggers = lipgloss.NewStyle().Foreground(gray).Render(fmt.Sprintf(" [%s]", strings.Join(ag.Triggers, ", ")))
			}
			agentList = append(agentList, fmt.Sprintf("  • %s%s", ag.Name, triggers))
		}
		sections = append(sections, agentSection)
		sections = append(sections, strings.Join(agentList, "\n"))
		sections = append(sections, "")
	}

	// Channels quick list
	if len(m.channels) > 0 {
		channelSection := cardTitleStyle.Render("📡 Channels")
		var channelList []string
		for _, ch := range m.channels {
			status := badgeInactive.Render("●")
			if ch.Status == "active" {
				status = badgeActive.Render("●")
			}
			channelList = append(channelList, fmt.Sprintf("  %s %s (%s)", status, ch.Name, ch.Type))
		}
		sections = append(sections, channelSection)
		sections = append(sections, strings.Join(channelList, "\n"))
	}

	return strings.Join(sections, "\n")
}

func (m Model) renderAgents(width int) string {
	var sections []string

	// Title
	title := lipgloss.NewStyle().Bold(true).Foreground(white).Render("🤖 Agents")
	sections = append(sections, title)
	sections = append(sections, "")

	if len(m.agents) == 0 {
		empty := cardStyle.Render("No agents configured.\n\nPress [n] to create your first agent.")
		sections = append(sections, empty)
		return strings.Join(sections, "\n")
	}

	// Table header
	header := tableHeaderStyle.Width(width - 4).Render(
		fmt.Sprintf("  %-15s %-30s %-20s %s", "NAME", "DESCRIPTION", "MODEL", "TRIGGERS"))
	sections = append(sections, header)

	// Rows
	for i, ag := range m.agents {
		style := tableRowStyle
		prefix := "  "
		if i == m.selectedIndex {
			style = tableRowSelectedStyle
			prefix = "→ "
		}

		desc := truncate(ag.Description, 28)
		model := truncate(ag.Model, 18)
		triggers := ""
		if len(ag.Triggers) > 0 {
			triggers = truncate(strings.Join(ag.Triggers, ","), 15)
		}

		row := style.Render(fmt.Sprintf("%s%-13s %-30s %-20s %s",
			prefix, ag.Name, desc, model, triggers))
		sections = append(sections, row)
	}

	return strings.Join(sections, "\n")
}

func (m Model) renderChannels(width int) string {
	var sections []string

	title := lipgloss.NewStyle().Bold(true).Foreground(white).Render("📡 Channels")
	sections = append(sections, title)
	sections = append(sections, "")

	if len(m.channels) == 0 {
		empty := cardStyle.Render("No channels configured.\n\nCreate one with:\n  klaw create channel slack --name bot --bot-token ... --app-token ...")
		sections = append(sections, empty)
		return strings.Join(sections, "\n")
	}

	// Table header
	header := tableHeaderStyle.Width(width - 4).Render(
		fmt.Sprintf("  %-15s %-10s %-10s %s", "NAME", "TYPE", "STATUS", "CREATED"))
	sections = append(sections, header)

	// Rows
	for i, ch := range m.channels {
		style := tableRowStyle
		prefix := "  "
		if i == m.selectedIndex {
			style = tableRowSelectedStyle
			prefix = "→ "
		}

		status := badgeInactive.Render(ch.Status)
		if ch.Status == "active" {
			status = badgeActive.Render(ch.Status)
		}

		created := ch.CreatedAt.Format("Jan 02 15:04")

		row := style.Render(fmt.Sprintf("%s%-13s %-10s %-10s %s",
			prefix, ch.Name, ch.Type, status, created))
		sections = append(sections, row)
	}

	return strings.Join(sections, "\n")
}

func (m Model) renderNodes(width int) string {
	var sections []string

	title := lipgloss.NewStyle().Bold(true).Foreground(white).Render("🖥️ Nodes")
	sections = append(sections, title)
	sections = append(sections, "")

	if len(m.nodes) == 0 {
		empty := cardStyle.Render("No nodes connected.\n\nStart nodes with: klaw node start")
		sections = append(sections, empty)
		return strings.Join(sections, "\n")
	}

	// Table header
	header := tableHeaderStyle.Render(fmt.Sprintf("%-12s %-15s %-10s %-20s", "ID", "NAME", "STATUS", "LAST SEEN"))
	sections = append(sections, header)

	for i, node := range m.nodes {
		style := tableRowStyle
		if i == m.selectedIndex {
			style = tableRowSelectedStyle
		}

		status := badgeActive.Render("ready")
		if node.Status != "ready" {
			status = badgeInactive.Render(node.Status)
		}

		lastSeen := node.LastSeen.Format("Jan 02 15:04:05")
		row := style.Render(fmt.Sprintf("%-12s %-15s %-10s %-20s",
			node.ID, truncate(node.Name, 15), status, lastSeen))
		sections = append(sections, row)
	}

	return strings.Join(sections, "\n")
}

func (m Model) renderJobs(width int) string {
	var sections []string

	title := lipgloss.NewStyle().Bold(true).Foreground(white).Render("⏰ Scheduled Jobs")
	sections = append(sections, title)
	sections = append(sections, "")

	if len(m.jobs) == 0 {
		empty := cardStyle.Render("No scheduled jobs.\n\nCreate one with: klaw cron create <name> --schedule \"every day at 9am\" --agent <agent> --task \"...\"")
		sections = append(sections, empty)
		return strings.Join(sections, "\n")
	}

	// Table header
	header := tableHeaderStyle.Render(fmt.Sprintf("%-8s %-15s %-20s %-12s %-10s", "ID", "NAME", "SCHEDULE", "AGENT", "STATUS"))
	sections = append(sections, header)

	for i, job := range m.jobs {
		style := tableRowStyle
		if i == m.selectedIndex {
			style = tableRowSelectedStyle
		}

		status := badgeActive.Render("enabled")
		if !job.Enabled {
			status = badgeInactive.Render("disabled")
		}

		row := style.Render(fmt.Sprintf("%-8s %-15s %-20s %-12s %-10s",
			job.ID, truncate(job.Name, 15), truncate(job.Schedule, 20), job.Agent, status))
		sections = append(sections, row)
	}

	return strings.Join(sections, "\n")
}

func (m Model) renderSettings(width int) string {
	var sections []string

	title := lipgloss.NewStyle().Bold(true).Foreground(white).Render("⚙️ Settings")
	sections = append(sections, title)
	sections = append(sections, "")

	// Cluster info
	clusterCard := cardStyle.Width(width - 6).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			cardTitleStyle.Render("Cluster"),
			fmt.Sprintf("Name:      %s", m.clusterName),
			fmt.Sprintf("Namespace: %s", m.namespace),
		),
	)
	sections = append(sections, clusterCard)

	// Orchestrator settings
	orchCard := cardStyle.Width(width - 6).Render(
		lipgloss.JoinVertical(lipgloss.Left,
			cardTitleStyle.Render("Orchestrator"),
			"Mode:          hybrid",
			"Allow Manual:  true (@agent syntax)",
			fmt.Sprintf("Default Agent: %s", func() string {
				if len(m.agents) > 0 {
					return m.agents[0].Name
				}
				return "(none)"
			}()),
		),
	)
	sections = append(sections, orchCard)

	return strings.Join(sections, "\n")
}

func (m Model) renderCreateForm() string {
	var sections []string

	title := lipgloss.NewStyle().Bold(true).Foreground(white).Render("🤖 Create Agent")
	sections = append(sections, title)
	sections = append(sections, "")

	labels := []string{"Name:", "Description:", "Triggers:", "Model:"}

	for i, input := range m.inputs {
		label := labelStyle.Render(labels[i])
		style := inputStyle
		if i == m.focusedInput {
			style = inputFocusedStyle
		}
		field := style.Render(input.View())
		sections = append(sections, label)
		sections = append(sections, field)
		sections = append(sections, "")
	}

	if m.err != nil {
		errMsg := badgeError.Render(fmt.Sprintf("Error: %v", m.err))
		sections = append(sections, errMsg)
	}

	hint := lipgloss.NewStyle().Foreground(gray).Render("Press Enter to submit • Esc to cancel")
	sections = append(sections, hint)

	return strings.Join(sections, "\n")
}

func (m Model) renderDetail() string {
	switch m.activeTab {
	case TabAgents:
		if m.selectedIndex < len(m.agents) {
			return m.renderAgentDetail(m.agents[m.selectedIndex])
		}
	case TabChannels:
		if m.selectedIndex < len(m.channels) {
			return m.renderChannelDetail(m.channels[m.selectedIndex])
		}
	}
	return ""
}

func (m Model) renderAgentDetail(ag *cluster.AgentBinding) string {
	var sections []string

	title := lipgloss.NewStyle().Bold(true).Foreground(white).Render(fmt.Sprintf("🤖 %s", ag.Name))
	sections = append(sections, title)
	sections = append(sections, "")

	// Basic info
	skillsStr := "(none)"
	if len(ag.Skills) > 0 {
		skillsStr = strings.Join(ag.Skills, ", ")
	}
	triggersStr := "(none)"
	if len(ag.Triggers) > 0 {
		triggersStr = strings.Join(ag.Triggers, ", ")
	}

	info := cardStyle.Render(
		lipgloss.JoinVertical(lipgloss.Left,
			fmt.Sprintf("Description:  %s", ag.Description),
			fmt.Sprintf("Model:        %s", ag.Model),
			fmt.Sprintf("Tools:        %s", strings.Join(ag.Tools, ", ")),
			fmt.Sprintf("Skills:       %s", skillsStr),
			fmt.Sprintf("Triggers:     %s", triggersStr),
			fmt.Sprintf("Created:      %s", ag.CreatedAt.Format(time.RFC3339)),
		),
	)
	sections = append(sections, info)
	sections = append(sections, "")

	// Bootstrap / System Prompt
	bootstrapTitle := cardTitleStyle.Render("📝 Bootstrap (System Prompt)")
	sections = append(sections, bootstrapTitle)

	// Format the system prompt with line numbers and proper display
	promptLines := strings.Split(ag.SystemPrompt, "\n")
	maxLines := 20 // Show max 20 lines in detail view
	displayLines := promptLines
	truncated := false
	if len(promptLines) > maxLines {
		displayLines = promptLines[:maxLines]
		truncated = true
	}

	// Create a styled prompt display
	promptStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(darkGray).
		Padding(1, 2).
		Foreground(lipgloss.Color("#D1D5DB"))

	promptContent := strings.Join(displayLines, "\n")
	if truncated {
		promptContent += fmt.Sprintf("\n\n%s", lipgloss.NewStyle().Foreground(gray).Italic(true).Render(fmt.Sprintf("... and %d more lines", len(promptLines)-maxLines)))
	}

	sections = append(sections, promptStyle.Render(promptContent))

	hint := lipgloss.NewStyle().Foreground(gray).Render("Esc: back • d: delete")
	sections = append(sections, "", hint)

	return strings.Join(sections, "\n")
}

func (m Model) renderChannelDetail(ch *cluster.ChannelBinding) string {
	var sections []string

	title := lipgloss.NewStyle().Bold(true).Foreground(white).Render(fmt.Sprintf("📡 %s", ch.Name))
	sections = append(sections, title)
	sections = append(sections, "")

	status := badgeInactive.Render(ch.Status)
	if ch.Status == "active" {
		status = badgeActive.Render(ch.Status)
	}

	info := cardStyle.Render(
		lipgloss.JoinVertical(lipgloss.Left,
			fmt.Sprintf("Type:     %s", ch.Type),
			fmt.Sprintf("Status:   %s", status),
			fmt.Sprintf("Created:  %s", ch.CreatedAt.Format(time.RFC3339)),
		),
	)
	sections = append(sections, info)
	sections = append(sections, "")

	// Logs section
	logsTitle := cardTitleStyle.Render("📜 Recent Messages")
	sections = append(sections, logsTitle)

	if len(m.channelLogs) == 0 {
		noLogs := lipgloss.NewStyle().Foreground(gray).Italic(true).Render("  No messages yet. Messages will appear here when users interact with agents.")
		sections = append(sections, noLogs)
	} else {
		// Calculate visible logs based on scroll position
		maxVisible := 15
		start := m.logScrollPos
		end := start + maxVisible
		if end > len(m.channelLogs) {
			end = len(m.channelLogs)
		}

		// Show scroll indicator if needed
		if start > 0 {
			sections = append(sections, lipgloss.NewStyle().Foreground(gray).Render("  ↑ scroll up for more"))
		}

		for i := start; i < end; i++ {
			log := m.channelLogs[i]
			timeStr := log.Timestamp.Format("15:04:05")

			// Format: [time] user → agent [via]: message
			routeIcon := "🔀"
			switch log.RoutedVia {
			case "manual":
				routeIcon = "👆"
			case "keyword":
				routeIcon = "🔑"
			case "ai":
				routeIcon = "🤖"
			}

			userStyle := lipgloss.NewStyle().Foreground(blue)
			agentStyle := lipgloss.NewStyle().Foreground(purple).Bold(true)
			timeStyle := lipgloss.NewStyle().Foreground(gray)
			msgStyle := lipgloss.NewStyle().Foreground(white)

			logLine := fmt.Sprintf("  %s %s → %s %s %s",
				timeStyle.Render(timeStr),
				userStyle.Render(log.User),
				agentStyle.Render(log.Agent),
				routeIcon,
				msgStyle.Render(truncate(log.Content, 50)),
			)
			sections = append(sections, logLine)

			// Show response if available
			if log.Response != "" {
				respLine := fmt.Sprintf("    └─ %s",
					lipgloss.NewStyle().Foreground(green).Render(truncate(log.Response, 60)))
				sections = append(sections, respLine)
			}
		}

		if end < len(m.channelLogs) {
			sections = append(sections, lipgloss.NewStyle().Foreground(gray).Render("  ↓ scroll down for more"))
		}
	}

	sections = append(sections, "")
	hint := lipgloss.NewStyle().Foreground(gray).Render("Esc: back • s: toggle status • d: delete • ↑/↓: scroll logs")
	sections = append(sections, hint)

	return strings.Join(sections, "\n")
}

func (m Model) renderHelp() string {
	var keys []string

	switch m.viewMode {
	case ViewCreate, ViewEdit:
		keys = []string{"Tab: next field", "Enter: submit", "Esc: cancel"}
	case ViewDetail:
		keys = []string{"Esc: back", "d: delete"}
	default:
		switch m.activeTab {
		case TabAgents:
			keys = []string{"n: new", "Enter: details", "d: delete", "1-5: tabs", "q: quit"}
		case TabChannels:
			keys = []string{"s: toggle status", "Enter: details", "d: delete", "1-5: tabs", "q: quit"}
		default:
			keys = []string{"1-5: tabs", "r: refresh", "q: quit"}
		}
	}

	help := strings.Join(keys, "  •  ")
	return helpStyle.Width(m.width).Render(help)
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

// Key bindings
type keyMap struct {
	Quit   key.Binding
	Tab    key.Binding
	Create key.Binding
	Delete key.Binding
	Enter  key.Binding
	Escape key.Binding
}

var keys = keyMap{
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch tab"),
	),
	Create: key.NewBinding(
		key.WithKeys("n", "c"),
		key.WithHelp("n", "create"),
	),
	Delete: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "delete"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "select"),
	),
	Escape: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
}
