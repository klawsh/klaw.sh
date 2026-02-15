// Package node provides the klaw node client.
package node

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// Client connects to the klaw controller
type Client struct {
	config     ClientConfig
	conn       net.Conn
	encoder    *messageEncoder
	decoder    *messageDecoder
	nodeID     string
	registered bool

	// Agent execution
	agentRunner AgentRunner

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
}

// AgentRunner is called when the controller dispatches a task
type AgentRunner func(ctx context.Context, agentName, prompt string) (string, error)

// NodeClient is the interface for node clients (TCP and gRPC)
type NodeClient interface {
	SetAgentRunner(runner AgentRunner)
	Connect() error
	Start() error
	Stop() error
	RegisterAgent(name, cluster, namespace, description, model string, skills []string) (string, error)
	GetNodeID() string
}

// ClientConfig holds node client configuration
type ClientConfig struct {
	ControllerAddr string
	NodeName       string
	Token          string
	Labels         map[string]string
	DataDir        string
}

// Message mirrors the controller Message type
type Message struct {
	Type string `json:"type"`

	// Registration
	NodeName string            `json:"node_name,omitempty"`
	NodeID   string            `json:"node_id,omitempty"`
	Token    string            `json:"token,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	Version  string            `json:"version,omitempty"`

	// Agent
	AgentID     string   `json:"agent_id,omitempty"`
	AgentName   string   `json:"agent_name,omitempty"`
	Cluster     string   `json:"cluster,omitempty"`
	Namespace   string   `json:"namespace,omitempty"`
	Description string   `json:"description,omitempty"`
	Model       string   `json:"model,omitempty"`
	Skills      []string `json:"skills,omitempty"`

	// Task
	TaskID string `json:"task_id,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Prompt string `json:"prompt,omitempty"`
	Result string `json:"result,omitempty"`

	// Error
	Error string `json:"error,omitempty"`
}

// NewClient creates a new node client
func NewClient(cfg ClientConfig) *Client {
	if cfg.NodeName == "" {
		hostname, _ := os.Hostname()
		cfg.NodeName = hostname
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Client{
		config: cfg,
		ctx:    ctx,
		cancel: cancel,
	}
}

// SetAgentRunner sets the function to run agents
func (c *Client) SetAgentRunner(runner AgentRunner) {
	c.agentRunner = runner
}

// Connect connects to the controller
func (c *Client) Connect() error {
	conn, err := net.DialTimeout("tcp", c.config.ControllerAddr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("failed to connect to controller: %w", err)
	}

	c.conn = conn
	c.encoder = newMessageEncoder(conn)
	c.decoder = newMessageDecoder(conn)

	// Send registration
	err = c.encoder.Encode(&Message{
		Type:     "register",
		NodeName: c.config.NodeName,
		Token:    c.config.Token,
		Labels:   c.config.Labels,
		Version:  "1.0.0",
	})
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to send registration: %w", err)
	}

	// Wait for response
	msg, err := c.decoder.Decode()
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to read registration response: %w", err)
	}

	if msg.Type == "error" {
		conn.Close()
		return fmt.Errorf("registration failed: %s", msg.Error)
	}

	if msg.Type != "registered" {
		conn.Close()
		return fmt.Errorf("unexpected response: %s", msg.Type)
	}

	c.nodeID = msg.NodeID
	c.registered = true

	// Save node ID for later
	c.saveNodeID()

	fmt.Printf("✅ Connected to controller as node: %s (%s)\n", c.config.NodeName, c.nodeID)

	return nil
}

// Start starts the node client loop
func (c *Client) Start() error {
	if !c.registered {
		return fmt.Errorf("not connected")
	}

	// Start heartbeat
	c.wg.Add(1)
	go c.heartbeatLoop()

	// Start message handler
	c.wg.Add(1)
	go c.messageLoop()

	return nil
}

// Stop stops the node client
func (c *Client) Stop() error {
	c.cancel()

	if c.conn != nil {
		c.conn.Close()
	}

	c.wg.Wait()
	return nil
}

