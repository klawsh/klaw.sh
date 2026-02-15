// Package controller provides the klaw controller gRPC server.
package controller

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Server is the klaw controller server
type Server struct {
	config   ServerConfig
	store    Store
	listener net.Listener

	// Connected nodes
	nodes   map[string]*connectedNode
	nodesMu sync.RWMutex

	// Task channels per node
	taskChans   map[string]chan *Task
	taskChansMu sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type connectedNode struct {
	node       *Node
	lastPing   time.Time
	taskChan   chan *Task
	resultChan chan *TaskResult
}

// TaskResult holds the result of a task execution
type TaskResult struct {
	TaskID  string
	Success bool
	Output  string
	Error   string
}

// ServerConfig holds server configuration
type ServerConfig struct {
	Port       int
	DataDir    string
	AuthToken  string
	StoreType  string   // "file" or "etcd"
	EtcdAddrs  []string
	TLSEnabled bool
	TLSCert    string
	TLSKey     string
}

// NewServer creates a new controller server
func NewServer(cfg ServerConfig) (*Server, error) {
	// Create store based on type
	var store Store
	var err error

	switch cfg.StoreType {
	case "etcd":
		// etcd store would be created here
		// For now, fall back to file store
		store, err = NewFileStore(cfg.DataDir)
	default:
		store, err = NewFileStore(cfg.DataDir)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Server{
		config:    cfg,
		store:     store,
		nodes:     make(map[string]*connectedNode),
		taskChans: make(map[string]chan *Task),
		ctx:       ctx,
		cancel:    cancel,
	}, nil
}

// Start starts the controller server
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.config.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	s.listener = listener

	// Start background tasks
	s.wg.Add(1)
	go s.heartbeatChecker()

	fmt.Printf("🚀 Klaw Controller started on %s\n", addr)
	fmt.Println()
	fmt.Println("Waiting for nodes to connect...")
	fmt.Printf("  Nodes join with: klaw node join %s\n", addr)

	// Accept connections
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return nil
			default:
				fmt.Printf("Accept error: %v\n", err)
				continue
			}
		}

		go s.handleConnection(conn)
	}
}

// Stop stops the controller server
func (s *Server) Stop() error {
	s.cancel()

	if s.listener != nil {
		s.listener.Close()
	}

	s.wg.Wait()
	return s.store.Close()
}

// handleConnection handles a new node connection
func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Read first message
	// For simplicity, using JSON over TCP
	// In production, use proper gRPC

	decoder := newMessageDecoder(conn)
	encoder := newMessageEncoder(conn)

	// First message determines connection type
	msg, err := decoder.Decode()
	if err != nil {
		fmt.Printf("Failed to read message: %v\n", err)
		return
	}

	// Handle dispatch requests (from CLI)
	if msg.Type == "dispatch" {
		s.handleDispatch(conn, msg, encoder)
		return
	}

	if msg.Type != "register" {
		encoder.Encode(&Message{Type: "error", Error: "expected register or dispatch message"})
		return
	}

	// Validate token
	if s.config.AuthToken != "" && msg.Token != s.config.AuthToken {
		encoder.Encode(&Message{Type: "error", Error: "invalid token"})
		return
	}

	// Register node
	node := &Node{
		ID:       uuid.New().String()[:8],
		Name:     msg.NodeName,
		Address:  conn.RemoteAddr().String(),
		Labels:   msg.Labels,
		Status:   "ready",
		JoinedAt: time.Now(),
		LastSeen: time.Now(),
		Version:  msg.Version,
	}

	if err := s.store.SaveNode(s.ctx, node); err != nil {
		encoder.Encode(&Message{Type: "error", Error: err.Error()})
		return
	}

	// Create task channel for this node
	taskChan := make(chan *Task, 100)

	s.nodesMu.Lock()
	s.nodes[node.ID] = &connectedNode{
		node:     node,
		lastPing: time.Now(),
		taskChan: taskChan,
	}
	s.nodesMu.Unlock()

	s.taskChansMu.Lock()
	s.taskChans[node.ID] = taskChan
	s.taskChansMu.Unlock()

	// Send registration response
	encoder.Encode(&Message{
		Type:   "registered",
		NodeID: node.ID,
	})

	fmt.Printf("✅ Node connected: %s (%s)\n", node.Name, node.ID)

	// Handle node messages
	s.handleNodeSession(node, conn, decoder, encoder, taskChan)

	// Cleanup on disconnect
	s.nodesMu.Lock()
	delete(s.nodes, node.ID)
	s.nodesMu.Unlock()

	s.taskChansMu.Lock()
	delete(s.taskChans, node.ID)
	close(taskChan)
	s.taskChansMu.Unlock()

	node.Status = "disconnected"
	s.store.SaveNode(s.ctx, node)

	fmt.Printf("❌ Node disconnected: %s (%s)\n", node.Name, node.ID)
}

