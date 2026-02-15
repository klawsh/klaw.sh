// Package orchestrator routes messages to appropriate agents.
package orchestrator

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/eachlabs/klaw/internal/agent"
	"github.com/eachlabs/klaw/internal/channel"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/tool"
)

// Config holds orchestrator configuration.
type Config struct {
	Mode          string            // "ai", "rules", "hybrid", "disabled"
	DefaultAgent  string            // fallback agent
	AllowManual   bool              // allow @agent syntax
	Rules         []RoutingRule     // keyword-based rules
	Provider      provider.Provider // for AI-based routing
	Tools         *tool.Registry
	SystemPrompt  string
}

// RoutingRule defines keyword-based routing.
type RoutingRule struct {
	Match  string // regex pattern
	Agent  string // target agent name
}

// Orchestrator routes messages to agents.
type Orchestrator struct {
	config       Config
	agents       map[string]*AgentConfig
	channel      channel.Channel
	mu           sync.RWMutex
	runningAgent *agent.Agent
}

// AgentConfig holds agent configuration for the orchestrator.
type AgentConfig struct {
	Name         string
	Description  string
	SystemPrompt string
	Tools        []string
	Model        string
	Triggers     []string // keywords that route to this agent
}

// New creates a new orchestrator.
func New(cfg Config) *Orchestrator {
	return &Orchestrator{
		config: cfg,
		agents: make(map[string]*AgentConfig),
	}
}

// RegisterAgent adds an agent to the orchestrator.
func (o *Orchestrator) RegisterAgent(cfg *AgentConfig) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.agents[cfg.Name] = cfg
}

// UnregisterAgent removes an agent from the orchestrator.
func (o *Orchestrator) UnregisterAgent(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.agents, name)

	// Remove associated rules
	var newRules []RoutingRule
	for _, rule := range o.config.Rules {
		if rule.Agent != name {
			newRules = append(newRules, rule)
		}
	}
	o.config.Rules = newRules
}

// AddRule adds a routing rule to the orchestrator.
func (o *Orchestrator) AddRule(rule RoutingRule) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.config.Rules = append(o.config.Rules, rule)
}

// SetChannel sets the communication channel.
func (o *Orchestrator) SetChannel(ch channel.Channel) {
	o.channel = ch
}

// ParsedMessage represents a parsed user message.
type ParsedMessage struct {
	Original    string
	Content     string   // message without @agent
	TargetAgent string   // specific agent if @agent used
	TargetAll   bool     // true if @all used
}

// ParseMessage extracts routing info from message.
func (o *Orchestrator) ParseMessage(msg string) *ParsedMessage {
	parsed := &ParsedMessage{
		Original: msg,
		Content:  msg,
	}

	// Check for @agent or @all syntax
	re := regexp.MustCompile(`^@(\w+)\s+(.*)$`)
	matches := re.FindStringSubmatch(strings.TrimSpace(msg))

	if len(matches) == 3 {
		target := strings.ToLower(matches[1])
		parsed.Content = matches[2]

		if target == "all" {
			parsed.TargetAll = true
		} else {
			parsed.TargetAgent = target
		}
	}

	return parsed
}

// Route determines which agent(s) should handle the message.
func (o *Orchestrator) Route(ctx context.Context, parsed *ParsedMessage) ([]string, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	// Manual routing with @agent
	if o.config.AllowManual && parsed.TargetAgent != "" {
		if _, exists := o.agents[parsed.TargetAgent]; exists {
			return []string{parsed.TargetAgent}, nil
		}
		return nil, fmt.Errorf("agent not found: %s", parsed.TargetAgent)
	}

	// @all - return all agents
	if parsed.TargetAll {
		agents := make([]string, 0, len(o.agents))
		for name := range o.agents {
			agents = append(agents, name)
		}
		return agents, nil
	}

	// Disabled mode - use default
	if o.config.Mode == "disabled" {
		if o.config.DefaultAgent != "" {
			return []string{o.config.DefaultAgent}, nil
		}
		// Return first available agent
		for name := range o.agents {
			return []string{name}, nil
		}
		return nil, fmt.Errorf("no agents available")
	}

	// Rules-based routing
	if o.config.Mode == "rules" || o.config.Mode == "hybrid" {
		for _, rule := range o.config.Rules {
			matched, _ := regexp.MatchString("(?i)"+rule.Match, parsed.Content)
			if matched {
				return []string{rule.Agent}, nil
			}
		}
	}

	// AI-based routing
	if o.config.Mode == "ai" || o.config.Mode == "hybrid" {
		agent, err := o.routeWithAI(ctx, parsed.Content)
		if err == nil && agent != "" {
			return []string{agent}, nil
		}
	}

	// Fallback to default
	if o.config.DefaultAgent != "" {
		return []string{o.config.DefaultAgent}, nil
	}

	return nil, fmt.Errorf("could not route message")
}

