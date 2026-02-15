package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/scheduler"
)

// CronCreateTool allows the AI to create scheduled jobs
type CronCreateTool struct {
	scheduler *scheduler.Scheduler
	store     *cluster.Store
	ctxMgr    *cluster.ContextManager
}

// NewCronCreateTool creates a new cron job creation tool
func NewCronCreateTool() *CronCreateTool {
	return NewCronCreateToolWithScheduler(nil)
}

// NewCronCreateToolWithScheduler creates a cron tool with a shared scheduler
func NewCronCreateToolWithScheduler(sched interface{}) *CronCreateTool {
	var s *scheduler.Scheduler
	if sched != nil {
		s = sched.(*scheduler.Scheduler)
	} else {
		s = scheduler.NewScheduler(config.StateDir() + "/scheduler")
		s.Load()
	}
	return &CronCreateTool{
		scheduler: s,
		store:     cluster.NewStore(config.StateDir()),
		ctxMgr:    cluster.NewContextManager(config.ConfigDir()),
	}
}

func (t *CronCreateTool) Name() string {
	return "cron_create"
}

func (t *CronCreateTool) Description() string {
	return `Create a scheduled cron job. Use this when the user wants something to run automatically at intervals.

IMPORTANT: Use this tool for ANY time-based recurring task like:
- "every 5 minutes", "every hour", "daily at 9am", "every monday"
- "her saat", "her 5 dakikada", "günlük"

CRITICAL - Task field must be VERY DETAILED and WELL-DEFINED:
- The task runs WITHOUT any conversation context
- Write EXACT step-by-step instructions for what to do
- Include ALL criteria, formats, and expected outputs
- Be specific about what to look for and how to respond

BAD task: "Kanalı kontrol et"
GOOD task: "1. Kanaldaki mesajlardaki URL/domain'leri bul. 2. Her domain için web sitesini ziyaret et. 3. Image/video/audio AI modeli kullanıyorlarsa puan ver (1-10). 4. Format: 'domain.com - 8/10 - Video generation' şeklinde yanıtla. 5. Hiç domain yoksa 'Yeni domain yok' de."

The job will run the specified agent with the given task at the scheduled times.`
}

func (t *CronCreateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {
				"type": "string",
				"description": "Job name (lowercase, no spaces, use hyphens). Example: channel-monitor, daily-report"
			},
			"schedule": {
				"type": "string",
				"description": "Schedule in plain English. Examples: 'every 5 minutes', 'every hour', 'every day at 9am', 'every monday at 10am'"
			},
			"agent": {
				"type": "string",
				"description": "Name of the agent to run the task"
			},
			"task": {
				"type": "string",
				"description": "The task/prompt to send to the agent when the job runs"
			},
			"channel": {
				"type": "string",
				"description": "Optional: Slack channel ID to read messages from. Use when the user says 'track this channel' or 'monitor messages here'. If not provided, the job runs without channel context."
			},
			"skip_replied": {
				"type": "boolean",
				"description": "Skip messages that already have a bot reply. Default: true. Set to false if user wants to re-analyze or update previous responses."
			}
		},
		"required": ["name", "schedule", "agent", "task"]
	}`)
}

type cronCreateParams struct {
	Name        string `json:"name"`
	Schedule    string `json:"schedule"`
	Agent       string `json:"agent"`
	Task        string `json:"task"`
	Channel     string `json:"channel,omitempty"`
	SkipReplied *bool  `json:"skip_replied,omitempty"` // pointer to detect if set, default true
}

func (t *CronCreateTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p cronCreateParams
	if err := json.Unmarshal(params, &p); err != nil {
		return &Result{Content: fmt.Sprintf("Invalid parameters: %v", err), IsError: true}, nil
	}

	if p.Name == "" {
		return &Result{Content: "Job name is required", IsError: true}, nil
	}
	if p.Schedule == "" {
		return &Result{Content: "Schedule is required", IsError: true}, nil
	}
	if p.Agent == "" {
		return &Result{Content: "Agent name is required", IsError: true}, nil
	}
	if p.Task == "" {
		return &Result{Content: "Task is required", IsError: true}, nil
	}

	// Normalize name
	p.Name = strings.ToLower(strings.ReplaceAll(p.Name, " ", "-"))

	// Get current context
	clusterName, namespace, err := t.ctxMgr.RequireCurrent()
	if err != nil {
		clusterName = "default"
		namespace = "default"
	}

	// Validate agent exists
	if !t.store.AgentBindingExists(clusterName, namespace, p.Agent) {
		return &Result{
			Content: fmt.Sprintf("Agent not found: %s. Create it first with agent_spawn.", p.Agent),
			IsError: true,
		}, nil
	}

	// Parse and validate schedule
	cron, err := scheduler.ParseSchedule(p.Schedule)
	if err != nil {
		return &Result{Content: fmt.Sprintf("Invalid schedule: %v", err), IsError: true}, nil
	}

	// Create the job
	job, err := t.scheduler.CreateJob(p.Name, p.Schedule, p.Agent, p.Task, clusterName, namespace)
	if err != nil {
		return &Result{Content: fmt.Sprintf("Failed to create job: %v", err), IsError: true}, nil
	}

	// Set config options
	if job.Config == nil {
		job.Config = make(map[string]string)
	}
	if p.Channel != "" {
		job.Config["channel"] = p.Channel
	}
	// Default skip_replied to true if not specified
	if p.SkipReplied == nil || *p.SkipReplied {
		job.Config["skip_replied"] = "true"
	} else {
		job.Config["skip_replied"] = "false"
	}
	t.scheduler.Save()

	// Build response
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Scheduled job '%s' created!\n\n", job.Name))
	sb.WriteString(fmt.Sprintf("Schedule: %s\n", scheduler.FormatSchedule(cron)))
	sb.WriteString(fmt.Sprintf("Agent: %s\n", job.Agent))
	sb.WriteString(fmt.Sprintf("Task: %s\n", truncateString(job.Task, 100)))
	if p.Channel != "" {
		sb.WriteString(fmt.Sprintf("Channel: %s (will read recent messages)\n", p.Channel))
	}
	if job.NextRun != nil {
		sb.WriteString(fmt.Sprintf("Next run: %s\n", job.NextRun.Format("Jan 02 15:04")))
	}

	return &Result{Content: sb.String()}, nil
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// AgentListTool allows the AI to list existing agents
type AgentListTool struct {
	store  *cluster.Store
	ctxMgr *cluster.ContextManager
}

// NewAgentListTool creates a new agent list tool
func NewAgentListTool() *AgentListTool {
	return &AgentListTool{
		store:  cluster.NewStore(config.StateDir()),
		ctxMgr: cluster.NewContextManager(config.ConfigDir()),
	}
}

func (t *AgentListTool) Name() string {
	return "agent_list"
}

func (t *AgentListTool) Description() string {
	return `List all existing agents. Use this BEFORE creating a new agent to check if a suitable one already exists.

