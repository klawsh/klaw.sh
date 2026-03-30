package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// RunConfig holds configuration for a sub-agent run.
type RunConfig struct {
	Provider     interface{} // provider.Provider passed opaquely to avoid circular import
	Tools        *Registry
	SystemPrompt string
	Prompt       string
	MaxTokens    int
}

// RunFunc is a callback that runs a sub-agent with the given config.
type RunFunc func(ctx context.Context, cfg RunConfig) (string, error)

// DelegateTool spawns an ephemeral sub-agent to handle a task.
type DelegateTool struct {
	runFn    RunFunc
	provider interface{}
	tools    *Registry
	depth    int
	maxDepth int
}

// NewDelegateTool creates a delegate tool at depth 0.
func NewDelegateTool(runFn RunFunc, provider interface{}, tools *Registry) *DelegateTool {
	return &DelegateTool{
		runFn:    runFn,
		provider: provider,
		tools:    tools,
		depth:    0,
		maxDepth: 3,
	}
}

// newChildDelegate creates a delegate tool at depth+1 for nesting.
func (d *DelegateTool) newChildDelegate(childTools *Registry) *DelegateTool {
	return &DelegateTool{
		runFn:    d.runFn,
		provider: d.provider,
		tools:    childTools,
		depth:    d.depth + 1,
		maxDepth: d.maxDepth,
	}
}

func (d *DelegateTool) Name() string { return "delegate" }

func (d *DelegateTool) Description() string {
	return "Delegate a task to an ephemeral sub-agent. The sub-agent runs independently with its own tool set and returns a text result. Use this for parallelizable sub-tasks or specialized work."
}

func (d *DelegateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"task": {
			"type": "string",
			"description": "The task/prompt for the sub-agent to execute"
		},
		"tools": {
			"type": "array",
			"items": {"type": "string"},
			"description": "Optional allowlist of tool names the sub-agent can use. If empty, all tools except delegate are available."
		},
		"system_prompt": {
			"type": "string",
			"description": "Optional custom system prompt for the sub-agent"
		},
		"max_tokens": {
			"type": "integer",
			"description": "Optional max tokens per response (default: 8192)"
		}
	},
	"required": ["task"]
}`)
}

type delegateParams struct {
	Task         string   `json:"task"`
	Tools        []string `json:"tools"`
	SystemPrompt string   `json:"system_prompt"`
	MaxTokens    int      `json:"max_tokens"`
}

const (
	delegateTimeout      = 5 * time.Minute
	delegateMaxOutputLen = 30000
)

func (d *DelegateTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p delegateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return &Result{Content: fmt.Sprintf("invalid params: %v", err), IsError: true}, nil
	}

	if p.Task == "" {
		return &Result{Content: "task parameter is required", IsError: true}, nil
	}

	// Check depth limit
	if d.depth >= d.maxDepth {
		return &Result{
			Content: fmt.Sprintf("maximum delegation depth reached (%d)", d.maxDepth),
			IsError: true,
		}, nil
	}

	// Build child tool registry
	childTools := d.buildChildTools(p.Tools)

	ctx, cancel := context.WithTimeout(ctx, delegateTimeout)
	defer cancel()

	result, err := d.runFn(ctx, RunConfig{
		Provider:     d.provider,
		Tools:        childTools,
		SystemPrompt: p.SystemPrompt,
		Prompt:       p.Task,
		MaxTokens:    p.MaxTokens,
	})
	if err != nil {
		return &Result{Content: fmt.Sprintf("sub-agent error: %v", err), IsError: true}, nil
	}

	// Truncate if too long
	if len(result) > delegateMaxOutputLen {
		result = result[:delegateMaxOutputLen] + "\n... (output truncated)"
	}

	return &Result{Content: result}, nil
}

// buildChildTools creates a filtered tool registry for the child agent.
// It excludes the parent's delegate tool and adds a child delegate at depth+1.
func (d *DelegateTool) buildChildTools(allowlist []string) *Registry {
	child := NewRegistry()

	if len(allowlist) > 0 {
		// Only include explicitly allowed tools (excluding "delegate")
		for _, name := range allowlist {
			if name == "delegate" {
				continue
			}
			if t, ok := d.tools.Get(name); ok {
				child.Register(t)
			}
		}
	} else {
		// Include all tools except delegate
		for _, t := range d.tools.All() {
			if t.Name() == "delegate" {
				continue
			}
			child.Register(t)
		}
	}

	// Add child delegate at depth+1 (if not at max depth)
	if d.depth+1 < d.maxDepth {
		child.Register(d.newChildDelegate(child))
	}

	return child
}