// RegisterAgent registers an agent with the controller
func (c *Client) RegisterAgent(name, cluster, namespace, description, model string, skills []string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.encoder.Encode(&Message{
		Type:        "register_agent",
		AgentName:   name,
		Cluster:     cluster,
		Namespace:   namespace,
		Description: description,
		Model:       model,
		Skills:      skills,
	})
	if err != nil {
		return "", err
	}

	// Wait for response
	msg, err := c.decoder.Decode()
	if err != nil {
		return "", err
	}

	if msg.Type == "error" {
		return "", fmt.Errorf(msg.Error)
	}

	return msg.AgentID, nil
}

// heartbeatLoop sends periodic heartbeats
func (c *Client) heartbeatLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			err := c.encoder.Encode(&Message{Type: "heartbeat"})
			c.mu.Unlock()

			if err != nil {
				fmt.Printf("⚠️  Heartbeat failed: %v\n", err)
				return
			}
		}
	}
}

// messageLoop handles incoming messages from controller
func (c *Client) messageLoop() {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			msg, err := c.decoder.Decode()
			if err != nil {
				select {
				case <-c.ctx.Done():
					return
				default:
					fmt.Printf("⚠️  Connection lost: %v\n", err)
					return
				}
			}

			c.handleMessage(msg)
		}
	}
}

// handleMessage processes a message from the controller
func (c *Client) handleMessage(msg *Message) {
	switch msg.Type {
	case "heartbeat_ack":
		// Heartbeat acknowledged

	case "task":
		// Execute task
		go c.executeTask(msg)

	case "error":
		fmt.Printf("❌ Error from controller: %s\n", msg.Error)
	}
}

// executeTask runs a task and reports the result
func (c *Client) executeTask(msg *Message) {
	fmt.Printf("📥 Task received: %s for agent %s\n", msg.TaskID, msg.Agent)

	var result string
	var taskErr string

	if c.agentRunner != nil {
		output, err := c.agentRunner(c.ctx, msg.Agent, msg.Prompt)
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
	c.encoder.Encode(&Message{
		Type:   "task_result",
		TaskID: msg.TaskID,
		Result: result,
		Error:  taskErr,
	})
	c.mu.Unlock()

	if taskErr != "" {
		fmt.Printf("❌ Task %s failed: %s\n", msg.TaskID, taskErr)
	} else {
		fmt.Printf("✅ Task %s completed\n", msg.TaskID)
	}
}

// saveNodeID saves the node ID to disk
func (c *Client) saveNodeID() {
	if c.config.DataDir == "" {
		return
	}

	os.MkdirAll(c.config.DataDir, 0755)
	data := map[string]string{
		"node_id":    c.nodeID,
		"controller": c.config.ControllerAddr,
	}
	jsonData, _ := json.MarshalIndent(data, "", "  ")
	os.WriteFile(filepath.Join(c.config.DataDir, "node.json"), jsonData, 0644)
}

// GetNodeID returns the node ID
func (c *Client) GetNodeID() string {
	return c.nodeID
}

// GetSystemInfo returns system information
func GetSystemInfo() map[string]interface{} {
	return map[string]interface{}{
		"os":        runtime.GOOS,
		"arch":      runtime.GOARCH,
		"cpus":      runtime.NumCPU(),
		"goroutines": runtime.NumGoroutine(),
	}
}

// messageEncoder/decoder (same as controller)
type messageEncoder struct {
	writer *bufio.Writer
}

func newMessageEncoder(conn net.Conn) *messageEncoder {
	return &messageEncoder{
		writer: bufio.NewWriter(conn),
	}
}

func (e *messageEncoder) Encode(msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = e.writer.Write(append(data, '\n'))
	if err != nil {
		return err
	}
	return e.writer.Flush()
}

type messageDecoder struct {
	reader *bufio.Reader
}

func newMessageDecoder(conn net.Conn) *messageDecoder {
	return &messageDecoder{
		reader: bufio.NewReader(conn),
	}
}

func (d *messageDecoder) Decode() (*Message, error) {
	line, err := d.reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var msg Message
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
