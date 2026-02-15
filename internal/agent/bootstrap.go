// Package agent provides agent functionality.
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/eachlabs/klaw/internal/provider"
)

// BootstrapConfig holds configuration for generating agent bootstrap.
type BootstrapConfig struct {
	Name        string
	Description string
	Skills      []string
	Tools       []string
	Model       string
}

// GenerateBootstrap uses AI to create an enhanced system prompt for an agent.
func GenerateBootstrap(ctx context.Context, prov provider.Provider, cfg BootstrapConfig) (string, error) {
	skillList := "none"
	if len(cfg.Skills) > 0 {
		skillList = strings.Join(cfg.Skills, ", ")
	}

	toolList := "none"
	if len(cfg.Tools) > 0 {
		toolList = strings.Join(cfg.Tools, ", ")
	}

	prompt := fmt.Sprintf(`You are an expert at designing AI agent system prompts. Create a comprehensive, well-structured system prompt for an AI agent with the following specifications:

**Agent Name:** %s
**Description:** %s
**Skills:** %s
**Tools Available:** %s

Generate a detailed system prompt in markdown format that includes:

1. **Identity & Role** - Clear definition of who the agent is and their primary purpose
2. **Core Capabilities** - What the agent can do based on their skills and tools
3. **Behavioral Guidelines** - How the agent should behave, communicate, and handle tasks
4. **Best Practices** - Specific guidelines for using tools effectively
5. **Limitations & Boundaries** - What the agent should NOT do or be careful about
6. **Example Interactions** - 2-3 example scenarios showing ideal behavior

The prompt should be:
- Professional and clear
- Specific to the agent's role
- Actionable with concrete guidance
- Under 1500 words

Output ONLY the system prompt content, no explanations or meta-commentary.`, cfg.Name, cfg.Description, skillList, toolList)

	resp, err := prov.Chat(ctx, &provider.ChatRequest{
		Messages: []provider.Message{
			{Role: "user", Content: prompt},
		},
		MaxTokens: 4096,
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate bootstrap: %w", err)
	}

	// Extract text from response
	var content string
	for _, block := range resp.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	return content, nil
}

// DefaultBootstrap generates a simple default bootstrap without AI.
func DefaultBootstrap(cfg BootstrapConfig) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# %s\n\n", cfg.Name))
	sb.WriteString(fmt.Sprintf("## Role\n%s\n\n", cfg.Description))

	sb.WriteString("## Guidelines\n")
	sb.WriteString("- Be helpful, accurate, and concise\n")
	sb.WriteString("- Take action when appropriate\n")
	sb.WriteString("- Ask for clarification when requirements are unclear\n")
	sb.WriteString("- Explain your reasoning when making decisions\n\n")

	if len(cfg.Skills) > 0 {
		sb.WriteString("## Skills\n")
		for _, skill := range cfg.Skills {
			sb.WriteString(fmt.Sprintf("- **%s**\n", skill))
		}
		sb.WriteString("\n")
	}

	if len(cfg.Tools) > 0 {
		sb.WriteString("## Available Tools\n")
		for _, tool := range cfg.Tools {
			sb.WriteString(fmt.Sprintf("- `%s`\n", tool))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Best Practices\n")
	sb.WriteString("- Always verify information before acting on it\n")
	sb.WriteString("- Use appropriate tools for the task at hand\n")
	sb.WriteString("- Keep the user informed of progress on longer tasks\n")
	sb.WriteString("- Handle errors gracefully and suggest alternatives\n")

	return sb.String()
}
