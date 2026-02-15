// Package skill provides a plugin system for agent capabilities.
package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eachlabs/klaw/internal/tool"
)

// Skill represents an installable capability for agents.
type Skill struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Description  string            `json:"description"`
	Author       string            `json:"author,omitempty"`
	Homepage     string            `json:"homepage,omitempty"`
	Tools        []string          `json:"tools"`        // Tool names this skill provides
	SystemPrompt string            `json:"system_prompt"` // Additional system prompt
	Config       map[string]string `json:"config,omitempty"`
	Installed    bool              `json:"installed"`
	Source       string            `json:"source"` // "builtin", "local", "remote"
}

// Registry manages available and installed skills.
type Registry struct {
	skillsDir string
	skills    map[string]*Skill
}

// NewRegistry creates a new skill registry.
func NewRegistry(skillsDir string) *Registry {
	r := &Registry{
		skillsDir: skillsDir,
		skills:    make(map[string]*Skill),
	}
	r.loadBuiltins()
	r.loadInstalled()
	r.loadRemoteSkills()
	return r
}

// loadBuiltins registers all built-in skills.
// Note: Default agent skills (eachlabs/*, vercel-labs/agent-browser) are fetched from skills.sh
func (r *Registry) loadBuiltins() {
	builtins := []*Skill{
		// === STANDARD BUILTIN SKILLS ===
		{
			Name:        "web-search",
			Version:     "1.0.0",
			Description: "Search the web using multiple search engines",
			Tools:       []string{"web_search"},
			SystemPrompt: `You have web search capabilities. When users ask questions that require current information or facts you don't know, use the web_search tool to find answers. Always cite your sources.`,
			Source:      "builtin",
		},
		{
			Name:        "browser",
			Version:     "1.0.0",
			Description: "Browse websites, take screenshots, interact with pages",
			Tools:       []string{"browser_open", "browser_screenshot", "browser_click", "browser_type"},
			SystemPrompt: `You can browse the web interactively. Use browser tools to:
- Open URLs and read page content
- Take screenshots of pages
- Click on elements and fill forms
- Navigate between pages`,
			Source: "builtin",
		},
		{
			Name:        "code-exec",
			Version:     "1.0.0",
			Description: "Execute code in Python, JavaScript, and other languages",
			Tools:       []string{"python_exec", "javascript_exec", "shell_exec"},
			SystemPrompt: `You can execute code directly. Use this for:
- Running calculations and data processing
- Testing code snippets
- Automating tasks
Always be careful with code execution and validate inputs.`,
			Source: "builtin",
		},
		{
			Name:        "git",
			Version:     "1.0.0",
			Description: "Git operations - clone, commit, push, pull, branch management",
			Tools:       []string{"git_clone", "git_status", "git_commit", "git_push", "git_pull", "git_branch"},
			SystemPrompt: `You have Git capabilities. You can:
- Clone repositories
- Check status and diff
- Create commits with meaningful messages
- Push and pull changes
- Manage branches
Always confirm before destructive operations like force push.`,
			Source: "builtin",
		},
		{
			Name:        "docker",
			Version:     "1.0.0",
			Description: "Docker/Podman container management",
			Tools:       []string{"container_run", "container_list", "container_stop", "container_logs", "image_build"},
			SystemPrompt: `You can manage containers. Use this for:
- Running containers from images
- Building images from Dockerfiles
- Viewing container logs
- Managing container lifecycle`,
			Source: "builtin",
		},
		{
			Name:        "api",
			Version:     "1.0.0",
			Description: "Make HTTP/API requests to external services",
			Tools:       []string{"http_get", "http_post", "http_request"},
			SystemPrompt: `You can make HTTP requests to APIs. Use this for:
- Fetching data from REST APIs
- Posting data to webhooks
- Integrating with external services
Always handle API keys and secrets securely.`,
			Source: "builtin",
		},
		{
			Name:        "database",
			Version:     "1.0.0",
			Description: "Query databases - PostgreSQL, MySQL, SQLite",
			Tools:       []string{"sql_query", "sql_execute"},
			SystemPrompt: `You can interact with databases. Use this for:
- Running SELECT queries to fetch data
- Executing INSERT, UPDATE, DELETE operations
- Analyzing data with SQL
Always be careful with data modifications and confirm before DELETE/UPDATE.`,
			Source: "builtin",
		},
		{
			Name:        "slack",
			Version:     "1.0.0",
			Description: "Slack integration - send messages, read channels",
			Tools:       []string{"slack_send", "slack_read", "slack_react"},
			SystemPrompt: `You can interact with Slack. Use this for:
- Sending messages to channels
- Reading channel history
- Adding reactions to messages`,
			Source: "builtin",
		},
		{
			Name:        "email",
			Version:     "1.0.0",
			Description: "Send and read emails",
			Tools:       []string{"email_send", "email_read", "email_search"},
			SystemPrompt: `You can manage emails. Use this for:
- Sending emails with attachments
- Reading inbox messages
- Searching email history`,
			Source: "builtin",
		},
		{
			Name:        "calendar",
			Version:     "1.0.0",
			Description: "Manage calendar events",
			Tools:       []string{"calendar_list", "calendar_create", "calendar_update"},
			SystemPrompt: `You can manage calendar events. Use this for:
- Listing upcoming events
- Creating new meetings
- Updating or canceling events`,
			Source: "builtin",
		},
	}

	for _, s := range builtins {
		r.skills[s.Name] = s
	}
}

