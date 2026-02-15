// Package skill provides skill loading from SKILL.md files.
package skill

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SkillLoader loads skills from the skills directory.
// Each skill is a folder containing a SKILL.md file.
type SkillLoader struct {
	skillsDir string
}

// NewSkillLoader creates a new skill loader.
func NewSkillLoader(skillsDir string) *SkillLoader {
	return &SkillLoader{
		skillsDir: skillsDir,
	}
}

// LoadSkill loads a skill by name. Returns the SKILL.md content.
// If skill doesn't exist locally, tries to install it.
func (l *SkillLoader) LoadSkill(name string) (string, error) {
	skillPath := filepath.Join(l.skillsDir, name, "SKILL.md")

	// Check if skill exists locally
	content, err := os.ReadFile(skillPath)
	if err == nil {
		return string(content), nil
	}

	// Skill not found locally - try to install
	if err := l.InstallSkill(name); err != nil {
		return "", fmt.Errorf("skill '%s' not found and could not be installed: %w", name, err)
	}

	// Try reading again after install
	content, err = os.ReadFile(skillPath)
	if err != nil {
		return "", fmt.Errorf("skill '%s' installed but SKILL.md not found", name)
	}

	return string(content), nil
}

// InstallSkill installs a skill from the registry.
// Format: "skill-name" or "org/skill-name"
func (l *SkillLoader) InstallSkill(name string) error {
	skillDir := filepath.Join(l.skillsDir, name)

	// Create skill directory
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return err
	}

	// Try npx first (for npm-based skills)
	// Format: npx @klaw/skill-<name> --output <skillDir>
	npmName := fmt.Sprintf("@klaw/skill-%s", strings.ReplaceAll(name, "/", "-"))
	cmd := exec.Command("npx", npmName, "--output", skillDir)
	if err := cmd.Run(); err == nil {
		return nil
	}

	// Try curl from skills.klaw.dev
	skillURL := fmt.Sprintf("https://skills.klaw.dev/%s/SKILL.md", name)
	curlCmd := exec.Command("curl", "-fsSL", "-o", filepath.Join(skillDir, "SKILL.md"), skillURL)
	if err := curlCmd.Run(); err == nil {
		return nil
	}

	// Clean up empty directory on failure
	os.RemoveAll(skillDir)

	return fmt.Errorf("could not install skill '%s' from any source", name)
}

// ListSkills returns all installed skills.
func (l *SkillLoader) ListSkills() ([]string, error) {
	entries, err := os.ReadDir(l.skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	var skills []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		// Check if SKILL.md exists
		skillPath := filepath.Join(l.skillsDir, entry.Name(), "SKILL.md")
		if _, err := os.Stat(skillPath); err == nil {
			skills = append(skills, entry.Name())
		}
	}

	return skills, nil
}

// GetSkillPrompt returns the system prompt addition for a skill.
func (l *SkillLoader) GetSkillPrompt(name string) (string, error) {
	content, err := l.LoadSkill(name)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("# %s Skill\n\n%s", name, content), nil
}

// GetSkillsPrompt returns combined system prompt for multiple skills.
func (l *SkillLoader) GetSkillsPrompt(names []string) string {
	var prompts []string

	for _, name := range names {
		prompt, err := l.GetSkillPrompt(name)
		if err != nil {
			continue
		}
		prompts = append(prompts, prompt)
	}

	if len(prompts) == 0 {
		return ""
	}

	return "\n\n# Available Skills\n\n" + strings.Join(prompts, "\n\n---\n\n")
}
