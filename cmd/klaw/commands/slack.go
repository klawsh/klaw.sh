package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/eachlabs/klaw/internal/agent"
	"github.com/eachlabs/klaw/internal/channel"
	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/memory"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/skill"
	"github.com/eachlabs/klaw/internal/tool"
	"github.com/spf13/cobra"
)

var (
	slackCmdBotToken  string
	slackCmdAppToken  string
	slackCmdModel     string
	slackCmdProvider  string
)

var slackCmd = &cobra.Command{
	Use:   "slack",
	Short: "Run Slack bot",
	Long: `Run klaw as a Slack bot using Socket Mode.

The bot responds to:
  - @mentions in channels
  - Direct messages
  - /klaw slash command

Required tokens:
  - Bot Token (xoxb-...): OAuth token with chat:write, app_mentions:read, im:history
  - App Token (xapp-...): Socket Mode token from App settings

Examples:
  klaw slack --bot-token xoxb-... --app-token xapp-...

  # Or using environment variables:
  export SLACK_BOT_TOKEN=xoxb-...
  export SLACK_APP_TOKEN=xapp-...
  klaw slack`,
	RunE: runSlack,
}

func init() {
	slackCmd.Flags().StringVar(&slackCmdBotToken, "bot-token", "", "Slack bot token (xoxb-...)")
	slackCmd.Flags().StringVar(&slackCmdAppToken, "app-token", "", "Slack app token (xapp-...)")
	slackCmd.Flags().StringVarP(&slackCmdModel, "model", "m", "", "model to use")
	slackCmd.Flags().StringVarP(&slackCmdProvider, "provider", "p", "", "provider: anthropic, eachlabs (default: auto-detect)")

	rootCmd.AddCommand(slackCmd)
}

