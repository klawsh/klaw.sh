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
	"github.com/eachlabs/klaw/internal/session"
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
	chatSession  string
	chatName     string
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
	chatCmd.Flags().StringVarP(&chatSession, "session", "s", "", "resume an existing session by ID")
	chatCmd.Flags().StringVar(&chatName, "name", "", "name for the new session")
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

	// Auto-detect provider based on available API keys and config
	if providerName == "" {
		if os.Getenv("ANTHROPIC_API_KEY") != "" {
			providerName = "anthropic"
		} else if os.Getenv("OPENROUTER_API_KEY") != "" {
			providerName = "openrouter"
		} else if os.Getenv("EACHLABS_API_KEY") != "" {
			providerName = "eachlabs"
		} else if cfg.Provider["anthropic"].APIKey != "" {
			providerName = "anthropic"
		} else if cfg.Provider["eachlabs"].APIKey != "" {
			providerName = "eachlabs"
		} else if name := firstCustomProvider(cfg); name != "" {
			providerName = name
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

	prov, err = buildProvider(cfg, providerName, model)
	if err != nil {
		return err
	}

	// Wrap provider with resilience (retry + fallback)
	provCfg := cfg.Provider[providerName]
	retryConfig := provider.DefaultRetryConfig()
	if provCfg.MaxRetries > 0 {
		retryConfig.MaxRetries = provCfg.MaxRetries
	}
	var fallbacks []provider.Provider
	if provCfg.Fallback != "" {
		if fbProv := buildFallbackProvider(cfg, provCfg.Fallback); fbProv != nil {
			fallbacks = append(fallbacks, fbProv)
		}
	}
	prov = provider.NewResilientProvider(provider.ResilientConfig{
		Primary:   prov,
		Fallbacks: fallbacks,
		Retry:     retryConfig,
	})

	// Get working directory
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}

	// Create tools, applying per-agent filtering if configured
	tools := tool.DefaultRegistry(workDir)
	var agentMaxIterations int
	var agentApproval []string
	if chatAgent != "" {
		if agentCfg, ok := cfg.Agents[chatAgent]; ok {
			if len(agentCfg.Tools) > 0 {
				tools = tools.Filter(agentCfg.Tools)
			}
			agentMaxIterations = agentCfg.MaxIterations
			agentApproval = agentCfg.RequireApproval
		}
	}

	// Register delegate tool for sub-agent spawning
	delegateTool := tool.NewDelegateTool(
		func(ctx context.Context, cfg tool.RunConfig) (string, error) {
			return agent.RunOnce(ctx, agent.RunOnceConfig{
				Provider:     cfg.Provider.(provider.Provider),
				Tools:        cfg.Tools,
				SystemPrompt: cfg.SystemPrompt,
				Prompt:       cfg.Prompt,
				MaxTokens:    cfg.MaxTokens,
			})
		},
		prov,
		tools,
	)
	tools.Register(delegateTool)

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

	// Create session manager and load/create session
	sessMgr := session.NewManager()
	var sess *session.Session
	var initialHistory []provider.Message

	if chatSession != "" {
		// Resume existing session
		var err error
		sess, err = sessMgr.Load(chatSession)
		if err != nil {
			return fmt.Errorf("failed to load session: %w", err)
		}
		initialHistory = sess.Messages
		fmt.Printf("Resuming session: %s", sess.ID)
		if sess.Name != "" {
			fmt.Printf(" (%s)", sess.Name)
		}
		fmt.Println()
	} else {
		// Create new session
		sess = sessMgr.New(model, providerName, chatAgent, systemPrompt, workDir)
		if chatName != "" {
			sessMgr.SetName(chatName)
		}
		fmt.Printf("Session: %s", sess.ID)
		if chatName != "" {
			fmt.Printf(" (%s)", chatName)
		}
		fmt.Println()
	}

	// Handle signals
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		// Force save before exit
		_ = sessMgr.ForceSave()
		cancel()
	}()

	// Build base agent config
	baseCfg := agent.Config{
		Provider:       prov,
		Tools:          tools,
		Memory:         mem,
		SessionManager: sessMgr,
		InitialHistory: initialHistory,
		SystemPrompt:   systemPrompt,
		MaxIterations:  agentMaxIterations,
		Model:          model,
		Cost: agent.CostConfig{
			MaxSessionCost: cfg.Defaults.MaxSessionCost,
			WarnThreshold:  0.8,
		},
	}
	if len(agentApproval) > 0 {
		baseCfg.Approval = agent.ApprovalConfig{
			Enabled:         true,
			RequireApproval: agentApproval,
		}
	}

	// Use simple mode or TUI mode
	if chatSimple {
		err := runSimpleChat(ctx, baseCfg)
		_ = sessMgr.ForceSave()
		return err
	}

	tuiErr := runTUIChat(ctx, baseCfg)
	_ = sessMgr.ForceSave()
	return tuiErr
}

