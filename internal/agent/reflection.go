package agent

import "github.com/eachlabs/klaw/internal/provider"

// ReflectionConfig controls the reflection loop behavior.
type ReflectionConfig struct {
	Enabled           bool
	ReflectAfterTools int    // inject reflection after this many tool calls (default: 3)
	Prompt            string // custom reflection prompt
}

const defaultReflectionPrompt = `Before continuing, briefly reflect on your progress so far:
1. What have you accomplished?
2. Are you on the right track to solve the user's request?
3. What should you do next?

Be concise — 2-3 sentences maximum.`

// ShouldReflect returns true if it's time to inject a reflection prompt.
func ShouldReflect(cfg ReflectionConfig, toolCallsSinceReflection int) bool {
	if !cfg.Enabled {
		return false
	}
	threshold := cfg.ReflectAfterTools
	if threshold <= 0 {
		threshold = 3
	}
	return toolCallsSinceReflection >= threshold
}

// InjectReflection appends a reflection prompt as a user message.
func InjectReflection(history []provider.Message, prompt string) []provider.Message {
	if prompt == "" {
		prompt = defaultReflectionPrompt
	}
	return append(history, provider.Message{
		Role:    "user",
		Content: prompt,
	})
}
