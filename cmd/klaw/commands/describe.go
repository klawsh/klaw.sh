package commands

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/session"
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

		mgr := session.NewManager()
		sess, err := mgr.Load(sessionID)
		if err != nil {
			return err
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(sess)
		}

		// Print metadata
		fmt.Printf("Session: %s\n", sess.ID)
		if sess.Name != "" {
			fmt.Printf("Name: %s\n", sess.Name)
		}
		fmt.Println("---")
		fmt.Printf("Model: %s\n", sess.Model)
		fmt.Printf("Provider: %s\n", sess.Provider)
		if sess.Agent != "" {
			fmt.Printf("Agent: %s\n", sess.Agent)
		}
		if sess.WorkDir != "" {
			fmt.Printf("Working Dir: %s\n", sess.WorkDir)
		}
		fmt.Printf("Created: %s\n", sess.CreatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("Updated: %s\n", sess.UpdatedAt.Format("2006-01-02 15:04:05"))
		fmt.Printf("Messages: %d\n", len(sess.Messages))

		// Print transcript
		if len(sess.Messages) > 0 {
			fmt.Println("\n--- Transcript ---")
			for _, msg := range sess.Messages {
				role := msg.Role
				content := msg.Content

				// Handle tool results
				if msg.ToolResult != nil {
					fmt.Printf("\n[tool_result]: %s\n", truncateContent(msg.ToolResult.Content, 200))
					continue
				}

				// Handle tool calls
				if len(msg.ToolCalls) > 0 {
					fmt.Printf("\n[%s]: %s\n", role, truncateContent(content, 200))
					for _, tc := range msg.ToolCalls {
						fmt.Printf("  -> tool_call: %s\n", tc.Name)
					}
					continue
				}

				fmt.Printf("\n[%s]: %s\n", role, truncateContent(content, 200))
			}
		}

		return nil
	},
}

// truncateContent truncates content for display
func truncateContent(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
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
