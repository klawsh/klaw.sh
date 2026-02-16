package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/eachlabs/klaw/internal/agent"
	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/node"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/tool"
	"github.com/spf13/cobra"
)

var (
	nodeToken   string
	nodeName    string
	nodeLabels  map[string]string
	nodeUseGRPC bool
)

var nodeCmd = &cobra.Command{
	Use:   "node",
	Short: "Manage this node",
	Long: `Run klaw as a node that connects to a controller.

The node registers with the controller and runs agents locally.
When the controller dispatches tasks, this node executes them.

Examples:
  klaw node join controller.example.com:9090
  klaw node start
  klaw node status
  klaw node leave`,
}

var nodeJoinCmd = &cobra.Command{
	Use:   "join <controller-address>",
	Short: "Join a controller",
	Long: `Connect this machine to a klaw controller.

Examples:
  klaw node join localhost:9090
  klaw node join controller.example.com:9090 --token secret123
  klaw node join 10.0.0.1:9090 --name worker-1 --labels gpu=true,region=us-east`,
	Args: cobra.ExactArgs(1),
	RunE: runNodeJoin,
}

var nodeStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the node agent",
	Long: `Start the node agent and connect to the controller.

This will:
1. Connect to the controller
2. Register all agents in the current namespace
3. Listen for tasks and execute them`,
	RunE: runNodeStart,
}

var nodeStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show node status",
	RunE:  runNodeStatus,
}

var nodeLeaveCmd = &cobra.Command{
	Use:   "leave",
	Short: "Leave the controller",
	RunE:  runNodeLeave,
}

func init() {
	nodeJoinCmd.Flags().StringVar(&nodeToken, "token", "", "Authentication token")
	nodeJoinCmd.Flags().StringVar(&nodeName, "name", "", "Node name (default: hostname)")
	nodeJoinCmd.Flags().StringToStringVar(&nodeLabels, "labels", nil, "Node labels (key=value,...)")
	nodeJoinCmd.Flags().BoolVar(&nodeUseGRPC, "grpc", true, "Use gRPC protocol (default: true)")

	nodeStartCmd.Flags().StringVar(&nodeToken, "token", "", "Authentication token")
	nodeStartCmd.Flags().StringVar(&nodeName, "name", "", "Node name (default: hostname)")
	nodeStartCmd.Flags().BoolVar(&nodeUseGRPC, "grpc", true, "Use gRPC protocol (default: true)")

	nodeCmd.AddCommand(nodeJoinCmd)
	nodeCmd.AddCommand(nodeStartCmd)
	nodeCmd.AddCommand(nodeStatusCmd)
	nodeCmd.AddCommand(nodeLeaveCmd)
	rootCmd.AddCommand(nodeCmd)
}

func runNodeJoin(cmd *cobra.Command, args []string) error {
	controllerAddr := args[0]

	cfg := node.ClientConfig{
		ControllerAddr: controllerAddr,
		NodeName:       nodeName,
		Token:          nodeToken,
		Labels:         nodeLabels,
		DataDir:        config.StateDir() + "/node",
	}

	if nodeUseGRPC {
		client := node.NewGRPCClient(cfg)
		if err := client.Connect(); err != nil {
			return err
		}
		fmt.Println()
		fmt.Println("Node joined successfully! (gRPC)")
		fmt.Println()
		fmt.Println("To start the node agent:")
		fmt.Println("  klaw node start", controllerAddr)
		return client.Stop()
	}

	// Legacy TCP client
	client := node.NewClient(cfg)
	if err := client.Connect(); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Node joined successfully!")
	fmt.Println()
	fmt.Println("To start the node agent:")
	fmt.Println("  klaw node start")

	return client.Stop()
}

