package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/eachlabs/klaw/internal/agent"
	"github.com/eachlabs/klaw/internal/channel"
	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/memory"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/skill"
	"github.com/eachlabs/klaw/internal/tool"
	"github.com/eachlabs/klaw/internal/tui"
	"github.com/spf13/cobra"
)

var (
	chatModel    string
	chatAgent    string
	chatSimple   bool
	chatProvider string
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start interactive chat",
	Long: `Start an interactive terminal chat session with the AI assistant.

Examples:
  klaw chat
  klaw chat --model claude-3-5-sonnet-20241022
  klaw chat --simple   # Use simple terminal mode`,
	RunE: runChat,
}

func init() {
	chatCmd.Flags().StringVarP(&chatModel, "model", "m", "", "model to use")
	chatCmd.Flags().StringVarP(&chatAgent, "agent", "a", "", "agent profile to use")
	chatCmd.Flags().BoolVar(&chatSimple, "simple", false, "use simple terminal mode (no TUI)")
	chatCmd.Flags().StringVarP(&chatProvider, "provider", "p", "", "provider: anthropic, eachlabs (default: auto-detect)")
}

func runChat(cmd *cobra.Command, args []string) error {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Ensure directories exist
	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Determine provider and create it
	var prov provider.Provider
	providerName := chatProvider

	// Auto-detect provider based on available API keys
	if providerName == "" {
		if os.Getenv("OPENROUTER_API_KEY") != "" {
			providerName = "openrouter"
		} else if os.Getenv("EACHLABS_API_KEY") != "" {
			providerName = "eachlabs"
		} else if os.Getenv("ANTHROPIC_API_KEY") != "" {
			providerName = "anthropic"
		} else if cfg.Provider["eachlabs"].APIKey != "" {
			providerName = "eachlabs"
		} else if cfg.Provider["anthropic"].APIKey != "" {
			providerName = "anthropic"
		} else {
			providerName = "anthropic" // default
		}
	}

	// Determine model
	model := chatModel
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
			model = cfg.Defaults.Model
			if model == "" {
				model = "claude-sonnet-4-20250514"
			}
		}
	}

	switch providerName {
	case "openrouter":
		apiKey := os.Getenv("OPENROUTER_API_KEY")
		if apiKey == "" {
			fmt.Println("ERROR: OPENROUTER_API_KEY not set")
			fmt.Println("")
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
			fmt.Println("")
			fmt.Println("Set it via environment variable:")
			fmt.Println("  export EACHLABS_API_KEY=your-api-key")
			fmt.Println("")
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

	default: // anthropic
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			if anthropicCfg, ok := cfg.Provider["anthropic"]; ok {
				apiKey = anthropicCfg.APIKey
			}
		}
		if apiKey == "" {
			fmt.Println("ERROR: ANTHROPIC_API_KEY not set")
			fmt.Println("")
			fmt.Println("Set it via environment variable:")
			fmt.Println("  export ANTHROPIC_API_KEY=sk-ant-api03-...")
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

	// Load skills and add to system prompt
	systemPrompt = loadSkillsIntoPrompt(systemPrompt)

	// Handle signals
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		cancel()
	}()

	// Use simple mode or TUI mode
	if chatSimple {
		return runSimpleChat(ctx, prov, tools, mem, systemPrompt)
	}

	return runTUIChat(ctx, prov, tools, mem, systemPrompt)
}

func runSimpleChat(ctx context.Context, prov provider.Provider, tools *tool.Registry, mem memory.Memory, systemPrompt string) error {
	// Simple terminal mode
	term := channel.NewStyledTerminal()

	ag := agent.New(agent.Config{
		Provider:     prov,
		Channel:      term,
		Tools:        tools,
		Memory:       mem,
		SystemPrompt: systemPrompt,
	})

	return ag.Run(ctx)
}

