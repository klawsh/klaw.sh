package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
)

// AgentTool allows the AI to manage agents programmatically
type AgentTool struct {
	store   *cluster.Store
	ctxMgr  *cluster.ContextManager
}

// NewAgentTool creates a new agent management tool
func NewAgentTool() *AgentTool {
	return &AgentTool{
		store:  cluster.NewStore(config.StateDir()),
		ctxMgr: cluster.NewContextManager(config.ConfigDir()),
	}
}

func (t *AgentTool) Name() string {
	return "agent_spawn"
}

func (t *AgentTool) Description() string {
	return `Create a new AI agent. Use this when the user wants to create a new agent to handle specific tasks.

You should use this tool when:
- User asks to create/spawn/add a new agent
- User describes a task they need automated
- User wants a specialist for a specific domain

Extract these from the user's request:
- name: Short, memorable name (lowercase, no spaces, e.g., "competitor-tracker", "code-reviewer")
- description: What the agent does (1-2 sentences)
- skills: Relevant capabilities from: web-search, browser, git, docker, api, database, slack, email
- triggers: Keywords that route messages to this agent

Example usage:
User: "I need something to track competitor ads on Facebook"
-> name: "ad-tracker", description: "Monitors competitor Facebook ads", skills: ["browser", "web-search"], triggers: ["ads", "competitor", "facebook"]`
}

func (t *AgentTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {
				"type": "string",
				"description": "Agent name (lowercase, no spaces, use hyphens)"
			},
			"description": {
				"type": "string",
				"description": "What this agent does (1-2 sentences)"
			},
			"skills": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Skills to enable: web-search, browser, git, docker, api, database, slack, email"
			},
			"triggers": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Keywords that route messages to this agent"
			},
			"model": {
				"type": "string",
				"description": "Model to use (default: claude-sonnet-4-20250514)"
			}
		},
		"required": ["name", "description"]
	}`)
}

type agentSpawnParams struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Skills      []string `json:"skills"`
	Triggers    []string `json:"triggers"`
	Model       string   `json:"model"`
}

func (t *AgentTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p agentSpawnParams
	if err := json.Unmarshal(params, &p); err != nil {
		return &Result{Content: fmt.Sprintf("Invalid parameters: %v", err), IsError: true}, nil
	}

	if p.Name == "" {
		return &Result{Content: "Agent name is required", IsError: true}, nil
	}

	if p.Description == "" {
		return &Result{Content: "Agent description is required", IsError: true}, nil
	}

	// Normalize name
	p.Name = strings.ToLower(strings.ReplaceAll(p.Name, " ", "-"))

	// Default model
	if p.Model == "" {
		p.Model = "claude-sonnet-4-20250514"
	}

	// Default tools
	tools := []string{"bash", "read", "write", "edit", "glob", "grep", "web_fetch", "web_search"}

	// Get current context
	clusterName, namespace, err := t.ctxMgr.RequireCurrent()
	if err != nil {
		// Create default context if none exists
		clusterName = "default"
		namespace = "default"
	}

	// Check if agent already exists
	existingAgent, _ := t.store.GetAgentBinding(clusterName, namespace, p.Name)
	isUpdate := existingAgent != nil

	// Build system prompt
	var systemPrompt strings.Builder
	systemPrompt.WriteString(fmt.Sprintf("You are %s, an AI agent.\n\n", p.Name))
	systemPrompt.WriteString(fmt.Sprintf("## Your Role\n%s\n\n", p.Description))
	systemPrompt.WriteString("## Guidelines\n")
	systemPrompt.WriteString("- Be concise and direct in your responses\n")
	systemPrompt.WriteString("- Take action when needed, don't just describe what you could do\n")
	systemPrompt.WriteString("- If you need more information, ask specific questions\n")
	if len(p.Skills) > 0 {
		systemPrompt.WriteString(fmt.Sprintf("- You have access to these skills: %s\n", strings.Join(p.Skills, ", ")))
	}
	systemPrompt.WriteString("\n## Response Format\n")
	systemPrompt.WriteString("- Keep responses short (1-3 sentences when possible)\n")
	systemPrompt.WriteString("- Use bullet points for lists\n")
	systemPrompt.WriteString("- Include specific data/numbers when available\n")

	// Create or update the agent binding
	binding := &cluster.AgentBinding{
		Name:         p.Name,
		Cluster:      clusterName,
		Namespace:    namespace,
		Description:  p.Description,
		SystemPrompt: systemPrompt.String(),
		Model:        p.Model,
		Tools:        tools,
		Skills:       p.Skills,
		Triggers:     p.Triggers,
	}

	if isUpdate {
		// Update existing agent
		if err := t.store.UpdateAgentBinding(binding); err != nil {
			return &Result{Content: fmt.Sprintf("Failed to update agent: %v", err), IsError: true}, nil
		}
	} else {
		// Create new agent
		if err := t.store.CreateAgentBinding(binding); err != nil {
			return &Result{Content: fmt.Sprintf("Failed to create agent: %v", err), IsError: true}, nil
		}
	}

	// Build response
	var sb strings.Builder
	if isUpdate {
		sb.WriteString(fmt.Sprintf("Agent '%s' updated!\n\n", p.Name))
	} else {
		sb.WriteString(fmt.Sprintf("Agent '%s' created!\n\n", p.Name))
	}
	sb.WriteString(fmt.Sprintf("Description: %s\n", p.Description))
	sb.WriteString(fmt.Sprintf("Model: %s\n", p.Model))

	if len(p.Skills) > 0 {
		sb.WriteString(fmt.Sprintf("Skills: %s\n", strings.Join(p.Skills, ", ")))
	}
	if len(p.Triggers) > 0 {
		sb.WriteString(fmt.Sprintf("Triggers: %s\n", strings.Join(p.Triggers, ", ")))
	}

	sb.WriteString(fmt.Sprintf("\nYou can now route tasks to @%s or use triggers: %s", p.Name, strings.Join(p.Triggers, ", ")))

	return &Result{Content: sb.String()}, nil
}
