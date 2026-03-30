package api

import (
	"testing"
)

func TestParseAgentOutput_RawJSON(t *testing.T) {
	input := `{"output":"Hello world","memories":["learned X"],"artifacts":[{"type":"image","url":"https://example.com/img.png","description":"test"}]}`
	result := ParseAgentOutput(input)

	if result.Output != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", result.Output)
	}
	if len(result.Memories) != 1 || result.Memories[0] != "learned X" {
		t.Errorf("expected 1 memory, got %v", result.Memories)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].URL != "https://example.com/img.png" {
		t.Errorf("expected 1 artifact, got %v", result.Artifacts)
	}
}

func TestParseAgentOutput_FencedJSON(t *testing.T) {
	input := "Here is the result:\n\n```json\n{\"output\":\"fenced\",\"memories\":[],\"artifacts\":[]}\n```\n\nDone."
	result := ParseAgentOutput(input)

	if result.Output != "fenced" {
		t.Errorf("expected 'fenced', got %q", result.Output)
	}
}

func TestParseAgentOutput_PlainText(t *testing.T) {
	input := "Just a plain text response with no JSON."
	result := ParseAgentOutput(input)

	if result.Output != input {
		t.Errorf("expected plain text passthrough, got %q", result.Output)
	}
	if len(result.Memories) != 0 {
		t.Errorf("expected empty memories, got %v", result.Memories)
	}
	if len(result.Artifacts) != 0 {
		t.Errorf("expected empty artifacts, got %v", result.Artifacts)
	}
}

func TestParseAgentOutput_MalformedJSON(t *testing.T) {
	input := `{"output": "broken", invalid`
	result := ParseAgentOutput(input)

	if result.Output != input {
		t.Errorf("expected fallback to plain text, got %q", result.Output)
	}
}

func TestParseAgentOutput_Empty(t *testing.T) {
	result := ParseAgentOutput("")

	if result.Output != "" {
		t.Errorf("expected empty output, got %q", result.Output)
	}
}
