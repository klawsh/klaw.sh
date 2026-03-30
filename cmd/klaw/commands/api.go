package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/eachlabs/klaw/internal/api"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/spf13/cobra"
)

var (
	apiPort    int
	apiHost    string
	apiWorkers int
	apiNoAuth  bool
)

var apiCmd = &cobra.Command{
	Use:   "api",
	Short: "Creative Agent HTTP API",
}

var apiStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Creative Agent API server",
	Long: `Start the HTTP API server for creative agent execution.

The API accepts POST /api/v1/run with a skill URL, prompt, and context,
then streams SSE events as the agent works.

Examples:
  klaw api start
  klaw api start --port 8081 --workers 50
  klaw api start --no-auth`,
	RunE: runAPIStart,
}

var apiKeyCmd = &cobra.Command{
	Use:   "api-key",
	Short: "Manage API keys",
}

var apiKeyCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new API key",
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		if name == "" {
			name = "default"
		}

		store, err := api.NewAuthStore()
		if err != nil {
			return err
		}

		key, err := store.Create(name)
		if err != nil {
			return err
		}

		fmt.Printf("Created API key: %s\n", key)
		fmt.Println("Store this key securely — it will not be shown again.")
		return nil
	},
}

var apiKeyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List API keys",
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := api.NewAuthStore()
		if err != nil {
			return err
		}

		keys := store.List()
		if len(keys) == 0 {
			fmt.Println("No API keys. Create one with: klaw api-key create --name <name>")
			return nil
		}

		fmt.Printf("%-20s %-20s %s\n", "NAME", "KEY", "CREATED")
		for _, k := range keys {
			fmt.Printf("%-20s %-20s %s\n", k.Name, k.Key, k.CreatedAt.Format("2006-01-02"))
		}
		return nil
	},
}

var apiKeyRevokeCmd = &cobra.Command{
	Use:   "revoke <key>",
	Short: "Revoke an API key",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := api.NewAuthStore()
		if err != nil {
			return err
		}

		if store.Revoke(args[0]) {
			fmt.Println("API key revoked.")
		} else {
			fmt.Println("API key not found.")
		}
		return nil
	},
}

func init() {
	apiStartCmd.Flags().IntVar(&apiPort, "port", 0, "port (default from config or 8081)")
	apiStartCmd.Flags().StringVar(&apiHost, "host", "", "host (default from config or 0.0.0.0)")
	apiStartCmd.Flags().IntVar(&apiWorkers, "workers", 0, "max concurrent tasks (default from config or 50)")
	apiStartCmd.Flags().BoolVar(&apiNoAuth, "no-auth", false, "disable API key authentication")

	apiKeyCreateCmd.Flags().String("name", "", "name for the API key")

	apiCmd.AddCommand(apiStartCmd)
	apiKeyCmd.AddCommand(apiKeyCreateCmd)
	apiKeyCmd.AddCommand(apiKeyListCmd)
	apiKeyCmd.AddCommand(apiKeyRevokeCmd)
}

func runAPIStart(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Resolve provider — prefer eachlabs (routes all models), fallback to anthropic
	var prov provider.Provider
	apiKey := os.Getenv("EACHLABS_API_KEY")
	if apiKey == "" {
		if p, ok := cfg.Provider["eachlabs"]; ok {
			apiKey = p.APIKey
		}
	}

	if apiKey != "" {
		prov, err = provider.NewEachLabs(provider.EachLabsConfig{
			APIKey: apiKey,
		})
		if err != nil {
			return fmt.Errorf("failed to create eachlabs provider: %w", err)
		}
	} else {
		// Fallback to anthropic
		anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
		if anthropicKey == "" {
			if p, ok := cfg.Provider["anthropic"]; ok {
				anthropicKey = p.APIKey
			}
		}
		if anthropicKey == "" {
			return fmt.Errorf("EACHLABS_API_KEY or ANTHROPIC_API_KEY required")
		}
		prov, err = provider.NewAnthropic(provider.AnthropicConfig{
			APIKey: anthropicKey,
		})
		if err != nil {
			return fmt.Errorf("failed to create anthropic provider: %w", err)
		}
	}

	// Wrap with resilience
	prov = provider.NewResilientProvider(provider.ResilientConfig{
		Primary: prov,
		Retry:   provider.DefaultRetryConfig(),
	})

	// Auth store
	var auth *api.AuthStore
	if !apiNoAuth {
		auth, err = api.NewAuthStore()
		if err != nil {
			return fmt.Errorf("failed to load auth store: %w", err)
		}
	} else {
		// Create a no-op auth store that accepts everything
		auth, _ = api.NewAuthStore()
	}

	// Resolve config with CLI overrides
	port := cfg.API.Port
	if apiPort > 0 {
		port = apiPort
	}
	host := cfg.API.Host
	if apiHost != "" {
		host = apiHost
	}
	workers := cfg.API.Workers
	if apiWorkers > 0 {
		workers = apiWorkers
	}
	maxTimeout := time.Duration(cfg.API.MaxTimeout) * time.Second

	// Create and start server
	srv := api.NewServer(api.ServerConfig{
		Host:       host,
		Port:       port,
		Workers:    workers,
		MaxTimeout: maxTimeout,
	}, prov, auth)

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

	// Print banner
	fmt.Println("╭─────────────────────────────────────────╮")
	fmt.Println("│         klaw Creative Agent API          │")
	fmt.Println("╰─────────────────────────────────────────╯")
	fmt.Printf("Endpoint:  http://%s:%d/api/v1/run\n", host, port)
	fmt.Printf("Health:    http://%s:%d/api/v1/health\n", host, port)
	fmt.Printf("Workers:   %d\n", workers)
	if apiNoAuth {
		fmt.Println("Auth:      disabled")
	} else {
		fmt.Println("Auth:      Bearer token required")
	}
	fmt.Println("")
	fmt.Println("Press Ctrl+C to stop")
	fmt.Println("")

	return srv.Start(ctx)
}
