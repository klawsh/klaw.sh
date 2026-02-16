package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/eachlabs/klaw/internal/agent"
	"github.com/eachlabs/klaw/internal/channel"
	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/tool"
	"github.com/spf13/cobra"
)

var (
	agentTask        string
	agentModel       string
	agentTools       string
	agentWorkdir     string
	agentRuntime     string
	agentDescription string
	agentTriggers    string
	agentSkills      string
	agentBootstrap   bool
)

// DefaultAgentSkills are included with every agent (from skills.sh)
var DefaultAgentSkills = []string{
	"vercel-labs/agent-browser", // Browser automation
}

func init() {
	// Add agent subcommands to existing commands
	createCmd.AddCommand(createAgentCmd)
	getCmd.AddCommand(getAgentsCmd)
	deleteCmd.AddCommand(deleteAgentCmd)
	describeCmd.AddCommand(describeAgentCmd)

	// Worker command (runs inside container)
	rootCmd.AddCommand(workerCmd)
}

// --- klaw create agent ---

var createAgentCmd = &cobra.Command{
	Use:   "agent <name>",
	Short: "Create an agent in the current namespace",
	Long: `Create a new agent in the current namespace.

The agent will be available to the orchestrator for routing messages.
Use --skills to give the agent special capabilities.

Examples:
  klaw create agent coder --description "Writes and reviews code"
  klaw create agent researcher --description "Researches topics" --skills web-search
  klaw create agent devops --description "Manages infrastructure" --skills docker,git,api
  klaw create agent writer --description "Writes content" --model claude-opus-4

Available skills: web-search, browser, code-exec, git, docker, api, database, slack, email, calendar
Run 'klaw skill list' to see all available skills.`,
	Args: cobra.ExactArgs(1),
	RunE: runCreateAgent,
}

func init() {
	createAgentCmd.Flags().StringVarP(&agentDescription, "description", "d", "", "What this agent does (required)")
	createAgentCmd.Flags().StringVar(&agentModel, "model", "claude-sonnet-4-20250514", "Model to use")
	createAgentCmd.Flags().StringVar(&agentTools, "tools", "bash,read,write,edit,glob,grep", "Comma-separated list of tools")
	createAgentCmd.Flags().StringVar(&agentTriggers, "triggers", "", "Keywords that route to this agent (comma-separated)")
	createAgentCmd.Flags().StringVar(&agentSkills, "skills", "", "Skills to enable (comma-separated, e.g., web-search,git,docker)")
	createAgentCmd.Flags().StringVar(&agentTask, "task", "", "System prompt / task (optional, uses description if not set)")
	createAgentCmd.Flags().BoolVar(&agentBootstrap, "bootstrap", true, "Generate AI-enhanced system prompt (default: true)")
	createAgentCmd.MarkFlagRequired("description")
}

