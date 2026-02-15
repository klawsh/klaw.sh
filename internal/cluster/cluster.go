// Package cluster manages multi-tenant cluster and namespace isolation.
package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Cluster represents a top-level isolation boundary (per company/organization).
type Cluster struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name,omitempty"`
	Description string            `json:"description,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// Namespace represents a subdivision within a cluster (per team/department).
type Namespace struct {
	Name         string              `json:"name"`
	Cluster      string              `json:"cluster"`
	DisplayName  string              `json:"display_name,omitempty"`
	Description  string              `json:"description,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
	Labels       map[string]string   `json:"labels,omitempty"`
	Orchestrator *OrchestratorConfig `json:"orchestrator,omitempty"`
}

// OrchestratorConfig defines how messages are routed in a namespace.
type OrchestratorConfig struct {
	Mode         string        `json:"mode"`          // "ai", "rules", "hybrid", "disabled"
	DefaultAgent string        `json:"default_agent"` // fallback agent
	AllowManual  bool          `json:"allow_manual"`  // allow @agent syntax
	Rules        []RoutingRule `json:"rules,omitempty"`
}

// RoutingRule defines keyword-based routing.
type RoutingRule struct {
	Match string `json:"match"` // regex pattern
	Agent string `json:"agent"` // target agent name
}

// AgentBinding connects an agent to a namespace.
type AgentBinding struct {
	Name         string    `json:"name"`
	Cluster      string    `json:"cluster"`
	Namespace    string    `json:"namespace"`
	Description  string    `json:"description"`
	SystemPrompt string    `json:"system_prompt,omitempty"`
	Model        string    `json:"model,omitempty"`
	Tools        []string  `json:"tools,omitempty"`
	Skills       []string  `json:"skills,omitempty"`   // installed skills (web-search, browser, etc.)
	Triggers     []string  `json:"triggers,omitempty"` // keywords for routing
	CreatedAt    time.Time `json:"created_at"`
}

// ChannelBinding connects a channel to a namespace.
type ChannelBinding struct {
	Name      string            `json:"name"`
	Type      string            `json:"type"` // slack, discord, telegram
	Cluster   string            `json:"cluster"`
	Namespace string            `json:"namespace"`
	Config    map[string]string `json:"config"` // tokens, settings
	CreatedAt time.Time         `json:"created_at"`
	Status    string            `json:"status"` // active, inactive
}

// Store manages cluster, namespace, and channel binding persistence.
type Store struct {
	baseDir string
}

// NewStore creates a new cluster store.
func NewStore(baseDir string) *Store {
	return &Store{baseDir: baseDir}
}

// --- Cluster Operations ---

func (s *Store) clustersDir() string {
	return filepath.Join(s.baseDir, "clusters")
}

func (s *Store) clusterFile(name string) string {
	return filepath.Join(s.clustersDir(), name+".json")
}

func (s *Store) CreateCluster(c *Cluster) error {
	if c.Name == "" {
		return fmt.Errorf("cluster name required")
	}

	if s.ClusterExists(c.Name) {
		return fmt.Errorf("cluster already exists: %s", c.Name)
	}

	c.CreatedAt = time.Now()

	if err := os.MkdirAll(s.clustersDir(), 0755); err != nil {
		return err
	}

	// Create default namespace
	if err := s.CreateNamespace(&Namespace{
		Name:    "default",
		Cluster: c.Name,
	}); err != nil {
		return fmt.Errorf("failed to create default namespace: %w", err)
	}

	return s.saveCluster(c)
}

func (s *Store) saveCluster(c *Cluster) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.clusterFile(c.Name), data, 0644)
}

func (s *Store) GetCluster(name string) (*Cluster, error) {
	data, err := os.ReadFile(s.clusterFile(name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("cluster not found: %s", name)
		}
		return nil, err
	}

	var c Cluster
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) ListClusters() ([]*Cluster, error) {
	if err := os.MkdirAll(s.clustersDir(), 0755); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(s.clustersDir())
	if err != nil {
		return nil, err
	}

	var clusters []*Cluster
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		name := entry.Name()[:len(entry.Name())-5] // remove .json
		c, err := s.GetCluster(name)
		if err != nil {
			continue
		}
		clusters = append(clusters, c)
	}
	return clusters, nil
}