func runTUIChat(ctx context.Context, prov provider.Provider, tools *tool.Registry, mem memory.Memory, systemPrompt string) error {
	// Create TUI channel
	tuiChan := channel.NewTUIChannel()

	// Create agent
	ag := agent.New(agent.Config{
		Provider:     prov,
		Channel:      tuiChan,
		Tools:        tools,
		Memory:       mem,
		SystemPrompt: systemPrompt,
	})

	// Start agent in background
	go func() {
		if err := tuiChan.Start(ctx); err != nil {
			return
		}
		ag.Run(ctx)
	}()

	// Convert channels for TUI
	inputChan := tuiChan.UserInput()
	outputChan := tuiChan.TUIOutput()

	// Adapter to convert TUIMessage to tui.ChatMessage
	chatOutput := make(chan tui.ChatMessage, 100)
	go func() {
		for msg := range outputChan {
			chatOutput <- tui.ChatMessage{
				Role:    msg.Role,
				Content: msg.Content,
				Tool:    msg.Tool,
			}
		}
		close(chatOutput)
	}()

	// Run TUI
	model := tui.NewChatModel(inputChan, chatOutput)
	p := tea.NewProgram(model, tea.WithAltScreen())
	_, err := p.Run()

	tuiChan.Stop()
	return err
}

// loadSkillsIntoPrompt loads skills from agents and adds their prompts to the system prompt
func loadSkillsIntoPrompt(basePrompt string) string {
	// Load skill registry
	skillReg := skill.NewRegistry(config.StateDir() + "/skills")

	// Try to get skills from cluster agents
	store := cluster.NewStore(config.StateDir())
	ctxMgr := cluster.NewContextManager(config.ConfigDir())
	clusterName, namespace, _ := ctxMgr.RequireCurrent()

	// Collect skills from agents
	skillSet := make(map[string]bool)
	var skillPrompts []string

	// Get all agents and their skills
	if agents, err := store.ListAgentBindings(clusterName, namespace); err == nil {
		for _, ag := range agents {
			for _, skillName := range ag.Skills {
				if skillSet[skillName] {
					continue
				}
				skillSet[skillName] = true

				if sk, err := skillReg.Get(skillName); err == nil && sk.SystemPrompt != "" {
					skillPrompts = append(skillPrompts, fmt.Sprintf("## %s\n%s", sk.Name, sk.SystemPrompt))
				}
			}
		}
	}

	// Always add default skills (browser, web-search) if not already present
	defaultSkills := []string{"browser", "web-search"}
	for _, skillName := range defaultSkills {
		if skillSet[skillName] {
			continue
		}
		if sk, err := skillReg.Get(skillName); err == nil && sk.SystemPrompt != "" {
			skillPrompts = append(skillPrompts, fmt.Sprintf("## %s\n%s", sk.Name, sk.SystemPrompt))
		}
	}

	// Add autonomous tool discovery instructions
	toolDiscoveryPrompt := `
# Finding and Installing Tools

When a user asks you to do something you don't have a tool for (like tweeting, sending emails, etc.):

1. **Search for CLI tools**: Use web_search to find CLI tools that can help
   - Example: "twitter cli tool" or "send email from command line"

2. **Check skills.sh**: Browse https://skills.sh to find skills
   - Use web_fetch to read skill pages: https://skills.sh/<org>/<skill-name>
   - Skills often include CLI tools you can install and use

3. **Install tools**: Use bash to install tools
   - brew install <tool>
   - npm install -g <tool>
   - pip install <tool>

4. **Run the tool**: After installing, use bash to run commands

5. **Ask user for credentials if needed**: Some tools need API keys or login

Always explain what you're doing and ask for confirmation before installing anything.
`

	result := basePrompt
	if len(skillPrompts) > 0 {
		result += "\n\n# Available Skills\n\nYou have access to the following skills:\n\n" + strings.Join(skillPrompts, "\n\n")
	}
	result += toolDiscoveryPrompt

	return result
}
