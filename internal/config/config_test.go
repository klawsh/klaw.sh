package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()

	if cfg.Defaults.Model != "claude-sonnet-4-20250514" {
		t.Errorf("default model = %q, want claude-sonnet-4-20250514", cfg.Defaults.Model)
	}
	if cfg.Defaults.Agent != "default" {
		t.Errorf("default agent = %q, want 'default'", cfg.Defaults.Agent)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("default port = %d, want 8080", cfg.Server.Port)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("default host = %q, want '127.0.0.1'", cfg.Server.Host)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("default log level = %q, want 'info'", cfg.Logging.Level)
	}
	if cfg.Provider == nil {
		t.Error("Provider map should be initialized")
	}
	if cfg.Channel == nil {
		t.Error("Channel map should be initialized")
	}
	if cfg.Agents == nil {
		t.Error("Agents map should be initialized")
	}
}

func TestLoadFromTOML(t *testing.T) {
	// Clear env vars that applyEnv() would pick up
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("KLAW_MODEL", "")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("DISCORD_BOT_TOKEN", "")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")

	content := `
[defaults]
model = "test-model"
agent = "coder"
max_session_cost = 5.50

[provider.anthropic]
api_key = "sk-test"
model = "claude-opus-4-20250514"
max_retries = 5
fallback = "eachlabs"

[agent.researcher]
tools = ["read", "grep", "web_fetch"]
max_iterations = 30
require_approval = ["bash"]
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	t.Setenv("KLAW_CONFIG", configPath)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if cfg.Defaults.Model != "test-model" {
		t.Errorf("model = %q, want 'test-model'", cfg.Defaults.Model)
	}
	if cfg.Defaults.Agent != "coder" {
		t.Errorf("agent = %q, want 'coder'", cfg.Defaults.Agent)
	}
	if cfg.Defaults.MaxSessionCost != 5.50 {
		t.Errorf("max_session_cost = %f, want 5.50", cfg.Defaults.MaxSessionCost)
	}

	anthCfg, ok := cfg.Provider["anthropic"]
	if !ok {
		t.Fatal("anthropic provider not found")
	}
	if anthCfg.APIKey != "sk-test" {
		t.Errorf("api_key = %q, want 'sk-test'", anthCfg.APIKey)
	}
	if anthCfg.MaxRetries != 5 {
		t.Errorf("max_retries = %d, want 5", anthCfg.MaxRetries)
	}
	if anthCfg.Fallback != "eachlabs" {
		t.Errorf("fallback = %q, want 'eachlabs'", anthCfg.Fallback)
	}

	agentCfg, ok := cfg.Agents["researcher"]
	if !ok {
		t.Fatal("researcher agent not found")
	}
	if len(agentCfg.Tools) != 3 {
		t.Errorf("expected 3 tools, got %d", len(agentCfg.Tools))
	}
	if agentCfg.MaxIterations != 30 {
		t.Errorf("max_iterations = %d, want 30", agentCfg.MaxIterations)
	}
	if len(agentCfg.RequireApproval) != 1 || agentCfg.RequireApproval[0] != "bash" {
		t.Errorf("unexpected require_approval: %v", agentCfg.RequireApproval)
	}
}

func TestApplyEnv(t *testing.T) {
	cfg := defaultConfig()

	t.Setenv("ANTHROPIC_API_KEY", "sk-env-test")
	t.Setenv("KLAW_MODEL", "env-model")

	cfg.applyEnv()

	anthCfg, ok := cfg.Provider["anthropic"]
	if !ok {
		t.Fatal("anthropic provider not created from env")
	}
	if anthCfg.APIKey != "sk-env-test" {
		t.Errorf("api_key = %q, want 'sk-env-test'", anthCfg.APIKey)
	}
	if cfg.Defaults.Model != "env-model" {
		t.Errorf("model = %q, want 'env-model'", cfg.Defaults.Model)
	}
}

func TestApplyEnv_Telegram(t *testing.T) {
	cfg := defaultConfig()
	t.Setenv("TELEGRAM_BOT_TOKEN", "tg-token")

	cfg.applyEnv()

	ch, ok := cfg.Channel["telegram"]
	if !ok {
		t.Fatal("telegram channel not created")
	}
	if ch.Token != "tg-token" {
		t.Errorf("token = %q, want 'tg-token'", ch.Token)
	}
	if !ch.Enabled {
		t.Error("telegram should be enabled")
	}
}

func TestApplyEnv_Discord(t *testing.T) {
	cfg := defaultConfig()
	t.Setenv("DISCORD_BOT_TOKEN", "dc-token")

	cfg.applyEnv()

	ch, ok := cfg.Channel["discord"]
	if !ok {
		t.Fatal("discord channel not created")
	}
	if ch.Token != "dc-token" {
		t.Errorf("token = %q, want 'dc-token'", ch.Token)
	}
}

func TestExpandPaths(t *testing.T) {
	home, _ := os.UserHomeDir()
	cfg := defaultConfig()

	cfg.Workspace.Path = "~/workspace"
	cfg.Logging.File = "$HOME/logs/klaw.log"

	cfg.expandPaths()

	expectedWS := filepath.Join(home, "workspace")
	if cfg.Workspace.Path != expectedWS {
		t.Errorf("workspace = %q, want %q", cfg.Workspace.Path, expectedWS)
	}

	expectedLog := filepath.Join(home, "logs/klaw.log")
	if cfg.Logging.File != expectedLog {
		t.Errorf("log file = %q, want %q", cfg.Logging.File, expectedLog)
	}
}

func TestExpandPaths_NoExpansion(t *testing.T) {
	cfg := defaultConfig()
	cfg.Workspace.Path = "/absolute/path"

	cfg.expandPaths()

	if cfg.Workspace.Path != "/absolute/path" {
		t.Errorf("absolute path should not change: %q", cfg.Workspace.Path)
	}
}

func TestSaveAndReload(t *testing.T) {
	// Clear env vars that applyEnv() would pick up
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("KLAW_MODEL", "")
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("DISCORD_BOT_TOKEN", "")

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	t.Setenv("KLAW_CONFIG", configPath)

	cfg := defaultConfig()
	cfg.Defaults.Model = "save-test-model"
	cfg.Provider["anthropic"] = ProviderConfig{APIKey: "sk-save"}
	cfg.Agents["test"] = AgentInstanceConfig{
		Tools:         []string{"bash", "read"},
		MaxIterations: 25,
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	// Reload
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if loaded.Defaults.Model != "save-test-model" {
		t.Errorf("reloaded model = %q, want 'save-test-model'", loaded.Defaults.Model)
	}
	if loaded.Provider["anthropic"].APIKey != "sk-save" {
		t.Errorf("reloaded api_key = %q", loaded.Provider["anthropic"].APIKey)
	}
	agCfg, ok := loaded.Agents["test"]
	if !ok {
		t.Fatal("reloaded agent 'test' not found")
	}
	if agCfg.MaxIterations != 25 {
		t.Errorf("reloaded max_iterations = %d, want 25", agCfg.MaxIterations)
	}
}

func TestEnsureDirs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KLAW_STATE_DIR", dir)

	if err := EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs error: %v", err)
	}

	// Verify directories were created
	dirs := []string{
		StateDir(),
		SessionsDir(),
		LogsDir(),
	}
	for _, d := range dirs {
		if _, err := os.Stat(d); os.IsNotExist(err) {
			t.Errorf("directory not created: %s", d)
		}
	}
}

func TestWorkspaceDir(t *testing.T) {
	t.Run("custom path", func(t *testing.T) {
		cfg := defaultConfig()
		cfg.Workspace.Path = "/custom/workspace"
		if cfg.WorkspaceDir() != "/custom/workspace" {
			t.Errorf("unexpected: %q", cfg.WorkspaceDir())
		}
	})

	t.Run("default path", func(t *testing.T) {
		cfg := defaultConfig()
		ws := cfg.WorkspaceDir()
		if ws == "" {
			t.Error("workspace dir should not be empty")
		}
	})
}

func TestConfigPath_EnvOverride(t *testing.T) {
	t.Setenv("KLAW_CONFIG", "/custom/config.toml")
	if ConfigPath() != "/custom/config.toml" {
		t.Errorf("ConfigPath = %q", ConfigPath())
	}
}

func TestStateDir_EnvOverride(t *testing.T) {
	t.Setenv("KLAW_STATE_DIR", "/custom/state")
	if StateDir() != "/custom/state" {
		t.Errorf("StateDir = %q", StateDir())
	}
}

func TestLoad_MissingFile(t *testing.T) {
	t.Setenv("KLAW_CONFIG", "/nonexistent/config.toml")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load should not error on missing file: %v", err)
	}
	// Should return defaults
	if cfg.Defaults.Model != "claude-sonnet-4-20250514" {
		t.Errorf("expected default model on missing file, got %q", cfg.Defaults.Model)
	}
}

func TestLoad_InvalidTOML(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	_ = os.WriteFile(configPath, []byte("invalid [[[toml"), 0644)
	t.Setenv("KLAW_CONFIG", configPath)

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}