func (s *Store) DeleteCluster(name string) error {
	// Delete all namespaces first
	namespaces, _ := s.ListNamespaces(name)
	for _, ns := range namespaces {
		s.DeleteNamespace(name, ns.Name)
	}

	// Delete cluster file
	if err := os.Remove(s.clusterFile(name)); err != nil && !os.IsNotExist(err) {
		return err
	}

	// Remove cluster directory
	clusterDir := filepath.Join(s.baseDir, "namespaces", name)
	os.RemoveAll(clusterDir)

	return nil
}

func (s *Store) ClusterExists(name string) bool {
	_, err := os.Stat(s.clusterFile(name))
	return err == nil
}

// --- Namespace Operations ---

func (s *Store) namespacesDir(cluster string) string {
	return filepath.Join(s.baseDir, "namespaces", cluster)
}

func (s *Store) namespaceFile(cluster, name string) string {
	return filepath.Join(s.namespacesDir(cluster), name+".json")
}

func (s *Store) CreateNamespace(ns *Namespace) error {
	if ns.Name == "" || ns.Cluster == "" {
		return fmt.Errorf("namespace name and cluster required")
	}

	if s.NamespaceExists(ns.Cluster, ns.Name) {
		return fmt.Errorf("namespace already exists: %s/%s", ns.Cluster, ns.Name)
	}

	ns.CreatedAt = time.Now()

	if err := os.MkdirAll(s.namespacesDir(ns.Cluster), 0755); err != nil {
		return err
	}

	return s.saveNamespace(ns)
}

func (s *Store) saveNamespace(ns *Namespace) error {
	data, err := json.MarshalIndent(ns, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.namespaceFile(ns.Cluster, ns.Name), data, 0644)
}

func (s *Store) GetNamespace(cluster, name string) (*Namespace, error) {
	data, err := os.ReadFile(s.namespaceFile(cluster, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("namespace not found: %s/%s", cluster, name)
		}
		return nil, err
	}

	var ns Namespace
	if err := json.Unmarshal(data, &ns); err != nil {
		return nil, err
	}
	return &ns, nil
}

func (s *Store) ListNamespaces(cluster string) ([]*Namespace, error) {
	if err := os.MkdirAll(s.namespacesDir(cluster), 0755); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(s.namespacesDir(cluster))
	if err != nil {
		if os.IsNotExist(err) {
			return []*Namespace{}, nil
		}
		return nil, err
	}

	var namespaces []*Namespace
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		name := entry.Name()[:len(entry.Name())-5]
		ns, err := s.GetNamespace(cluster, name)
		if err != nil {
			continue
		}
		namespaces = append(namespaces, ns)
	}
	return namespaces, nil
}

func (s *Store) DeleteNamespace(cluster, name string) error {
	if name == "default" {
		return fmt.Errorf("cannot delete default namespace")
	}

	// Delete all channel bindings in namespace
	bindings, _ := s.ListChannelBindings(cluster, name)
	for _, b := range bindings {
		s.DeleteChannelBinding(cluster, name, b.Name)
	}

	return os.Remove(s.namespaceFile(cluster, name))
}

func (s *Store) NamespaceExists(cluster, name string) bool {
	_, err := os.Stat(s.namespaceFile(cluster, name))
	return err == nil
}

// --- Channel Binding Operations ---

func (s *Store) channelBindingsDir(cluster, namespace string) string {
	return filepath.Join(s.baseDir, "channels", cluster, namespace)
}

func (s *Store) channelBindingFile(cluster, namespace, name string) string {
	return filepath.Join(s.channelBindingsDir(cluster, namespace), name+".json")
}

func (s *Store) CreateChannelBinding(cb *ChannelBinding) error {
	if cb.Name == "" || cb.Cluster == "" || cb.Namespace == "" {
		return fmt.Errorf("channel name, cluster, and namespace required")
	}

	if !s.ClusterExists(cb.Cluster) {
		return fmt.Errorf("cluster not found: %s", cb.Cluster)
	}

	if !s.NamespaceExists(cb.Cluster, cb.Namespace) {
		return fmt.Errorf("namespace not found: %s/%s", cb.Cluster, cb.Namespace)
	}

	cb.CreatedAt = time.Now()
	cb.Status = "inactive"

	if err := os.MkdirAll(s.channelBindingsDir(cb.Cluster, cb.Namespace), 0755); err != nil {
		return err
	}

	return s.saveChannelBinding(cb)
}