// handleNodeSession handles an active node session
func (s *Server) handleNodeSession(node *Node, conn net.Conn, decoder *messageDecoder, encoder *messageEncoder, taskChan chan *Task) {
	// Start goroutine to send tasks to node
	go func() {
		for task := range taskChan {
			encoder.Encode(&Message{
				Type:   "task",
				TaskID: task.ID,
				Prompt: task.Prompt,
				Agent:  task.AgentName,
			})
		}
	}()

	// Handle incoming messages
	for {
		msg, err := decoder.Decode()
		if err != nil {
			return
		}

		switch msg.Type {
		case "heartbeat":
			s.nodesMu.Lock()
			if cn, ok := s.nodes[node.ID]; ok {
				cn.lastPing = time.Now()
			}
			s.nodesMu.Unlock()

			node.LastSeen = time.Now()
			s.store.SaveNode(s.ctx, node)

			encoder.Encode(&Message{Type: "heartbeat_ack"})

		case "register_agent":
			agent := &Agent{
				ID:          uuid.New().String()[:8],
				Name:        msg.AgentName,
				NodeID:      node.ID,
				Cluster:     msg.Cluster,
				Namespace:   msg.Namespace,
				Description: msg.Description,
				Model:       msg.Model,
				Skills:      msg.Skills,
				Status:      "running",
				CreatedAt:   time.Now(),
				LastActive:  time.Now(),
			}

			if err := s.store.SaveAgent(s.ctx, agent); err != nil {
				encoder.Encode(&Message{Type: "error", Error: err.Error()})
				continue
			}

			// Update node's agent list
			node.AgentIDs = append(node.AgentIDs, agent.ID)
			s.store.SaveNode(s.ctx, node)

			encoder.Encode(&Message{
				Type:    "agent_registered",
				AgentID: agent.ID,
			})

			fmt.Printf("  📦 Agent registered: %s on %s\n", agent.Name, node.Name)

		case "deregister_agent":
			s.store.DeleteAgent(s.ctx, msg.AgentID)
			encoder.Encode(&Message{Type: "agent_deregistered"})

		case "task_result":
			task, err := s.store.GetTask(s.ctx, msg.TaskID)
			if err == nil {
				now := time.Now()
				task.FinishedAt = &now
				if msg.Error != "" {
					task.Status = "failed"
					task.Error = msg.Error
				} else {
					task.Status = "completed"
					task.Result = msg.Result
				}
				s.store.SaveTask(s.ctx, task)
			}
		}
	}
}

// heartbeatChecker checks for dead nodes
func (s *Server) heartbeatChecker() {
	defer s.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.nodesMu.Lock()
			for id, cn := range s.nodes {
				if time.Since(cn.lastPing) > 60*time.Second {
					cn.node.Status = "not-ready"
					s.store.SaveNode(s.ctx, cn.node)
					fmt.Printf("⚠️  Node not responding: %s\n", cn.node.Name)
					_ = id // TODO: handle dead nodes
				}
			}
			s.nodesMu.Unlock()
		}
	}
}

