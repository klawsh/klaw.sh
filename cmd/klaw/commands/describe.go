package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eachlabs/klaw/internal/config"
	"github.com/spf13/cobra"
)

var describeCmd = &cobra.Command{
	Use:   "describe <resource> [name]",
	Short: "Show detailed resource information",
	Long: `Show detailed information about a resource.

Resources:
  server     Server details
  session    Session transcript and metadata
  model      Model capabilities and pricing
  channel    Channel configuration`,
}

func init() {
	describeCmd.AddCommand(describeServerCmd)
	describeCmd.AddCommand(describeSessionCmd)
	describeCmd.AddCommand(describeModelCmd)
	describeCmd.AddCommand(describeChannelCmd)
}

var describeServerCmd = &cobra.Command{
	Use:     "server",
	Aliases: []string{"srv"},
	Short:   "Show server details",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("No server running.")
		fmt.Println("\nTo start a server:")
		fmt.Println("  klaw create server")
		return nil
	},
}

var describeSessionCmd = &cobra.Command{
	Use:     "session <id>",
	Aliases: []string{"sess"},
	Short:   "Show session details",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionID := args[0]
		sessionPath := filepath.Join(config.SessionsDir(), sessionID+".json")

		data, err := os.ReadFile(sessionPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("session not found: %s", sessionID)
			}
			return err
		}

		var session map[string]interface{}
		if err := json.Unmarshal(data, &session); err != nil {
			return fmt.Errorf("invalid session data: %w", err)
		}

		if jsonOut {
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Session: %s\n", sessionID)
		fmt.Println("---")

		if messages, ok := session["messages"].([]interface{}); ok {
			fmt.Printf("Messages: %d\n", len(messages))
			fmt.Println("\nTranscript:")
			for _, m := range messages {
				if msg, ok := m.(map[string]interface{}); ok {
					role := msg["role"]
					content := msg["content"]
					if s, ok := content.(string); ok && len(s) > 200 {
						content = s[:200] + "..."
					}
					fmt.Printf("\n[%s]: %v\n", role, content)
				}
			}
		}

		return nil
	},
}

var describeModelCmd = &cobra.Command{
	Use:     "model <id>",
	Aliases: []string{"models"},
	Short:   "Show model details",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		modelID := "claude-sonnet-4-20250514"
		if len(args) > 0 {
			modelID = args[0]
		}

		models := map[string]modelDetail{
			"claude-sonnet-4-20250514": {
				ID:            "claude-sonnet-4-20250514",
				Provider:      "anthropic",
				Description:   "Fast, intelligent model for most tasks",
				ContextWindow: 200000,
				MaxOutput:     8192,
				InputCost:     3.0,
				OutputCost:    15.0,
			},
			"claude-opus-4-20250514": {
				ID:            "claude-opus-4-20250514",
				Provider:      "anthropic",
				Description:   "Most capable model for complex tasks",
				ContextWindow: 200000,
				MaxOutput:     8192,
				InputCost:     15.0,
				OutputCost:    75.0,
			},
			"claude-3-5-sonnet-20241022": {
				ID:            "claude-3-5-sonnet-20241022",
				Provider:      "anthropic",
				Description:   "Previous generation Sonnet",
				ContextWindow: 200000,
				MaxOutput:     8192,
				InputCost:     3.0,
				OutputCost:    15.0,
			},
		}

		model, ok := models[modelID]
		if !ok {
			return fmt.Errorf("unknown model: %s", modelID)
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(model)
		}

		fmt.Printf("Model: %s\n", model.ID)
		fmt.Printf("Provider: %s\n", model.Provider)
		fmt.Printf("Description: %s\n", model.Description)
		fmt.Println("---")
		fmt.Printf("Context Window: %d tokens\n", model.ContextWindow)
		fmt.Printf("Max Output: %d tokens\n", model.MaxOutput)
		fmt.Printf("Input Cost: $%.2f / 1M tokens\n", model.InputCost)
		fmt.Printf("Output Cost: $%.2f / 1M tokens\n", model.OutputCost)

		return nil
	},
}

type modelDetail struct {
	ID            string  `json:"id"`
	Provider      string  `json:"provider"`
	Description   string  `json:"description"`
	ContextWindow int     `json:"context_window"`
	MaxOutput     int     `json:"max_output"`
	InputCost     float64 `json:"input_cost_per_million"`
	OutputCost    float64 `json:"output_cost_per_million"`
}

var describeChannelCmd = &cobra.Command{
	Use:     "channel <type>",
	Aliases: []string{"ch"},
	Short:   "Show channel details",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		channelType := args[0]

		if channelType == "terminal" {
			fmt.Println("Channel: terminal")
			fmt.Println("Status: active")
			fmt.Println("Description: Interactive terminal input/output")
			return nil
		}

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		ch, ok := cfg.Channel[channelType]
		if !ok {
			return fmt.Errorf("channel not configured: %s", channelType)
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(ch)
		}

		fmt.Printf("Channel: %s\n", channelType)
		fmt.Printf("Enabled: %v\n", ch.Enabled)
		fmt.Printf("Token: %s\n", maskToken(ch.Token))
		if ch.GuildID != "" {
			fmt.Printf("Guild ID: %s\n", ch.GuildID)
		}

		return nil
	},
}
