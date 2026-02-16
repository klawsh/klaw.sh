package commands

import (
	"fmt"

	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/spf13/cobra"
)

var createCmd = &cobra.Command{
	Use:   "create <resource>",
	Short: "Create a resource",
	Long: `Create a new resource.

Resources:
  cluster    Create a cluster (company/organization)
  namespace  Create a namespace (team/department)
  channel    Add a messaging channel
  agent      Create an agent definition
  server     Start a background server
  session    Start a new session`,
}

func init() {
	createCmd.AddCommand(createServerCmd)
	createCmd.AddCommand(createChannelCmd)
	createCmd.AddCommand(createSessionCmd)
}

var serverPort int
var serverHost string

var createServerCmd = &cobra.Command{
	Use:     "server",
	Aliases: []string{"srv"},
	Short:   "Start a background server",
	Long: `Start a klaw server running in the background.

The server handles:
- Channel connections (telegram, discord)
- WebSocket API for UI integration
- Health monitoring

Examples:
  klaw create server
  klaw create server --port 8080
  klaw create server --host 0.0.0.0`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Phase 4 - not implemented yet
		fmt.Printf("Starting server on %s:%d...\n", serverHost, serverPort)
		fmt.Println("Server mode is planned for Phase 4.")
		fmt.Println("For now, use 'klaw chat' for interactive mode.")
		return nil
	},
}

func init() {
	createServerCmd.Flags().IntVar(&serverPort, "port", 8080, "server port")
	createServerCmd.Flags().StringVar(&serverHost, "host", "127.0.0.1", "server host")
}

var channelType string
var channelToken string
var channelName string

var slackBotToken string
var slackAppToken string

var createChannelCmd = &cobra.Command{
	Use:     "channel <type>",
	Aliases: []string{"ch"},
	Short:   "Add a messaging channel",
	Long: `Add a messaging channel to the current namespace.

Channel types:
  slack      Slack (Socket Mode)
  telegram   Telegram bot
  discord    Discord bot

The channel is bound to the current cluster/namespace context.

Examples:
  klaw create channel slack --name sales-bot --bot-token xoxb-... --app-token xapp-...
  klaw create channel telegram --name support-bot --token <bot_token>
  klaw create channel discord --name community-bot --token <bot_token>`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		channelType := args[0]

		store := cluster.NewStore(config.StateDir())
		ctxMgr := cluster.NewContextManager(config.ConfigDir())

		// Get current context
		clusterName, namespace, err := ctxMgr.RequireCurrent()
		if err != nil {
			return err
		}

		// Default channel name from type
		name := channelName
		if name == "" {
			name = channelType + "-bot"
		}

		// Build config based on type
		channelConfig := make(map[string]string)

		switch channelType {
		case "slack":
			if slackBotToken == "" || slackAppToken == "" {
				return fmt.Errorf("slack requires both --bot-token and --app-token flags")
			}
			channelConfig["bot_token"] = slackBotToken
			channelConfig["app_token"] = slackAppToken

		case "telegram", "discord":
			if channelToken == "" {
				return fmt.Errorf("--%s requires --token flag", channelType)
			}
			channelConfig["token"] = channelToken

		default:
			return fmt.Errorf("unknown channel type: %s (use: slack, telegram, discord)", channelType)
		}

		// Create channel binding
		binding := &cluster.ChannelBinding{
			Name:      name,
			Type:      channelType,
			Cluster:   clusterName,
			Namespace: namespace,
			Config:    channelConfig,
		}

		if err := store.CreateChannelBinding(binding); err != nil {
			return err
		}

		fmt.Printf("Channel '%s' created in %s/%s\n", name, clusterName, namespace)
		fmt.Println("")
		fmt.Printf("Start it with: klaw run channel %s\n", name)

		return nil
	},
}

func init() {
	createChannelCmd.Flags().StringVar(&channelName, "name", "", "channel name (default: <type>-bot)")
	createChannelCmd.Flags().StringVar(&channelToken, "token", "", "bot token (telegram/discord)")
	createChannelCmd.Flags().StringVar(&slackBotToken, "bot-token", "", "Slack bot token (xoxb-...)")
	createChannelCmd.Flags().StringVar(&slackAppToken, "app-token", "", "Slack app token (xapp-...)")
}

var createSessionCmd = &cobra.Command{
	Use:     "session",
	Aliases: []string{"sess"},
	Short:   "Start a new session",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Starting new session...")
		fmt.Println("Use 'klaw chat' to start an interactive session.")
		return nil
	},
}
