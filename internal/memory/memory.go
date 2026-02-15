// Package memory handles workspace files and daily memory.
package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Memory manages workspace files and daily logs.
type Memory interface {
	// LoadWorkspace loads all workspace files (SOUL.md, AGENTS.md, etc).
	LoadWorkspace(ctx context.Context) (*Workspace, error)

	// SaveDaily appends an entry to today's memory file.
	SaveDaily(ctx context.Context, entry string) error

	// GetDaily returns the content of a specific day's memory.
	GetDaily(ctx context.Context, date time.Time) (string, error)

	// ListDaily returns all daily memory entries.
	ListDaily(ctx context.Context) ([]DailyEntry, error)
}

// Workspace holds loaded workspace files.
type Workspace struct {
	Soul   string // SOUL.md content
	Agents string // AGENTS.md content
	Tools  string // TOOLS.md content
	User   string // USER.md content (optional)
	Memory string // MEMORY.md content (optional)
}

// DailyEntry represents a daily memory file.
type DailyEntry struct {
	Date    time.Time
	Path    string
	Content string
}

// FileMemory implements Memory using filesystem.
type FileMemory struct {
	workspaceDir string
}

// NewFileMemory creates a new filesystem-based memory.
func NewFileMemory(workspaceDir string) *FileMemory {
	return &FileMemory{workspaceDir: workspaceDir}
}

func (m *FileMemory) LoadWorkspace(ctx context.Context) (*Workspace, error) {
	ws := &Workspace{}

	// Required files
	soul, err := m.readFile("SOUL.md")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read SOUL.md: %w", err)
	}
	ws.Soul = soul

	// Optional files - don't fail if missing
	ws.Agents, _ = m.readFile("AGENTS.md")
	ws.Tools, _ = m.readFile("TOOLS.md")
	ws.User, _ = m.readFile("USER.md")
	ws.Memory, _ = m.readFile("MEMORY.md")

	return ws, nil
}

func (m *FileMemory) readFile(name string) (string, error) {
	path := filepath.Join(m.workspaceDir, name)
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func (m *FileMemory) SaveDaily(ctx context.Context, entry string) error {
	memoryDir := filepath.Join(m.workspaceDir, "memory")
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		return fmt.Errorf("failed to create memory dir: %w", err)
	}

	filename := time.Now().Format("2006-01-02") + ".md"
	path := filepath.Join(memoryDir, filename)

	// Append to file
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open memory file: %w", err)
	}
	defer f.Close()

	timestamp := time.Now().Format("15:04:05")
	_, err = fmt.Fprintf(f, "\n## %s\n\n%s\n", timestamp, entry)
	return err
}

func (m *FileMemory) GetDaily(ctx context.Context, date time.Time) (string, error) {
	filename := date.Format("2006-01-02") + ".md"
	path := filepath.Join(m.workspaceDir, "memory", filename)

	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	return string(content), nil
}

func (m *FileMemory) ListDaily(ctx context.Context) ([]DailyEntry, error) {
	memoryDir := filepath.Join(m.workspaceDir, "memory")

	entries, err := os.ReadDir(memoryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var result []DailyEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}

		// Parse date from filename
		name := strings.TrimSuffix(e.Name(), ".md")
		date, err := time.Parse("2006-01-02", name)
		if err != nil {
			continue
		}

		path := filepath.Join(memoryDir, e.Name())
		content, _ := os.ReadFile(path)

		result = append(result, DailyEntry{
			Date:    date,
			Path:    path,
			Content: string(content),
		})
	}

	// Sort by date descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].Date.After(result[j].Date)
	})

	return result, nil
}

// BuildSystemPrompt constructs the system prompt from workspace files.
func BuildSystemPrompt(ws *Workspace) string {
	var parts []string

	if ws.Soul != "" {
		parts = append(parts, ws.Soul)
	}

	if ws.Agents != "" {
		parts = append(parts, "---\n\n"+ws.Agents)
	}

	if ws.Tools != "" {
		parts = append(parts, "---\n\n"+ws.Tools)
	}

	if ws.User != "" {
		parts = append(parts, "---\n\n# User Context\n\n"+ws.User)
	}

	if ws.Memory != "" {
		parts = append(parts, "---\n\n# Memory\n\n"+ws.Memory)
	}

	if len(parts) == 0 {
		return defaultSystemPrompt
	}

	return strings.Join(parts, "\n\n")
}

const defaultSystemPrompt = `You are klaw, an AI employee ready to help with any task.

You have access to tools for:
- Reading, writing, and editing files
- Running shell commands and scripts
- Searching files with glob and grep patterns

Be direct, efficient, and proactive. Execute tasks thoroughly:
- Take action immediately when the task is clear
- Break complex tasks into steps and execute them
- Report results concisely
- Ask clarifying questions only when truly necessary

You work for the user - treat their requests as work assignments to be completed.`