Always check existing agents before creating new ones to avoid duplicates.`
}

func (t *AgentListTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {},
		"required": []
	}`)
}

func (t *AgentListTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	clusterName, namespace, err := t.ctxMgr.RequireCurrent()
	if err != nil {
		clusterName = "default"
		namespace = "default"
	}

	agents, err := t.store.ListAgentBindings(clusterName, namespace)
	if err != nil {
		return &Result{Content: fmt.Sprintf("Failed to list agents: %v", err), IsError: true}, nil
	}

	if len(agents) == 0 {
		return &Result{Content: "No agents found."}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Agents in %s/%s:\n\n", clusterName, namespace))
	for _, ag := range agents {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", ag.Name, ag.Description))
		if len(ag.Triggers) > 0 {
			sb.WriteString(fmt.Sprintf("  Triggers: %s\n", strings.Join(ag.Triggers, ", ")))
		}
	}

	return &Result{Content: sb.String()}, nil
}

// CronListTool allows the AI to list scheduled jobs
type CronListTool struct {
	scheduler *scheduler.Scheduler
	ctxMgr    *cluster.ContextManager
}

// NewCronListTool creates a new cron list tool
func NewCronListTool() *CronListTool {
	return NewCronListToolWithScheduler(nil)
}

// NewCronListToolWithScheduler creates a cron list tool with a shared scheduler
func NewCronListToolWithScheduler(sched interface{}) *CronListTool {
	var s *scheduler.Scheduler
	if sched != nil {
		s = sched.(*scheduler.Scheduler)
	} else {
		s = scheduler.NewScheduler(config.StateDir() + "/scheduler")
		s.Load()
	}
	return &CronListTool{
		scheduler: s,
		ctxMgr:    cluster.NewContextManager(config.ConfigDir()),
	}
}

func (t *CronListTool) Name() string {
	return "cron_list"
}

func (t *CronListTool) Description() string {
	return `List all scheduled cron jobs. Use this to check existing scheduled tasks before creating new ones.`
}

func (t *CronListTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {},
		"required": []
	}`)
}

func (t *CronListTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	clusterName, namespace, err := t.ctxMgr.RequireCurrent()
	if err != nil {
		clusterName = "default"
		namespace = "default"
	}

	jobs := t.scheduler.ListJobs(clusterName, namespace)
	if len(jobs) == 0 {
		return &Result{Content: "No scheduled jobs found."}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Scheduled jobs in %s/%s:\n\n", clusterName, namespace))
	for _, job := range jobs {
		status := "enabled"
		if !job.Enabled {
			status = "disabled"
		}
		sb.WriteString(fmt.Sprintf("- %s (%s)\n", job.Name, status))
		sb.WriteString(fmt.Sprintf("  Schedule: %s\n", scheduler.FormatSchedule(job.Cron)))
		sb.WriteString(fmt.Sprintf("  Agent: %s\n", job.Agent))
		if job.NextRun != nil {
			sb.WriteString(fmt.Sprintf("  Next run: %s\n", job.NextRun.Format("Jan 02 15:04")))
		}
	}

	return &Result{Content: sb.String()}, nil
}
