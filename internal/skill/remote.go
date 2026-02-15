// Package skill provides remote skill installation from skills.sh
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

// RemoteManifest represents a skill manifest from skills.sh
type RemoteManifest struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Author      string            `json:"author"`
	Homepage    string            `json:"homepage"`
	License     string            `json:"license"`
	Repository  string            `json:"repository"`
	Tools       []RemoteTool      `json:"tools"`
	Config      map[string]string `json:"config,omitempty"`

	// MCP server configuration
	MCP *MCPConfig `json:"mcp,omitempty"`

	// Installation
	Install *InstallConfig `json:"install,omitempty"`
}

// RemoteTool represents a tool provided by a remote skill
type RemoteTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// MCPConfig holds MCP server configuration
type MCPConfig struct {
	Command string            `json:"command"` // Command to run the MCP server
	Args    []string          `json:"args"`    // Arguments
	Env     map[string]string `json:"env"`     // Environment variables
	Port    int               `json:"port"`    // Port if network-based
}

// InstallConfig holds installation instructions
type InstallConfig struct {
	// NPM package to install
	NPM string `json:"npm,omitempty"`

	// Python package to install
	Pip string `json:"pip,omitempty"`

	// Shell commands to run
	Commands []string `json:"commands,omitempty"`

	// Binary URL to download
	Binary string `json:"binary,omitempty"`
}

// InstalledSkill tracks installed remote skills
type InstalledSkill struct {
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	Source      string          `json:"source"` // URL it was installed from
	Manifest    *RemoteManifest `json:"manifest"`
	InstalledAt time.Time       `json:"installed_at"`
	Path        string          `json:"path"` // Installation directory
}

// InstallFromURL installs a skill from a remote URL
func (r *Registry) InstallFromURL(urlOrPath string) error {
	// Parse the URL/path
	// Format: https://skills.sh/org/skill-name
	// Or: org/skill-name (shorthand)
	// Or: skills.sh/org/skill-name

	var manifestURL string

	if strings.HasPrefix(urlOrPath, "https://skills.sh/") {
		// Full URL
		manifestURL = urlOrPath + "/manifest.json"
	} else if strings.HasPrefix(urlOrPath, "skills.sh/") {
		// Without https
		manifestURL = "https://" + urlOrPath + "/manifest.json"
	} else if strings.Contains(urlOrPath, "/") && !strings.Contains(urlOrPath, "://") {
		// Shorthand: org/skill-name
		manifestURL = "https://skills.sh/" + urlOrPath + "/manifest.json"
	} else {
		return fmt.Errorf("invalid skill URL: %s (expected format: skills.sh/org/skill-name)", urlOrPath)
	}

	fmt.Printf("📦 Fetching skill manifest from %s...\n", manifestURL)

	// Fetch manifest
	manifest, err := fetchManifest(manifestURL)
	if err != nil {
		return fmt.Errorf("failed to fetch manifest: %w", err)
	}

	fmt.Printf("✓ Found skill: %s v%s\n", manifest.Name, manifest.Version)
	fmt.Printf("  %s\n", manifest.Description)

	// Create skill directory
	skillDir := filepath.Join(r.skillsDir, "remote", manifest.Name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		return fmt.Errorf("failed to create skill directory: %w", err)
	}

	// Run installation
	if manifest.Install != nil {
		fmt.Println("📥 Installing dependencies...")
		if err := runInstall(manifest.Install, skillDir); err != nil {
			return fmt.Errorf("installation failed: %w", err)
		}
	}

	// Save manifest
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(skillDir, "manifest.json"), manifestData, 0644); err != nil {
		return fmt.Errorf("failed to save manifest: %w", err)
	}

	// Extract tool names
	var toolNames []string
	for _, t := range manifest.Tools {
		toolNames = append(toolNames, t.Name)
	}

	// Build system prompt
	systemPrompt := fmt.Sprintf("You have the %s skill installed.\n%s", manifest.Name, manifest.Description)
	if len(manifest.Tools) > 0 {
		systemPrompt += "\n\nAvailable tools:"
		for _, t := range manifest.Tools {
			systemPrompt += fmt.Sprintf("\n- %s: %s", t.Name, t.Description)
		}
	}

	// Register skill
	skill := &Skill{
		Name:         manifest.Name,
		Version:      manifest.Version,
		Description:  manifest.Description,
		Author:       manifest.Author,
		Homepage:     manifest.Homepage,
		Tools:        toolNames,
		SystemPrompt: systemPrompt,
		Config:       manifest.Config,
		Installed:    true,
		Source:       "remote:" + urlOrPath,
	}

	r.skills[manifest.Name] = skill

	// Save installed skill record
	installed := &InstalledSkill{
		Name:        manifest.Name,
		Version:     manifest.Version,
		Source:      urlOrPath,
		Manifest:    manifest,
		InstalledAt: time.Now(),
		Path:        skillDir,
	}

	installedData, _ := json.MarshalIndent(installed, "", "  ")
	if err := os.WriteFile(filepath.Join(skillDir, "installed.json"), installedData, 0644); err != nil {
		return fmt.Errorf("failed to save installation record: %w", err)
	}

	// Save to registry
	if err := r.saveInstalled(); err != nil {
		return err
	}

	fmt.Println()
	fmt.Printf("✅ Successfully installed %s\n", manifest.Name)

	if manifest.MCP != nil {
		fmt.Printf("   This skill runs as an MCP server: %s\n", manifest.MCP.Command)
	}

	fmt.Printf("\nUse with: klaw create agent myagent --skills %s\n", manifest.Name)

	return nil
}