// routeWithAI uses AI to determine the best agent.
func (o *Orchestrator) routeWithAI(ctx context.Context, content string) (string, error) {
	if o.config.Provider == nil {
		return "", fmt.Errorf("no provider configured")
	}

	// Build agent descriptions
	var agentList strings.Builder
	for name, cfg := range o.agents {
		agentList.WriteString(fmt.Sprintf("- %s: %s\n", name, cfg.Description))
	}

	prompt := fmt.Sprintf(`You are a routing assistant. Based on the user's message, determine which agent should handle it.

Available agents:
%s

User message: %s

Respond with ONLY the agent name, nothing else.`, agentList.String(), content)

	// Use Chat for simple routing decision
	resp, err := o.config.Provider.Chat(ctx, &provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "user", Content: prompt},
		},
		MaxTokens: 50,
	})
	if err != nil {
		return "", err
	}

	// Extract text from content blocks
	var textContent string
	for _, block := range resp.Content {
		if block.Type == "text" {
			textContent += block.Text
		}
	}

	agentName := strings.TrimSpace(strings.ToLower(textContent))

	// Validate agent exists
	if _, exists := o.agents[agentName]; exists {
		return agentName, nil
	}

	return "", fmt.Errorf("AI returned invalid agent: %s", agentName)
}

// Run starts the orchestrator main loop.
func (o *Orchestrator) Run(ctx context.Context) error {
	if o.channel == nil {
		return fmt.Errorf("no channel configured")
	}

	// Start channel
	if err := o.channel.Start(ctx); err != nil {
		return err
	}

	fmt.Println("Orchestrator running...")

	// Process incoming messages
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case msg := <-o.channel.Receive():
			if msg.Role != "user" {
				continue
			}

			// Parse message for routing
			parsed := o.ParseMessage(msg.Content)

			// Route to agent(s)
			agents, err := o.Route(ctx, parsed)
			if err != nil {
				o.channel.Send(ctx, &channel.Message{
					Role:    "error",
					Content: fmt.Sprintf("Routing error: %v", err),
				})
				continue
			}

			fmt.Printf("Routing to agent(s): %v\n", agents)

			// For now, just use first agent
			// TODO: parallel execution for @all
			if len(agents) > 0 {
				agentName := agents[0]
				if err := o.runAgent(ctx, agentName, parsed.Content); err != nil {
					o.channel.Send(ctx, &channel.Message{
						Role:    "error",
						Content: fmt.Sprintf("Agent error: %v", err),
					})
				}
			}
		}
	}
}

// runAgent executes an agent with the given message.
func (o *Orchestrator) runAgent(ctx context.Context, name string, content string) error {
	o.mu.RLock()
	agentCfg, exists := o.agents[name]
	o.mu.RUnlock()

	if !exists {
		return fmt.Errorf("agent not found: %s", name)
	}

	// Create a one-shot channel that sends the message and captures response
	msgChan := make(chan *channel.Message, 10)

	// Create agent
	ag := agent.New(agent.Config{
		Provider:     o.config.Provider,
		Channel:      &proxyChannel{input: msgChan, output: o.channel, ctx: ctx},
		Tools:        o.config.Tools, // TODO: per-agent tools
		SystemPrompt: agentCfg.SystemPrompt,
	})

	// Send the message
	msgChan <- &channel.Message{
		Role:    "user",
		Content: content,
	}

	// Run agent for one turn
	return ag.RunOnce(ctx)
}

// proxyChannel wraps messages between orchestrator and agent.
type proxyChannel struct {
	input  chan *channel.Message
	output channel.Channel
	ctx    context.Context
}

func (p *proxyChannel) Name() string { return "proxy" }

func (p *proxyChannel) Start(ctx context.Context) error { return nil }

func (p *proxyChannel) Send(ctx context.Context, msg *channel.Message) error {
	return p.output.Send(ctx, msg)
}

func (p *proxyChannel) Receive() <-chan *channel.Message {
	return p.input
}

func (p *proxyChannel) Stop() error { return nil }

func (p *proxyChannel) Done() <-chan struct{} {
	done := make(chan struct{})
	close(done)
	return done
}