func (s *Store) saveChannelBinding(cb *ChannelBinding) error {
	data, err := json.MarshalIndent(cb, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.channelBindingFile(cb.Cluster, cb.Namespace, cb.Name), data, 0644)
}

func (s *Store) GetChannelBinding(cluster, namespace, name string) (*ChannelBinding, error) {
	data, err := os.ReadFile(s.channelBindingFile(cluster, namespace, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("channel not found: %s/%s/%s", cluster, namespace, name)
		}
		return nil, err
	}

	var cb ChannelBinding
	if err := json.Unmarshal(data, &cb); err != nil {
		return nil, err
	}
	return &cb, nil
}

func (s *Store) ListChannelBindings(cluster, namespace string) ([]*ChannelBinding, error) {
	dir := s.channelBindingsDir(cluster, namespace)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*ChannelBinding{}, nil
		}
		return nil, err
	}

	var bindings []*ChannelBinding
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		name := entry.Name()[:len(entry.Name())-5]
		cb, err := s.GetChannelBinding(cluster, namespace, name)
		if err != nil {
			continue
		}
		bindings = append(bindings, cb)
	}
	return bindings, nil
}

func (s *Store) ListAllChannelBindings(cluster string) ([]*ChannelBinding, error) {
	namespaces, err := s.ListNamespaces(cluster)
	if err != nil {
		return nil, err
	}

	var all []*ChannelBinding
	for _, ns := range namespaces {
		bindings, err := s.ListChannelBindings(cluster, ns.Name)
		if err != nil {
			continue
		}
		all = append(all, bindings...)
	}
	return all, nil
}

func (s *Store) DeleteChannelBinding(cluster, namespace, name string) error {
	return os.Remove(s.channelBindingFile(cluster, namespace, name))
}

func (s *Store) UpdateChannelBindingStatus(cluster, namespace, name, status string) error {
	cb, err := s.GetChannelBinding(cluster, namespace, name)
	if err != nil {
		return err
	}
	cb.Status = status
	return s.saveChannelBinding(cb)
}

// --- Agent Binding Operations ---

func (s *Store) agentBindingsDir(cluster, namespace string) string {
	return filepath.Join(s.baseDir, "agents", cluster, namespace)
}

func (s *Store) agentBindingFile(cluster, namespace, name string) string {
	return filepath.Join(s.agentBindingsDir(cluster, namespace), name+".json")
}

func (s *Store) CreateAgentBinding(ab *AgentBinding) error {
	if ab.Name == "" || ab.Cluster == "" || ab.Namespace == "" {
		return fmt.Errorf("agent name, cluster, and namespace required")
	}

	if !s.ClusterExists(ab.Cluster) {
		return fmt.Errorf("cluster not found: %s", ab.Cluster)
	}

	if !s.NamespaceExists(ab.Cluster, ab.Namespace) {
		return fmt.Errorf("namespace not found: %s/%s", ab.Cluster, ab.Namespace)
	}

	ab.CreatedAt = time.Now()

	if err := os.MkdirAll(s.agentBindingsDir(ab.Cluster, ab.Namespace), 0755); err != nil {
		return err
	}

	return s.saveAgentBinding(ab)
}

func (s *Store) saveAgentBinding(ab *AgentBinding) error {
	data, err := json.MarshalIndent(ab, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.agentBindingFile(ab.Cluster, ab.Namespace, ab.Name), data, 0644)
}

func (s *Store) GetAgentBinding(cluster, namespace, name string) (*AgentBinding, error) {
	data, err := os.ReadFile(s.agentBindingFile(cluster, namespace, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("agent not found: %s/%s/%s", cluster, namespace, name)
		}
		return nil, err
	}

	var ab AgentBinding
	if err := json.Unmarshal(data, &ab); err != nil {
		return nil, err
	}
	return &ab, nil
}

