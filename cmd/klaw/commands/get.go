package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/runtime"
	"github.com/spf13/cobra"
)

var getCmd = &cobra.Command{
	Use:     "get <resource>",
	Aliases: []string{"list", "ls"},
	Short:   "List resources",
	Long: `List resources of a given type.

Resources:
  agents           Configured agents
  servers, srv     Running server instances
  sessions, sess   Chat sessions
  models           Available models
  channels, ch     Configured channels
  memory, mem      Memory files
  tools            Available tools

Examples:
  klaw get agents
  klaw list agents
  klaw ls agents`,
}

func init() {
	getCmd.AddCommand(getServersCmd)
	getCmd.AddCommand(getSessionsCmd)
	getCmd.AddCommand(getModelsCmd)
	getCmd.AddCommand(getChannelsCmd)
	getCmd.AddCommand(getMemoryCmd)
	getCmd.AddCommand(getToolsCmd)
}

var getServersCmd = &cobra.Command{
	Use:     "servers",
	Aliases: []string{"srv", "server", "containers"},
	Short:   "List running agent containers",
	RunE: func(cmd *cobra.Command, args []string) error {
		rt := runtime.NewPodmanRuntime(config.StateDir())
		containers, err := rt.List()
		if err != nil {
			return err
		}

		if len(containers) == 0 {
			fmt.Println("No containers running.")
			fmt.Println("Start an agent with: klaw run <agent-name>")
			return nil
		}

		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(containers)
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

var getSessionsCmd = &cobra.Command{
	Use:     "sessions",
	Aliases: []string{"sess", "session"},
	Short:   "List chat sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		sessDir := config.SessionsDir()
		entries, err := os.ReadDir(sessDir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No sessions found.")
				return nil
			}
			return err
		}

		var sessions []sessionInfo
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			info, _ := e.Info()
			sessions = append(sessions, sessionInfo{
				ID:      strings.TrimSuffix(e.Name(), ".json"),
				ModTime: info.ModTime(),
			})
		}

		if len(sessions) == 0 {
			fmt.Println("No sessions found.")
			return nil
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(sessions)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tLAST MODIFIED")
		for _, s := range sessions {
			fmt.Fprintf(w, "%s\t%s\n", s.ID, s.ModTime.Format(time.RFC3339))
		}
		return w.Flush()
	},
}

type sessionInfo struct {
	ID      string    `json:"id"`
	ModTime time.Time `json:"modified"`
}

var getModelsCmd = &cobra.Command{
	Use:     "models",
	Aliases: []string{"model"},
	Short:   "List available models",
	RunE: func(cmd *cobra.Command, args []string) error {
		models := []modelInfo{
			{ID: "claude-sonnet-4-20250514", Provider: "anthropic", Description: "Fast, intelligent"},
			{ID: "claude-opus-4-20250514", Provider: "anthropic", Description: "Most capable"},
			{ID: "claude-3-5-sonnet-20241022", Provider: "anthropic", Description: "Previous Sonnet"},
			{ID: "claude-3-5-haiku-20241022", Provider: "anthropic", Description: "Fast, efficient"},
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(models)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "MODEL\tPROVIDER\tDESCRIPTION")
		for _, m := range models {
			fmt.Fprintf(w, "%s\t%s\t%s\n", m.ID, m.Provider, m.Description)
		}
		return w.Flush()
	},
}

type modelInfo struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	Description string `json:"description"`
}

var getChannelsCmd = &cobra.Command{
	Use:     "channels",
	Aliases: []string{"ch", "channel"},
	Short:   "List channels in current namespace",
	RunE: func(cmd *cobra.Command, args []string) error {
		store := cluster.NewStore(config.StateDir())
		ctxMgr := cluster.NewContextManager(config.ConfigDir())

		clusterName, namespace, err := ctxMgr.GetCurrent()
		if err != nil {
			return err
		}

		// If no cluster selected, show legacy channels from config
		if clusterName == "" {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			channels := []channelInfo{
				{Name: "terminal", Status: "active", Description: "Interactive terminal"},
			}

			for name, ch := range cfg.Channel {
				status := "disabled"
				if ch.Enabled {
					status = "configured"
				}
				channels = append(channels, channelInfo{
					Name:        name,
					Status:      status,
					Description: fmt.Sprintf("Token: %s", maskToken(ch.Token)),
				})
			}

			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(channels)
			}

			fmt.Println("No cluster selected. Showing legacy channels from config.")
			fmt.Println("Create a cluster with: klaw create cluster <name>")
			fmt.Println("")

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "CHANNEL\tSTATUS\tDESCRIPTION")
			for _, c := range channels {
				fmt.Fprintf(w, "%s\t%s\t%s\n", c.Name, c.Status, c.Description)
			}
			return w.Flush()
		}

		// Show cluster-aware channels
		bindings, err := store.ListChannelBindings(clusterName, namespace)
		if err != nil {
			return err
		}

		if len(bindings) == 0 {
			fmt.Printf("No channels in %s/%s.\n", clusterName, namespace)
			fmt.Println("Create one with: klaw create channel <type> --name <name>")
			return nil
		}

		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(bindings)
		}

		fmt.Printf("Channels in %s/%s:\n\n", clusterName, namespace)

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tTYPE\tSTATUS\tCREATED")
		for _, ch := range bindings {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				ch.Name, ch.Type, ch.Status, ch.CreatedAt.Format("2006-01-02 15:04"))
		}
		return w.Flush()
	},
}

type channelInfo struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

func maskToken(token string) string {
	if token == "" {
		return "(not set)"
	}
	if len(token) < 8 {
		return "***"
	}
	return token[:4] + "..." + token[len(token)-4:]
}

var getMemoryCmd = &cobra.Command{
	Use:     "memory",
	Aliases: []string{"mem"},
	Short:   "List memory files",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		memDir := filepath.Join(cfg.WorkspaceDir(), "memory")
		entries, err := os.ReadDir(memDir)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("No memory files found.")
				return nil
			}
			return err
		}

		var files []memoryInfo
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			info, _ := e.Info()
			files = append(files, memoryInfo{
				Name: e.Name(),
				Size: info.Size(),
			})
		}

		if len(files) == 0 {
			fmt.Println("No memory files found.")
			return nil
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(files)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "FILE\tSIZE")
		for _, f := range files {
			fmt.Fprintf(w, "%s\t%d bytes\n", f.Name, f.Size)
		}
		return w.Flush()
	},
}

type memoryInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

var getToolsCmd = &cobra.Command{
	Use:     "tools",
	Aliases: []string{"tool"},
	Short:   "List available tools",
	RunE: func(cmd *cobra.Command, args []string) error {
		tools := []toolInfo{
			{Name: "bash", Description: "Execute shell commands"},
			{Name: "read", Description: "Read file contents"},
			{Name: "write", Description: "Write file contents"},
			{Name: "edit", Description: "Edit files with string replacement"},
			{Name: "glob", Description: "Find files by pattern"},
			{Name: "grep", Description: "Search file contents"},
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(tools)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TOOL\tDESCRIPTION")
		for _, t := range tools {
			fmt.Fprintf(w, "%s\t%s\n", t.Name, t.Description)
		}
		return w.Flush()
	},
}

type toolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
