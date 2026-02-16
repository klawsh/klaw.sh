package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/skill"
	"github.com/spf13/cobra"
)

const (
	skillsRegistryURL = "https://skills.klaw.sh"
	// Fallback to GitHub if API not available
	skillsGitHubURL = "https://raw.githubusercontent.com/eachlabs/klaw-skills/main"
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage agent skills",
	Long: `Manage skills that can be assigned to agents.

Skills are SKILL.md files that teach agents how to perform tasks.

Registry: github.com/eachlabs/klaw-skills

Examples:
  klaw skill list                 # List installed skills
  klaw skill browse               # Browse registry
  klaw skill install image-gen    # Install from registry
  klaw skill push my-skill        # Push to registry (PR)
  klaw skill show web-search      # Show skill content
  klaw skill create my-skill      # Create a new skill`,
}

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed skills",
	RunE:  runSkillList,
}

var skillBrowseCmd = &cobra.Command{
	Use:   "browse",
	Short: "Browse skills in the registry",
	RunE:  runSkillBrowse,
}

var skillInstallCmd = &cobra.Command{
	Use:   "install <skill-name>",
	Short: "Install a skill from the registry",
	Args:  cobra.ExactArgs(1),
	RunE:  runSkillInstall,
}

var skillPushCmd = &cobra.Command{
	Use:   "push <skill-name>",
	Short: "Push a skill to the registry (creates PR)",
	Args:  cobra.ExactArgs(1),
	RunE:  runSkillPush,
}

var skillShowCmd = &cobra.Command{
	Use:   "show <skill-name>",
	Short: "Show skill content",
	Args:  cobra.ExactArgs(1),
	RunE:  runSkillShow,
}

var skillCreateCmd = &cobra.Command{
	Use:   "create <skill-name>",
	Short: "Create a new skill",
	Args:  cobra.ExactArgs(1),
	RunE:  runSkillCreate,
}

var skillEditCmd = &cobra.Command{
	Use:   "edit <skill-name>",
	Short: "Edit a skill (opens in $EDITOR)",
	Args:  cobra.ExactArgs(1),
	RunE:  runSkillEdit,
}

var skillDeleteCmd = &cobra.Command{
	Use:   "delete <skill-name>",
	Short: "Delete a skill",
	Args:  cobra.ExactArgs(1),
	RunE:  runSkillDelete,
}

func init() {
	skillCmd.AddCommand(skillListCmd)
	skillCmd.AddCommand(skillBrowseCmd)
	skillCmd.AddCommand(skillInstallCmd)
	skillCmd.AddCommand(skillPushCmd)
	skillCmd.AddCommand(skillShowCmd)
	skillCmd.AddCommand(skillCreateCmd)
	skillCmd.AddCommand(skillEditCmd)
	skillCmd.AddCommand(skillDeleteCmd)
	rootCmd.AddCommand(skillCmd)
}

func getSkillLoader() *skill.SkillLoader {
	return skill.NewSkillLoader(config.ConfigDir() + "/skills")
}

func runSkillList(cmd *cobra.Command, args []string) error {
	loader := getSkillLoader()
	skills, err := loader.ListSkills()
	if err != nil {
		return err
	}

	if len(skills) == 0 {
		fmt.Println("No skills installed.")
		fmt.Println()
		fmt.Println("Browse registry:  klaw skill browse")
		fmt.Println("Install skill:    klaw skill install <name>")
		fmt.Println("Create skill:     klaw skill create <name>")
		return nil
	}

	fmt.Println("Installed Skills:")
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPATH")
	fmt.Fprintln(w, "----\t----")

	skillsDir := config.ConfigDir() + "/skills"
	for _, name := range skills {
		path := filepath.Join(skillsDir, name, "SKILL.md")
		fmt.Fprintf(w, "%s\t%s\n", name, path)
	}
	w.Flush()

	fmt.Println()
	fmt.Println("Show skill:    klaw skill show <name>")
	fmt.Println("Push to registry:  klaw skill push <name>")

	return nil
}

