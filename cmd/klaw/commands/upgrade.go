package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	githubRepo     = "klawsh/klaw.sh"
	githubAPIURL   = "https://api.github.com/repos/" + githubRepo + "/releases/latest"
	downloadURLFmt = "https://github.com/" + githubRepo + "/releases/download/%s/klaw-%s-%s"
)

var upgradeCheckOnly bool

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade klaw to the latest version",
	Long: `Check for and install the latest version of klaw.

Downloads the latest release from GitHub and replaces the current binary.

Examples:
  klaw upgrade          # Upgrade to latest version
  klaw upgrade --check  # Only check, don't upgrade`,
	RunE: runUpgrade,
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeCheckOnly, "check", false, "only check for updates, don't install")
}

type githubRelease struct {
	TagName string `json:"tag_name"`
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	fmt.Printf("Current version: %s\n", version)

	if version == "dev" {
		fmt.Println("Warning: running development build. Will upgrade to latest release.")
	}

	fmt.Println("Checking for updates...")

	latest, err := fetchLatestVersion()
	if err != nil {
		return fmt.Errorf("failed to check for updates: %w", err)
	}

	if !isNewer(latest, version) {
		fmt.Println("Already up to date.")
		return nil
	}

	fmt.Printf("New version available: %s\n", latest)

	if upgradeCheckOnly {
		fmt.Println("Run 'klaw upgrade' to install.")
		return nil
	}

	execPath, err := getExecutablePath()
	if err != nil {
		return fmt.Errorf("failed to find current binary: %w", err)
	}

	// Build download URL
	osName := runtime.GOOS
	arch := runtime.GOARCH
	binaryName := fmt.Sprintf("klaw-%s-%s", osName, arch)
	if osName == "windows" {
		binaryName += ".exe"
	}
	url := fmt.Sprintf(downloadURLFmt, latest, osName, arch)
	if osName == "windows" {
		url += ".exe"
	}

	fmt.Printf("Downloading %s...\n", binaryName)

	if err := downloadAndReplace(url, execPath, latest); err != nil {
		return err
	}

	fmt.Printf("\nSuccessfully upgraded klaw to %s\n", latest)
	return nil
}

func fetchLatestVersion() (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	req, err := http.NewRequest("GET", githubAPIURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "klaw-upgrade")

	// Use GITHUB_TOKEN if available to avoid rate limits
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("network error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 403 {
		return "", fmt.Errorf("GitHub API rate limit exceeded. Try again later, or set GITHUB_TOKEN environment variable")
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("failed to parse release info: %w", err)
	}

	if release.TagName == "" {
		return "", fmt.Errorf("no release found")
	}

	return release.TagName, nil
}

func isNewer(latest, current string) bool {
	if current == "dev" || current == "" {
		return true
	}

	latestParts, err := parseVersion(latest)
	if err != nil {
		// Fall back to string comparison
		return latest > current
	}

	currentParts, err := parseVersion(current)
	if err != nil {
		return latest > current
	}

	for i := 0; i < 4; i++ {
		if latestParts[i] > currentParts[i] {
			return true
		}
		if latestParts[i] < currentParts[i] {
			return false
		}
	}

	return false // equal
}

func parseVersion(v string) ([4]int, error) {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")

	var result [4]int
	for i := 0; i < 4 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return result, fmt.Errorf("invalid version segment %q: %w", parts[i], err)
		}
		result[i] = n
	}

	return result, nil
}

func getExecutablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func downloadAndReplace(url, execPath, newVersion string) error {
	client := &http.Client{Timeout: 5 * time.Minute}

	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return fmt.Errorf("no binary available for %s/%s. Build from source instead", runtime.GOOS, runtime.GOARCH)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Create temp file in same directory (needed for atomic rename)
	dir := filepath.Dir(execPath)
	tmpFile, err := os.CreateTemp(dir, ".klaw-upgrade-*")
	if err != nil {
		if os.IsPermission(err) {
			return permissionError()
		}
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Clean up temp file on failure
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	// Download with progress
	pw := &progressWriter{total: resp.ContentLength}
	reader := io.TeeReader(resp.Body, pw)

	if _, err := io.Copy(tmpFile, reader); err != nil {
		tmpFile.Close()
		return fmt.Errorf("download interrupted: %w", err)
	}
	tmpFile.Close()
	fmt.Fprint(os.Stderr, "\n") // newline after progress

	// Make executable
	if err := os.Chmod(tmpPath, 0755); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Atomic replace
	if runtime.GOOS == "windows" {
		// Windows: rename current to .old, then rename new into place
		oldPath := execPath + ".old"
		os.Remove(oldPath) // ignore error
		if err := os.Rename(execPath, oldPath); err != nil {
			return fmt.Errorf("failed to replace binary (try running as Administrator): %w", err)
		}
		if err := os.Rename(tmpPath, execPath); err != nil {
			// Try to restore
			os.Rename(oldPath, execPath)
			return fmt.Errorf("failed to install new binary: %w", err)
		}
		os.Remove(oldPath) // best-effort cleanup
	} else {
		// Unix: atomic rename
		if err := os.Rename(tmpPath, execPath); err != nil {
			if os.IsPermission(err) {
				return permissionError()
			}
			return fmt.Errorf("failed to replace binary: %w", err)
		}
	}

	success = true
	return nil
}

func permissionError() error {
	if runtime.GOOS == "windows" {
		return fmt.Errorf("permission denied. Try running as Administrator")
	}
	return fmt.Errorf("permission denied. Try: sudo klaw upgrade")
}

// progressWriter tracks download progress and prints to stderr.
type progressWriter struct {
	total      int64
	downloaded int64
	lastPct    int
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	pw.downloaded += int64(len(p))

	if pw.total > 0 {
		pct := int(pw.downloaded * 100 / pw.total)
		if pct != pw.lastPct {
			fmt.Fprintf(os.Stderr, "\rDownloading... %d%%", pct)
			pw.lastPct = pct
		}
	} else {
		fmt.Fprintf(os.Stderr, "\rDownloading... %d KB", pw.downloaded/1024)
	}

	return len(p), nil
}