func runNodeStart(cmd *cobra.Command, args []string) error {
	// Load node config
	nodeDataDir := config.StateDir() + "/node"

	// Check if joined
	// For now, require explicit controller address
	if len(args) == 0 {
		return fmt.Errorf("usage: klaw node start <controller-address>\n\nOr first join: klaw node join <controller-address>")
	}

	controllerAddr := args[0]

	// Get cluster context
	store := cluster.NewStore(config.StateDir())
	ctxMgr := cluster.NewContextManager(config.ConfigDir())
	clusterName, namespace, err := ctxMgr.RequireCurrent()
	if err != nil {
		return err
	}

	// Load config for API key
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		if p, ok := cfg.Provider["anthropic"]; ok {
			apiKey = p.APIKey
		}
	}
	if apiKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	// Create node client (gRPC or TCP)
	clientCfg := node.ClientConfig{
		ControllerAddr: controllerAddr,
		NodeName:       nodeName,
		Token:          nodeToken,
		Labels:         nodeLabels,
		DataDir:        nodeDataDir,
	}

	var client node.NodeClient
	protocol := "TCP"
	if nodeUseGRPC {
		client = node.NewGRPCClient(clientCfg)
		protocol = "gRPC"
	} else {
		client = node.NewClient(clientCfg)
	}

	// Set up agent runner
	client.SetAgentRunner(func(ctx context.Context, agentName, prompt string) (string, error) {
		// Get agent config
		agentBinding, err := store.GetAgentBinding(clusterName, namespace, agentName)
		if err != nil {
			return "", fmt.Errorf("agent not found: %s", agentName)
		}

		// Create provider
		model := agentBinding.Model
		if model == "" {
			model = cfg.Defaults.Model
		}
		if model == "" {
			model = "claude-sonnet-4-20250514"
		}

		prov, err := provider.NewAnthropic(provider.AnthropicConfig{
			APIKey: apiKey,
			Model:  model,
		})
		if err != nil {
			return "", err
		}

		// Create tools
		workDir, _ := os.Getwd()
		tools := tool.DefaultRegistry(workDir)

		// Run agent
		result, err := agent.RunOnce(ctx, agent.RunOnceConfig{
			Provider:     prov,
			Tools:        tools,
			SystemPrompt: agentBinding.SystemPrompt,
			Prompt:       prompt,
			MaxTokens:    8192,
		})

		if err != nil {
			return "", err
		}

		return result, nil
	})

	// Connect to controller
	if err := client.Connect(); err != nil {
		return err
	}

	// Start the client
	if err := client.Start(); err != nil {
		return err
	}

	// Register agents
	agents, err := store.ListAgentBindings(clusterName, namespace)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("Registering agents...")
	for _, ag := range agents {
		agentID, err := client.RegisterAgent(
			ag.Name,
			clusterName,
			namespace,
			ag.Description,
			ag.Model,
			ag.Skills,
		)
		if err != nil {
			fmt.Printf("  ⚠️  Failed to register %s: %v\n", ag.Name, err)
		} else {
			fmt.Printf("  ✅ %s (%s)\n", ag.Name, agentID)
		}
	}

	fmt.Println()
	fmt.Println("╭─────────────────────────────────────────╮")
	fmt.Println("│             klaw node                   │")
	fmt.Println("╰─────────────────────────────────────────╯")
	fmt.Printf("Node ID:    %s\n", client.GetNodeID())
	fmt.Printf("Controller: %s\n", controllerAddr)
	fmt.Printf("Protocol:   %s\n", protocol)
	fmt.Printf("Cluster:    %s/%s\n", clusterName, namespace)
	fmt.Printf("Agents:     %d\n", len(agents))
	fmt.Println()
	fmt.Println("Waiting for tasks... (Ctrl+C to stop)")
	fmt.Println()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh
	fmt.Println("\n👋 Stopping node...")

	return client.Stop()
}

func runNodeStatus(cmd *cobra.Command, args []string) error {
	// TODO: Read saved node info and check status
	fmt.Println("Node status not yet implemented.")
	return nil
}

func runNodeLeave(cmd *cobra.Command, args []string) error {
	// TODO: Deregister from controller
	fmt.Println("Node leave not yet implemented.")
	return nil
}
