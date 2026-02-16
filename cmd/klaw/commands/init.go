package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/eachlabs/klaw/internal/config"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize klaw workspace",
	Long: `Initialize a new klaw workspace with default files.

Creates:
  ~/.klaw/config.toml      Configuration file
  ~/.klaw/workspace/       Workspace directory
    SOUL.md               Agent personality
    AGENTS.md             Agent behavior rules
    TOOLS.md              Tool documentation`,
	RunE: runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	// Create directories
	if err := config.EnsureDirs(); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	wsDir := cfg.WorkspaceDir()
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		return fmt.Errorf("failed to create workspace: %w", err)
	}

	// Create workspace files if they don't exist
	files := map[string]string{
		"SOUL.md":   defaultSoulMD,
		"AGENTS.md": defaultAgentsMD,
		"TOOLS.md":  defaultToolsMD,
	}

	for name, content := range files {
		path := filepath.Join(wsDir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return fmt.Errorf("failed to create %s: %w", name, err)
			}
			fmt.Printf("Created %s\n", path)
		} else {
			fmt.Printf("Exists: %s\n", path)
		}
	}

	// Create memory directory
	memDir := filepath.Join(wsDir, "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		return fmt.Errorf("failed to create memory dir: %w", err)
	}

	// Save config
	if err := cfg.Save(); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}
	fmt.Printf("Config: %s\n", config.ConfigPath())

	fmt.Println("\nklaw initialized!")
	fmt.Println("\nNext steps:")
	fmt.Println("  1. Set your API key:")
	fmt.Println("     export ANTHROPIC_API_KEY=sk-ant-...")
	fmt.Println("  2. Start chatting:")
	fmt.Println("     klaw chat")

	return nil
}

const defaultSoulMD = `# SOUL.md — klaw

You are klaw, a helpful AI coding assistant.

## Personality

- Direct and helpful, not performatively enthusiastic
- Concise by default, thorough when needed
- Opinionated when asked, with reasoning
- Honest about limitations

## Guidelines

- Read files before modifying them
- Use edit for targeted changes, write for new files
- Run tests after making changes
- Be concise but thorough
`

const defaultAgentsMD = `# AGENTS.md

## Default Agent

The default agent is a general-purpose coding assistant.

### Capabilities
- Read, write, and edit files
- Run shell commands
- Search code with glob and grep
- Answer questions about code

### Guidelines
- Understand existing code before making changes
- Make minimal, focused changes
- Test changes when possible
- Explain your reasoning
`

const defaultToolsMD = `# TOOLS.md

## Available Tools

### bash
Execute shell commands. Use for:
- Git operations
- Running tests
- Package management
- System commands

### read
Read file contents with line numbers.

### write
Write content to a file. Creates parent directories.

### edit
Edit files with exact string replacement.
The old_string must match exactly.

### glob
Find files matching a pattern.
Supports ** for recursive matching.

### grep
Search for patterns in files.
Returns matching lines with context.
`
