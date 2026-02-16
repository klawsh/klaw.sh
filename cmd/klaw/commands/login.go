package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/eachlabs/klaw/internal/config"
	"github.com/spf13/cobra"
)

const skillsAPIURL = "https://skills.klaw.sh"

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type pollResponse struct {
	Status string `json:"status"`
	APIKey string `json:"api_key,omitempty"`
	Error  string `json:"error,omitempty"`
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Login to Klaw Skills registry",
	Long: `Authenticate with the Klaw Skills registry using GitHub.

This will open your browser to authenticate with GitHub,
then save your API key locally for pushing skills.

Your credentials are stored in ~/.klaw/credentials`,
	RunE: runLogin,
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Logout from Klaw Skills registry",
	RunE:  runLogout,
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show current logged in user",
	RunE:  runWhoami,
}

func init() {
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(whoamiCmd)
}

func runLogin(cmd *cobra.Command, args []string) error {
	fmt.Println()
	fmt.Println("  ╭─────────────────────────────────────╮")
	fmt.Println("  │      🔐 Klaw Skills Login           │")
	fmt.Println("  ╰─────────────────────────────────────╯")
	fmt.Println()

	// 1. Request device code
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Post(skillsAPIURL+"/auth/device", "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to connect to Klaw Skills: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get device code (HTTP %d)", resp.StatusCode)
	}

	var deviceResp deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&deviceResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	// 2. Show user code and open browser
	fmt.Printf("  Your code: %s\n", deviceResp.UserCode)
	fmt.Println()
	fmt.Printf("  Opening browser to: %s\n", deviceResp.VerificationURL)
	fmt.Println()

	// Open browser
	if err := openBrowser(deviceResp.VerificationURL); err != nil {
		fmt.Println("  Could not open browser automatically.")
		fmt.Println("  Please open this URL manually:")
		fmt.Printf("  %s\n", deviceResp.VerificationURL)
	}

	fmt.Println("  Waiting for authorization...")
	fmt.Println()

	// 3. Poll for result
	interval := time.Duration(deviceResp.Interval) * time.Second
	if interval < time.Second {
		interval = 5 * time.Second
	}

	deadline := time.Now().Add(time.Duration(deviceResp.ExpiresIn) * time.Second)
	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinIdx := 0

	for time.Now().Before(deadline) {
		// Show spinner
		fmt.Printf("\r  %s Waiting...", spinner[spinIdx%len(spinner)])
		spinIdx++

		time.Sleep(interval)

		pollResp, err := client.Get(skillsAPIURL + "/auth/device/poll?device_code=" + deviceResp.DeviceCode)
		if err != nil {
			continue
		}

		var poll pollResponse
		if err := json.NewDecoder(pollResp.Body).Decode(&poll); err != nil {
			pollResp.Body.Close()
			continue
		}
		pollResp.Body.Close()

		switch poll.Status {
		case "authorized":
			fmt.Printf("\r                              \r") // Clear spinner line

			if poll.APIKey == "" {
				return fmt.Errorf("authorization successful but no API key received")
			}

			// Save credentials
			if err := saveCredentials(poll.APIKey); err != nil {
				return fmt.Errorf("failed to save credentials: %w", err)
			}

			fmt.Println("  ✅ Login successful!")
			fmt.Println()
			fmt.Println("  Your API key has been saved to ~/.klaw/credentials")
			fmt.Println()
			fmt.Println("  You can now push skills:")
			fmt.Println("    klaw skill push <skill-name>")
			fmt.Println()
			return nil

		case "expired":
			fmt.Printf("\r                              \r")
			return fmt.Errorf("authorization expired. Please try again")

		case "pending":
			// Keep polling
			continue

		default:
			if poll.Error != "" {
				fmt.Printf("\r                              \r")
				return fmt.Errorf("authorization failed: %s", poll.Error)
			}
		}
	}

	fmt.Printf("\r                              \r")
	return fmt.Errorf("authorization timed out. Please try again")
}

func runLogout(cmd *cobra.Command, args []string) error {
	credPath := credentialsPath()

	if _, err := os.Stat(credPath); os.IsNotExist(err) {
		fmt.Println("Not logged in.")
		return nil
	}

	if err := os.Remove(credPath); err != nil {
		return fmt.Errorf("failed to remove credentials: %w", err)
	}

	fmt.Println("✅ Logged out successfully.")
	return nil
}

func runWhoami(cmd *cobra.Command, args []string) error {
	apiKey, err := loadCredentials()
	if err != nil || apiKey == "" {
		fmt.Println("Not logged in.")
		fmt.Println()
		fmt.Println("Run 'klaw login' to authenticate.")
		return nil
	}

	// Fetch user info from API
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", skillsAPIURL+"/api/me", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch user info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		fmt.Println("Session expired. Please run 'klaw login' again.")
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		// Fallback: just show key prefix
		fmt.Printf("Logged in with key: %s...\n", apiKey[:12])
		return nil
	}

	var user struct {
		GithubUsername string `json:"github_username"`
		Email          string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		fmt.Printf("Logged in with key: %s...\n", apiKey[:12])
		return nil
	}

	fmt.Printf("Logged in as: @%s\n", user.GithubUsername)
	if user.Email != "" {
		fmt.Printf("Email: %s\n", user.Email)
	}

	return nil
}

func credentialsPath() string {
	return filepath.Join(config.StateDir(), "credentials")
}

func saveCredentials(apiKey string) error {
	credPath := credentialsPath()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(credPath), 0755); err != nil {
		return err
	}

	// Save with restricted permissions
	return os.WriteFile(credPath, []byte(apiKey), 0600)
}

func loadCredentials() (string, error) {
	credPath := credentialsPath()
	data, err := os.ReadFile(credPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// GetAPIKey returns the API key from credentials or environment
func GetAPIKey() string {
	// Environment variable takes precedence
	if key := os.Getenv("KLAW_SKILLS_API_KEY"); key != "" {
		return key
	}

	// Try credentials file
	if key, err := loadCredentials(); err == nil && key != "" {
		return key
	}

	// Try config file
	cfg, _ := config.Load()
	if cfg != nil && cfg.SkillsAPIKey != "" {
		return cfg.SkillsAPIKey
	}

	return ""
}

func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "linux":
		cmd = "xdg-open"
		args = []string{url}
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	default:
		return fmt.Errorf("unsupported platform")
	}

	return exec.Command(cmd, args...).Start()
}
