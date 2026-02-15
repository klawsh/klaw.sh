// Package runtime manages agent execution in Podman containers.
package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultImage = "localhost/klaw:latest"
)

// Container represents a running agent container.
type Container struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	AgentName string    `json:"agent_name"`
	Task      string    `json:"task"`
	Status    string    `json:"status"` // "starting", "running", "stopped", "failed"
	StartedAt time.Time `json:"started_at"`
	StoppedAt time.Time `json:"stopped_at,omitempty"`
	WorkDir   string    `json:"workdir"`
	Error     string    `json:"error,omitempty"`
}

// PodmanRuntime manages agent containers via Podman.
type PodmanRuntime struct {
	image    string
	stateDir string
	mu       sync.RWMutex
	// Track containers we've started
	containers map[string]*Container
}

// NewPodmanRuntime creates a new Podman runtime manager.
func NewPodmanRuntime(stateDir string) *PodmanRuntime {
	os.MkdirAll(stateDir, 0755)
	return &PodmanRuntime{
		image:      DefaultImage,
		stateDir:   stateDir,
		containers: make(map[string]*Container),
	}
}

// SetImage sets the container image to use.
func (p *PodmanRuntime) SetImage(image string) {
	p.image = image
}

// StartConfig holds configuration for starting an agent container.
type StartConfig struct {
	AgentName string
	Task      string
	Model     string
	Tools     []string
	WorkDir   string
	APIKey    string
}