func runCreateAgent(cmd *cobra.Command, args []string) error {
	name := args[0]

	store := cluster.NewStore(config.StateDir())
	ctxMgr := cluster.NewContextManager(config.ConfigDir())

	clusterName, namespace, err := ctxMgr.RequireCurrent()
	if err != nil {
		return err
	}

	// Check if exists
	if store.AgentBindingExists(clusterName, namespace, name) {
		return fmt.Errorf("agent already exists: %s (use 'klaw delete agent %s' first)", name, name)
	}

	// Parse skills early for bootstrap - always include default skills
	skills := make([]string, len(DefaultAgentSkills))
	copy(skills, DefaultAgentSkills)

	// Add user-specified skills
	if agentSkills != "" {
		userSkills := strings.Split(agentSkills, ",")
		for _, s := range userSkills {
			s = strings.TrimSpace(s)
			if s != "" && !containsSkill(skills, s) {
				skills = append(skills, s)
			}
		}
	}

	// Build system prompt
	var systemPrompt string
	if agentTask != "" {
		// User provided explicit task
		systemPrompt = agentTask
	} else if agentBootstrap {
		// Generate AI-enhanced bootstrap
		fmt.Println("🤖 Generating AI-enhanced system prompt...")

		cfg, err := config.Load()
		if err == nil {
			apiKey := os.Getenv("ANTHROPIC_API_KEY")
			if apiKey == "" {
				if p, ok := cfg.Provider["anthropic"]; ok {
					apiKey = p.APIKey
				}
			}

			if apiKey != "" {
				prov, err := provider.NewAnthropic(provider.AnthropicConfig{
					APIKey: apiKey,
					Model:  agentModel,
				})
				if err == nil {
					bootstrapCfg := agent.BootstrapConfig{
						Name:        name,
						Description: agentDescription,
						Skills:      skills,
						Tools:       strings.Split(agentTools, ","),
						Model:       agentModel,
					}

					ctx := context.Background()
					if generated, err := agent.GenerateBootstrap(ctx, prov, bootstrapCfg); err == nil {
						systemPrompt = generated
						fmt.Println("✓ Generated enhanced system prompt")
					} else {
						fmt.Printf("⚠ Could not generate AI bootstrap: %v\n", err)
						fmt.Println("  Using default prompt instead")
					}
				}
			}
		}
	}

	// Fallback to default if no prompt generated
	if systemPrompt == "" {
		systemPrompt = agent.DefaultBootstrap(agent.BootstrapConfig{
			Name:        name,
			Description: agentDescription,
			Skills:      skills,
			Tools:       strings.Split(agentTools, ","),
		})
	}

	// Parse triggers
	var triggers []string
	if agentTriggers != "" {
		triggers = strings.Split(agentTriggers, ",")
		for i := range triggers {
			triggers[i] = strings.TrimSpace(triggers[i])
		}
	}

	ab := &cluster.AgentBinding{
		Name:         name,
		Cluster:      clusterName,
		Namespace:    namespace,
		Description:  agentDescription,
		SystemPrompt: systemPrompt,
		Model:        agentModel,
		Tools:        strings.Split(agentTools, ","),
		Skills:       skills,
		Triggers:     triggers,
	}

	if err := store.CreateAgentBinding(ab); err != nil {
		return err
	}

	fmt.Printf("Agent '%s' created in %s/%s\n", name, clusterName, namespace)
	fmt.Printf("  Description: %s\n", agentDescription)
	fmt.Printf("  Model: %s\n", agentModel)
	if len(skills) > 0 {
		fmt.Printf("  Skills: %s\n", strings.Join(skills, ", "))
	}
	if len(triggers) > 0 {
		fmt.Printf("  Triggers: %s\n", strings.Join(triggers, ", "))
	}
	fmt.Println("")
	fmt.Println("The orchestrator will route messages to this agent based on:")
	fmt.Println("  - Manual: /klaw @" + name + " <message>")
	if len(triggers) > 0 {
		fmt.Println("  - Keywords: " + strings.Join(triggers, ", "))
	}

	return nil
}

// --- klaw get agents ---

var getAgentsCmd = &cobra.Command{
	Use:     "agents",
	Aliases: []string{"agent"},
	Short:   "List agents in current namespace",
	RunE:    runGetAgents,
}

