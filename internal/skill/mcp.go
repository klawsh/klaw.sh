// Package skill provides MCP (Model Context Protocol) client implementation.
package skill

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/eachlabs/klaw/internal/tool"
)

// MCPClient connects to an MCP server and provides tools
type MCPClient struct {
	config  *MCPConfig
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	mu      sync.Mutex
	nextID  atomic.Int64
	tools   []MCPTool
	running bool
}

// MCPTool represents a tool from an MCP server
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// MCPRequest is a JSON-RPC request to MCP server
type MCPRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// MCPResponse is a JSON-RPC response from MCP server
type MCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *MCPError       `json:"error,omitempty"`
}

// MCPError represents an MCP error
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewMCPClient creates a new MCP client
func NewMCPClient(config *MCPConfig) *MCPClient {
	return &MCPClient{
		config: config,
	}
}

// Start starts the MCP server process
func (c *MCPClient) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return nil
	}

	// Build command
	args := c.config.Args
	c.cmd = exec.CommandContext(ctx, c.config.Command, args...)

	// Set environment
	c.cmd.Env = os.Environ()
	for k, v := range c.config.Env {
		c.cmd.Env = append(c.cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Get stdin/stdout pipes
	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}
	c.stdin = stdin

	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	c.stdout = bufio.NewReader(stdout)

	// Capture stderr for debugging
	c.cmd.Stderr = os.Stderr

	// Start the process
	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start MCP server: %w", err)
	}

	c.running = true

	// Initialize the connection
	if err := c.initialize(); err != nil {
		c.Stop()
		return fmt.Errorf("failed to initialize MCP connection: %w", err)
	}

	// Fetch available tools
	if err := c.fetchTools(); err != nil {
		c.Stop()
		return fmt.Errorf("failed to fetch tools: %w", err)
	}

	return nil
}

// Stop stops the MCP server process
func (c *MCPClient) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil
	}

	c.running = false

	if c.stdin != nil {
		c.stdin.Close()
	}

	if c.cmd != nil && c.cmd.Process != nil {
		c.cmd.Process.Kill()
		c.cmd.Wait()
	}

	return nil
}

// initialize sends the MCP initialize request
func (c *MCPClient) initialize() error {
	resp, err := c.call("initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "klaw",
			"version": "1.0.0",
		},
	})
	if err != nil {
		return err
	}

	// Send initialized notification
	c.notify("notifications/initialized", nil)

	_ = resp // We could parse server capabilities here
	return nil
}

// fetchTools fetches the list of available tools from the server
func (c *MCPClient) fetchTools() error {
	resp, err := c.call("tools/list", nil)
	if err != nil {
		return err
	}

	var result struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return fmt.Errorf("failed to parse tools: %w", err)
	}

	c.tools = result.Tools
	return nil
}

// call makes a JSON-RPC call and waits for the response
func (c *MCPClient) call(method string, params interface{}) (json.RawMessage, error) {
	id := c.nextID.Add(1)

	req := MCPRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	// Send request
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	_, err = fmt.Fprintf(c.stdin, "%s\n", data)
	c.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	// Read response
	c.mu.Lock()
	line, err := c.stdout.ReadBytes('\n')
	c.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var resp MCPResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("MCP error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	return resp.Result, nil
}

// notify sends a notification (no response expected)
func (c *MCPClient) notify(method string, params interface{}) error {
	req := MCPRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	c.mu.Lock()
	_, err = fmt.Fprintf(c.stdin, "%s\n", data)
	c.mu.Unlock()

	return err
}

// Tools returns the list of available tools
func (c *MCPClient) Tools() []MCPTool {
	return c.tools
}

// CallTool calls a tool on the MCP server
func (c *MCPClient) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	resp, err := c.call("tools/call", map[string]interface{}{
		"name":      name,
		"arguments": json.RawMessage(args),
	})
	if err != nil {
		return "", err
	}

	// Parse the response
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("failed to parse tool result: %w", err)
	}

	// Collect text content
	var output string
	for _, c := range result.Content {
		if c.Type == "text" {
			output += c.Text
		}
	}

	if result.IsError {
		return "", fmt.Errorf("tool error: %s", output)
	}

	return output, nil
}

// MCPToolWrapper wraps an MCP tool as a klaw tool.Tool
type MCPToolWrapper struct {
	client      *MCPClient
	name        string
	description string
	schema      json.RawMessage
}

// NewMCPToolWrapper creates a tool wrapper for an MCP tool
func NewMCPToolWrapper(client *MCPClient, mcpTool MCPTool) *MCPToolWrapper {
	return &MCPToolWrapper{
		client:      client,
		name:        mcpTool.Name,
		description: mcpTool.Description,
		schema:      mcpTool.InputSchema,
	}
}

func (t *MCPToolWrapper) Name() string {
	return t.name
}

func (t *MCPToolWrapper) Description() string {
	return t.description
}

func (t *MCPToolWrapper) Schema() json.RawMessage {
	return t.schema
}

func (t *MCPToolWrapper) Execute(ctx context.Context, params json.RawMessage) (*tool.Result, error) {
	result, err := t.client.CallTool(ctx, t.name, params)
	if err != nil {
		return &tool.Result{
			Content: fmt.Sprintf("Error: %v", err),
			IsError: true,
		}, nil
	}

	return &tool.Result{
		Content: result,
	}, nil
}

// MCPManager manages multiple MCP clients for different skills
type MCPManager struct {
	registry *Registry
	clients  map[string]*MCPClient
	mu       sync.Mutex
}

// NewMCPManager creates a new MCP manager
func NewMCPManager(registry *Registry) *MCPManager {
	return &MCPManager{
		registry: registry,
		clients:  make(map[string]*MCPClient),
	}
}

// GetClient gets or creates an MCP client for a skill
func (m *MCPManager) GetClient(ctx context.Context, skillName string) (*MCPClient, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if client already exists
	if client, ok := m.clients[skillName]; ok {
		return client, nil
	}

	// Get MCP config for skill
	config, err := m.registry.GetMCPConfig(skillName)
	if err != nil {
		return nil, err
	}

	// Create and start client
	client := NewMCPClient(config)
	if err := client.Start(ctx); err != nil {
		return nil, err
	}

	m.clients[skillName] = client
	return client, nil
}

// GetToolsForSkill returns klaw tools for an MCP-based skill
func (m *MCPManager) GetToolsForSkill(ctx context.Context, skillName string) ([]tool.Tool, error) {
	client, err := m.GetClient(ctx, skillName)
	if err != nil {
		return nil, err
	}

	var tools []tool.Tool
	for _, mcpTool := range client.Tools() {
		tools = append(tools, NewMCPToolWrapper(client, mcpTool))
	}

	return tools, nil
}

// StopAll stops all MCP clients
func (m *MCPManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, client := range m.clients {
		client.Stop()
	}
	m.clients = make(map[string]*MCPClient)
}
