package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/eachlabs/klaw/internal/agent"
	"github.com/eachlabs/klaw/internal/channel"
	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/memory"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/tool"
	"github.com/spf13/cobra"
)

// --- klaw run channel ---

var runChannelModel string

var runChannelCmd = &cobra.Command{
	Use:   "channel <name>",
	Short: "Run a channel bot",
	Long: `Run a channel bot from the current namespace.

Examples:
  klaw run channel slack-bot
  klaw run channel sales-bot --model claude-opus-4`,
	Args: cobra.ExactArgs(1),
	RunE: runChannelBot,
}

func init() {
	runChannelCmd.Flags().StringVarP(&runChannelModel, "model", "m", "", "model to use")

	// Add to runCmd (defined in podman.go)
	runCmd.AddCommand(runChannelCmd)
}

func runChannelBot(cmd *cobra.Command, args []string) error {
	name := args[0]

	store := cluster.NewStore(config.StateDir())
	ctxMgr := cluster.NewContextManager(config.ConfigDir())

	clusterName, namespace, err := ctxMgr.RequireCurrent()
	if err != nil {
		return err
	}

	// Get channel binding
	binding, err := store.GetChannelBinding(clusterName, namespace, name)
	if err != nil {
		return err
	}

	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Get API key
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		anthropicCfg := cfg.Provider["anthropic"]
		apiKey = anthropicCfg.APIKey
	}

	if apiKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	// Determine model
	model := runChannelModel
	if model == "" {
		if anthropicCfg, ok := cfg.Provider["anthropic"]; ok && anthropicCfg.Model != "" {
			model = anthropicCfg.Model
		}
	}
	if model == "" {
		model = cfg.Defaults.Model
	}
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	// Create provider
	prov, err := provider.NewAnthropic(provider.AnthropicConfig{
		APIKey: apiKey,
		Model:  model,
	})
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
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

	// Create channel based on type
	var ch channel.Channel
	switch binding.Type {
	case "slack":
		botToken := binding.Config["bot_token"]
		appToken := binding.Config["app_token"]
		if botToken == "" || appToken == "" {
			return fmt.Errorf("slack channel missing tokens")
		}
		ch, err = channel.NewSlackChannel(channel.SlackConfig{
			BotToken: botToken,
			AppToken: appToken,
		})
		if err != nil {
			return fmt.Errorf("failed to create Slack channel: %w", err)
		}

	case "telegram", "discord":
		return fmt.Errorf("%s channel not yet implemented", binding.Type)

	default:
		return fmt.Errorf("unknown channel type: %s", binding.Type)
	}

	// Create agent
	ag := agent.New(agent.Config{
		Provider:     prov,
		Channel:      ch,
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
		fmt.Println("\nShutting down...")
		cancel()
	}()

	// Update status
	store.UpdateChannelBindingStatus(clusterName, namespace, name, "active")
	defer store.UpdateChannelBindingStatus(clusterName, namespace, name, "inactive")

	// Start channel
	if err := ch.Start(ctx); err != nil {
		return fmt.Errorf("failed to start channel: %w", err)
	}

	fmt.Println("╭─────────────────────────────────────────╮")
	fmt.Printf("│  klaw channel: %-24s │\n", name)
	fmt.Println("╰─────────────────────────────────────────╯")
	fmt.Printf("Cluster:   %s\n", clusterName)
	fmt.Printf("Namespace: %s\n", namespace)
	fmt.Printf("Type:      %s\n", binding.Type)
	fmt.Printf("Model:     %s\n", model)
	fmt.Println("")
	fmt.Println("Listening for messages...")
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println("")

	// Run agent
	return ag.Run(ctx)
}