// loadInstalled loads installed skills from disk.
func (r *Registry) loadInstalled() {
	if r.skillsDir == "" {
		return
	}

	installedFile := filepath.Join(r.skillsDir, "installed.json")
	data, err := os.ReadFile(installedFile)
	if err != nil {
		return
	}

	var installed []string
	if err := json.Unmarshal(data, &installed); err != nil {
		return
	}

	for _, name := range installed {
		if skill, ok := r.skills[name]; ok {
			skill.Installed = true
		}
	}
}

// saveInstalled persists the installed skills list.
func (r *Registry) saveInstalled() error {
	if r.skillsDir == "" {
		return nil
	}

	if err := os.MkdirAll(r.skillsDir, 0755); err != nil {
		return err
	}

	var installed []string
	for name, skill := range r.skills {
		if skill.Installed {
			installed = append(installed, name)
		}
	}

	data, err := json.MarshalIndent(installed, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(r.skillsDir, "installed.json"), data, 0644)
}

// List returns all available skills.
func (r *Registry) List() []*Skill {
	var skills []*Skill
	for _, s := range r.skills {
		skills = append(skills, s)
	}
	return skills
}

// Get returns a skill by name.
func (r *Registry) Get(name string) (*Skill, error) {
	skill, ok := r.skills[name]
	if !ok {
		return nil, fmt.Errorf("skill not found: %s", name)
	}
	return skill, nil
}

// Install marks a skill as installed.
func (r *Registry) Install(name string) error {
	skill, ok := r.skills[name]
	if !ok {
		return fmt.Errorf("skill not found: %s", name)
	}

	skill.Installed = true
	return r.saveInstalled()
}

// Uninstall removes a skill.
func (r *Registry) Uninstall(name string) error {
	skill, ok := r.skills[name]
	if !ok {
		return fmt.Errorf("skill not found: %s", name)
	}

	skill.Installed = false
	return r.saveInstalled()
}

// InstallFromURL is implemented in remote.go

// GetToolsForSkills returns the combined tools for a list of skill names.
func (r *Registry) GetToolsForSkills(skillNames []string, workDir string) (*tool.Registry, string) {
	tools := tool.NewRegistry()
	var prompts []string

	for _, name := range skillNames {
		skill, ok := r.skills[name]
		if !ok {
			continue
		}

		if skill.SystemPrompt != "" {
			prompts = append(prompts, fmt.Sprintf("## %s Skill\n%s", skill.Name, skill.SystemPrompt))
		}

		// Add tools based on skill
		for _, toolName := range skill.Tools {
			switch toolName {
			// Core tools (always available)
			case "bash":
				tools.Register(tool.NewBash(workDir))
			case "read":
				tools.Register(tool.NewRead(workDir))
			case "write":
				tools.Register(tool.NewWrite(workDir))
			case "edit":
				tools.Register(tool.NewEdit(workDir))
			case "glob":
				tools.Register(tool.NewGlob(workDir))
			case "grep":
				tools.Register(tool.NewGrep(workDir))

			// Web search - stub for now
			case "web_search":
				tools.Register(&webSearchTool{})

			// HTTP tools - stub for now
			case "http_get", "http_post", "http_request":
				tools.Register(&httpTool{name: toolName})

			// Other tools would be registered here
			// For now, they're stubs that will be implemented later
			}
		}
	}

	systemPrompt := ""
	if len(prompts) > 0 {
		systemPrompt = "\n\n# Skills\n" + strings.Join(prompts, "\n\n")
	}

	return tools, systemPrompt
}

// Stub tool implementations

type webSearchTool struct{}

func (t *webSearchTool) Name() string        { return "web_search" }
func (t *webSearchTool) Description() string { return "Search the web for information" }
func (t *webSearchTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "The search query"
			}
		},
		"required": ["query"]
	}`)
}
func (t *webSearchTool) Execute(ctx context.Context, params json.RawMessage) (*tool.Result, error) {
	var p struct {
		Query string `json:"query"`
	}
	json.Unmarshal(params, &p)
	// TODO: Implement actual web search
	return &tool.Result{
		Content: fmt.Sprintf("Web search for '%s' - feature coming soon", p.Query),
	}, nil
}

type httpTool struct {
	name string
}

func (t *httpTool) Name() string        { return t.name }
func (t *httpTool) Description() string { return "Make HTTP requests" }
func (t *httpTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {
				"type": "string",
				"description": "The URL to request"
			},
			"method": {
				"type": "string",
				"description": "HTTP method (GET, POST, etc.)",
				"default": "GET"
			}
		},
		"required": ["url"]
	}`)
}
func (t *httpTool) Execute(ctx context.Context, params json.RawMessage) (*tool.Result, error) {
	var p struct {
		URL string `json:"url"`
	}
	json.Unmarshal(params, &p)
	return &tool.Result{
		Content: fmt.Sprintf("HTTP request to '%s' - feature coming soon", p.URL),
	}, nil
}
