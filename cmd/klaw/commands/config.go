package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/eachlabs/klaw/internal/cluster"
	"github.com/eachlabs/klaw/internal/config"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration",
	Long: `Manage klaw configuration.

Subcommands:
  get [key]              Show configuration value(s)
  set <key> <value>      Set a configuration value
  edit                   Open config in $EDITOR
  path                   Show config file path
  use-cluster <name>     Switch to a cluster
  use-namespace <name>   Switch to a namespace
  current-context        Show current cluster/namespace`,
}

func init() {
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configEditCmd)
	configCmd.AddCommand(configPathCmd)
	configCmd.AddCommand(useClusterCmd)
	configCmd.AddCommand(useNamespaceCmd)
	configCmd.AddCommand(currentContextCmd)
}

var configGetCmd = &cobra.Command{
	Use:   "get [key]",
	Short: "Show configuration",
	Long: `Show configuration values.

Examples:
  klaw config get                    # Show all config
  klaw config get provider.anthropic.api_key
  klaw config get defaults.model`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		if len(args) == 0 {
			// Show all config
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(cfg)
			}

			enc := toml.NewEncoder(os.Stdout)
			return enc.Encode(cfg)
		}

		// Get specific key
		key := args[0]
		value := getConfigValue(cfg, key)
		if value == nil {
			return fmt.Errorf("key not found: %s", key)
		}

		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			return enc.Encode(value)
		}

		fmt.Printf("%v\n", value)
		return nil
	},
}

func getConfigValue(cfg *config.Config, key string) interface{} {
	parts := strings.Split(key, ".")

	switch parts[0] {
	case "defaults":
		if len(parts) == 1 {
			return cfg.Defaults
		}
		switch parts[1] {
		case "model":
			return cfg.Defaults.Model
		case "agent":
			return cfg.Defaults.Agent
		}

	case "workspace":
		if len(parts) == 1 {
			return cfg.Workspace
		}
		switch parts[1] {
		case "path":
			return cfg.Workspace.Path
		}

	case "provider":
		if len(parts) == 1 {
			return cfg.Provider
		}
		if len(parts) >= 2 {
			p, ok := cfg.Provider[parts[1]]
			if !ok {
				return nil
			}
			if len(parts) == 2 {
				return p
			}
			switch parts[2] {
			case "api_key":
				return maskToken(p.APIKey)
			case "base_url":
				return p.BaseURL
			case "model":
				return p.Model
			}
		}

	case "channel":
		if len(parts) == 1 {
			return cfg.Channel
		}
		if len(parts) >= 2 {
			ch, ok := cfg.Channel[parts[1]]
			if !ok {
				return nil
			}
			if len(parts) == 2 {
				return ch
			}
			switch parts[2] {
			case "enabled":
				return ch.Enabled
			case "token":
				return maskToken(ch.Token)
			}
		}

	case "server":
		if len(parts) == 1 {
			return cfg.Server
		}
		switch parts[1] {
		case "port":
			return cfg.Server.Port
		case "host":
			return cfg.Server.Host
		}

	case "logging":
		if len(parts) == 1 {
			return cfg.Logging
		}
		switch parts[1] {
		case "level":
			return cfg.Logging.Level
		case "file":
			return cfg.Logging.File
		}
	}

	return nil
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Long: `Set a configuration value.

Examples:
  klaw config set defaults.model claude-opus-4-20250514
  klaw config set provider.anthropic.api_key sk-ant-...
  klaw config set server.port 9090`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		value := args[1]

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		if err := setConfigValue(cfg, key, value); err != nil {
			return err
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Set %s = %s\n", key, value)
		return nil
	},
}

