// Package skill provides skill loading from SKILL.md files.
package skill

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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

// GetAllSkillsPrompt loads all installed skills and returns combined system prompt.
func (l *SkillLoader) GetAllSkillsPrompt() string {
	skills, err := l.ListSkills()
	if err != nil || len(skills) == 0 {
		return ""
	}
	return l.GetSkillsPrompt(skills)
}

// GetSkillsIndex returns a compact index of given skills for the system prompt.
// Only includes skill names — the agent can load full content on-demand via the skill tool.
func (l *SkillLoader) GetSkillsIndex(skills []string) string {
	if len(skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n# Available Skills\n\n")
	sb.WriteString("You have the following creative AI skills installed. ")
	sb.WriteString("Use the `skill` tool with action=show to read a skill's full instructions before using it.\n\n")
	for _, name := range skills {
		sb.WriteString("- ")
		sb.WriteString(name)
		sb.WriteString("\n")
	}
	return sb.String()
}

// GetAllSkillsIndex returns a compact index of ALL installed skills.
func (l *SkillLoader) GetAllSkillsIndex() string {
	skills, err := l.ListSkills()
	if err != nil || len(skills) == 0 {
		return ""
	}
	return l.GetSkillsIndex(skills)
}

// InstallFromGitHub installs a skill from a GitHub repository.
// repoURL format: https://github.com/org/repo
// skillName: specific skill name, or "all" to install every skill found in the repo.
func (l *SkillLoader) InstallFromGitHub(repoURL, skillName string) error {
	org, repo := parseGitHubURL(repoURL)
	if org == "" || repo == "" {
		return fmt.Errorf("invalid GitHub URL: %s", repoURL)
	}

	if skillName == "all" {
		return l.installAllFromGitHub(org, repo)
	}

	return l.installOneFromGitHub(org, repo, skillName)
}

// parseGitHubURL extracts org and repo from a GitHub URL.
func parseGitHubURL(url string) (org, repo string) {
	url = strings.TrimSuffix(url, "/")
	url = strings.TrimSuffix(url, ".git")
	parts := strings.Split(url, "/")
	if len(parts) < 2 {
		return "", ""
	}
	return parts[len(parts)-2], parts[len(parts)-1]
}

// installOneFromGitHub downloads a single skill's SKILL.md from a GitHub repo.
func (l *SkillLoader) installOneFromGitHub(org, repo, skillName string) error {
	skillDir := filepath.Join(l.skillsDir, skillName)
	skillPath := filepath.Join(skillDir, "SKILL.md")

	// Skip if already installed
	if _, err := os.Stat(skillPath); err == nil {
		return nil
	}

	// Try multiple paths within the repo
	urls := []string{
		fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/skills/%s/SKILL.md", org, repo, skillName),
		fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/%s/SKILL.md", org, repo, skillName),
		fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/SKILL.md", org, repo),
	}

	client := &http.Client{Timeout: 30 * time.Second}
	var lastErr error

	for _, url := range urls {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusOK {
			content, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				lastErr = err
				continue
			}
			if err := os.MkdirAll(skillDir, 0755); err != nil {
				return err
			}
			return os.WriteFile(skillPath, content, 0644)
		}
		resp.Body.Close()
		lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	return fmt.Errorf("skill '%s' not found in %s/%s: %w", skillName, org, repo, lastErr)
}

// installAllFromGitHub discovers and installs all skills from a GitHub repo.
// It checks the skills/ directory first, then falls back to root-level directories.
func (l *SkillLoader) installAllFromGitHub(org, repo string) error {
	client := &http.Client{Timeout: 30 * time.Second}

	// Try skills/ directory first, then root
	apiURLs := []string{
		fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/skills", org, repo),
		fmt.Sprintf("https://api.github.com/repos/%s/%s/contents", org, repo),
	}

	var entries []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}

	var found bool
	for _, apiURL := range apiURLs {
		resp, err := client.Get(apiURL)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}
		if err := json.Unmarshal(body, &entries); err != nil {
			continue
		}
		found = true
		break
	}

	if !found {
		return fmt.Errorf("could not list skills from %s/%s", org, repo)
	}

	var installed int
	for _, entry := range entries {
		if entry.Type != "dir" {
			continue
		}
		// Skip common non-skill directories
		if entry.Name == ".github" || entry.Name == "node_modules" || entry.Name == ".git" {
			continue
		}
		if err := l.installOneFromGitHub(org, repo, entry.Name); err == nil {
			installed++
		}
	}

	if installed == 0 {
		return fmt.Errorf("no skills found in %s/%s", org, repo)
	}
	return nil
}
