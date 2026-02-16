package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/eachlabs/klaw/internal/agent"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/runtime"
	"github.com/spf13/cobra"
)

var podmanLogsFollow bool

var podmanRuntime *runtime.PodmanRuntime

func init() {
	podmanRuntime = runtime.NewPodmanRuntime(config.StateDir())
}

// --- klaw build ---

var buildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build the klaw container image",
	Long: `Build the klaw container image using Podman.

This builds a lightweight container image (~10MB) containing the klaw binary.
The image is required before running agents.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Find klaw source directory
		execPath, err := os.Executable()
		if err != nil {
			return err
		}
		srcDir := filepath.Dir(filepath.Dir(execPath))

		// Check for Containerfile
		containerfile := filepath.Join(srcDir, "Containerfile")
		if _, err := os.Stat(containerfile); os.IsNotExist(err) {
			// Try current directory
			cwd, _ := os.Getwd()
			containerfile = filepath.Join(cwd, "Containerfile")
			if _, err := os.Stat(containerfile); os.IsNotExist(err) {
				return fmt.Errorf("Containerfile not found. Run from klaw source directory.")
			}
			srcDir = cwd
		}

		fmt.Println("Building klaw container image...")
		fmt.Printf("  Source: %s\n", srcDir)

		if err := podmanRuntime.Build(context.Background(), srcDir); err != nil {
			return err
		}

		fmt.Println("\nImage built successfully: localhost/klaw:latest")
		fmt.Println("Run agents with: klaw run <agent-name>")
		return nil
	},
}

// --- klaw run ---

var (
	runTask    string
	runModel   string
	runDetach  bool
	runWorkdir string
)

var runCmd = &cobra.Command{
	Use:   "run <agent-name>",
	Short: "Run an agent in a container",
	Long: `Run an agent in an isolated Podman container.

If the agent is defined (created with 'klaw create agent'), uses that configuration.
Otherwise, requires --task flag for ad-hoc agents.

Examples:
  klaw run coder
  klaw run coder --detach
  klaw run adhoc --task "List all files"
  klaw run myagent --workdir /path/to/project`,
	Args: cobra.ExactArgs(1),
	RunE: runAgent,
}

func init() {
	runCmd.Flags().StringVar(&runTask, "task", "", "Task for the agent (required for ad-hoc)")
	runCmd.Flags().StringVar(&runModel, "model", "claude-sonnet-4-20250514", "Model to use")
	runCmd.Flags().BoolVarP(&runDetach, "detach", "d", false, "Run in background")
	runCmd.Flags().StringVar(&runWorkdir, "workdir", "", "Working directory to mount")
}

func runAgent(cmd *cobra.Command, args []string) error {
	name := args[0]

	// Check if image exists
	if !podmanRuntime.ImageExists() {
		fmt.Println("Container image not found. Building...")
		cwd, _ := os.Getwd()
		if err := podmanRuntime.Build(context.Background(), cwd); err != nil {
			return fmt.Errorf("failed to build image: %w\nRun 'klaw build' from the klaw source directory", err)
		}
	}

	// Try to load agent definition
	store := agent.NewDefinitionStore(agentsDir())
	def, err := store.Load(name)

	task := runTask
	model := runModel
	workdir := runWorkdir

	if err == nil {
		// Use definition
		if task == "" {
			task = def.Task
		}
		if model == "claude-sonnet-4-20250514" && def.Model != "" {
			model = def.Model
		}
		if workdir == "" {
			workdir = def.WorkDir
		}
	} else if task == "" {
		return fmt.Errorf("agent '%s' not found. Create it with 'klaw create agent %s --task \"...\"' or use --task flag", name, name)
	}

	// Get API key
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		cfg, _ := config.Load()
		if p, ok := cfg.Provider["anthropic"]; ok {
			apiKey = p.APIKey
		}
	}
	if apiKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	fmt.Printf("Starting agent '%s' in container...\n", name)

	container, err := podmanRuntime.Start(context.Background(), runtime.StartConfig{
		AgentName: name,
		Task:      task,
		Model:     model,
		WorkDir:   workdir,
		APIKey:    apiKey,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Container started: %s\n", container.Name)
	fmt.Printf("  ID: %s\n", container.ID)
	if workdir != "" {
		fmt.Printf("  Workdir: %s\n", workdir)
	}

	if runDetach {
		fmt.Println("\nRunning in background.")
		fmt.Printf("  View logs: klaw logs %s\n", container.Name)
		fmt.Printf("  Stop:      klaw stop %s\n", container.Name)
		return nil
	}

	// Stream logs
	fmt.Println("\n--- Agent Output ---")
	logs, err := podmanRuntime.StreamLogs(context.Background(), container.Name)
	if err != nil {
		return err
	}

	for line := range logs {
		fmt.Println(line)
	}

	return nil
}

// --- klaw ps ---

var psCmd = &cobra.Command{
	Use:   "ps",
	Short: "List running agent containers",
	RunE: func(cmd *cobra.Command, args []string) error {
		containers, err := podmanRuntime.List()
		if err != nil {
			return err
		}

		if len(containers) == 0 {
			fmt.Println("No agent containers running.")
			fmt.Println("Start one with: klaw run <agent-name>")
			return nil
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(containers)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "CONTAINER ID\tNAME\tAGENT\tSTATUS\tAGE")
		for _, c := range containers {
			age := time.Since(c.StartedAt).Round(time.Second).String()
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.ID, c.Name, c.AgentName, c.Status, age)
		}
		return w.Flush()
	},
}

// --- klaw logs (updated for podman) ---

var podmanLogsCmd = &cobra.Command{
	Use:   "logs <container>",
	Short: "View container logs",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		nameOrID := args[0]

		logCmd, err := podmanRuntime.Logs(nameOrID, podmanLogsFollow)
		if err != nil {
			return err
		}

		logCmd.Stdout = os.Stdout
		logCmd.Stderr = os.Stderr
		return logCmd.Run()
	},
}

func init() {
	podmanLogsCmd.Flags().BoolVarP(&podmanLogsFollow, "follow", "f", false, "Follow log output")
}

// --- klaw stop (updated for podman) ---

var podmanStopCmd = &cobra.Command{
	Use:   "stop <container>",
	Short: "Stop an agent container",
	Args:  cobra.MinimumNArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 || args[0] == "--all" {
			fmt.Println("Stopping all klaw containers...")
			return podmanRuntime.StopAll()
		}

		nameOrID := args[0]
		if err := podmanRuntime.Stop(nameOrID); err != nil {
			return err
		}

		fmt.Printf("Container %s stopped.\n", nameOrID)
		return nil
	},
}

// --- klaw attach ---

var attachCmd = &cobra.Command{
	Use:   "attach <container>",
	Short: "Attach to a running container",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return podmanRuntime.Attach(args[0])
	},
}

// --- klaw exec (for containers) ---

var containerExecCmd = &cobra.Command{
	Use:   "exec <container> -- <command>",
	Short: "Execute a command in a container",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		container := args[0]
		command := args[1:]
		return podmanRuntime.Exec(container, command)
	},
}

func init() {
	// Register new commands
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(psCmd)
	rootCmd.AddCommand(attachCmd)

	// Replace old commands with podman versions
	rootCmd.AddCommand(podmanLogsCmd)
	rootCmd.AddCommand(podmanStopCmd)

	// Exec for containers
	rootCmd.AddCommand(containerExecCmd)
}

// Helper to get relative time
func relativeTime(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