func (s *Store) ListAgentBindings(cluster, namespace string) ([]*AgentBinding, error) {
	dir := s.agentBindingsDir(cluster, namespace)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*AgentBinding{}, nil
		}
		return nil, err
	}

	var bindings []*AgentBinding
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		name := entry.Name()[:len(entry.Name())-5]
		ab, err := s.GetAgentBinding(cluster, namespace, name)
		if err != nil {
			continue
		}
		bindings = append(bindings, ab)
	}
	return bindings, nil
}

func (s *Store) DeleteAgentBinding(cluster, namespace, name string) error {
	return os.Remove(s.agentBindingFile(cluster, namespace, name))
}

func (s *Store) UpdateAgentBinding(ab *AgentBinding) error {
	if ab.Name == "" || ab.Cluster == "" || ab.Namespace == "" {
		return fmt.Errorf("agent name, cluster, and namespace required")
	}

	// Get existing to preserve CreatedAt
	existing, err := s.GetAgentBinding(ab.Cluster, ab.Namespace, ab.Name)
	if err != nil {
		return err
	}
	ab.CreatedAt = existing.CreatedAt

	return s.saveAgentBinding(ab)
}

func (s *Store) AgentBindingExists(cluster, namespace, name string) bool {
	_, err := os.Stat(s.agentBindingFile(cluster, namespace, name))
	return err == nil
}

// UpdateNamespaceOrchestrator updates the orchestrator config for a namespace.
func (s *Store) UpdateNamespaceOrchestrator(cluster, namespace string, cfg *OrchestratorConfig) error {
	ns, err := s.GetNamespace(cluster, namespace)
	if err != nil {
		return err
	}
	ns.Orchestrator = cfg
	return s.saveNamespace(ns)
}

// --- Message Log Operations ---

// MessageLog represents a conversation message.
type MessageLog struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Channel   string    `json:"channel"`
	User      string    `json:"user"`
	Agent     string    `json:"agent"`
	Content   string    `json:"content"`
	Response  string    `json:"response,omitempty"`
	RoutedVia string    `json:"routed_via"` // "manual", "keyword", "ai"
}

func (s *Store) logsDir(cluster, namespace, channel string) string {
	return filepath.Join(s.baseDir, "logs", cluster, namespace, channel)
}

func (s *Store) logFile(cluster, namespace, channel string) string {
	// Use date-based log files
	date := time.Now().Format("2006-01-02")
	return filepath.Join(s.logsDir(cluster, namespace, channel), date+".json")
}

// AppendMessageLog adds a message to the channel's log.
func (s *Store) AppendMessageLog(cluster, namespace, channel string, msg *MessageLog) error {
	dir := s.logsDir(cluster, namespace, channel)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	msg.Timestamp = time.Now()

	// Read existing logs
	logPath := s.logFile(cluster, namespace, channel)
	var logs []*MessageLog

	data, err := os.ReadFile(logPath)
	if err == nil {
		json.Unmarshal(data, &logs)
	}

	logs = append(logs, msg)

	// Keep only last 1000 messages per day
	if len(logs) > 1000 {
		logs = logs[len(logs)-1000:]
	}

	data, err = json.MarshalIndent(logs, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(logPath, data, 0644)
}

// GetMessageLogs retrieves recent messages for a channel.
func (s *Store) GetMessageLogs(cluster, namespace, channel string, limit int) ([]*MessageLog, error) {
	dir := s.logsDir(cluster, namespace, channel)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*MessageLog{}, nil
		}
		return nil, err
	}

	var allLogs []*MessageLog

	// Read logs from most recent files first
	for i := len(entries) - 1; i >= 0 && len(allLogs) < limit; i-- {
		entry := entries[i]
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}

		var logs []*MessageLog
		if err := json.Unmarshal(data, &logs); err != nil {
			continue
		}

		// Prepend to get chronological order
		allLogs = append(logs, allLogs...)
	}

	// Return only the requested limit (most recent)
	if len(allLogs) > limit {
		allLogs = allLogs[len(allLogs)-limit:]
	}

	return allLogs, nil
}