func runGetAgents(cmd *cobra.Command, args []string) error {
	store := cluster.NewStore(config.StateDir())
	ctxMgr := cluster.NewContextManager(config.ConfigDir())

	clusterName, namespace, err := ctxMgr.RequireCurrent()
	if err != nil {
		return err
	}

	agents, err := store.ListAgentBindings(clusterName, namespace)
	if err != nil {
		return err
	}

	if len(agents) == 0 {
		fmt.Printf("No agents in %s/%s.\n", clusterName, namespace)
		fmt.Println("Create one with: klaw create agent <name> --description \"...\"")
		return nil
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(agents)
	}

	fmt.Printf("Agents in %s/%s:\n\n", clusterName, namespace)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tMODEL\tDESCRIPTION\tTRIGGERS")
	for _, ag := range agents {
		desc := truncateStr(ag.Description, 30)
		triggers := ""
		if len(ag.Triggers) > 0 {
			triggers = strings.Join(ag.Triggers, ",")
			if len(triggers) > 20 {
				triggers = triggers[:17] + "..."
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", ag.Name, ag.Model, desc, triggers)
	}
	return w.Flush()
}

// --- klaw delete agent ---

var deleteAgentCmd = &cobra.Command{
	Use:   "agent <name>",
	Short: "Delete an agent from current namespace",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		store := cluster.NewStore(config.StateDir())
		ctxMgr := cluster.NewContextManager(config.ConfigDir())

		clusterName, namespace, err := ctxMgr.RequireCurrent()
		if err != nil {
			return err
		}

		if err := store.DeleteAgentBinding(clusterName, namespace, name); err != nil {
			return err
		}

		fmt.Printf("Agent '%s' deleted from %s/%s.\n", name, clusterName, namespace)
		return nil
	},
}

// --- klaw describe agent ---

var describeAgentCmd = &cobra.Command{
	Use:   "agent <name>",
	Short: "Show agent details",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		store := cluster.NewStore(config.StateDir())
		ctxMgr := cluster.NewContextManager(config.ConfigDir())

		clusterName, namespace, err := ctxMgr.RequireCurrent()
		if err != nil {
			return err
		}

		ag, err := store.GetAgentBinding(clusterName, namespace, name)
		if err != nil {
			return err
		}

		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(ag)
		}

		fmt.Printf("Name:        %s\n", ag.Name)
		fmt.Printf("Cluster:     %s\n", ag.Cluster)
		fmt.Printf("Namespace:   %s\n", ag.Namespace)
		fmt.Printf("Description: %s\n", ag.Description)
		fmt.Printf("Model:       %s\n", ag.Model)
		fmt.Printf("Tools:       %s\n", strings.Join(ag.Tools, ", "))
		if len(ag.Triggers) > 0 {
			fmt.Printf("Triggers:    %s\n", strings.Join(ag.Triggers, ", "))
		}
		fmt.Printf("Created:     %s\n", ag.CreatedAt.Format(time.RFC3339))
		fmt.Println("---")
		fmt.Printf("System Prompt:\n%s\n", ag.SystemPrompt)

		return nil
	},
}

// --- klaw worker (internal, runs inside container) ---

var workerCmd = &cobra.Command{
	Use:    "worker",
	Hidden: true,
	Short:  "Run as agent worker (internal)",
	RunE:   runWorker,
}

var workerTask string
var workerModel string

func init() {
	workerCmd.Flags().StringVar(&workerTask, "task", "", "Task to execute")
	workerCmd.Flags().StringVar(&workerModel, "model", "", "Model to use")
}

func runWorker(cmd *cobra.Command, args []string) error {
	// This runs inside the container
	fmt.Println("╭─────────────────────────────────────────╮")
	fmt.Println("│           klaw agent worker             │")
	fmt.Println("╰─────────────────────────────────────────╯")
	fmt.Printf("Model: %s\n", workerModel)
	fmt.Printf("Task: %s\n", workerTask)
	fmt.Println("---")

	// Get API key from environment
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	// Create provider
	prov, err := provider.NewAnthropic(provider.AnthropicConfig{
		APIKey: apiKey,
		Model:  workerModel,
	})
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	// Create tools
	workdir := "/workspace"
	tools := tool.NewRegistry()
	tools.Register(tool.NewBash(workdir))
	tools.Register(tool.NewRead(workdir))
	tools.Register(tool.NewWrite(workdir))
	tools.Register(tool.NewEdit(workdir))
	tools.Register(tool.NewGlob(workdir))
	tools.Register(tool.NewGrep(workdir))

	// Create a task-based channel that auto-sends the task
	taskChan := channel.NewTaskChannel(workerTask)

	// Create agent
	ag := agent.New(agent.Config{
		Provider: prov,
		Channel:  taskChan,
		Tools:    tools,
		SystemPrompt: `You are klaw, an AI employee running inside an isolated container.
You have access to the /workspace directory which is mounted from the host.

Your job is to complete the assigned task efficiently:
- Take action immediately
- Use tools to read, write, and execute commands
- Report results concisely when done
- You work for the user - treat tasks as work assignments to be completed.`,
		MaxTokens: 8192,
	})

	// Run agent
	ctx := context.Background()
	return ag.Run(ctx)
}

// --- Helpers ---

func agentsDir() string {
	return filepath.Join(config.StateDir(), "agents")
}

func truncateStr(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

func containsSkill(skills []string, skill string) bool {
	for _, s := range skills {
		if s == skill {
			return true
		}
	}
	return false
}
