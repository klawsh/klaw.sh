package agent

import "github.com/eachlabs/klaw/internal/provider"

// PlannerConfig controls the planning phase.
type PlannerConfig struct {
	Enabled    bool
	PlanPrompt string
}

const defaultPlanPrompt = `Before taking any action, create a brief plan:
1. Analyze what the user is asking for
2. List the steps you'll take (max 5 steps)
3. Identify any potential issues or questions

Keep the plan concise. Then proceed with execution.`

// InjectPlanRequest prepends a planning instruction to the first user message.
func InjectPlanRequest(messages []provider.Message, prompt string) []provider.Message {
	if len(messages) == 0 {
		return messages
	}
	if prompt == "" {
		prompt = defaultPlanPrompt
	}

	// Find the first user message and augment it
	result := make([]provider.Message, len(messages))
	copy(result, messages)

	for i, msg := range result {
		if msg.Role == "user" {
			result[i].Content = prompt + "\n\nUser request: " + msg.Content
			break
		}
	}

	return result
}
