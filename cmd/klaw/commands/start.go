package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/eachlabs/klaw/internal/agent"
	"github.com/eachlabs/klaw/internal/channel"
	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/memory"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/scheduler"
	"github.com/eachlabs/klaw/internal/skill"
	"github.com/eachlabs/klaw/internal/tool"
	"github.com/spf13/cobra"
)

var (
	startModel    string
	startProvider string
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start klaw (Slack bot + scheduler)",
	Long: `Start klaw with all components:
- Slack bot for receiving messages
- Scheduler for cron jobs
- All configured agents

Required environment variables:
  SLACK_BOT_TOKEN  - Slack bot token (xoxb-...)
  SLACK_APP_TOKEN  - Slack app token (xapp-...)
  ANTHROPIC_API_KEY or OPENROUTER_API_KEY

Examples:
  klaw start
  klaw start -p anthropic
  klaw start -m claude-sonnet-4-20250514`,
	RunE: runStart,
}

func init() {
	startCmd.Flags().StringVarP(&startModel, "model", "m", "", "model to use")
	startCmd.Flags().StringVarP(&startProvider, "provider", "p", "", "provider: anthropic, openrouter, eachlabs")
	rootCmd.AddCommand(startCmd)
}

func runStart(cmd *cobra.Command, args []string) error {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Get Slack tokens
	botToken := os.Getenv("SLACK_BOT_TOKEN")
	appToken := os.Getenv("SLACK_APP_TOKEN")

	if botToken == "" || appToken == "" {
		fmt.Println("ERROR: Slack tokens not set")
		fmt.Println("")
		fmt.Println("Set environment variables:")
		fmt.Println("  export SLACK_BOT_TOKEN=xoxb-...")
		fmt.Println("  export SLACK_APP_TOKEN=xapp-...")
		return fmt.Errorf("Slack tokens required")
	}

	// Determine provider
	var prov provider.Provider
	providerName := startProvider

	if providerName == "" {
		if os.Getenv("OPENROUTER_API_KEY") != "" {
			providerName = "openrouter"
		} else if os.Getenv("EACHLABS_API_KEY") != "" {
			providerName = "eachlabs"
		} else if os.Getenv("ANTHROPIC_API_KEY") != "" {
			providerName = "anthropic"
		} else {
			providerName = "anthropic"
		}
	}

	// Determine model
	model := startModel
	if model == "" {
		if provCfg, ok := cfg.Provider[providerName]; ok && provCfg.Model != "" {
			model = provCfg.Model
		}
	}
	if model == "" {
		switch providerName {
		case "openrouter":
			model = "anthropic/claude-sonnet-4"
		case "eachlabs":
			model = "anthropic/claude-sonnet-4-5"
		default:
			model = "claude-sonnet-4-20250514"
		}
	}

	// Create provider
	switch providerName {
	case "openrouter":
		apiKey := os.Getenv("OPENROUTER_API_KEY")
		if apiKey == "" {
			return fmt.Errorf("OPENROUTER_API_KEY not set")
		}
		prov, err = provider.NewOpenRouter(provider.OpenRouterConfig{
			APIKey: apiKey,
			Model:  model,
		})
		if err != nil {
			return fmt.Errorf("failed to create openrouter provider: %w", err)
		}

	case "eachlabs":
		apiKey := os.Getenv("EACHLABS_API_KEY")
		if apiKey == "" {
			if eachCfg, ok := cfg.Provider["eachlabs"]; ok {
				apiKey = eachCfg.APIKey
			}
		}
		if apiKey == "" {
			return fmt.Errorf("EACHLABS_API_KEY not set")
		}
		prov, err = provider.NewEachLabs(provider.EachLabsConfig{
			APIKey: apiKey,
			Model:  model,
		})
		if err != nil {
			return fmt.Errorf("failed to create eachlabs provider: %w", err)
		}

	default: // anthropic
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			if anthropicCfg, ok := cfg.Provider["anthropic"]; ok {
				apiKey = anthropicCfg.APIKey
			}
		}
		if apiKey == "" {
			return fmt.Errorf("ANTHROPIC_API_KEY not set")
		}
		prov, err = provider.NewAnthropic(provider.AnthropicConfig{
			APIKey: apiKey,
			Model:  model,
		})
		if err != nil {
			return fmt.Errorf("failed to create provider: %w", err)
		}
	}

	// Get context
	ctxMgr := cluster.NewContextManager(config.ConfigDir())
	clusterName, namespace, _ := ctxMgr.RequireCurrent()
	if clusterName == "" {
		clusterName = "default"
		namespace = "default"
	}

	// Get working directory
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}

	// Create scheduler (before tools so we can share it)
	sched := scheduler.NewScheduler(config.StateDir() + "/scheduler")
	if err := sched.Load(); err != nil {
		fmt.Printf("Warning: failed to load scheduler: %v\n", err)
	}

	// Create tools with shared scheduler
	tools := tool.DefaultRegistryWithScheduler(workDir, sched)

	// Create memory
	mem := memory.NewFileMemory(cfg.WorkspaceDir())

	// Load workspace and build system prompt
	ws, err := mem.LoadWorkspace(cmd.Context())
	if err != nil {
		ws = &memory.Workspace{}
	}
	systemPrompt := memory.BuildSystemPrompt(ws)

	// Load skills from SKILL.md files
	store := cluster.NewStore(config.StateDir())
	agents, _ := store.ListAgentBindings(clusterName, namespace)
	skillLoader := skill.NewSkillLoader(config.ConfigDir() + "/skills")

	// Default skills that all agents have
	defaultSkills := []string{"find-skills"}

	var skillNames []string
	skillSet := make(map[string]bool)

	// Add default skills first
	for _, skillName := range defaultSkills {
		if !skillSet[skillName] {
			skillSet[skillName] = true
			skillNames = append(skillNames, skillName)
		}
	}

	// Add agent-specific skills
	for _, ag := range agents {
		for _, skillName := range ag.Skills {
			if skillSet[skillName] {
				continue
			}
			skillSet[skillName] = true
			skillNames = append(skillNames, skillName)
		}
	}

	// Load skill prompts from SKILL.md files
	skillsPrompt := skillLoader.GetSkillsPrompt(skillNames)
	if skillsPrompt != "" {
		systemPrompt = systemPrompt + skillsPrompt
	}

	// Add Slack instructions
	slackInstructions := `

# Slack Communication Guidelines

You are communicating through Slack. Follow these rules:

1. **Be concise**: Keep responses short and to the point.
2. **No tool details**: Never mention tool calls. Just provide results.
3. **Direct answers**: Answer directly without preamble.

# Scheduled Tasks (IMPORTANT)

When a user mentions time-based recurring tasks like "her 5 dakikada", "every hour", "daily":

1. **USE cron_create tool** - MANDATORY for scheduled tasks
2. Do NOT just create an agent - agents don't run automatically
3. Create a cron job that triggers the agent

CRITICAL - Channel parameter for cron jobs:
- Every message starts with [Context: channel=XXXXX] - this is the current Slack channel ID
- When user says "bu kanalı", "this channel", "kanalı takip et", "monitor here" -> YOU MUST pass this channel ID to cron_create
- Example: User in channel C0A8KUEBT3M says "her dakika bu kanalı kontrol et"
  -> Call cron_create with channel="C0A8KUEBT3M" (from the context)
- If user wants a general task not related to channel monitoring -> omit channel parameter

Example with channel monitoring:
User: "Her 5 dakikada bu kanaldaki mesajları analiz et"
-> cron_create with channel parameter set to current channel

Example without channel:
User: "Her gün saat 9'da hava durumunu söyle"
-> cron_create WITHOUT channel parameter

# Agent Management

1. **FIRST check existing agents** with agent_list tool
2. **If agent exists**: Ask to update or create new
3. **If no suitable agent**: Create new one

# Clarifying Questions

Before creating agents or cron jobs, ASK about:
- Evaluation criteria
- Output format
- Exclusions

# NEVER GIVE UP - Research & Learn

CRITICAL: You must NEVER say "I can't do this" or "I don't have this capability" without trying!

When faced with a task you don't immediately know how to do:

1. **Check your skills**: Use skill action=list to see what you know
2. **Search for skills**: Use skill action=install name=<relevant-skill>
3. **Research online**: Use web_fetch to learn how to do it
4. **Create a skill**: If no skill exists, research and create one yourself
5. **Try multiple approaches**: If one method fails, try another
6. **Break it down**: Complex tasks can be split into smaller steps

Examples:
- "Generate an image" -> Find/install image generation skill, then use it
- "Analyze Facebook ads" -> Check facebook-ads skill, follow its instructions
- "Create a video" -> Search for video generation skill, install it, use it

You are a capable AI that can LEARN and ADAPT. Use your tools to extend your abilities!
`
	systemPrompt = systemPrompt + slackInstructions

	// Create Slack channel
	slackChan, err := channel.NewSlackChannel(channel.SlackConfig{
		BotToken: botToken,
		AppToken: appToken,
	})
	if err != nil {
		return fmt.Errorf("failed to create Slack channel: %w", err)
	}

	// Create agent
	ag := agent.New(agent.Config{
		Provider:     prov,
		Channel:      slackChan,
		Tools:        tools,
		Memory:       mem,
		SystemPrompt: systemPrompt,
	})

	// Set job runner - this runs the agent for cron jobs
	sched.SetJobRunner(func(ctx context.Context, job *scheduler.Job) (string, error) {
		fmt.Printf("\n")
		fmt.Printf("╭─────────────────────────────────────────╮\n")
		fmt.Printf("│  🕐 CRON JOB RUNNING                    │\n")
		fmt.Printf("╰─────────────────────────────────────────╯\n")
		fmt.Printf("  Job:   %s\n", job.Name)
		fmt.Printf("  Agent: %s\n", job.Agent)
		fmt.Printf("  Task:  %s\n", job.Task)

		// Read channel messages if configured
		var channelID string
		if job.Config != nil {
			channelID = job.Config["channel"]
		}

		var messages []channel.ChannelMessage
		if channelID != "" {
			fmt.Printf("  Channel: %s\n", channelID)

			// Get messages since previous run (stored by scheduler before updating LastRun)
			since := time.Now().Add(-5 * time.Minute)
			if prevRunStr, ok := job.Config["_previousRun"]; ok {
				if prevUnix, err := strconv.ParseInt(prevRunStr, 10, 64); err == nil {
					since = time.Unix(prevUnix, 0)
				}
			}
			fmt.Printf("  Since: %s\n", since.Format("15:04:05"))

			var err error
			messages, err = slackChan.GetChannelHistory(channelID, since, 50)
			if err != nil {
				fmt.Printf("  Error reading channel: %v\n", err)
			} else {
				fmt.Printf("  Messages: %d\n", len(messages))
				for i, m := range messages {
					fmt.Printf("    [%d] %s\n", i+1, m.Text[:min(50, len(m.Text))])
				}
			}
		}
		fmt.Printf("\n")

		// Check if we should skip already-replied messages (default: true)
		skipReplied := true
		if job.Config != nil && job.Config["skip_replied"] == "false" {
			skipReplied = false
		}

		// Filter out messages that already have bot replies (if enabled)
		var newMessages []channel.ChannelMessage
		var skippedReplied int
		for _, msg := range messages {
			if skipReplied && msg.SlackTS != "" && slackChan.HasBotReply(channelID, msg.SlackTS) {
				skippedReplied++
				continue
			}
			newMessages = append(newMessages, msg)
		}

		if skippedReplied > 0 {
			fmt.Printf("  Skipped %d messages (already replied)\n", skippedReplied)
		}

		if len(newMessages) == 0 {
			fmt.Printf("  No new messages to process\n")
			return "No new messages", nil
		}

		fmt.Printf("  New messages to process: %d\n", len(newMessages))

		// Process each message individually - let the AI decide what to do
		var results []string
		for _, msg := range newMessages {
			// Build prompt for this specific message
			var prompt strings.Builder
			prompt.WriteString("You are running as a SCHEDULED CRON JOB. Do NOT create new cron jobs or agents.\n")
			prompt.WriteString("Your task:\n")
			prompt.WriteString(job.Task)
			prompt.WriteString("\n\nMessage to process:\n")
			prompt.WriteString(msg.Text)
			prompt.WriteString("\n\nIf this message is relevant to your task, respond with your analysis. If not relevant, respond with exactly: SKIP")

			// Execute with agent
			result, err := agent.RunOnce(ctx, agent.RunOnceConfig{
				Provider:     prov,
				Tools:        tools,
				SystemPrompt: systemPrompt,
				Prompt:       prompt.String(),
			})
			if err != nil {
				fmt.Printf("  ❌ Error analyzing %s: %v\n", msg.Text[:min(30, len(msg.Text))], err)
				continue
			}

			// Post as thread reply if AI decided to respond (not SKIP)
			if result != "" && strings.TrimSpace(strings.ToUpper(result)) != "SKIP" {
				if len(result) > 1000 {
					result = result[:1000] + "..."
				}
				if msg.SlackTS != "" {
					slackChan.PostThreadReply(channelID, msg.SlackTS, result)
					fmt.Printf("  ✓ Replied to: %s\n", msg.Text[:min(30, len(msg.Text))])
				}
				results = append(results, result)
			} else {
				fmt.Printf("  ⊘ Skipped (not relevant): %s\n", msg.Text[:min(30, len(msg.Text))])
			}
		}

		fmt.Printf("  ✓ Completed (%d analyzed)\n", len(results))
		return strings.Join(results, "\n---\n"), nil
	})

	// Handle signals
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	// Start Slack channel
	if err := slackChan.Start(ctx); err != nil {
		return fmt.Errorf("failed to start Slack channel: %w", err)
	}

	// Start scheduler
	sched.Start(ctx)

	// Print startup info
	fmt.Println("╭─────────────────────────────────────────╮")
	fmt.Println("│               klaw                      │")
	fmt.Println("│        AI Employee Platform            │")
	fmt.Println("╰─────────────────────────────────────────╯")
	fmt.Printf("Provider:  %s\n", providerName)
	fmt.Printf("Model:     %s\n", model)
	fmt.Printf("Namespace: %s/%s\n", clusterName, namespace)
	fmt.Println("")

	// Show agents
	if len(agents) > 0 {
		fmt.Println("Agents:")
		for _, ag := range agents {
			fmt.Printf("  • %s: %s\n", ag.Name, ag.Description)
		}
		fmt.Println("")
	}

	// Show cron jobs
	jobs := sched.ListJobs(clusterName, namespace)
	if len(jobs) > 0 {
		fmt.Println("Scheduled Jobs:")
		for _, job := range jobs {
			status := "✓"
			if !job.Enabled {
				status = "○"
			}
			fmt.Printf("  %s %s: %s (agent: %s)\n", status, job.Name, job.Schedule, job.Agent)
		}
		fmt.Println("")
	}

	fmt.Println("Slack bot active. Listening for messages...")
	fmt.Println("Scheduler running. Cron jobs will execute automatically.")
	fmt.Println("")
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println("")

	// Run agent
	return ag.Run(ctx)
}
