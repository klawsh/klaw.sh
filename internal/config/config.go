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
	Defaults  DefaultsConfig            `toml:"defaults"`
	Workspace WorkspaceConfig           `toml:"workspace"`
	Provider  map[string]ProviderConfig `toml:"provider"`
	API       APIConfig                 `toml:"api"`
	Logging   LoggingConfig             `toml:"logging"`
}

// DefaultsConfig holds default settings.
type DefaultsConfig struct {
	Model          string  `toml:"model"`
	MaxSessionCost float64 `toml:"max_session_cost"`
}

// WorkspaceConfig holds workspace settings.
type WorkspaceConfig struct {
	Path string `toml:"path"`
}

// ProviderConfig holds LLM provider settings.
type ProviderConfig struct {
	APIKey     string `toml:"api_key"`
	BaseURL    string `toml:"base_url"`
	Model      string `toml:"model"`
	MaxRetries int    `toml:"max_retries"`
	Fallback   string `toml:"fallback"`
}

// APIConfig holds Creative Agent API settings.
type APIConfig struct {
	Port       int `toml:"port"`
	Host       string `toml:"host"`
	Workers    int `toml:"workers"`
	MaxTimeout int `toml:"max_timeout"` // seconds
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level string `toml:"level"`
	File  string `toml:"file"`
}

// Load reads configuration from file and environment.
func Load() (*Config, error) {
	cfg := defaultConfig()

	configPath := ConfigPath()
	if _, err := os.Stat(configPath); err == nil {
		if _, err := toml.DecodeFile(configPath, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config: %w", err)
		}
	}

	cfg.applyEnv()
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

// ConfigDir returns the klaw config directory.
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
		},
		Workspace: WorkspaceConfig{},
		Provider:  make(map[string]ProviderConfig),
		API: APIConfig{
			Port:       8081,
			Host:       "0.0.0.0",
			Workers:    50,
			MaxTimeout: 600,
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}
}

func (c *Config) applyEnv() {
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		p := c.Provider["anthropic"]
		p.APIKey = key
		c.Provider["anthropic"] = p
	}

	if key := os.Getenv("EACHLABS_API_KEY"); key != "" {
		p := c.Provider["eachlabs"]
		p.APIKey = key
		c.Provider["eachlabs"] = p
	}

	if key := os.Getenv("OPENROUTER_API_KEY"); key != "" {
		p := c.Provider["openrouter"]
		p.APIKey = key
		c.Provider["openrouter"] = p
	}

	if model := os.Getenv("KLAW_MODEL"); model != "" {
		c.Defaults.Model = model
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

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return err
	}

	f, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	encoder := toml.NewEncoder(f)
	return encoder.Encode(c)
}

// EnsureDirs creates necessary directories.
func EnsureDirs() error {
	dirs := []string{StateDir(), SessionsDir(), LogsDir()}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", dir, err)
		}
	}

	return nil
}
