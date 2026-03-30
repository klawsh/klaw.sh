package orchestrator

import (
	"context"
	"testing"

	"github.com/eachlabs/klaw/internal/channel"
)

func TestParseMessage(t *testing.T) {
	o := New(Config{AllowManual: true})

	tests := []struct {
		name        string
		input       string
		wantContent string
		wantAgent   string
		wantAll     bool
	}{
		{
			name:        "plain message",
			input:       "hello world",
			wantContent: "hello world",
			wantAgent:   "",
			wantAll:     false,
		},
		{
			name:        "direct agent",
			input:       "@coder fix this bug",
			wantContent: "fix this bug",
			wantAgent:   "coder",
			wantAll:     false,
		},
		{
			name:        "all agents",
			input:       "@all what do you think?",
			wantContent: "what do you think?",
			wantAgent:   "",
			wantAll:     true,
		},
		{
			name:        "uppercase agent",
			input:       "@CODER fix this",
			wantContent: "fix this",
			wantAgent:   "coder",
			wantAll:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := o.ParseMessage(tt.input)

			if parsed.Content != tt.wantContent {
				t.Errorf("Content = %q, want %q", parsed.Content, tt.wantContent)
			}
			if parsed.TargetAgent != tt.wantAgent {
				t.Errorf("TargetAgent = %q, want %q", parsed.TargetAgent, tt.wantAgent)
			}
			if parsed.TargetAll != tt.wantAll {
				t.Errorf("TargetAll = %v, want %v", parsed.TargetAll, tt.wantAll)
			}
		})
	}
}

func TestRoute_Manual(t *testing.T) {
	o := New(Config{
		AllowManual:  true,
		DefaultAgent: "default",
	})

	o.RegisterAgent(&AgentConfig{Name: "coder", Description: "codes"})
	o.RegisterAgent(&AgentConfig{Name: "writer", Description: "writes"})

	ctx := context.Background()

	// Direct routing
	parsed := o.ParseMessage("@coder fix bug")
	agents, err := o.Route(ctx, parsed)
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if len(agents) != 1 || agents[0] != "coder" {
		t.Errorf("got %v, want [coder]", agents)
	}

	// Unknown agent
	parsed = o.ParseMessage("@unknown hello")
	_, err = o.Route(ctx, parsed)
	if err == nil {
		t.Error("expected error for unknown agent")
	}
}

func TestRoute_Rules(t *testing.T) {
	o := New(Config{
		Mode:         "rules",
		DefaultAgent: "default",
		AllowManual:  true,
		Rules: []RoutingRule{
			{Match: "(?i)code|fix|bug", Agent: "coder"},
			{Match: "(?i)write|draft", Agent: "writer"},
		},
	})

	o.RegisterAgent(&AgentConfig{Name: "coder", Description: "codes"})
	o.RegisterAgent(&AgentConfig{Name: "writer", Description: "writes"})
	o.RegisterAgent(&AgentConfig{Name: "default", Description: "default"})

	ctx := context.Background()

	tests := []struct {
		msg       string
		wantAgent string
	}{
		{"fix this bug", "coder"},
		{"write an email", "writer"},
		{"hello world", "default"}, // fallback
	}

	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			parsed := o.ParseMessage(tt.msg)
			agents, err := o.Route(ctx, parsed)
			if err != nil {
				t.Fatalf("Route error: %v", err)
			}
			if len(agents) != 1 || agents[0] != tt.wantAgent {
				t.Errorf("got %v, want [%s]", agents, tt.wantAgent)
			}
		})
	}
}

func TestRoute_All(t *testing.T) {
	o := New(Config{
		Mode:        "disabled",
		AllowManual: true,
	})

	o.RegisterAgent(&AgentConfig{Name: "coder"})
	o.RegisterAgent(&AgentConfig{Name: "writer"})
	o.RegisterAgent(&AgentConfig{Name: "researcher"})

	ctx := context.Background()

	parsed := o.ParseMessage("@all what do you think?")
	agents, err := o.Route(ctx, parsed)
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if len(agents) != 3 {
		t.Errorf("got %d agents, want 3", len(agents))
	}
}

func TestRoute_Disabled_DefaultAgent(t *testing.T) {
	o := New(Config{
		Mode:         "disabled",
		DefaultAgent: "myagent",
	})
	o.RegisterAgent(&AgentConfig{Name: "myagent"})

	ctx := context.Background()
	parsed := o.ParseMessage("hello")
	agents, err := o.Route(ctx, parsed)
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if len(agents) != 1 || agents[0] != "myagent" {
		t.Errorf("got %v, want [myagent]", agents)
	}
}

func TestRoute_Disabled_NoDefault_FallsBackToFirst(t *testing.T) {
	o := New(Config{
		Mode: "disabled",
	})
	o.RegisterAgent(&AgentConfig{Name: "only-one"})

	ctx := context.Background()
	parsed := o.ParseMessage("hello")
	agents, err := o.Route(ctx, parsed)
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if len(agents) != 1 {
		t.Errorf("expected 1 agent, got %d", len(agents))
	}
}

