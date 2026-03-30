package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/eachlabs/klaw/internal/agent"
	"github.com/eachlabs/klaw/internal/channel"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/memory"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/session"
	"github.com/eachlabs/klaw/internal/tool"
	"github.com/spf13/cobra"
)

var (
	chatModel    string
	chatProvider string
	chatSession  string
	chatName     string
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "Start interactive chat",
	Long: `Start an interactive terminal chat session.

Examples:
  klaw chat
  klaw chat --model claude-sonnet-4-20250514
  klaw chat -p eachlabs -m anthropic/claude-sonnet-4`,
	RunE: runChat,
}

func init() {
	chatCmd.Flags().StringVarP(&chatModel, "model", "m", "", "model to use")
	chatCmd.Flags().StringVarP(&chatProvider, "provider", "p", "", "provider: anthropic, eachlabs, openrouter (default: auto-detect)")
	chatCmd.Flags().StringVarP(&chatSession, "session", "s", "", "resume an existing session by ID")
	chatCmd.Flags().StringVar(&chatName, "name", "", "name for the new session")
}

func runChat(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Determine provider
	providerName := chatProvider
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
			providerName = "anthropic"
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

	prov, err := buildProvider(cfg, providerName, model)
	if err != nil {
		return err
	}

	// Wrap with resilience
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

	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}

	tools := tool.DefaultRegistry(workDir)

	// Memory + system prompt
	mem := memory.NewFileMemory(cfg.WorkspaceDir())
	ws, err := mem.LoadWorkspace(cmd.Context())
	if err != nil {
		ws = &memory.Workspace{}
	}
	systemPrompt := memory.BuildSystemPrompt(ws)

	// Session
	sessMgr := session.NewManager()
	var sess *session.Session
	var initialHistory []provider.Message

	if chatSession != "" {
		sess, err = sessMgr.Load(chatSession)
		if err != nil {
			return fmt.Errorf("failed to load session: %w", err)
		}
		initialHistory = sess.Messages
		fmt.Printf("Resuming session: %s\n", sess.ID)
	} else {
		sess = sessMgr.New(model, providerName, "", systemPrompt, workDir)
		if chatName != "" {
			sessMgr.SetName(chatName)
		}
		fmt.Printf("Session: %s\n", sess.ID)
	}

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		_ = sessMgr.ForceSave()
		cancel()
	}()

	ag := agent.New(agent.Config{
		Provider:       prov,
		Channel:        channel.NewTerminal(),
		Tools:          tools,
		Memory:         mem,
		SessionManager: sessMgr,
		InitialHistory: initialHistory,
		SystemPrompt:   systemPrompt,
		Model:          model,
		Cost: agent.CostConfig{
			MaxSessionCost: cfg.Defaults.MaxSessionCost,
			WarnThreshold:  0.8,
		},
	})

	err = ag.Run(ctx)
	_ = sessMgr.ForceSave()
	return err
}

// firstCustomProvider returns the name of the first config-defined provider with a base_url.
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

func resolveAPIKey(cfg *config.Config, name string) string {
	if provCfg, ok := cfg.Provider[name]; ok && provCfg.APIKey != "" {
		return provCfg.APIKey
	}
	if envKey := envKeyForProvider(name); envKey != "" {
		return os.Getenv(envKey)
	}
	return ""
}

func buildProvider(cfg *config.Config, name, model string) (provider.Provider, error) {
	provCfg := cfg.Provider[name]
	apiKey := resolveAPIKey(cfg, name)

	switch name {
	case "anthropic":
		if apiKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
		}
		return provider.NewAnthropic(provider.AnthropicConfig{
			APIKey: apiKey, BaseURL: provCfg.BaseURL, Model: model,
		})
	case "openrouter":
		if apiKey == "" {
			return nil, fmt.Errorf("OPENROUTER_API_KEY not set")
		}
		return provider.NewOpenRouter(provider.OpenRouterConfig{
			APIKey: apiKey, BaseURL: provCfg.BaseURL, Model: model,
		})
	case "eachlabs":
		if apiKey == "" {
			return nil, fmt.Errorf("EACHLABS_API_KEY not set")
		}
		return provider.NewEachLabs(provider.EachLabsConfig{
			APIKey: apiKey, BaseURL: provCfg.BaseURL, Model: model,
		})
	default:
		if provCfg.BaseURL == "" {
			return nil, fmt.Errorf("provider %q requires base_url in config", name)
		}
		if model == "" {
			return nil, fmt.Errorf("provider %q requires a model", name)
		}
		return provider.NewOpenAICompat(provider.OpenAICompatConfig{
			Name: name, APIKey: apiKey, BaseURL: provCfg.BaseURL, Model: model,
		})
	}
}

func buildFallbackProvider(cfg *config.Config, name string) provider.Provider {
	provCfg, ok := cfg.Provider[name]
	if !ok {
		return nil
	}
	p, err := buildProvider(cfg, name, provCfg.Model)
	if err != nil {
		return nil
	}
	return p
}
