package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/memory"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/server"
	"github.com/eachlabs/klaw/internal/skill"
	"github.com/eachlabs/klaw/internal/tool"
	"github.com/spf13/cobra"
)

var (
	servePort     int
	serveHost     string
	serveModel    string
	serveProvider string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start OpenAI-compatible HTTP gateway",
	Long: `Start the klaw OpenAI-compatible API server.

This exposes klaw agents as an OpenAI Chat Completions API.
Any OpenAI-compatible client can connect and use klaw agents.

Required environment variables:
  ANTHROPIC_API_KEY or OPENROUTER_API_KEY or EACHLABS_API_KEY

Examples:
  klaw serve
  klaw serve --port 8080
  klaw serve -p anthropic -m claude-sonnet-4-20250514`,
	RunE: runServe,
}

func init() {
	serveCmd.Flags().IntVar(&servePort, "port", 0, "port to listen on (default: from config or 8080)")
	serveCmd.Flags().StringVar(&serveHost, "host", "", "host to bind to (default: from config or 127.0.0.1)")
	serveCmd.Flags().StringVarP(&serveModel, "model", "m", "", "model to use")
	serveCmd.Flags().StringVarP(&serveProvider, "provider", "p", "", "provider: anthropic, openrouter, eachlabs")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Determine provider
	providerName := serveProvider
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
	model := serveModel
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
	prov, err := createProvider(providerName, model, cfg)
	if err != nil {
		return err
	}

	// Working directory
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}

	// Create tools
	tools := tool.DefaultRegistry(workDir)

	// Bootstrap default skills + additional sources from config
	skillsDir := filepath.Join(config.ConfigDir(), "skills")
	bootstrapSkills(skillsDir, cfg)

	// Create skill loader (used by server for per-model skill resolution)
	loader := skill.NewSkillLoader(skillsDir)

	// Create memory and system prompt (base — skills are appended per-model at request time)
	mem := memory.NewFileMemory(cfg.WorkspaceDir())
	ws, err := mem.LoadWorkspace(cmd.Context())
	if err != nil {
		ws = &memory.Workspace{}
	}
	systemPrompt := memory.BuildSystemPrompt(ws)

	// Determine host/port
	host := cfg.Server.Host
	if serveHost != "" {
		host = serveHost
	}
	port := cfg.Server.Port
	if servePort != 0 {
		port = servePort
	}

	// Build OpenAI config — if config has [openai] section use it, otherwise create default
	openaiCfg := server.OpenAIConfig{
		Enabled:       true,
		AuthRequired:  cfg.OpenAI.AuthRequired,
		APIKeys:       cfg.OpenAI.APIKeys,
		DefaultModel:  cfg.OpenAI.DefaultModel,
		CORSOrigins:   cfg.OpenAI.CORSOrigins,
		MaxConcurrent: cfg.OpenAI.MaxConcurrent,
		Models:        make(map[string]server.ModelMapping),
	}

	// Map config models (including per-model skills)
	if len(cfg.OpenAI.Models) > 0 {
		for id, m := range cfg.OpenAI.Models {
			p := m.Provider
			if p == "" {
				p = providerName
			}
			openaiCfg.Models[id] = server.ModelMapping{Agent: m.Agent, Provider: p, Skills: m.Skills}
		}
	}

	// If no models configured, create a default one
	if len(openaiCfg.Models) == 0 {
		defaultModelID := openaiCfg.DefaultModel
		if defaultModelID == "" {
			defaultModelID = "default"
		}
		openaiCfg.Models[defaultModelID] = server.ModelMapping{
			Agent:    "default",
			Provider: providerName,
		}
		openaiCfg.DefaultModel = defaultModelID
	}

	providerMap := map[string]provider.Provider{
		providerName: prov,
	}

	srv := server.New(
		openaiCfg,
		server.ServerConfig{Host: host, Port: port},
		providerMap,
		tools,
		mem,
		systemPrompt,
		loader,
	)

	// Signal handling
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	// Print startup info
	fmt.Println("╭─────────────────────────────────────────╮")
	fmt.Println("│          klaw serve                     │")
	fmt.Println("│     OpenAI-Compatible Gateway           │")
	fmt.Println("╰─────────────────────────────────────────╯")
	fmt.Printf("Provider:  %s\n", providerName)
	fmt.Printf("Model:     %s\n", model)
	fmt.Printf("Endpoint:  http://%s:%d/v1/chat/completions\n", host, port)
	fmt.Printf("Models:    http://%s:%d/v1/models\n", host, port)
	fmt.Printf("Health:    http://%s:%d/health\n", host, port)
	fmt.Println("")
	fmt.Println("Models:")
	for id := range openaiCfg.Models {
		fmt.Printf("  • %s\n", id)
	}
	fmt.Println("")
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println("")

	return srv.Start(ctx)
}

type defaultSkill struct {
	repoURL   string
	skillName string
}

var defaultSkills = []defaultSkill{
	{"https://github.com/eachlabs/skills", "all"},
	{"https://github.com/vercel-labs/skills", "find-skills"},
	{"https://github.com/vercel-labs/agent-browser", "agent-browser"},
	{"https://github.com/browser-use/browser-use", "browser-use"},
}

func bootstrapSkills(skillsDir string, cfg *config.Config) {
	loader := skill.NewSkillLoader(skillsDir)

	fmt.Println("Bootstrapping default skills...")
	for _, s := range defaultSkills {
		if s.skillName == "all" {
			fmt.Printf("  Installing all skills from %s...\n", s.repoURL)
		} else {
			fmt.Printf("  Installing %s from %s...\n", s.skillName, s.repoURL)
		}
		if err := loader.InstallFromGitHub(s.repoURL, s.skillName); err != nil {
			fmt.Printf("    ⚠ %v\n", err)
		} else {
			fmt.Printf("    ✓ done\n")
		}
	}

	// Install from additional sources defined in config
	for _, src := range cfg.OpenAI.SkillSources {
		for _, sk := range src.Skills {
			if sk == "all" {
				fmt.Printf("  Installing all skills from %s...\n", src.Repo)
			} else {
				fmt.Printf("  Installing %s from %s...\n", sk, src.Repo)
			}
			if err := loader.InstallFromGitHub(src.Repo, sk); err != nil {
				fmt.Printf("    ⚠ %v\n", err)
			} else {
				fmt.Printf("    ✓ done\n")
			}
		}
	}
	fmt.Println("")
}

func createProvider(name, model string, cfg *config.Config) (provider.Provider, error) {
	switch name {
	case "openrouter":
		apiKey := os.Getenv("OPENROUTER_API_KEY")
		if apiKey == "" {
			return nil, fmt.Errorf("OPENROUTER_API_KEY not set")
		}
		return provider.NewOpenRouter(provider.OpenRouterConfig{APIKey: apiKey, Model: model})

	case "eachlabs":
		apiKey := os.Getenv("EACHLABS_API_KEY")
		if apiKey == "" {
			if eachCfg, ok := cfg.Provider["eachlabs"]; ok {
				apiKey = eachCfg.APIKey
			}
		}
		if apiKey == "" {
			return nil, fmt.Errorf("EACHLABS_API_KEY not set")
		}
		return provider.NewEachLabs(provider.EachLabsConfig{APIKey: apiKey, Model: model})

	default: // anthropic
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			if anthropicCfg, ok := cfg.Provider["anthropic"]; ok {
				apiKey = anthropicCfg.APIKey
			}
		}
		if apiKey == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY not set")
		}
		return provider.NewAnthropic(provider.AnthropicConfig{APIKey: apiKey, Model: model})
	}
}
