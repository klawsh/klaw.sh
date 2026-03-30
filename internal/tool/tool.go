// Package tool defines the tool interface and registry.
package tool

import (
	"context"
	"encoding/json"
	"sort"
)

// Tool is any capability the agent can invoke.
type Tool interface {
	// Name returns the tool name (e.g., "bash", "read").
	Name() string

	// Description returns a human-readable description.
	Description() string

	// Schema returns the JSON Schema for tool parameters.
	Schema() json.RawMessage

	// Execute runs the tool with the given parameters.
	Execute(ctx context.Context, params json.RawMessage) (*Result, error)
}

// Result is the outcome of tool execution.
type Result struct {
	Content string
	IsError bool
}

// Registry holds available tools.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry creates a new tool registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get retrieves a tool by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// All returns all registered tools.
func (r *Registry) All() []Tool {
	result := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// Filter returns a new registry containing only the named tools.
// If allowlist is empty, the original registry is returned.
func (r *Registry) Filter(allowlist []string) *Registry {
	if len(allowlist) == 0 {
		return r
	}
	filtered := NewRegistry()
	for _, name := range allowlist {
		if t, ok := r.tools[name]; ok {
			filtered.Register(t)
		}
	}
	return filtered
}

// Names returns a sorted list of all registered tool names.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DefaultRegistry returns a registry with all standard tools.
func DefaultRegistry(workDir string) *Registry {
	return DefaultRegistryWithScheduler(workDir, nil)
}

// DefaultRegistryWithScheduler returns a registry with all standard tools and a shared scheduler.
func DefaultRegistryWithScheduler(workDir string, sched interface{}) *Registry {
	r := NewRegistry()
	r.Register(NewBash(workDir))
	r.Register(NewRead(workDir))
	r.Register(NewWrite(workDir))
	r.Register(NewEdit(workDir))
	r.Register(NewGlob(workDir))
	r.Register(NewGrep(workDir))
	r.Register(NewSkillTool())
	r.Register(NewWebFetch())
	r.Register(NewWebSearch())
	r.Register(NewAgentTool())
	r.Register(NewAgentListTool())
	r.Register(NewCronCreateToolWithScheduler(sched))
	r.Register(NewCronListToolWithScheduler(sched))
	return r
}