// DispatchTask sends a task to the appropriate node
func (s *Server) DispatchTask(ctx context.Context, agentName, prompt string, metadata map[string]string) (*Task, error) {
	// Find agent on a connected node
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}

	// Get connected nodes
	s.taskChansMu.RLock()
	connectedNodes := make(map[string]bool)
	for nodeID := range s.taskChans {
		connectedNodes[nodeID] = true
	}
	s.taskChansMu.RUnlock()

	var agent *Agent
	for _, a := range agents {
		if a.Name == agentName && a.Status == "running" {
			// Only select if node is connected
			if connectedNodes[a.NodeID] {
				agent = a
				break
			}
		}
	}

	if agent == nil {
		return nil, fmt.Errorf("agent not found or no connected node running it: %s", agentName)
	}

	// Create task
	task := &Task{
		ID:        uuid.New().String()[:8],
		Type:      "message",
		AgentID:   agent.ID,
		AgentName: agent.Name,
		NodeID:    agent.NodeID,
		Prompt:    prompt,
		Status:    "pending",
		CreatedAt: time.Now(),
		Metadata:  metadata,
	}

	if err := s.store.SaveTask(ctx, task); err != nil {
		return nil, err
	}

	// Send to node
	s.taskChansMu.RLock()
	taskChan, ok := s.taskChans[agent.NodeID]
	s.taskChansMu.RUnlock()

	if !ok {
		task.Status = "failed"
		task.Error = "node not connected"
		s.store.SaveTask(ctx, task)
		return task, fmt.Errorf("node not connected: %s", agent.NodeID)
	}

	task.Status = "dispatched"
	now := time.Now()
	task.StartedAt = &now
	s.store.SaveTask(ctx, task)

	taskChan <- task

	return task, nil
}

// GetNodes returns all registered nodes
func (s *Server) GetNodes(ctx context.Context) ([]*Node, error) {
	return s.store.ListNodes(ctx)
}

// GetAgents returns all registered agents
func (s *Server) GetAgents(ctx context.Context) ([]*Agent, error) {
	return s.store.ListAgents(ctx)
}

// GetTasks returns all tasks
func (s *Server) GetTasks(ctx context.Context) ([]*Task, error) {
	return s.store.ListPendingTasks(ctx)
}

// handleDispatch handles a dispatch request from CLI
func (s *Server) handleDispatch(conn net.Conn, msg *Message, encoder *messageEncoder) {
	// Validate token
	if s.config.AuthToken != "" && msg.Token != s.config.AuthToken {
		encoder.Encode(&Message{Type: "error", Error: "invalid token"})
		return
	}

	fmt.Printf("📥 Dispatch request: agent=%s\n", msg.Agent)

	// Dispatch the task
	task, err := s.DispatchTask(s.ctx, msg.Agent, msg.Prompt, nil)
	if err != nil {
		encoder.Encode(&Message{Type: "error", Error: err.Error()})
		return
	}

	// Send task created response
	encoder.Encode(&Message{
		Type:   "task_created",
		TaskID: task.ID,
	})

	// Wait for task completion and stream updates
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.After(5 * time.Minute)

	for {
		select {
		case <-timeout:
			encoder.Encode(&Message{
				Type:  "task_failed",
				Error: "timeout waiting for task completion",
			})
			return

		case <-ticker.C:
			// Check task status
			updatedTask, err := s.store.GetTask(s.ctx, task.ID)
			if err != nil {
				continue
			}

			switch updatedTask.Status {
			case "completed":
				encoder.Encode(&Message{
					Type:   "task_completed",
					TaskID: task.ID,
					Result: updatedTask.Result,
				})
				return

			case "failed":
				encoder.Encode(&Message{
					Type:   "task_failed",
					TaskID: task.ID,
					Error:  updatedTask.Error,
				})
				return

			case "dispatched", "running":
				encoder.Encode(&Message{
					Type:   "task_progress",
					TaskID: task.ID,
					Status: updatedTask.Status,
				})
			}
		}
	}
}
