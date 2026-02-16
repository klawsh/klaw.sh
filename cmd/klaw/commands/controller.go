package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/controller"
	"github.com/spf13/cobra"
)

var (
	controllerPort      int
	controllerToken     string
	controllerStoreType string
	controllerEtcdAddrs []string
	controllerUseGRPC   bool
)

var controllerCmd = &cobra.Command{
	Use:   "controller",
	Short: "Manage the klaw controller",
	Long: `The klaw controller is the central brain that manages all nodes and agents.

Nodes connect to the controller to register agents and receive tasks.
The controller handles:
  - Node registration and heartbeat
  - Agent registry
  - Task dispatch
  - Cron job scheduling

Examples:
  klaw controller start                    # Start with defaults
  klaw controller start --port 9090        # Custom port
  klaw controller start --token secret123  # With auth token
  klaw controller start --store etcd --etcd-endpoints etcd1:2379,etcd2:2379`,
}

var controllerStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the controller",
	Long: `Start the klaw controller server.

The controller listens for node connections and manages the cluster state.`,
	RunE: runControllerStart,
}

var controllerStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show controller status",
	RunE:  runControllerStatus,
}

func init() {
	controllerStartCmd.Flags().IntVarP(&controllerPort, "port", "p", 9090, "Port to listen on")
	controllerStartCmd.Flags().StringVar(&controllerToken, "token", "", "Authentication token for nodes")
	controllerStartCmd.Flags().StringVar(&controllerStoreType, "store", "file", "Storage backend (file, etcd)")
	controllerStartCmd.Flags().StringSliceVar(&controllerEtcdAddrs, "etcd-endpoints", nil, "etcd endpoints (comma-separated)")
	controllerStartCmd.Flags().BoolVar(&controllerUseGRPC, "grpc", true, "Use gRPC protocol (default: true)")

	controllerCmd.AddCommand(controllerStartCmd)
	controllerCmd.AddCommand(controllerStatusCmd)
	rootCmd.AddCommand(controllerCmd)
}

func runControllerStart(cmd *cobra.Command, args []string) error {
	dataDir := config.StateDir() + "/controller"

	cfg := controller.ServerConfig{
		Port:      controllerPort,
		DataDir:   dataDir,
		AuthToken: controllerToken,
		StoreType: controllerStoreType,
		EtcdAddrs: controllerEtcdAddrs,
	}

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("╭─────────────────────────────────────────╮")
	fmt.Println("│          klaw controller                │")
	fmt.Println("╰─────────────────────────────────────────╯")
	fmt.Printf("Port:     %d\n", controllerPort)
	fmt.Printf("Protocol: %s\n", map[bool]string{true: "gRPC", false: "TCP/JSON"}[controllerUseGRPC])
	fmt.Printf("Store:    %s\n", controllerStoreType)
	if controllerToken != "" {
		fmt.Println("Auth:     enabled (token required)")
	} else {
		fmt.Println("Auth:     disabled (no token)")
	}
	fmt.Println()

	if controllerUseGRPC {
		// Use gRPC server
		server, err := controller.NewGRPCServer(cfg)
		if err != nil {
			return err
		}

		go func() {
			<-sigCh
			fmt.Println("\n👋 Shutting down controller...")
			server.Stop()
		}()

		return server.Start()
	}

	// Use legacy TCP/JSON server
	server, err := controller.NewServer(cfg)
	if err != nil {
		return err
	}

	go func() {
		<-sigCh
		fmt.Println("\n👋 Shutting down controller...")
		server.Stop()
	}()

	return server.Start()
}

func runControllerStatus(cmd *cobra.Command, args []string) error {
	// TODO: Connect to running controller and get status
	fmt.Println("Controller status check not yet implemented.")
	fmt.Println("Use 'klaw get nodes' to see connected nodes.")
	return nil
}

// --- Node listing commands ---

var getNodesCmd = &cobra.Command{
	Use:   "nodes",
	Short: "List connected nodes",
	RunE:  runGetNodes,
}

func init() {
	getCmd.AddCommand(getNodesCmd)
}

func runGetNodes(cmd *cobra.Command, args []string) error {
	dataDir := config.StateDir() + "/controller"
	store, err := controller.NewFileStore(dataDir)
	if err != nil {
		return fmt.Errorf("controller not running or no data: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	nodes, err := store.ListNodes(ctx)
	if err != nil {
		return err
	}

	if len(nodes) == 0 {
		fmt.Println("No nodes connected.")
		fmt.Println()
		fmt.Println("Start the controller and connect nodes:")
		fmt.Println("  1. klaw controller start")
		fmt.Println("  2. klaw node join localhost:9090")
		return nil
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(nodes)
	}

	fmt.Printf("Nodes (%d):\n\n", len(nodes))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSTATUS\tAGENTS\tLAST SEEN")
	fmt.Fprintln(w, "--\t----\t------\t------\t---------")

	for _, node := range nodes {
		status := node.Status
		switch status {
		case "ready":
			status = "✓ ready"
		case "not-ready":
			status = "⚠ not-ready"
		case "disconnected":
			status = "✗ disconnected"
		}

		lastSeen := node.LastSeen.Format("15:04:05")
		if time.Since(node.LastSeen) > time.Hour {
			lastSeen = node.LastSeen.Format("Jan 02 15:04")
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
			node.ID, node.Name, status, len(node.AgentIDs), lastSeen)
	}
	w.Flush()

	return nil
}

// --- Describe node ---

var describeNodeCmd = &cobra.Command{
	Use:   "node <node-id>",
	Short: "Show node details",
	Args:  cobra.ExactArgs(1),
	RunE:  runDescribeNode,
}

func init() {
	describeCmd.AddCommand(describeNodeCmd)
}

func runDescribeNode(cmd *cobra.Command, args []string) error {
	nodeID := args[0]

	dataDir := config.StateDir() + "/controller"
	store, err := controller.NewFileStore(dataDir)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx := context.Background()
	node, err := store.GetNode(ctx, nodeID)
	if err != nil {
		return err
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(node)
	}

	fmt.Printf("ID:        %s\n", node.ID)
	fmt.Printf("Name:      %s\n", node.Name)
	fmt.Printf("Address:   %s\n", node.Address)
	fmt.Printf("Status:    %s\n", node.Status)
	fmt.Printf("Joined:    %s\n", node.JoinedAt.Format(time.RFC3339))
	fmt.Printf("Last Seen: %s\n", node.LastSeen.Format(time.RFC3339))

	if len(node.Labels) > 0 {
		fmt.Println("Labels:")
		for k, v := range node.Labels {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}

	if len(node.AgentIDs) > 0 {
		fmt.Println()
		fmt.Println("Agents:")
		agents, _ := store.ListAgentsByNode(ctx, nodeID)
		for _, a := range agents {
			fmt.Printf("  - %s (%s)\n", a.Name, a.Status)
		}
	}

	return nil
}
