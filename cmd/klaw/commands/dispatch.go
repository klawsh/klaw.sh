package commands

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/controller/pb"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	dispatchController string
	dispatchToken      string
	dispatchWait       bool
	dispatchTimeout    int
	dispatchUseGRPC    bool
)

var dispatchCmd = &cobra.Command{
	Use:   "dispatch <agent> <prompt>",
	Short: "Dispatch a task to an agent via the controller",
	Long: `Send a task to a specific agent through the controller.

The controller will route the task to the node running the agent.

Examples:
  klaw dispatch researcher "Find the latest AI news"
  klaw dispatch coder "Write a hello world in Go" --wait
  klaw dispatch writer "Draft an email" --controller localhost:9090`,
	Args: cobra.ExactArgs(2),
	RunE: runDispatch,
}

func init() {
	dispatchCmd.Flags().StringVar(&dispatchController, "controller", "localhost:9090", "Controller address")
	dispatchCmd.Flags().StringVar(&dispatchToken, "token", "", "Authentication token")
	dispatchCmd.Flags().BoolVar(&dispatchWait, "wait", true, "Wait for task completion")
	dispatchCmd.Flags().IntVar(&dispatchTimeout, "timeout", 300, "Timeout in seconds")
	dispatchCmd.Flags().BoolVar(&dispatchUseGRPC, "grpc", true, "Use gRPC protocol (default: true)")

	rootCmd.AddCommand(dispatchCmd)
}

// DispatchMessage is the wire format for dispatch client
type DispatchMessage struct {
	Type string `json:"type"`

	// Auth
	Token string `json:"token,omitempty"`

	// Task dispatch
	Agent  string `json:"agent,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	TaskID string `json:"task_id,omitempty"`

	// Response
	Status string `json:"status,omitempty"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func runDispatch(cmd *cobra.Command, args []string) error {
	agentName := args[0]
	prompt := args[1]

	// Load token from config if not provided
	if dispatchToken == "" {
		cfg, _ := config.Load()
		if cfg != nil && cfg.Controller != nil {
			dispatchToken = cfg.Controller.Token
			if cfg.Controller.Address != "" {
				dispatchController = cfg.Controller.Address
			}
		}
	}

	fmt.Printf("📤 Dispatching task to agent: %s\n", agentName)
	fmt.Printf("   Controller: %s\n", dispatchController)
	fmt.Printf("   Protocol:   %s\n", map[bool]string{true: "gRPC", false: "TCP/JSON"}[dispatchUseGRPC])
	fmt.Println()

	if dispatchUseGRPC {
		return runDispatchGRPC(agentName, prompt)
	}

	return runDispatchTCP(agentName, prompt)
}

func runDispatchGRPC(agentName, prompt string) error {
	// Connect via gRPC
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(dispatchTimeout)*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(dispatchController, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to controller: %w", err)
	}
	defer conn.Close()

	client := pb.NewControllerServiceClient(conn)

	// Dispatch task
	resp, err := client.DispatchTask(ctx, &pb.DispatchTaskRequest{
		Token:          dispatchToken,
		AgentName:      agentName,
		Prompt:         prompt,
		Wait:           dispatchWait,
		TimeoutSeconds: int32(dispatchTimeout),
	})
	if err != nil {
		return fmt.Errorf("dispatch failed: %w", err)
	}

	if resp.Error != "" {
		return fmt.Errorf("dispatch failed: %s", resp.Error)
	}

	fmt.Printf("✅ Task created: %s\n", resp.TaskId)

	if !dispatchWait {
		fmt.Println("\nTask dispatched. Use 'klaw get tasks' to check status.")
		return nil
	}

	if resp.Status == "completed" {
		fmt.Println("\n✅ Task completed!")
		fmt.Println()
		fmt.Println("Result:")
		fmt.Println("───────────────────────────────────────")
		fmt.Println(resp.Result)
		fmt.Println("───────────────────────────────────────")
	} else if resp.Status == "timeout" {
		return fmt.Errorf("task timed out")
	}

	return nil
}

func runDispatchTCP(agentName, prompt string) error {
	// Connect to controller
	conn, err := net.DialTimeout("tcp", dispatchController, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to controller: %w", err)
	}
	defer conn.Close()

	encoder := json.NewEncoder(conn)
	reader := bufio.NewReader(conn)

	// Send dispatch request
	err = encoder.Encode(&DispatchMessage{
		Type:   "dispatch",
		Token:  dispatchToken,
		Agent:  agentName,
		Prompt: prompt,
	})
	if err != nil {
		return fmt.Errorf("failed to send dispatch request: %w", err)
	}

	// Read response
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var resp DispatchMessage
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if resp.Type == "error" {
		return fmt.Errorf("dispatch failed: %s", resp.Error)
	}

	if resp.Type != "task_created" {
		return fmt.Errorf("unexpected response: %s", resp.Type)
	}

	fmt.Printf("✅ Task created: %s\n", resp.TaskID)

	if !dispatchWait {
		fmt.Println("\nTask dispatched. Use 'klaw get tasks' to check status.")
		return nil
	}

	// Wait for completion
	fmt.Println("\n⏳ Waiting for completion...")

	// Set read deadline
	conn.SetReadDeadline(time.Now().Add(time.Duration(dispatchTimeout) * time.Second))

	// Poll for result
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if os.IsTimeout(err) {
				return fmt.Errorf("timeout waiting for task completion")
			}
			return fmt.Errorf("connection lost: %w", err)
		}

		var update DispatchMessage
		if err := json.Unmarshal(line, &update); err != nil {
			continue
		}

		switch update.Type {
		case "task_completed":
			fmt.Println("\n✅ Task completed!")
			fmt.Println()
			fmt.Println("Result:")
			fmt.Println("───────────────────────────────────────")
			fmt.Println(update.Result)
			fmt.Println("───────────────────────────────────────")
			return nil

		case "task_failed":
			return fmt.Errorf("task failed: %s", update.Error)

		case "task_progress":
			fmt.Printf("   %s\n", update.Status)
		}
	}
}

