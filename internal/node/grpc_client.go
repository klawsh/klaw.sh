package node

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/eachlabs/klaw/internal/controller/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// GRPCClient connects to the klaw controller via gRPC
type GRPCClient struct {
	config      ClientConfig
	conn        *grpc.ClientConn
	client      pb.ControllerServiceClient
	nodeID      string
	registered  bool
	agentRunner AgentRunner

	taskStream pb.ControllerService_TaskStreamClient

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
}

// NewGRPCClient creates a new gRPC node client
func NewGRPCClient(cfg ClientConfig) *GRPCClient {
	ctx, cancel := context.WithCancel(context.Background())

	return &GRPCClient{
		config: cfg,
		ctx:    ctx,
		cancel: cancel,
	}
}

// SetAgentRunner sets the function to run agents
func (c *GRPCClient) SetAgentRunner(runner AgentRunner) {
	c.agentRunner = runner
}

// Connect connects to the controller via gRPC
func (c *GRPCClient) Connect() error {
	// Dial the controller
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}

	conn, err := grpc.NewClient(c.config.ControllerAddr, opts...)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	c.conn = conn
	c.client = pb.NewControllerServiceClient(conn)

	// Register node
	resp, err := c.client.Register(c.ctx, &pb.RegisterRequest{
		NodeName: c.config.NodeName,
		Token:    c.config.Token,
		Labels:   c.config.Labels,
		Version:  "1.0.0",
	})
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}

	if resp.Error != "" {
		return fmt.Errorf("registration failed: %s", resp.Error)
	}

	c.nodeID = resp.NodeId
	c.registered = true

	fmt.Printf("✅ Connected to controller as node: %s (%s)\n", c.config.NodeName, c.nodeID)

	return nil
}

// Start starts the gRPC client
func (c *GRPCClient) Start() error {
	if !c.registered {
		return fmt.Errorf("not connected")
	}

	// Establish task stream
	stream, err := c.client.TaskStream(c.ctx)
	if err != nil {
		return fmt.Errorf("failed to establish task stream: %w", err)
	}
	c.taskStream = stream

	// Send connect message with node ID
	if err := stream.Send(&pb.TaskMessage{
		Type:   "connect",
		TaskId: c.nodeID, // Reusing TaskId for nodeID
	}); err != nil {
		return fmt.Errorf("failed to send connect: %w", err)
	}

	// Start heartbeat
	c.wg.Add(1)
	go c.heartbeatLoop()

	// Start task handler
	c.wg.Add(1)
	go c.taskLoop()

	return nil
}

// Stop stops the gRPC client
func (c *GRPCClient) Stop() error {
	c.cancel()

	if c.taskStream != nil {
		c.taskStream.CloseSend()
	}

	if c.conn != nil {
		c.conn.Close()
	}

	c.wg.Wait()
	return nil
}

// RegisterAgent registers an agent with the controller
func (c *GRPCClient) RegisterAgent(name, cluster, namespace, description, model string, skills []string) (string, error) {
	resp, err := c.client.RegisterAgent(c.ctx, &pb.RegisterAgentRequest{
		NodeId:      c.nodeID,
		Name:        name,
		Cluster:     cluster,
		Namespace:   namespace,
		Description: description,
		Model:       model,
		Skills:      skills,
	})
	if err != nil {
		return "", err
	}

	if resp.Error != "" {
		return "", fmt.Errorf(resp.Error)
	}

	return resp.AgentId, nil
}

// GetNodeID returns the node ID
func (c *GRPCClient) GetNodeID() string {
	return c.nodeID
}

func (c *GRPCClient) heartbeatLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			// Send heartbeat via stream
			c.mu.Lock()
			if c.taskStream != nil {
				c.taskStream.Send(&pb.TaskMessage{
					Type:   "heartbeat",
					TaskId: c.nodeID,
				})
			}
			c.mu.Unlock()
		}
	}
}

func (c *GRPCClient) taskLoop() {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			msg, err := c.taskStream.Recv()
			if err == io.EOF {
				return
			}
			if err != nil {
				select {
				case <-c.ctx.Done():
					return
				default:
					fmt.Printf("⚠️  Stream error: %v\n", err)
					return
				}
			}

			if msg.Type == "task" {
				go c.executeTask(msg)
			}
		}
	}
}

func (c *GRPCClient) executeTask(msg *pb.TaskMessage) {
	fmt.Printf("📥 Task received: %s for agent %s\n", msg.TaskId, msg.AgentName)

	var result string
	var taskErr string

	if c.agentRunner != nil {
		output, err := c.agentRunner(c.ctx, msg.AgentName, msg.Prompt)
		if err != nil {
			taskErr = err.Error()
		} else {
			result = output
		}
	} else {
		taskErr = "no agent runner configured"
	}

	// Send result back
	c.mu.Lock()
	if c.taskStream != nil {
		c.taskStream.Send(&pb.TaskMessage{
			Type:   "result",
			TaskId: msg.TaskId,
			Result: result,
			Error:  taskErr,
		})
	}
	c.mu.Unlock()

	if taskErr != "" {
		fmt.Printf("❌ Task %s failed: %s\n", msg.TaskId, taskErr)
	} else {
		fmt.Printf("✅ Task %s completed\n", msg.TaskId)
	}
}
