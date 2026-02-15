// Package controller provides the communication protocol.
package controller

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
)

// Message is the wire format for controller-node communication
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
	Status string `json:"status,omitempty"`

	// Error
	Error string `json:"error,omitempty"`
}

// messageEncoder encodes messages to a connection
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

	// Write length-prefixed JSON
	_, err = e.writer.Write(append(data, '\n'))
	if err != nil {
		return err
	}

	return e.writer.Flush()
}

// messageDecoder decodes messages from a connection
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
		if err == io.EOF {
			return nil, err
		}
		return nil, err
	}

	var msg Message
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil, err
	}

	return &msg, nil
}
