package controller

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/eachlabs/klaw/internal/controller/pb"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GRPCServer implements the gRPC controller service
type GRPCServer struct {
	pb.UnimplementedControllerServiceServer

	config   ServerConfig
	store    Store
	server   *grpc.Server
	listener net.Listener

	// Connected nodes with their task streams
	nodeStreams   map[string]*nodeStream
	nodeStreamsMu sync.RWMutex

	// Task result channels
	taskResults   map[string]chan *pb.TaskMessage
	taskResultsMu sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type nodeStream struct {
	nodeID   string
	stream   pb.ControllerService_TaskStreamServer
	lastSeen time.Time
}

// NewGRPCServer creates a new gRPC controller server
func NewGRPCServer(cfg ServerConfig) (*GRPCServer, error) {
	var store Store
	var err error

	switch cfg.StoreType {
	case "etcd":
		store, err = NewFileStore(cfg.DataDir) // TODO: implement etcd store
	default:
		store, err = NewFileStore(cfg.DataDir)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create store: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &GRPCServer{
		config:      cfg,
		store:       store,
		nodeStreams: make(map[string]*nodeStream),
		taskResults: make(map[string]chan *pb.TaskMessage),
		ctx:         ctx,
		cancel:      cancel,
	}, nil
}

// Start starts the gRPC server
func (s *GRPCServer) Start() error {
	addr := fmt.Sprintf(":%d", s.config.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	s.listener = listener

	// Create gRPC server with options
	opts := []grpc.ServerOption{}

	s.server = grpc.NewServer(opts...)
	pb.RegisterControllerServiceServer(s.server, s)

	// Start heartbeat checker
	s.wg.Add(1)
	go s.heartbeatChecker()

	fmt.Printf("🚀 Klaw gRPC Controller started on %s\n", addr)
	fmt.Println()
	fmt.Println("Waiting for nodes to connect...")

	return s.server.Serve(listener)
}

// Stop stops the gRPC server
func (s *GRPCServer) Stop() error {
	s.cancel()
	if s.server != nil {
		s.server.GracefulStop()
	}
	s.wg.Wait()
	return s.store.Close()
}

// --- Node Registration ---

func (s *GRPCServer) Register(ctx context.Context, req *pb.RegisterRequest) (*pb.RegisterResponse, error) {
	// Validate token
	if s.config.AuthToken != "" && req.Token != s.config.AuthToken {
		return &pb.RegisterResponse{Error: "invalid token"}, nil
	}

	// Create node
	node := &Node{
		ID:       uuid.New().String()[:8],
		Name:     req.NodeName,
		Labels:   req.Labels,
		Status:   "ready",
		Version:  req.Version,
		JoinedAt: time.Now(),
		LastSeen: time.Now(),
	}

	if err := s.store.SaveNode(ctx, node); err != nil {
		return &pb.RegisterResponse{Error: err.Error()}, nil
	}

	fmt.Printf("✅ Node registered: %s (%s)\n", node.Name, node.ID)

	return &pb.RegisterResponse{NodeId: node.ID}, nil
}

func (s *GRPCServer) Heartbeat(ctx context.Context, req *pb.HeartbeatRequest) (*pb.HeartbeatResponse, error) {
	s.nodeStreamsMu.Lock()
	if ns, ok := s.nodeStreams[req.NodeId]; ok {
		ns.lastSeen = time.Now()
	}
	s.nodeStreamsMu.Unlock()

	// Update node in store
	node, err := s.store.GetNode(ctx, req.NodeId)
	if err == nil {
		node.LastSeen = time.Now()
		s.store.SaveNode(ctx, node)
	}

	return &pb.HeartbeatResponse{Ok: true}, nil
}

func (s *GRPCServer) Deregister(ctx context.Context, req *pb.DeregisterRequest) (*pb.DeregisterResponse, error) {
	s.store.DeleteNode(ctx, req.NodeId)
	return &pb.DeregisterResponse{Ok: true}, nil
}

// --- Agent Management ---

func (s *GRPCServer) RegisterAgent(ctx context.Context, req *pb.RegisterAgentRequest) (*pb.RegisterAgentResponse, error) {
	agent := &Agent{
		ID:          uuid.New().String()[:8],
		Name:        req.Name,
		NodeID:      req.NodeId,
		Cluster:     req.Cluster,
		Namespace:   req.Namespace,
		Description: req.Description,
		Model:       req.Model,
		Skills:      req.Skills,
		Status:      "running",
		CreatedAt:   time.Now(),
		LastActive:  time.Now(),
	}

	if err := s.store.SaveAgent(ctx, agent); err != nil {
		return &pb.RegisterAgentResponse{Error: err.Error()}, nil
	}

	// Update node's agent list
	node, err := s.store.GetNode(ctx, req.NodeId)
	if err == nil {
		node.AgentIDs = append(node.AgentIDs, agent.ID)
		s.store.SaveNode(ctx, node)
	}

	fmt.Printf("  📦 Agent registered: %s on node %s\n", agent.Name, req.NodeId)

	return &pb.RegisterAgentResponse{AgentId: agent.ID}, nil
}

func (s *GRPCServer) DeregisterAgent(ctx context.Context, req *pb.DeregisterAgentRequest) (*pb.DeregisterAgentResponse, error) {
	s.store.DeleteAgent(ctx, req.AgentId)
	return &pb.DeregisterAgentResponse{Ok: true}, nil
}

// --- Task Streaming ---

func (s *GRPCServer) TaskStream(stream pb.ControllerService_TaskStreamServer) error {
	// First message should identify the node
	msg, err := stream.Recv()
	if err != nil {
		return err
	}

	if msg.Type != "connect" {
		return status.Error(codes.InvalidArgument, "first message must be connect")
	}

	nodeID := msg.TaskId // Reusing TaskId field for nodeID in connect message

	// Register stream
	s.nodeStreamsMu.Lock()
	s.nodeStreams[nodeID] = &nodeStream{
		nodeID:   nodeID,
		stream:   stream,
		lastSeen: time.Now(),
	}
	s.nodeStreamsMu.Unlock()

	defer func() {
		s.nodeStreamsMu.Lock()
		delete(s.nodeStreams, nodeID)
		s.nodeStreamsMu.Unlock()
	}()

	// Handle incoming messages (results from node)
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		switch msg.Type {
		case "result":
			// Forward result to waiting dispatch
			s.taskResultsMu.RLock()
			if ch, ok := s.taskResults[msg.TaskId]; ok {
				select {
				case ch <- msg:
				default:
				}
			}
			s.taskResultsMu.RUnlock()

			// Update task in store
			task, err := s.store.GetTask(s.ctx, msg.TaskId)
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

		case "progress":
			// Forward progress update
			s.taskResultsMu.RLock()
			if ch, ok := s.taskResults[msg.TaskId]; ok {
				select {
				case ch <- msg:
				default:
				}
			}
			s.taskResultsMu.RUnlock()

		case "heartbeat":
			s.nodeStreamsMu.Lock()
			if ns, ok := s.nodeStreams[nodeID]; ok {
				ns.lastSeen = time.Now()
			}
			s.nodeStreamsMu.Unlock()
		}
	}
}

// --- Task Dispatch ---

func (s *GRPCServer) DispatchTask(ctx context.Context, req *pb.DispatchTaskRequest) (*pb.DispatchTaskResponse, error) {
	// Validate token
	if s.config.AuthToken != "" && req.Token != s.config.AuthToken {
		return &pb.DispatchTaskResponse{Error: "invalid token"}, nil
	}

	// Find agent on a connected node
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return &pb.DispatchTaskResponse{Error: err.Error()}, nil
	}

	s.nodeStreamsMu.RLock()
	connectedNodes := make(map[string]bool)
	for nodeID := range s.nodeStreams {
		connectedNodes[nodeID] = true
	}
	s.nodeStreamsMu.RUnlock()

	var agent *Agent
	for _, a := range agents {
		if a.Name == req.AgentName && a.Status == "running" && connectedNodes[a.NodeID] {
			agent = a
			break
		}
	}

	if agent == nil {
		return &pb.DispatchTaskResponse{Error: "agent not found or no connected node running it"}, nil
	}

	// Create task
	task := &Task{
		ID:        uuid.New().String()[:8],
		Type:      "message",
		AgentID:   agent.ID,
		AgentName: agent.Name,
		NodeID:    agent.NodeID,
		Prompt:    req.Prompt,
		Status:    "pending",
		CreatedAt: time.Now(),
		Metadata:  req.Metadata,
	}

	if err := s.store.SaveTask(ctx, task); err != nil {
		return &pb.DispatchTaskResponse{Error: err.Error()}, nil
	}

	// Get node stream
	s.nodeStreamsMu.RLock()
	ns, ok := s.nodeStreams[agent.NodeID]
	s.nodeStreamsMu.RUnlock()

	if !ok {
		task.Status = "failed"
		task.Error = "node not connected"
		s.store.SaveTask(ctx, task)
		return &pb.DispatchTaskResponse{Error: "node not connected"}, nil
	}

	// Create result channel if waiting
	var resultCh chan *pb.TaskMessage
	if req.Wait {
		resultCh = make(chan *pb.TaskMessage, 10)
		s.taskResultsMu.Lock()
		s.taskResults[task.ID] = resultCh
		s.taskResultsMu.Unlock()

		defer func() {
			s.taskResultsMu.Lock()
			delete(s.taskResults, task.ID)
			s.taskResultsMu.Unlock()
			close(resultCh)
		}()
	}

	// Send task to node
	err = ns.stream.Send(&pb.TaskMessage{
		Type:      "task",
		TaskId:    task.ID,
		AgentName: agent.Name,
		Prompt:    req.Prompt,
		Metadata:  req.Metadata,
	})
	if err != nil {
		task.Status = "failed"
		task.Error = err.Error()
		s.store.SaveTask(ctx, task)
		return &pb.DispatchTaskResponse{Error: err.Error()}, nil
	}

	now := time.Now()
	task.Status = "dispatched"
	task.StartedAt = &now
	s.store.SaveTask(ctx, task)

	if !req.Wait {
		return &pb.DispatchTaskResponse{
			TaskId: task.ID,
			Status: "dispatched",
		}, nil
	}

	// Wait for result
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	select {
	case <-time.After(timeout):
		return &pb.DispatchTaskResponse{
			TaskId: task.ID,
			Status: "timeout",
			Error:  "task timed out",
		}, nil

	case msg := <-resultCh:
		if msg.Type == "result" {
			return &pb.DispatchTaskResponse{
				TaskId: task.ID,
				Status: "completed",
				Result: msg.Result,
				Error:  msg.Error,
			}, nil
		}
	}

	return &pb.DispatchTaskResponse{TaskId: task.ID, Status: "unknown"}, nil
}

func (s *GRPCServer) GetTaskStatus(ctx context.Context, req *pb.GetTaskStatusRequest) (*pb.GetTaskStatusResponse, error) {
	task, err := s.store.GetTask(ctx, req.TaskId)
	if err != nil {
		return nil, status.Error(codes.NotFound, "task not found")
	}

	return &pb.GetTaskStatusResponse{
		Task: taskToProto(task),
	}, nil
}

// --- Query Endpoints ---

func (s *GRPCServer) ListNodes(ctx context.Context, req *pb.ListNodesRequest) (*pb.ListNodesResponse, error) {
	nodes, err := s.store.ListNodes(ctx)
	if err != nil {
		return nil, err
	}

	pbNodes := make([]*pb.Node, len(nodes))
	for i, n := range nodes {
		pbNodes[i] = nodeToProto(n)
	}

	return &pb.ListNodesResponse{Nodes: pbNodes}, nil
}

func (s *GRPCServer) ListAgents(ctx context.Context, req *pb.ListAgentsRequest) (*pb.ListAgentsResponse, error) {
	agents, err := s.store.ListAgents(ctx)
	if err != nil {
		return nil, err
	}

	// Filter by node if specified
	var filtered []*Agent
	for _, a := range agents {
		if req.NodeId == "" || a.NodeID == req.NodeId {
			filtered = append(filtered, a)
		}
	}

	pbAgents := make([]*pb.Agent, len(filtered))
	for i, a := range filtered {
		pbAgents[i] = agentToProto(a)
	}

	return &pb.ListAgentsResponse{Agents: pbAgents}, nil
}

func (s *GRPCServer) ListTasks(ctx context.Context, req *pb.ListTasksRequest) (*pb.ListTasksResponse, error) {
	tasks, err := s.store.ListPendingTasks(ctx)
	if err != nil {
		return nil, err
	}

	// Filter
	var filtered []*Task
	for _, t := range tasks {
		if req.Status != "" && t.Status != req.Status {
			continue
		}
		if req.AgentName != "" && t.AgentName != req.AgentName {
			continue
		}
		filtered = append(filtered, t)
	}

	pbTasks := make([]*pb.Task, len(filtered))
	for i, t := range filtered {
		pbTasks[i] = taskToProto(t)
	}

	return &pb.ListTasksResponse{Tasks: pbTasks}, nil
}

// --- Helpers ---

func (s *GRPCServer) heartbeatChecker() {
	defer s.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.nodeStreamsMu.Lock()
			for nodeID, ns := range s.nodeStreams {
				if time.Since(ns.lastSeen) > 60*time.Second {
					fmt.Printf("⚠️  Node not responding: %s\n", nodeID)
					// Update node status
					node, err := s.store.GetNode(s.ctx, nodeID)
					if err == nil {
						node.Status = "not-ready"
						s.store.SaveNode(s.ctx, node)
					}
				}
			}
			s.nodeStreamsMu.Unlock()
		}
	}
}