// fetchManifest fetches a manifest from a URL
func fetchManifest(url string) (*RemoteManifest, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try without manifest.json (maybe it's a direct manifest)
		if strings.HasSuffix(url, "/manifest.json") {
			baseURL := strings.TrimSuffix(url, "/manifest.json")
			resp2, err2 := client.Get(baseURL)
			if err2 == nil && resp2.StatusCode == http.StatusOK {
				defer resp2.Body.Close()
				body, _ := io.ReadAll(resp2.Body)
				var manifest RemoteManifest
				if err := json.Unmarshal(body, &manifest); err == nil {
					return &manifest, nil
				}
			}
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var manifest RemoteManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	return &manifest, nil
}

// runInstall executes installation steps
func runInstall(cfg *InstallConfig, workDir string) error {
	// NPM install
	if cfg.NPM != "" {
		fmt.Printf("  npm install %s\n", cfg.NPM)
		cmd := exec.Command("npm", "install", "-g", cfg.NPM)
		cmd.Dir = workDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("npm install failed: %w", err)
		}
	}

	// Pip install
	if cfg.Pip != "" {
		fmt.Printf("  pip install %s\n", cfg.Pip)
		cmd := exec.Command("pip3", "install", cfg.Pip)
		cmd.Dir = workDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			// Try with pip
			cmd = exec.Command("pip", "install", cfg.Pip)
			cmd.Dir = workDir
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("pip install failed: %w", err)
			}
		}
	}

	// Shell commands
	for _, cmdStr := range cfg.Commands {
		fmt.Printf("  %s\n", cmdStr)
		cmd := exec.Command("sh", "-c", cmdStr)
		cmd.Dir = workDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("command failed: %w", err)
		}
	}

	// Binary download
	if cfg.Binary != "" {
		fmt.Printf("  Downloading binary from %s\n", cfg.Binary)
		if err := downloadBinary(cfg.Binary, workDir); err != nil {
			return fmt.Errorf("binary download failed: %w", err)
		}
	}

	return nil
}

// downloadBinary downloads a binary to the work directory
func downloadBinary(url, workDir string) error {
	client := &http.Client{Timeout: 5 * time.Minute}

	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Extract filename from URL
	parts := strings.Split(url, "/")
	filename := parts[len(parts)-1]
	if filename == "" {
		filename = "binary"
	}

	binPath := filepath.Join(workDir, filename)
	out, err := os.Create(binPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	// Make executable
	return os.Chmod(binPath, 0755)
}

// loadRemoteSkills loads installed remote skills from disk
func (r *Registry) loadRemoteSkills() {
	remoteDir := filepath.Join(r.skillsDir, "remote")
	entries, err := os.ReadDir(remoteDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		installedPath := filepath.Join(remoteDir, entry.Name(), "installed.json")
		data, err := os.ReadFile(installedPath)
		if err != nil {
			continue
		}

		var installed InstalledSkill
		if err := json.Unmarshal(data, &installed); err != nil {
			continue
		}

		// Extract tool names
		var toolNames []string
		if installed.Manifest != nil {
			for _, t := range installed.Manifest.Tools {
				toolNames = append(toolNames, t.Name)
			}
		}

		// Build system prompt
		systemPrompt := fmt.Sprintf("You have the %s skill installed.", installed.Name)
		if installed.Manifest != nil {
			systemPrompt = fmt.Sprintf("You have the %s skill installed.\n%s", installed.Name, installed.Manifest.Description)
			if len(installed.Manifest.Tools) > 0 {
				systemPrompt += "\n\nAvailable tools:"
				for _, t := range installed.Manifest.Tools {
					systemPrompt += fmt.Sprintf("\n- %s: %s", t.Name, t.Description)
				}
			}
		}

		// Register skill
		skill := &Skill{
			Name:         installed.Name,
			Version:      installed.Version,
			Description:  installed.Manifest.Description,
			Author:       installed.Manifest.Author,
			Homepage:     installed.Manifest.Homepage,
			Tools:        toolNames,
			SystemPrompt: systemPrompt,
			Installed:    true,
			Source:       "remote:" + installed.Source,
		}

		r.skills[installed.Name] = skill
	}
}

// GetMCPConfig returns MCP configuration for a skill if it has one
func (r *Registry) GetMCPConfig(skillName string) (*MCPConfig, error) {
	remoteDir := filepath.Join(r.skillsDir, "remote", skillName)
	installedPath := filepath.Join(remoteDir, "installed.json")

	data, err := os.ReadFile(installedPath)
	if err != nil {
		return nil, fmt.Errorf("skill not found or not a remote skill")
	}

	var installed InstalledSkill
	if err := json.Unmarshal(data, &installed); err != nil {
		return nil, err
	}

	if installed.Manifest == nil || installed.Manifest.MCP == nil {
		return nil, fmt.Errorf("skill %s does not have MCP configuration", skillName)
	}

	return installed.Manifest.MCP, nil
}
