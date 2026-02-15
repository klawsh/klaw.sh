//go:build etcd

// Package controller provides etcd-based storage for distributed mode.
package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

const (
	keyPrefix     = "/klaw/"
	nodesPrefix   = keyPrefix + "nodes/"
	agentsPrefix  = keyPrefix + "agents/"
	tasksPrefix   = keyPrefix + "tasks/"
	leaderKey     = keyPrefix + "leader"
)

// EtcdStore implements Store using etcd
type EtcdStore struct {
	client  *clientv3.Client
	session *concurrency.Session
	leaseID clientv3.LeaseID
}

// EtcdConfig holds etcd connection configuration
type EtcdConfig struct {
	Endpoints   []string
	DialTimeout time.Duration
	Username    string
	Password    string
	TLSCert     string
	TLSKey      string
	TLSCA       string
}

// NewEtcdStore creates a new etcd-based store
func NewEtcdStore(cfg EtcdConfig) (*EtcdStore, error) {
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 5 * time.Second
	}

	config := clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: cfg.DialTimeout,
		Username:    cfg.Username,
		Password:    cfg.Password,
	}

	// TODO: Add TLS configuration

	client, err := clientv3.New(config)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}

	return &EtcdStore{
		client: client,
	}, nil
}

// Node operations
func (es *EtcdStore) GetNode(ctx context.Context, id string) (*Node, error) {
	resp, err := es.client.Get(ctx, nodesPrefix+id)
	if err != nil {
		return nil, err
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("node not found: %s", id)
	}

	var node Node
	if err := json.Unmarshal(resp.Kvs[0].Value, &node); err != nil {
		return nil, err
	}
	return &node, nil
}

func (es *EtcdStore) ListNodes(ctx context.Context) ([]*Node, error) {
	resp, err := es.client.Get(ctx, nodesPrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	var nodes []*Node
	for _, kv := range resp.Kvs {
		var node Node
		if err := json.Unmarshal(kv.Value, &node); err == nil {
			nodes = append(nodes, &node)
		}
	}
	return nodes, nil
}

func (es *EtcdStore) SaveNode(ctx context.Context, node *Node) error {
	data, err := json.Marshal(node)
	if err != nil {
		return err
	}
	_, err = es.client.Put(ctx, nodesPrefix+node.ID, string(data))
	return err
}

func (es *EtcdStore) DeleteNode(ctx context.Context, id string) error {
	_, err := es.client.Delete(ctx, nodesPrefix+id)
	return err
}

// Agent operations
func (es *EtcdStore) GetAgent(ctx context.Context, id string) (*Agent, error) {
	resp, err := es.client.Get(ctx, agentsPrefix+id)
	if err != nil {
		return nil, err
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("agent not found: %s", id)
	}

	var agent Agent
	if err := json.Unmarshal(resp.Kvs[0].Value, &agent); err != nil {
		return nil, err
	}
	return &agent, nil
}

func (es *EtcdStore) ListAgents(ctx context.Context) ([]*Agent, error) {
	resp, err := es.client.Get(ctx, agentsPrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	var agents []*Agent
	for _, kv := range resp.Kvs {
		var agent Agent
		if err := json.Unmarshal(kv.Value, &agent); err == nil {
			agents = append(agents, &agent)
		}
	}
	return agents, nil
}

func (es *EtcdStore) ListAgentsByNode(ctx context.Context, nodeID string) ([]*Agent, error) {
	agents, err := es.ListAgents(ctx)
	if err != nil {
		return nil, err
	}

	var filtered []*Agent
	for _, a := range agents {
		if a.NodeID == nodeID {
			filtered = append(filtered, a)
		}
	}
	return filtered, nil
}

func (es *EtcdStore) SaveAgent(ctx context.Context, agent *Agent) error {
	data, err := json.Marshal(agent)
	if err != nil {
		return err
	}
	_, err = es.client.Put(ctx, agentsPrefix+agent.ID, string(data))
	return err
}

func (es *EtcdStore) DeleteAgent(ctx context.Context, id string) error {
	_, err := es.client.Delete(ctx, agentsPrefix+id)
	return err
}

// Task operations
func (es *EtcdStore) GetTask(ctx context.Context, id string) (*Task, error) {
	resp, err := es.client.Get(ctx, tasksPrefix+id)
	if err != nil {
		return nil, err
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("task not found: %s", id)
	}

	var task Task
	if err := json.Unmarshal(resp.Kvs[0].Value, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

func (es *EtcdStore) ListPendingTasks(ctx context.Context) ([]*Task, error) {
	resp, err := es.client.Get(ctx, tasksPrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	var tasks []*Task
	for _, kv := range resp.Kvs {
		var task Task
		if err := json.Unmarshal(kv.Value, &task); err == nil {
			if task.Status == "pending" {
				tasks = append(tasks, &task)
			}
		}
	}
	return tasks, nil
}

func (es *EtcdStore) SaveTask(ctx context.Context, task *Task) error {
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}
	_, err = es.client.Put(ctx, tasksPrefix+task.ID, string(data))
	return err
}

func (es *EtcdStore) DeleteTask(ctx context.Context, id string) error {
	_, err := es.client.Delete(ctx, tasksPrefix+id)
	return err
}

// Leader election
func (es *EtcdStore) TryBecomeLeader(ctx context.Context, controllerID string, ttl time.Duration) (bool, error) {
	// Create session with TTL
	session, err := concurrency.NewSession(es.client, concurrency.WithTTL(int(ttl.Seconds())))
	if err != nil {
		return false, err
	}
	es.session = session

	// Try to become leader
	election := concurrency.NewElection(session, keyPrefix+"election/")

	// Campaign with timeout
	campaignCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err = election.Campaign(campaignCtx, controllerID)
	if err != nil {
		return false, nil // Someone else is leader
	}

	return true, nil
}

func (es *EtcdStore) RenewLeadership(ctx context.Context, controllerID string, ttl time.Duration) error {
	if es.session != nil {
		// Session auto-renews with keepalive
		return nil
	}
	return fmt.Errorf("no active session")
}

func (es *EtcdStore) GetLeader(ctx context.Context) (string, error) {
	resp, err := es.client.Get(ctx, leaderKey)
	if err != nil {
		return "", err
	}
	if len(resp.Kvs) == 0 {
		return "", nil
	}
	return string(resp.Kvs[0].Value), nil
}

// Watch for changes
func (es *EtcdStore) Watch(ctx context.Context, prefix string) (<-chan WatchEvent, error) {
	ch := make(chan WatchEvent, 100)

	go func() {
		defer close(ch)

		watchCh := es.client.Watch(ctx, keyPrefix+prefix, clientv3.WithPrefix())
		for resp := range watchCh {
			for _, ev := range resp.Events {
				event := WatchEvent{
					Key:   string(ev.Kv.Key),
					Value: ev.Kv.Value,
				}
				switch ev.Type {
				case clientv3.EventTypePut:
					event.Type = "put"
				case clientv3.EventTypeDelete:
					event.Type = "delete"
				}
				select {
				case ch <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}

func (es *EtcdStore) Close() error {
	if es.session != nil {
		es.session.Close()
	}
	return es.client.Close()
}
