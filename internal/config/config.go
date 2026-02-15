// Package config handles configuration loading and management.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config represents the klaw configuration.
type Config struct {
	Defaults     DefaultsConfig            `toml:"defaults"`
	Workspace    WorkspaceConfig           `toml:"workspace"`
	Provider     map[string]ProviderConfig `toml:"provider"`
	Channel      map[string]ChannelConfig  `toml:"channel"`
	Server       ServerConfig              `toml:"server"`
	Controller   *ControllerConfig         `toml:"controller"`
	Logging      LoggingConfig             `toml:"logging"`
	SkillsAPIKey string                    `toml:"skills_api_key"`
}

// ControllerConfig holds controller connection settings.
type ControllerConfig struct {
	Address string `toml:"address"`
	Token   string `toml:"token"`
}

// DefaultsConfig holds default settings.
type DefaultsConfig struct {
	Model string `toml:"model"`
	Agent string `toml:"agent"`
}

// WorkspaceConfig holds workspace settings.
type WorkspaceConfig struct {
	Path string `toml:"path"`
}

// ProviderConfig holds LLM provider settings.
type ProviderConfig struct {
	APIKey  string `toml:"api_key"`
	BaseURL string `toml:"base_url"`
	Model   string `toml:"model"`
}

// ChannelConfig holds channel settings.
type ChannelConfig struct {
	Enabled bool   `toml:"enabled"`
	Token   string `toml:"token"`
	GuildID string `toml:"guild_id"` // Discord
}

// ServerConfig holds server settings.
type ServerConfig struct {
	Port int    `toml:"port"`
	Host string `toml:"host"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level string `toml:"level"`
	File  string `toml:"file"`
}

// Load reads configuration from file and environment.
func Load() (*Config, error) {
	cfg := defaultConfig()

	// Try to load from file
	configPath := ConfigPath()
	if _, err := os.Stat(configPath); err == nil {
		if _, err := toml.DecodeFile(configPath, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config: %w", err)
		}
	}

	// Override with environment variables
	cfg.applyEnv()

	// Expand paths
	cfg.expandPaths()

	return cfg, nil
}

// ConfigPath returns the path to the config file.
func ConfigPath() string {
	if p := os.Getenv("KLAW_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(StateDir(), "config.toml")
}

// StateDir returns the klaw state directory.
func StateDir() string {
	if p := os.Getenv("KLAW_STATE_DIR"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".klaw")
}

// ConfigDir returns the klaw config directory (same as StateDir for now).
func ConfigDir() string {
	return StateDir()
}

// WorkspaceDir returns the workspace directory.
func (c *Config) WorkspaceDir() string {
	if c.Workspace.Path != "" {
		return c.Workspace.Path
	}
	return filepath.Join(StateDir(), "workspace")
}

// SessionsDir returns the sessions directory.
func SessionsDir() string {
	return filepath.Join(StateDir(), "sessions")
}

// LogsDir returns the logs directory.
func LogsDir() string {
	return filepath.Join(StateDir(), "logs")
}

func defaultConfig() *Config {
	return &Config{
		Defaults: DefaultsConfig{
			Model: "claude-sonnet-4-20250514",
			Agent: "default",
		},
		Workspace: WorkspaceConfig{
			Path: "",
		},
		Provider: make(map[string]ProviderConfig),
		Channel:  make(map[string]ChannelConfig),
		Server: ServerConfig{
			Port: 8080,
			Host: "127.0.0.1",
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}
}

func (c *Config) applyEnv() {
	// Anthropic
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		p := c.Provider["anthropic"]
		p.APIKey = key
		c.Provider["anthropic"] = p
	}

	// OpenAI
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		p := c.Provider["openai"]
		p.APIKey = key
		c.Provider["openai"] = p
	}

	// Telegram
	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		ch := c.Channel["telegram"]
		ch.Token = token
		ch.Enabled = true
		c.Channel["telegram"] = ch
	}

	// Discord
	if token := os.Getenv("DISCORD_BOT_TOKEN"); token != "" {
		ch := c.Channel["discord"]
		ch.Token = token
		ch.Enabled = true
		c.Channel["discord"] = ch
	}

	// Model override
	if model := os.Getenv("KLAW_MODEL"); model != "" {
		c.Defaults.Model = model
	}

	// Skills API key
	if key := os.Getenv("KLAW_SKILLS_API_KEY"); key != "" {
		c.SkillsAPIKey = key
	}
}

func (c *Config) expandPaths() {
	home, _ := os.UserHomeDir()

	expand := func(p string) string {
		if strings.HasPrefix(p, "~/") {
			return filepath.Join(home, p[2:])
		}
		if strings.HasPrefix(p, "$HOME/") {
			return filepath.Join(home, p[6:])
		}
		return p
	}

	c.Workspace.Path = expand(c.Workspace.Path)
	c.Logging.File = expand(c.Logging.File)
}

// Save writes the config to file.
func (c *Config) Save() error {
	configPath := ConfigPath()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return err
	}

	f, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	return encoder.Encode(c)
}

// EnsureDirs creates necessary directories.
func EnsureDirs() error {
	dirs := []string{
		StateDir(),
		SessionsDir(),
		LogsDir(),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}

	return nil
}