func TestRoute_Disabled_NoAgents(t *testing.T) {
	o := New(Config{
		Mode: "disabled",
	})

	ctx := context.Background()
	parsed := o.ParseMessage("hello")
	_, err := o.Route(ctx, parsed)
	if err == nil {
		t.Fatal("expected error when no agents available")
	}
}

func TestRoute_NoRouteMatch(t *testing.T) {
	o := New(Config{
		Mode: "rules",
		Rules: []RoutingRule{
			{Match: "code", Agent: "coder"},
		},
	})
	o.RegisterAgent(&AgentConfig{Name: "coder"})

	ctx := context.Background()
	parsed := o.ParseMessage("something unrelated")
	_, err := o.Route(ctx, parsed)
	if err == nil {
		t.Fatal("expected error when no route matches and no default")
	}
}

func TestRegisterAndUnregisterAgent(t *testing.T) {
	o := New(Config{
		Mode:         "rules",
		DefaultAgent: "default",
		Rules: []RoutingRule{
			{Match: "test", Agent: "tester"},
		},
	})

	o.RegisterAgent(&AgentConfig{Name: "tester"})
	o.RegisterAgent(&AgentConfig{Name: "default"})

	// Verify agent exists
	o.mu.RLock()
	_, exists := o.agents["tester"]
	o.mu.RUnlock()
	if !exists {
		t.Fatal("tester agent should exist")
	}

	// Unregister should remove agent and associated rules
	o.UnregisterAgent("tester")

	o.mu.RLock()
	_, exists = o.agents["tester"]
	rulesCount := len(o.config.Rules)
	o.mu.RUnlock()

	if exists {
		t.Error("tester agent should be removed")
	}
	if rulesCount != 0 {
		t.Errorf("expected 0 rules after unregister, got %d", rulesCount)
	}
}

func TestAddRule(t *testing.T) {
	o := New(Config{Mode: "rules"})
	o.RegisterAgent(&AgentConfig{Name: "coder"})

	o.AddRule(RoutingRule{Match: "code", Agent: "coder"})

	o.mu.RLock()
	rulesCount := len(o.config.Rules)
	o.mu.RUnlock()

	if rulesCount != 1 {
		t.Errorf("expected 1 rule, got %d", rulesCount)
	}

	// Test that the rule actually works
	ctx := context.Background()
	parsed := o.ParseMessage("write some code")
	agents, err := o.Route(ctx, parsed)
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if len(agents) != 1 || agents[0] != "coder" {
		t.Errorf("got %v, want [coder]", agents)
	}
}

func TestSetChannel(t *testing.T) {
	o := New(Config{})
	if o.channel != nil {
		t.Error("channel should be nil initially")
	}

	// Just verify it doesn't panic
	o.SetChannel(nil)
}

func TestParseMessage_NoAgentSyntax(t *testing.T) {
	o := New(Config{})

	// Message without @ prefix
	parsed := o.ParseMessage("just a normal message")
	if parsed.TargetAgent != "" {
		t.Errorf("TargetAgent should be empty, got %q", parsed.TargetAgent)
	}
	if parsed.TargetAll {
		t.Error("TargetAll should be false")
	}
	if parsed.Original != "just a normal message" {
		t.Errorf("Original = %q", parsed.Original)
	}
}

func TestParseMessage_WhitespaceHandling(t *testing.T) {
	o := New(Config{})

	parsed := o.ParseMessage("  @coder  fix this  ")
	if parsed.TargetAgent != "coder" {
		t.Errorf("TargetAgent = %q, want 'coder'", parsed.TargetAgent)
	}
}

func TestRoute_ManualDisabled(t *testing.T) {
	o := New(Config{
		Mode:         "disabled",
		AllowManual:  false,
		DefaultAgent: "default",
	})
	o.RegisterAgent(&AgentConfig{Name: "coder"})
	o.RegisterAgent(&AgentConfig{Name: "default"})

	ctx := context.Background()

	// Even with @coder syntax, should fall through to default since AllowManual=false
	parsed := o.ParseMessage("@coder fix bug")
	agents, err := o.Route(ctx, parsed)
	if err != nil {
		t.Fatalf("Route error: %v", err)
	}
	if len(agents) != 1 || agents[0] != "default" {
		t.Errorf("got %v, want [default]", agents)
	}
}

func TestRunWithoutChannel(t *testing.T) {
	o := New(Config{})

	err := o.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when no channel configured")
	}
}

func TestProxyChannel(t *testing.T) {
	input := make(chan *channel.Message, 1)
	pc := &proxyChannel{
		input: input,
		ctx:   context.Background(),
	}

	if pc.Name() != "proxy" {
		t.Errorf("Name() = %q, want 'proxy'", pc.Name())
	}
	if err := pc.Start(context.Background()); err != nil {
		t.Errorf("Start error: %v", err)
	}
	if err := pc.Stop(); err != nil {
		t.Errorf("Stop error: %v", err)
	}

	// Receive returns input channel
	ch := pc.Receive()
	if ch == nil {
		t.Fatal("Receive() should return channel")
	}

	// Done returns closed channel
	done := pc.Done()
	select {
	case <-done:
		// good, it's closed
	default:
		t.Error("Done() should return closed channel")
	}
}