func runSlack(cmd *cobra.Command, args []string) error {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Get Slack tokens
	botToken := slackCmdBotToken
	if botToken == "" {
		botToken = os.Getenv("SLACK_BOT_TOKEN")
	}
	appToken := slackCmdAppToken
	if appToken == "" {
		appToken = os.Getenv("SLACK_APP_TOKEN")
	}

	if botToken == "" || appToken == "" {
		fmt.Println("ERROR: Slack tokens not set")
		fmt.Println("")
		fmt.Println("Set via flags:")
		fmt.Println("  klaw slack --bot-token xoxb-... --app-token xapp-...")
		fmt.Println("")
		fmt.Println("Or environment variables:")
		fmt.Println("  export SLACK_BOT_TOKEN=xoxb-...")
		fmt.Println("  export SLACK_APP_TOKEN=xapp-...")
		return fmt.Errorf("Slack tokens required")
	}

	// Determine provider and create it
	var prov provider.Provider
	providerName := slackCmdProvider

	// Auto-detect provider based on available API keys
	if providerName == "" {
		if os.Getenv("OPENROUTER_API_KEY") != "" {
			providerName = "openrouter"
		} else if os.Getenv("EACHLABS_API_KEY") != "" {
			providerName = "eachlabs"
		} else if os.Getenv("ANTHROPIC_API_KEY") != "" {
			providerName = "anthropic"
		} else {
			providerName = "anthropic" // default
		}
	}

	// Determine model
	model := slackCmdModel
	if model == "" {
		if provCfg, ok := cfg.Provider[providerName]; ok && provCfg.Model != "" {
			model = provCfg.Model
		}
	}
	if model == "" {
		// Default model based on provider
		switch providerName {
		case "openrouter":
			model = "anthropic/claude-sonnet-4"
		case "eachlabs":
			model = "anthropic/claude-sonnet-4-5"
		default:
			model = "claude-sonnet-4-20250514"
		}
	}

	switch providerName {
	case "openrouter":
		apiKey := os.Getenv("OPENROUTER_API_KEY")
		if apiKey == "" {
			fmt.Println("ERROR: OPENROUTER_API_KEY not set")
			fmt.Println("Get your API key at: https://openrouter.ai")
			return fmt.Errorf("OpenRouter API key required")
		}
		var err error
		prov, err = provider.NewOpenRouter(provider.OpenRouterConfig{
			APIKey: apiKey,
			Model:  model,
		})
		if err != nil {
			return fmt.Errorf("failed to create openrouter provider: %w", err)
		}
		fmt.Printf("Using OpenRouter (model: %s)\n", model)

	case "eachlabs":
		apiKey := os.Getenv("EACHLABS_API_KEY")
		if apiKey == "" {
			if eachCfg, ok := cfg.Provider["eachlabs"]; ok {
				apiKey = eachCfg.APIKey
			}
		}
		if apiKey == "" {
			fmt.Println("ERROR: EACHLABS_API_KEY not set")
			fmt.Println("Get your API key at: https://eachlabs.ai")
			return fmt.Errorf("each::labs API key required")
		}
		var err error
		prov, err = provider.NewEachLabs(provider.EachLabsConfig{
			APIKey: apiKey,
			Model:  model,
		})
		if err != nil {
			return fmt.Errorf("failed to create eachlabs provider: %w", err)
		}
		fmt.Printf("Using each::labs LLM Router (model: %s)\n", model)

	default: // anthropic
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			if anthropicCfg, ok := cfg.Provider["anthropic"]; ok {
				apiKey = anthropicCfg.APIKey
			}
		}
		if apiKey == "" {
			fmt.Println("ERROR: ANTHROPIC_API_KEY not set")
			return fmt.Errorf("API key required")
		}
		var err error
		prov, err = provider.NewAnthropic(provider.AnthropicConfig{
			APIKey: apiKey,
			Model:  model,
		})
		if err != nil {
			return fmt.Errorf("failed to create provider: %w", err)
		}
	}

	// Get working directory
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}

	// Create tools
	tools := tool.DefaultRegistry(workDir)

	// Create memory
	mem := memory.NewFileMemory(cfg.WorkspaceDir())

	// Load workspace and build system prompt
	ws, err := mem.LoadWorkspace(cmd.Context())
	if err != nil {
		ws = &memory.Workspace{}
	}
	systemPrompt := memory.BuildSystemPrompt(ws)

	// Load agent configuration and skills
	store := cluster.NewStore(config.StateDir())
	ctxMgr := cluster.NewContextManager(config.ConfigDir())
	clusterName, namespace, _ := ctxMgr.RequireCurrent()

	// Get all agents and their skills
	agents, _ := store.ListAgentBindings(clusterName, namespace)

	// Load skill registry
	skillReg := skill.NewRegistry(config.StateDir() + "/skills")

	// Collect all skills from agents and add to system prompt
	var skillPrompts []string
	skillSet := make(map[string]bool)

	for _, ag := range agents {
		for _, skillName := range ag.Skills {
			if skillSet[skillName] {
				continue
			}
			skillSet[skillName] = true

			sk, err := skillReg.Get(skillName)
			if err == nil && sk.SystemPrompt != "" {
				skillPrompts = append(skillPrompts, fmt.Sprintf("## %s Skill\n%s", sk.Name, sk.SystemPrompt))
			}
		}
	}

	// Add default agent-browser skill if not present
	if !skillSet["vercel-labs/agent-browser"] && !skillSet["agent-browser"] && !skillSet["browser"] {
		sk, err := skillReg.Get("browser")
		if err == nil && sk.SystemPrompt != "" {
			skillPrompts = append(skillPrompts, fmt.Sprintf("## %s Skill\n%s", sk.Name, sk.SystemPrompt))
		}
	}

	if len(skillPrompts) > 0 {
		systemPrompt = systemPrompt + "\n\n# Available Skills\n\n" + strings.Join(skillPrompts, "\n\n")
	}

	// Add Slack-specific instructions for concise responses
	slackInstructions := `

# Slack Communication Guidelines

You are communicating through Slack. Follow these rules:

1. **Be concise**: Keep responses short and to the point. No walls of text.
2. **No tool details**: Never mention tool calls or technical operations. Just provide results.
3. **Direct answers**: Answer directly without preamble like "Let me check that".
4. **Use formatting**: Use Slack formatting (*bold*, bullet points) sparingly.

# Scheduled Tasks (IMPORTANT)

When a user mentions time-based recurring tasks like "her 5 dakikada", "every hour", "daily", etc:

1. **USE cron_create tool** - This is MANDATORY for scheduled tasks
2. Do NOT just create an agent - agents don't run automatically on a schedule
3. Create a cron job that triggers the agent

CRITICAL - Channel parameter for cron jobs:
- Every message starts with [Context: channel=XXXXX] - this is the current Slack channel ID
- When user says "bu kanalı", "this channel", "kanalı takip et", "monitor here" -> YOU MUST pass this channel ID to cron_create
- Example: User in channel C0A8KUEBT3M says "her dakika bu kanalı kontrol et"
  -> Call cron_create with channel="C0A8KUEBT3M" (from the context)
- If user wants a general task not related to channel monitoring -> omit channel parameter

Example with channel monitoring:
User: "Her 5 dakikada bu kanaldaki mesajları analiz et"
-> cron_create with channel parameter set to current channel ID

Example without channel:
User: "Her gün saat 9'da hava durumunu söyle"
-> cron_create WITHOUT channel parameter

# Agent Management

When a user requests a task that requires specialized expertise:

1. **FIRST check existing agents** with agent_list tool
2. **If agent exists with same/similar name**:
   - Ask "X agent'ı zaten var, onu güncelleyeyim mi yoksa yeni mi oluşturayım?"
   - Or update the existing one if the request is clearly an update
3. **If no suitable agent exists**: Create a new one

# Asking Clarifying Questions (IMPORTANT)

Before creating agents or cron jobs, ASK about:
- **Evaluation criteria**: "Lead'leri neye göre değerlendirelim? (şirket büyüklüğü, sektör, teknoloji kullanımı?)"
- **Output format**: "Sonuçları nasıl raporlayayım?"
- **Exclusions**: Confirm exclusions user mentioned

Key points:
- Check existing agents BEFORE creating new ones
- Use cron_create for ANY scheduled/recurring task
- Ask 1-2 clarifying questions about evaluation criteria
- Don't assume - ASK
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

	// Handle signals
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nShutting down Slack bot...")
		cancel()
	}()

	// Start Slack channel
	if err := slackChan.Start(ctx); err != nil {
		return fmt.Errorf("failed to start Slack channel: %w", err)
	}

	fmt.Println("╭─────────────────────────────────────────╮")
	fmt.Println("│           klaw Slack Bot                │")
	fmt.Println("╰─────────────────────────────────────────╯")
	fmt.Printf("Model: %s\n", model)
	fmt.Println("Listening for messages...")
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println("")

	// Run agent
	return ag.Run(ctx)
}
