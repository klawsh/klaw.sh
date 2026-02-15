// Package controller provides the klaw controller implementation.
package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store is the interface for controller state storage.
type Store interface {
	// Nodes
	GetNode(ctx context.Context, id string) (*Node, error)
	ListNodes(ctx context.Context) ([]*Node, error)
	SaveNode(ctx context.Context, node *Node) error
	DeleteNode(ctx context.Context, id string) error

	// Agents
	GetAgent(ctx context.Context, id string) (*Agent, error)
	ListAgents(ctx context.Context) ([]*Agent, error)
	ListAgentsByNode(ctx context.Context, nodeID string) ([]*Agent, error)
	SaveAgent(ctx context.Context, agent *Agent) error
	DeleteAgent(ctx context.Context, id string) error

	// Tasks
	GetTask(ctx context.Context, id string) (*Task, error)
	ListPendingTasks(ctx context.Context) ([]*Task, error)
	SaveTask(ctx context.Context, task *Task) error
	DeleteTask(ctx context.Context, id string) error

	// Leader election (for HA)
	TryBecomeLeader(ctx context.Context, controllerID string, ttl time.Duration) (bool, error)
	RenewLeadership(ctx context.Context, controllerID string, ttl time.Duration) error
	GetLeader(ctx context.Context) (string, error)

	// Watch for changes (optional, for etcd)
	Watch(ctx context.Context, prefix string) (<-chan WatchEvent, error)

	// Close
	Close() error
}

// WatchEvent represents a change in the store
type WatchEvent struct {
	Type  string // "put", "delete"
	Key   string
	Value []byte
}

// Node represents a klaw node
type Node struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Address   string            `json:"address"`
	Labels    map[string]string `json:"labels,omitempty"`
	Status    string            `json:"status"` // "ready", "not-ready", "disconnected"
	AgentIDs  []string          `json:"agent_ids,omitempty"`
	Resources *Resources        `json:"resources,omitempty"`
	LastSeen  time.Time         `json:"last_seen"`
	JoinedAt  time.Time         `json:"joined_at"`
	Version   string            `json:"version,omitempty"`
}

// Resources represents node resources
type Resources struct {
	CPUCores    int   `json:"cpu_cores"`
	MemoryMB    int64 `json:"memory_mb"`
	MaxAgents   int   `json:"max_agents"`
	RunningJobs int   `json:"running_jobs"`
}

