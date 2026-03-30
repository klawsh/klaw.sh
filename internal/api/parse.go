package api

import (
	"encoding/json"
	"strings"
)

// ParsedOutput is the structured result extracted from agent text.
type ParsedOutput struct {
	Output    string     `json:"output"`
	Memories  []string   `json:"memories,omitempty"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
}

// Artifact represents a generated asset.
type Artifact struct {
	Type        string `json:"type"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// ParseAgentOutput tries to extract structured JSON from the agent's final text.
// Tries: (1) raw JSON, (2) ```json code fence, (3) plain text fallback.
func ParseAgentOutput(text string) ParsedOutput {
	text = strings.TrimSpace(text)
	if text == "" {
		return ParsedOutput{}
	}

	// Try raw JSON
	var result ParsedOutput
	if err := json.Unmarshal([]byte(text), &result); err == nil && result.Output != "" {
		return result
	}

	// Try extracting from ```json fence
	if idx := strings.Index(text, "```json"); idx != -1 {
		start := idx + 7
		end := strings.Index(text[start:], "```")
		if end != -1 {
			jsonStr := strings.TrimSpace(text[start : start+end])
			var fenced ParsedOutput
			if err := json.Unmarshal([]byte(jsonStr), &fenced); err == nil && fenced.Output != "" {
				return fenced
			}
		}
	}

	// Fallback: treat entire output as plain text
	return ParsedOutput{
		Output:    text,
		Memories:  []string{},
		Artifacts: []Artifact{},
	}
}