// CheckPodman verifies Podman is available and running.
func (p *PodmanRuntime) CheckPodman() error {
	cmd := exec.Command("podman", "info", "--format", "{{.Host.RemoteSocket.Exists}}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(`Podman is not running.

On macOS, start Podman with:
  podman machine init   # (first time only)
  podman machine start

On Linux, start the Podman service:
  systemctl --user start podman.socket

Error: %w`, err)
	}

	if strings.TrimSpace(string(output)) == "false" {
		return fmt.Errorf(`Podman socket not available.

On macOS:
  podman machine start

On Linux:
  systemctl --user start podman.socket`)
	}

	return nil
}

// Start starts a new agent container.
func (p *PodmanRuntime) Start(ctx context.Context, cfg StartConfig) (*Container, error) {
	// Check Podman first
	if err := p.CheckPodman(); err != nil {
		return nil, err
	}
	// Generate short ID
	id := "klaw-" + uuid.New().String()[:8]
	name := fmt.Sprintf("klaw-%s-%s", cfg.AgentName, id[5:])

	container := &Container{
		ID:        id,
		Name:      name,
		AgentName: cfg.AgentName,
		Task:      cfg.Task,
		Status:    "starting",
		StartedAt: time.Now(),
		WorkDir:   cfg.WorkDir,
	}

	// Build podman run command
	args := []string{
		"run",
		"-d",                              // detached
		"--name", name,                    // container name
		"--hostname", cfg.AgentName,       // hostname = agent name
		"-e", "ANTHROPIC_API_KEY=" + cfg.APIKey,
		"-e", "KLAW_MODEL=" + cfg.Model,
		"-e", "KLAW_TASK=" + cfg.Task,
	}

	// Mount workspace if specified
	if cfg.WorkDir != "" {
		args = append(args, "-v", cfg.WorkDir+":/workspace:z")
	}

	// Add image and command
	args = append(args, p.image, "worker",
		"--task", cfg.Task,
		"--model", cfg.Model,
	)

	cmd := exec.CommandContext(ctx, "podman", args...)
	// Bypass Docker credential helpers that may interfere
	cmd.Env = append(os.Environ(), "DOCKER_CONFIG=/tmp/klaw-docker-config")
	output, err := cmd.CombinedOutput()
	if err != nil {
		container.Status = "failed"
		container.Error = fmt.Sprintf("%v: %s", err, string(output))
		return container, fmt.Errorf("podman run failed: %w: %s", err, string(output))
	}

	// Get actual container ID from output
	containerID := strings.TrimSpace(string(output))
	if len(containerID) > 12 {
		container.ID = containerID[:12]
	}
	container.Status = "running"

	p.mu.Lock()
	p.containers[name] = container
	p.mu.Unlock()

	return container, nil
}

// Stop stops a running container.
func (p *PodmanRuntime) Stop(nameOrID string) error {
	cmd := exec.Command("podman", "stop", nameOrID)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("podman stop failed: %w", err)
	}

	// Remove container
	exec.Command("podman", "rm", nameOrID).Run()

	p.mu.Lock()
	delete(p.containers, nameOrID)
	p.mu.Unlock()

	return nil
}

// StopAll stops all klaw containers.
func (p *PodmanRuntime) StopAll() error {
	// Find all klaw containers
	cmd := exec.Command("podman", "ps", "-a", "--filter", "name=klaw-", "--format", "{{.Names}}")
	output, err := cmd.Output()
	if err != nil {
		return err
	}

	names := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, name := range names {
		if name != "" {
			p.Stop(name)
		}
	}

	return nil
}

// List returns all klaw containers.
func (p *PodmanRuntime) List() ([]*Container, error) {
	cmd := exec.Command("podman", "ps", "-a",
		"--filter", "name=klaw-",
		"--format", "json",
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	if len(output) == 0 || string(output) == "null\n" {
		return []*Container{}, nil
	}

	var podmanContainers []struct {
		ID      string   `json:"Id"`
		Names   []string `json:"Names"`
		State   string   `json:"State"`
		Created int64    `json:"Created"`
	}
	if err := json.Unmarshal(output, &podmanContainers); err != nil {
		return nil, fmt.Errorf("failed to parse podman output: %w", err)
	}

	containers := make([]*Container, 0, len(podmanContainers))
	for _, pc := range podmanContainers {
		name := ""
		if len(pc.Names) > 0 {
			name = pc.Names[0]
		}

		// Parse agent name from container name (klaw-<agent>-<id>)
		agentName := "unknown"
		parts := strings.SplitN(name, "-", 3)
		if len(parts) >= 2 {
			agentName = parts[1]
		}

		status := "stopped"
		if pc.State == "running" {
			status = "running"
		} else if pc.State == "exited" {
			status = "stopped"
		}

		containers = append(containers, &Container{
			ID:        pc.ID[:12],
			Name:      name,
			AgentName: agentName,
			Status:    status,
			StartedAt: time.Unix(pc.Created, 0),
		})
	}

	return containers, nil
}

// Get returns a container by name or ID.
func (p *PodmanRuntime) Get(nameOrID string) (*Container, error) {
	containers, err := p.List()
	if err != nil {
		return nil, err
	}

	for _, c := range containers {
		if c.Name == nameOrID || c.ID == nameOrID || strings.HasPrefix(c.ID, nameOrID) {
			return c, nil
		}
	}

	return nil, fmt.Errorf("container not found: %s", nameOrID)
}

// Logs returns the logs of a container.
func (p *PodmanRuntime) Logs(nameOrID string, follow bool) (*exec.Cmd, error) {
	args := []string{"logs"}
	if follow {
		args = append(args, "-f")
	}
	args = append(args, nameOrID)

	cmd := exec.Command("podman", args...)
	return cmd, nil
}

// Exec executes a command in a running container.
func (p *PodmanRuntime) Exec(nameOrID string, command []string) error {
	args := append([]string{"exec", "-it", nameOrID}, command...)
	cmd := exec.Command("podman", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Attach attaches to a container's stdin/stdout.
func (p *PodmanRuntime) Attach(nameOrID string) error {
	cmd := exec.Command("podman", "attach", nameOrID)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Build builds the klaw container image.
func (p *PodmanRuntime) Build(ctx context.Context, contextDir string) error {
	containerfile := filepath.Join(contextDir, "Containerfile")
	if _, err := os.Stat(containerfile); os.IsNotExist(err) {
		return fmt.Errorf("Containerfile not found in %s", contextDir)
	}

	cmd := exec.CommandContext(ctx, "podman", "build",
		"-t", p.image,
		"-f", containerfile,
		contextDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// ImageExists checks if the klaw image exists.
func (p *PodmanRuntime) ImageExists() bool {
	cmd := exec.Command("podman", "image", "exists", p.image)
	return cmd.Run() == nil
}

// StreamLogs streams logs from a container.
func (p *PodmanRuntime) StreamLogs(ctx context.Context, nameOrID string) (<-chan string, error) {
	cmd := exec.CommandContext(ctx, "podman", "logs", "-f", nameOrID)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	lines := make(chan string, 100)

	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			lines <- "[stderr] " + scanner.Text()
		}
	}()

	return lines, nil
}