func runSimpleChat(ctx context.Context, baseCfg agent.Config) error {
	// Simple terminal mode
	baseCfg.Channel = channel.NewStyledTerminal()
	ag := agent.New(baseCfg)
	return ag.Run(ctx)
}

func runTUIChat(ctx context.Context, baseCfg agent.Config) error {
	// Create TUI channel
	tuiChan := channel.NewTUIChannel()

	// Create agent
	baseCfg.Channel = tuiChan
	ag := agent.New(baseCfg)

	// Start agent in background
	go func() {
		if err := tuiChan.Start(ctx); err != nil {
			return
		}
		_ = ag.Run(ctx)
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

	_ = tuiChan.Stop()
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

// firstCustomProvider returns the name of the first config-defined provider with a base_url,
// excluding the built-in providers (anthropic, openrouter, eachlabs).
// This allows auto-detection of custom OpenAI-compatible providers like ollama.
func firstCustomProvider(cfg *config.Config) string {
	builtIn := map[string]bool{"anthropic": true, "openrouter": true, "eachlabs": true, "openai": true}
	for name, provCfg := range cfg.Provider {
		if builtIn[name] {
			continue
		}
		if provCfg.BaseURL != "" {
			return name
		}
	}
	return ""
}

// envKeyForProvider returns the conventional environment variable name for a provider.
func envKeyForProvider(name string) string {
	switch name {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "eachlabs":
		return "EACHLABS_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	default:
		return ""
	}
}

// resolveAPIKey returns the API key for a provider from config or environment.
func resolveAPIKey(cfg *config.Config, name string) string {
	if provCfg, ok := cfg.Provider[name]; ok && provCfg.APIKey != "" {
		return provCfg.APIKey
	}
	if envKey := envKeyForProvider(name); envKey != "" {
		return os.Getenv(envKey)
	}
	return ""
}

// buildProvider creates a provider by name, using config and environment variables.
func buildProvider(cfg *config.Config, name, model string) (provider.Provider, error) {
	provCfg := cfg.Provider[name]
	apiKey := resolveAPIKey(cfg, name)

	switch name {
	case "anthropic":
		if apiKey == "" {
			fmt.Println("ERROR: ANTHROPIC_API_KEY not set")
			fmt.Println("")
			fmt.Println("Set it via environment variable:")
			fmt.Println("  export ANTHROPIC_API_KEY=sk-ant-api03-...")
			return nil, fmt.Errorf("API key required")
		}
		return provider.NewAnthropic(provider.AnthropicConfig{
			APIKey:  apiKey,
			BaseURL: provCfg.BaseURL,
			Model:   model,
		})

	case "openrouter":
		if apiKey == "" {
			fmt.Println("ERROR: OPENROUTER_API_KEY not set")
			fmt.Println("")
			fmt.Println("Get your API key at: https://openrouter.ai")
			return nil, fmt.Errorf("OpenRouter API key required")
		}
		return provider.NewOpenRouter(provider.OpenRouterConfig{
			APIKey:  apiKey,
			BaseURL: provCfg.BaseURL,
			Model:   model,
		})

	case "eachlabs":
		if apiKey == "" {
			fmt.Println("ERROR: EACHLABS_API_KEY not set")
			fmt.Println("")
			fmt.Println("Set it via environment variable:")
			fmt.Println("  export EACHLABS_API_KEY=your-api-key")
			fmt.Println("")
			fmt.Println("Get your API key at: https://eachlabs.ai")
			return nil, fmt.Errorf("each::labs API key required")
		}
		return provider.NewEachLabs(provider.EachLabsConfig{
			APIKey:  apiKey,
			BaseURL: provCfg.BaseURL,
			Model:   model,
		})

	default:
		// Any other provider name: use OpenAI-compatible provider with base_url from config.
		// This supports Ollama, LM Studio, vLLM, GLM, Minimax, Together AI, etc.
		if provCfg.BaseURL == "" {
			return nil, fmt.Errorf("provider %q requires base_url in config (e.g. [provider.%s] base_url = \"http://localhost:11434/v1\")", name, name)
		}
		if model == "" {
			return nil, fmt.Errorf("provider %q requires a model (set model in config or use --model flag)", name)
		}
		return provider.NewOpenAICompat(provider.OpenAICompatConfig{
			Name:    name,
			APIKey:  apiKey,
			BaseURL: provCfg.BaseURL,
			Model:   model,
		})
	}
}

// buildFallbackProvider creates a provider from config by name, returning nil on failure.
func buildFallbackProvider(cfg *config.Config, name string) provider.Provider {
	provCfg, ok := cfg.Provider[name]
	if !ok {
		return nil
	}
	model := provCfg.Model
	p, err := buildProvider(cfg, name, model)
	if err != nil {
		return nil
	}
	return p
}