func runSkillBrowse(cmd *cobra.Command, args []string) error {
	fmt.Println("╭─────────────────────────────────────────╮")
	fmt.Println("│         🛒 Klaw Skills Registry         │")
	fmt.Println("╰─────────────────────────────────────────╯")
	fmt.Println()
	fmt.Printf("Registry: %s\n", skillsRegistryURL)
	fmt.Println()

	// Fetch registry index
	index, err := fetchSkillIndex()
	if err != nil {
		// Fallback: show known skills
		fmt.Println("Available Skills (cached):")
		fmt.Println()
		defaultSkills := []struct {
			name string
			desc string
		}{
			{"find-skills", "Discover and install skills from the ecosystem"},
			{"web-search", "Search the web using search engines"},
			{"browser", "Browse websites and extract information"},
			{"facebook-ads", "Search Facebook Ad Library for competitor ads"},
			{"eachlabs-image-generation", "Generate images using EachLabs AI models"},
			{"eachlabs-video-generation", "Generate videos using EachLabs AI models"},
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tDESCRIPTION")
		fmt.Fprintln(w, "----\t-----------")
		for _, s := range defaultSkills {
			fmt.Fprintf(w, "%s\t%s\n", s.name, s.desc)
		}
		w.Flush()

		fmt.Println()
		fmt.Println("Install: klaw skill install <name>")
		return nil
	}

	fmt.Println("Available Skills:")
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tAUTHOR\tDESCRIPTION")
	fmt.Fprintln(w, "----\t------\t-----------")
	for _, s := range index.Skills {
		desc := s.Description
		if len(desc) > 50 {
			desc = desc[:47] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, s.Author, desc)
	}
	w.Flush()

	fmt.Println()
	fmt.Println("Install: klaw skill install <name>")

	return nil
}

type skillIndex struct {
	Skills []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Author      string `json:"author"`
	} `json:"skills"`
}

func fetchSkillIndex() (*skillIndex, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	// Try API first
	resp, err := client.Get(skillsRegistryURL + "/index.json")
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		var index skillIndex
		if err := json.NewDecoder(resp.Body).Decode(&index); err == nil {
			return &index, nil
		}
	}
	if resp != nil {
		resp.Body.Close()
	}

	// Fallback to GitHub
	resp, err = client.Get(skillsGitHubURL + "/index.json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var index skillIndex
	if err := json.NewDecoder(resp.Body).Decode(&index); err != nil {
		return nil, err
	}
	return &index, nil
}

func runSkillInstall(cmd *cobra.Command, args []string) error {
	name := strings.ToLower(strings.TrimSpace(args[0]))
	name = strings.ReplaceAll(name, " ", "-")

	skillsDir := config.ConfigDir() + "/skills"
	skillDir := filepath.Join(skillsDir, name)
	skillPath := filepath.Join(skillDir, "SKILL.md")

	// Check if already installed
	if _, err := os.Stat(skillPath); err == nil {
		fmt.Printf("Skill '%s' is already installed.\n", name)
		fmt.Printf("Path: %s\n", skillPath)
		return nil
	}

	fmt.Printf("📦 Installing skill '%s'...\n", name)

	// Try to download from registry (API first, then GitHub fallback)
	content, err := downloadSkillContent(name)
	if err != nil {
		return fmt.Errorf("skill '%s' not found: %w\n\nBrowse available skills: klaw skill browse", name, err)
	}

	// Create directory and save
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return err
	}

	if err := os.WriteFile(skillPath, content, 0644); err != nil {
		return err
	}

	fmt.Printf("✓ Installed skill '%s'\n", name)
	fmt.Printf("  Path: %s\n", skillPath)
	fmt.Println()
	fmt.Printf("Show skill: klaw skill show %s\n", name)

	return nil
}

func downloadSkillContent(name string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	// Try API first
	urls := []string{
		fmt.Sprintf("%s/%s/SKILL.md", skillsRegistryURL, name),
		fmt.Sprintf("%s/%s", skillsRegistryURL, name), // shorthand
		fmt.Sprintf("%s/%s/SKILL.md", skillsGitHubURL, name),
	}

	for _, url := range urls {
		resp, err := client.Get(url)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			return io.ReadAll(resp.Body)
		}
	}

	return nil, fmt.Errorf("not found in registry")
}