func setConfigValue(cfg *config.Config, key, value string) error {
	parts := strings.Split(key, ".")

	switch parts[0] {
	case "defaults":
		if len(parts) != 2 {
			return fmt.Errorf("invalid key: %s", key)
		}
		switch parts[1] {
		case "model":
			cfg.Defaults.Model = value
		case "agent":
			cfg.Defaults.Agent = value
		default:
			return fmt.Errorf("unknown key: %s", key)
		}

	case "workspace":
		if len(parts) != 2 || parts[1] != "path" {
			return fmt.Errorf("invalid key: %s", key)
		}
		cfg.Workspace.Path = value

	case "provider":
		if len(parts) != 3 {
			return fmt.Errorf("invalid key: %s (use provider.<name>.<field>)", key)
		}
		p := cfg.Provider[parts[1]]
		switch parts[2] {
		case "api_key":
			p.APIKey = value
		case "base_url":
			p.BaseURL = value
		case "model":
			p.Model = value
		default:
			return fmt.Errorf("unknown field: %s", parts[2])
		}
		cfg.Provider[parts[1]] = p

	case "channel":
		if len(parts) != 3 {
			return fmt.Errorf("invalid key: %s (use channel.<name>.<field>)", key)
		}
		ch := cfg.Channel[parts[1]]
		switch parts[2] {
		case "enabled":
			ch.Enabled = value == "true"
		case "token":
			ch.Token = value
		case "guild_id":
			ch.GuildID = value
		default:
			return fmt.Errorf("unknown field: %s", parts[2])
		}
		cfg.Channel[parts[1]] = ch

	case "server":
		if len(parts) != 2 {
			return fmt.Errorf("invalid key: %s", key)
		}
		switch parts[1] {
		case "port":
			var port int
			fmt.Sscanf(value, "%d", &port)
			cfg.Server.Port = port
		case "host":
			cfg.Server.Host = value
		default:
			return fmt.Errorf("unknown field: %s", parts[1])
		}

	case "logging":
		if len(parts) != 2 {
			return fmt.Errorf("invalid key: %s", key)
		}
		switch parts[1] {
		case "level":
			cfg.Logging.Level = value
		case "file":
			cfg.Logging.File = value
		default:
			return fmt.Errorf("unknown field: %s", parts[1])
		}

	default:
		return fmt.Errorf("unknown section: %s", parts[0])
	}

	return nil
}

var configEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open config in editor",
	RunE: func(cmd *cobra.Command, args []string) error {
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vim"
		}

		configPath := config.ConfigPath()

		// Ensure config exists
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if err := cfg.Save(); err != nil {
			return err
		}

		c := exec.Command(editor, configPath)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr

		return c.Run()
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Show config file path",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(config.ConfigPath())
	},
}

// --- Context switching commands ---

var useClusterCmd = &cobra.Command{
	Use:   "use-cluster <name>",
	Short: "Switch to a cluster",
	Long: `Switch the current context to a cluster.

Examples:
  klaw config use-cluster acme-corp`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		store := cluster.NewStore(config.StateDir())
		ctxMgr := cluster.NewContextManager(config.ConfigDir())

		// Verify cluster exists
		if !store.ClusterExists(name) {
			return fmt.Errorf("cluster not found: %s", name)
		}

		if err := ctxMgr.SetCluster(name); err != nil {
			return err
		}

		fmt.Printf("Switched to cluster '%s' (namespace: default)\n", name)
		return nil
	},
}

var useNamespaceCmd = &cobra.Command{
	Use:     "use-namespace <name>",
	Aliases: []string{"use-ns"},
	Short:   "Switch to a namespace",
	Long: `Switch the current namespace within the current cluster.

Examples:
  klaw config use-namespace marketing
  klaw config use-ns sales`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		store := cluster.NewStore(config.StateDir())
		ctxMgr := cluster.NewContextManager(config.ConfigDir())

		// Get current cluster
		clusterName, _, err := ctxMgr.RequireCurrent()
		if err != nil {
			return err
		}

		// Verify namespace exists
		if !store.NamespaceExists(clusterName, name) {
			return fmt.Errorf("namespace not found: %s/%s", clusterName, name)
		}

		if err := ctxMgr.SetNamespace(name); err != nil {
			return err
		}

		fmt.Printf("Switched to namespace '%s' in cluster '%s'\n", name, clusterName)
		return nil
	},
}

var currentContextCmd = &cobra.Command{
	Use:     "current-context",
	Aliases: []string{"ctx"},
	Short:   "Show current cluster/namespace",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctxMgr := cluster.NewContextManager(config.ConfigDir())

		clusterName, namespace, err := ctxMgr.GetCurrent()
		if err != nil {
			return err
		}

		if clusterName == "" {
			fmt.Println("No cluster selected.")
			fmt.Println("Create one with: klaw create cluster <name>")
			return nil
		}

		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]string{
				"cluster":   clusterName,
				"namespace": namespace,
			})
		}

		fmt.Printf("Cluster:   %s\n", clusterName)
		fmt.Printf("Namespace: %s\n", namespace)
		return nil
	},
}
