package orchestrator

import (
	"context"
	"testing"
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
