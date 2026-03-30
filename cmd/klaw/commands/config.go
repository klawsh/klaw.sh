package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/BurntSushi/toml"
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
  path                   Show config file path`,
}

func init() {
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configEditCmd)
	configCmd.AddCommand(configPathCmd)
}

var configGetCmd = &cobra.Command{
	Use:   "get [key]",
	Short: "Show configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		if len(args) == 0 {
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(cfg)
			}
			enc := toml.NewEncoder(os.Stdout)
			return enc.Encode(cfg)
		}

		key := args[0]
		value := getConfigValue(cfg, key)
		if value == nil {
			return fmt.Errorf("key not found: %s", key)
		}

		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(value)
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
	case "api":
		if len(parts) == 1 {
			return cfg.API
		}
		switch parts[1] {
		case "port":
			return cfg.API.Port
		case "host":
			return cfg.API.Host
		case "workers":
			return cfg.API.Workers
		case "max_timeout":
			return cfg.API.MaxTimeout
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

func maskToken(token string) string {
	if len(token) <= 8 {
		return "***"
	}
	return token[:8] + "..."
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Args:  cobra.ExactArgs(2),
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
		default:
			return fmt.Errorf("unknown key: %s", key)
		}
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
	case "api":
		if len(parts) != 2 {
			return fmt.Errorf("invalid key: %s", key)
		}
		switch parts[1] {
		case "port":
			var port int
			_, _ = fmt.Sscanf(value, "%d", &port)
			cfg.API.Port = port
		case "host":
			cfg.API.Host = value
		case "workers":
			var w int
			_, _ = fmt.Sscanf(value, "%d", &w)
			cfg.API.Workers = w
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