func runSkillPush(cmd *cobra.Command, args []string) error {
	name := args[0]
	skillsDir := config.ConfigDir() + "/skills"
	skillPath := filepath.Join(skillsDir, name, "SKILL.md")

	// Check if skill exists
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		return fmt.Errorf("skill '%s' not found. Create it first: klaw skill create %s", name, name)
	}

	// Read skill content
	content, err := os.ReadFile(skillPath)
	if err != nil {
		return err
	}

	// Get API key (from credentials, env, or config)
	apiKey := GetAPIKey()
	if apiKey == "" {
		fmt.Println("📤 Pushing skill requires authentication.")
		fmt.Println()
		fmt.Println("Run 'klaw login' to authenticate with GitHub.")
		fmt.Println()
		fmt.Println("Or set KLAW_SKILLS_API_KEY environment variable.")
		return nil
	}

	fmt.Printf("📤 Pushing skill '%s' to registry...\n", name)

	// Extract description from content
	description := extractFirstParagraph(string(content))

	// Build request
	payload := map[string]string{
		"name":        name,
		"content":     string(content),
		"description": description,
		"author":      os.Getenv("USER"),
	}
	payloadBytes, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", skillsRegistryURL+"/upload", strings.NewReader(string(payloadBytes)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid API key")
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to push (HTTP %d): %s", resp.StatusCode, string(body))
	}

	fmt.Printf("✓ Pushed skill '%s' to registry\n", name)
	fmt.Println()
	fmt.Println("Others can now install with:")
	fmt.Printf("  klaw skill install %s\n", name)

	return nil
}

func extractFirstParagraph(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 100 {
			return line[:100]
		}
		return line
	}
	return ""
}

func runSkillShow(cmd *cobra.Command, args []string) error {
	name := args[0]
	loader := getSkillLoader()

	content, err := loader.LoadSkill(name)
	if err != nil {
		return fmt.Errorf("skill '%s' not found: %w", name, err)
	}

	fmt.Printf("# Skill: %s\n", name)
	fmt.Println(strings.Repeat("─", 40))
	fmt.Println(content)

	return nil
}

func runSkillCreate(cmd *cobra.Command, args []string) error {
	name := args[0]
	skillsDir := config.ConfigDir() + "/skills"
	skillDir := filepath.Join(skillsDir, name)
	skillPath := filepath.Join(skillDir, "SKILL.md")

	// Check if already exists
	if _, err := os.Stat(skillPath); err == nil {
		return fmt.Errorf("skill '%s' already exists at %s", name, skillPath)
	}

	// Create directory
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return err
	}

	// Create template SKILL.md
	template := fmt.Sprintf(`# %s Skill

Describe what this skill enables the agent to do.

## When to Use

Use this skill when the user wants to:
- Task 1
- Task 2

## How to Use

1. Step one
2. Step two
3. Step three

## Tools Required

- web_fetch - for fetching URLs
- bash - for running commands

## Examples

### Example 1: Basic usage

%s

## Tips

- Tip 1
- Tip 2
`, name, "```\nweb_fetch url=\"https://example.com\"\n```")

	if err := os.WriteFile(skillPath, []byte(template), 0644); err != nil {
		return err
	}

	fmt.Printf("✓ Created skill: %s\n", name)
	fmt.Printf("  Path: %s\n", skillPath)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("  1. Edit:  klaw skill edit %s\n", name)
	fmt.Printf("  2. Test:  klaw skill show %s\n", name)
	fmt.Printf("  3. Share: klaw skill push %s\n", name)

	return nil
}

func runSkillEdit(cmd *cobra.Command, args []string) error {
	name := args[0]
	skillsDir := config.ConfigDir() + "/skills"
	skillPath := filepath.Join(skillsDir, name, "SKILL.md")

	// Check if exists
	if _, err := os.Stat(skillPath); os.IsNotExist(err) {
		return fmt.Errorf("skill '%s' not found. Create it first: klaw skill create %s", name, name)
	}

	// Get editor
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "nano"
	}

	// Run editor
	editorCmd := exec.Command(editor, skillPath)
	editorCmd.Stdin = os.Stdin
	editorCmd.Stdout = os.Stdout
	editorCmd.Stderr = os.Stderr

	return editorCmd.Run()
}

func runSkillDelete(cmd *cobra.Command, args []string) error {
	name := args[0]
	skillsDir := config.ConfigDir() + "/skills"
	skillDir := filepath.Join(skillsDir, name)

	// Check if exists
	if _, err := os.Stat(skillDir); os.IsNotExist(err) {
		return fmt.Errorf("skill '%s' not found", name)
	}

	// Confirm deletion
	fmt.Printf("Delete skill '%s' and all its files? [y/N] ", name)
	var response string
	fmt.Scanln(&response)

	if strings.ToLower(response) != "y" {
		fmt.Println("Cancelled.")
		return nil
	}

	if err := os.RemoveAll(skillDir); err != nil {
		return err
	}

	fmt.Printf("✓ Deleted skill: %s\n", name)
	return nil
}