// --- Get tasks command ---

var getTasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "List tasks",
	RunE:  runGetTasks,
}

func init() {
	getCmd.AddCommand(getTasksCmd)
}

func runGetTasks(cmd *cobra.Command, args []string) error {
	// Read tasks from controller store
	dataDir := config.StateDir() + "/controller"

	// Check if controller data exists
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		fmt.Println("No controller data found.")
		fmt.Println("Start a controller first: klaw controller start")
		return nil
	}

	// Read tasks file
	tasksFile := dataDir + "/tasks.json"
	data, err := os.ReadFile(tasksFile)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No tasks found.")
			return nil
		}
		return err
	}

	var tasks []map[string]interface{}
	if err := json.Unmarshal(data, &tasks); err != nil {
		return err
	}

	if len(tasks) == 0 {
		fmt.Println("No tasks found.")
		return nil
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(tasks)
	}

	fmt.Printf("Tasks (%d):\n\n", len(tasks))
	fmt.Println("ID        AGENT           STATUS      CREATED")
	fmt.Println("--        -----           ------      -------")

	for _, task := range tasks {
		id := task["id"]
		agentName := task["agent_name"]
		status := task["status"]
		created := task["created_at"]

		// Format status with icons
		statusStr := fmt.Sprintf("%v", status)
		switch statusStr {
		case "completed":
			statusStr = "✅ completed"
		case "failed":
			statusStr = "❌ failed"
		case "dispatched":
			statusStr = "🚀 dispatched"
		case "pending":
			statusStr = "⏳ pending"
		}

		// Parse and format time
		createdStr := ""
		if created != nil {
			if ts, err := time.Parse(time.RFC3339, created.(string)); err == nil {
				createdStr = ts.Format("15:04:05")
			}
		}

		fmt.Printf("%-9v %-15v %-13s %s\n", id, agentName, statusStr, createdStr)
	}

	return nil
}