func nodeToProto(n *Node) *pb.Node {
	return &pb.Node{
		Id:       n.ID,
		Name:     n.Name,
		Address:  n.Address,
		Labels:   n.Labels,
		Status:   n.Status,
		Version:  n.Version,
		JoinedAt: n.JoinedAt.Unix(),
		LastSeen: n.LastSeen.Unix(),
		AgentIds: n.AgentIDs,
	}
}

func agentToProto(a *Agent) *pb.Agent {
	return &pb.Agent{
		Id:          a.ID,
		Name:        a.Name,
		NodeId:      a.NodeID,
		Cluster:     a.Cluster,
		Namespace:   a.Namespace,
		Description: a.Description,
		Model:       a.Model,
		Skills:      a.Skills,
		Status:      a.Status,
		CreatedAt:   a.CreatedAt.Unix(),
		LastActive:  a.LastActive.Unix(),
	}
}

func taskToProto(t *Task) *pb.Task {
	pt := &pb.Task{
		Id:        t.ID,
		Type:      t.Type,
		AgentId:   t.AgentID,
		AgentName: t.AgentName,
		NodeId:    t.NodeID,
		Prompt:    t.Prompt,
		Priority:  int32(t.Priority),
		Status:    t.Status,
		Result:    t.Result,
		Error:     t.Error,
		Metadata:  t.Metadata,
		CreatedAt: t.CreatedAt.Unix(),
	}
	if t.StartedAt != nil {
		pt.StartedAt = t.StartedAt.Unix()
	}
	if t.FinishedAt != nil {
		pt.FinishedAt = t.FinishedAt.Unix()
	}
	return pt
}
