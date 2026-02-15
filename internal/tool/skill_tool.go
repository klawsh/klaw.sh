package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eachlabs/klaw/internal/config"
)

// SkillTool allows installing, listing, and creating skills.
type SkillTool struct {
	skillsDir string
}

// NewSkillTool creates a new skill management tool.
func NewSkillTool() *SkillTool {
	return &SkillTool{
		skillsDir: config.ConfigDir() + "/skills",
	}
}

func (t *SkillTool) Name() string {
	return "skill"
}

func (t *SkillTool) Description() string {
	return `Manage agent skills. Skills are SKILL.md files that teach you how to perform specific tasks.

Actions:
- list: Show installed skills
- install: Download skill from skills.sh (e.g., "eachlabs-image-generation")
- show: Read a skill's content
- create: Create a new skill with custom content

Use install to get skills from the registry. Use create for custom skills.`
}

func (t *SkillTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["list", "install", "show", "create"],
				"description": "Action to perform"
			},
			"name": {
				"type": "string",
				"description": "Skill name (for install/show/create)"
			},
			"content": {
				"type": "string",
				"description": "SKILL.md content (for create action only)"
			}
		},
		"required": ["action"]
	}`)
}

type skillParams struct {
	Action  string `json:"action"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

func (t *SkillTool) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p skillParams
	if err := json.Unmarshal(params, &p); err != nil {
		return &Result{Content: fmt.Sprintf("Invalid parameters: %v", err), IsError: true}, nil
	}

	switch p.Action {
	case "list":
		return t.listSkills()
	case "install":
		return t.installSkill(p.Name)
	case "show":
		return t.showSkill(p.Name)
	case "create":
		return t.createSkill(p.Name, p.Content)
	default:
		return &Result{Content: fmt.Sprintf("Unknown action: %s. Use: list, install, show, create", p.Action), IsError: true}, nil
	}
}

func (t *SkillTool) listSkills() (*Result, error) {
	entries, err := os.ReadDir(t.skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return &Result{Content: "No skills installed.\n\nInstall from skills.sh: skill action=install name=<skill-name>"}, nil
		}
		return &Result{Content: fmt.Sprintf("Failed to list skills: %v", err), IsError: true}, nil
	}

	var skills []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(t.skillsDir, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillPath); err == nil {
			skills = append(skills, entry.Name())
		}
	}

	if len(skills) == 0 {
		return &Result{Content: "No skills installed.\n\nInstall from skills.sh: skill action=install name=<skill-name>"}, nil
	}

	return &Result{Content: fmt.Sprintf("Installed skills:\n- %s\n\nShow skill: skill action=show name=<skill-name>", strings.Join(skills, "\n- "))}, nil
}

func (t *SkillTool) installSkill(name string) (*Result, error) {
	if name == "" {
		return &Result{Content: "Skill name required", IsError: true}, nil
	}

	// Normalize name
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")

	// Check if already installed
	skillDir := filepath.Join(t.skillsDir, name)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if _, err := os.Stat(skillPath); err == nil {
		content, _ := os.ReadFile(skillPath)
		return &Result{Content: fmt.Sprintf("Skill '%s' already installed.\n\n%s", name, string(content))}, nil
	}

	// Try to download from skills.sh
	urls := []string{
		fmt.Sprintf("https://skills.sh/%s/SKILL.md", name),
		fmt.Sprintf("https://raw.githubusercontent.com/eachlabs/klaw-skills/main/%s/SKILL.md", name),
		fmt.Sprintf("https://skills.klaw.dev/%s/SKILL.md", name),
	}

	var content []byte
	var downloadErr error
	var successURL string

	client := &http.Client{Timeout: 30 * time.Second}
	for _, url := range urls {
		resp, err := client.Get(url)
		if err != nil {
			downloadErr = err
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			content, err = io.ReadAll(resp.Body)
			if err != nil {
				downloadErr = err
				continue
			}
			successURL = url
			break
		}
		downloadErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	if content == nil {
		return &Result{
			Content: fmt.Sprintf("Skill '%s' not found in skills.sh registry.\n\nLast error: %v\n\nYou can create it manually with:\nskill action=create name=%s content=\"# %s Skill\\n\\nYour skill content here...\"", name, downloadErr, name, name),
			IsError: true,
		}, nil
	}

	// Create skill directory
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return &Result{Content: fmt.Sprintf("Failed to create skill directory: %v", err), IsError: true}, nil
	}

	// Save SKILL.md
	if err := os.WriteFile(skillPath, content, 0644); err != nil {
		return &Result{Content: fmt.Sprintf("Failed to save skill: %v", err), IsError: true}, nil
	}

	return &Result{Content: fmt.Sprintf("✓ Installed skill '%s' from %s\n\n%s", name, successURL, string(content))}, nil
}

func (t *SkillTool) showSkill(name string) (*Result, error) {
	if name == "" {
		return &Result{Content: "Skill name required", IsError: true}, nil
	}

	skillPath := filepath.Join(t.skillsDir, name, "SKILL.md")
	content, err := os.ReadFile(skillPath)
	if err != nil {
		return &Result{Content: fmt.Sprintf("Skill '%s' not found. Install it first: skill action=install name=%s", name, name), IsError: true}, nil
	}

	return &Result{Content: fmt.Sprintf("# Skill: %s\n\n%s", name, string(content))}, nil
}

func (t *SkillTool) createSkill(name, content string) (*Result, error) {
	if name == "" {
		return &Result{Content: "Skill name required", IsError: true}, nil
	}
	if content == "" {
		return &Result{Content: "Skill content required. Provide the SKILL.md content.", IsError: true}, nil
	}

	// Normalize name
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")

	// Create skill directory
	skillDir := filepath.Join(t.skillsDir, name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return &Result{Content: fmt.Sprintf("Failed to create skill directory: %v", err), IsError: true}, nil
	}

	// Save SKILL.md
	skillPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillPath, []byte(content), 0644); err != nil {
		return &Result{Content: fmt.Sprintf("Failed to save skill: %v", err), IsError: true}, nil
	}

	return &Result{Content: fmt.Sprintf("✓ Created skill '%s'\n\nPath: %s\n\nThe skill is now available.", name, skillPath)}, nil
}