// Agent represents an AI agent registered with the controller
type Agent struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	NodeID       string    `json:"node_id"`
	Cluster      string    `json:"cluster"`
	Namespace    string    `json:"namespace"`
	Description  string    `json:"description"`
	Model        string    `json:"model"`
	Skills       []string  `json:"skills,omitempty"`
	Status       string    `json:"status"` // "running", "stopped", "error"
	SystemPrompt string    `json:"system_prompt,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	LastActive   time.Time `json:"last_active"`
}

// Task represents a task to be executed by an agent
type Task struct {
	ID         string        `json:"id"`
	Type       string        `json:"type"` // "message", "cron", "manual"
	AgentID    string        `json:"agent_id"`
	AgentName  string        `json:"agent_name"`
	NodeID     string        `json:"node_id"`
	Prompt     string        `json:"prompt"`
	Priority   int           `json:"priority"`
	Timeout    time.Duration `json:"timeout"`
	Status     string        `json:"status"` // "pending", "dispatched", "running", "completed", "failed"
	Result     string        `json:"result,omitempty"`
	Error      string        `json:"error,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
	StartedAt  *time.Time    `json:"started_at,omitempty"`
	FinishedAt *time.Time    `json:"finished_at,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// ============================================================================
// File-based Store Implementation
// ============================================================================

// FileStore implements Store using local filesystem
type FileStore struct {
	dataDir string
	mu      sync.RWMutex

	// In-memory cache
	nodes  map[string]*Node
	agents map[string]*Agent
	tasks  map[string]*Task
	leader string
}

// NewFileStore creates a new file-based store
func NewFileStore(dataDir string) (*FileStore, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	fs := &FileStore{
		dataDir: dataDir,
		nodes:   make(map[string]*Node),
		agents:  make(map[string]*Agent),
		tasks:   make(map[string]*Task),
	}

	// Load existing data
	if err := fs.load(); err != nil {
		return nil, err
	}

	return fs, nil
}

func (fs *FileStore) load() error {
	// Load nodes
	nodesFile := filepath.Join(fs.dataDir, "nodes.json")
	if data, err := os.ReadFile(nodesFile); err == nil {
		var nodes []*Node
		if err := json.Unmarshal(data, &nodes); err == nil {
			for _, n := range nodes {
				fs.nodes[n.ID] = n
			}
		}
	}

	// Load agents
	agentsFile := filepath.Join(fs.dataDir, "agents.json")
	if data, err := os.ReadFile(agentsFile); err == nil {
		var agents []*Agent
		if err := json.Unmarshal(data, &agents); err == nil {
			for _, a := range agents {
				fs.agents[a.ID] = a
			}
		}
	}

	// Load tasks
	tasksFile := filepath.Join(fs.dataDir, "tasks.json")
	if data, err := os.ReadFile(tasksFile); err == nil {
		var tasks []*Task
		if err := json.Unmarshal(data, &tasks); err == nil {
			for _, t := range tasks {
				fs.tasks[t.ID] = t
			}
		}
	}

	return nil
}

func (fs *FileStore) save() error {
	// Save nodes
	var nodes []*Node
	for _, n := range fs.nodes {
		nodes = append(nodes, n)
	}
	if data, err := json.MarshalIndent(nodes, "", "  "); err == nil {
		os.WriteFile(filepath.Join(fs.dataDir, "nodes.json"), data, 0644)
	}

	// Save agents
	var agents []*Agent
	for _, a := range fs.agents {
		agents = append(agents, a)
	}
	if data, err := json.MarshalIndent(agents, "", "  "); err == nil {
		os.WriteFile(filepath.Join(fs.dataDir, "agents.json"), data, 0644)
	}

	// Save tasks
	var tasks []*Task
	for _, t := range fs.tasks {
		tasks = append(tasks, t)
	}
	if data, err := json.MarshalIndent(tasks, "", "  "); err == nil {
		os.WriteFile(filepath.Join(fs.dataDir, "tasks.json"), data, 0644)
	}

	return nil
}

// Node operations
func (fs *FileStore) GetNode(ctx context.Context, id string) (*Node, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	node, ok := fs.nodes[id]
	if !ok {
		return nil, fmt.Errorf("node not found: %s", id)
	}
	return node, nil
}

func (fs *FileStore) ListNodes(ctx context.Context) ([]*Node, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	var nodes []*Node
	for _, n := range fs.nodes {
		nodes = append(nodes, n)
	}
	return nodes, nil
}

func (fs *FileStore) SaveNode(ctx context.Context, node *Node) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.nodes[node.ID] = node
	return fs.save()
}

func (fs *FileStore) DeleteNode(ctx context.Context, id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	delete(fs.nodes, id)
	return fs.save()
}

// Agent operations
func (fs *FileStore) GetAgent(ctx context.Context, id string) (*Agent, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	agent, ok := fs.agents[id]
	if !ok {
		return nil, fmt.Errorf("agent not found: %s", id)
	}
	return agent, nil
}

func (fs *FileStore) ListAgents(ctx context.Context) ([]*Agent, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	var agents []*Agent
	for _, a := range fs.agents {
		agents = append(agents, a)
	}
	return agents, nil
}

func (fs *FileStore) ListAgentsByNode(ctx context.Context, nodeID string) ([]*Agent, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	var agents []*Agent
	for _, a := range fs.agents {
		if a.NodeID == nodeID {
			agents = append(agents, a)
		}
	}
	return agents, nil
}

func (fs *FileStore) SaveAgent(ctx context.Context, agent *Agent) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.agents[agent.ID] = agent
	return fs.save()
}

func (fs *FileStore) DeleteAgent(ctx context.Context, id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	delete(fs.agents, id)
	return fs.save()
}

// Task operations
func (fs *FileStore) GetTask(ctx context.Context, id string) (*Task, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	task, ok := fs.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	return task, nil
}

func (fs *FileStore) ListPendingTasks(ctx context.Context) ([]*Task, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	var tasks []*Task
	for _, t := range fs.tasks {
		if t.Status == "pending" {
			tasks = append(tasks, t)
		}
	}
	return tasks, nil
}

func (fs *FileStore) SaveTask(ctx context.Context, task *Task) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.tasks[task.ID] = task
	return fs.save()
}

func (fs *FileStore) DeleteTask(ctx context.Context, id string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	delete(fs.tasks, id)
	return fs.save()
}

// Leader election (single controller mode - always leader)
func (fs *FileStore) TryBecomeLeader(ctx context.Context, controllerID string, ttl time.Duration) (bool, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.leader = controllerID
	return true, nil
}

func (fs *FileStore) RenewLeadership(ctx context.Context, controllerID string, ttl time.Duration) error {
	return nil
}

func (fs *FileStore) GetLeader(ctx context.Context) (string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.leader, nil
}

// Watch - not implemented for file store
func (fs *FileStore) Watch(ctx context.Context, prefix string) (<-chan WatchEvent, error) {
	// File store doesn't support watching
	ch := make(chan WatchEvent)
	close(ch)
	return ch, nil
}

func (fs *FileStore) Close() error {
	return fs.save()
}
