package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Context holds the current cluster and namespace selection.
type Context struct {
	CurrentCluster   string `json:"current_cluster"`
	CurrentNamespace string `json:"current_namespace"`
}

// ContextManager manages the active cluster/namespace context.
type ContextManager struct {
	configDir string
}

// NewContextManager creates a context manager.
func NewContextManager(configDir string) *ContextManager {
	return &ContextManager{configDir: configDir}
}

func (m *ContextManager) contextFile() string {
	return filepath.Join(m.configDir, "context.json")
}

// Get returns the current context.
func (m *ContextManager) Get() (*Context, error) {
	data, err := os.ReadFile(m.contextFile())
	if err != nil {
		if os.IsNotExist(err) {
			return &Context{}, nil
		}
		return nil, err
	}

	var ctx Context
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, err
	}
	return &ctx, nil
}

// Set saves the current context.
func (m *ContextManager) Set(ctx *Context) error {
	if err := os.MkdirAll(m.configDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.contextFile(), data, 0644)
}

// SetCluster sets the current cluster (resets namespace to default).
func (m *ContextManager) SetCluster(cluster string) error {
	ctx, err := m.Get()
	if err != nil {
		ctx = &Context{}
	}
	ctx.CurrentCluster = cluster
	ctx.CurrentNamespace = "default"
	return m.Set(ctx)
}

// SetNamespace sets the current namespace.
func (m *ContextManager) SetNamespace(namespace string) error {
	ctx, err := m.Get()
	if err != nil {
		return fmt.Errorf("no context set")
	}
	if ctx.CurrentCluster == "" {
		return fmt.Errorf("no cluster selected, use 'klaw config use-cluster <name>' first")
	}
	ctx.CurrentNamespace = namespace
	return m.Set(ctx)
}

// GetCurrent returns cluster and namespace, with defaults.
func (m *ContextManager) GetCurrent() (cluster, namespace string, err error) {
	ctx, err := m.Get()
	if err != nil {
		return "", "", err
	}

	cluster = ctx.CurrentCluster
	namespace = ctx.CurrentNamespace

	if namespace == "" {
		namespace = "default"
	}

	return cluster, namespace, nil
}

// RequireCurrent returns error if no cluster is selected.
func (m *ContextManager) RequireCurrent() (cluster, namespace string, err error) {
	cluster, namespace, err = m.GetCurrent()
	if err != nil {
		return "", "", err
	}
	if cluster == "" {
		return "", "", fmt.Errorf("no cluster selected\n\nCreate one with: klaw create cluster <name>\nThen select it: klaw config use-cluster <name>")
	}
	return cluster, namespace, nil
}
