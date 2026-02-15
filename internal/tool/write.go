package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Write writes content to a file.
type Write struct {
	workDir string
}

// NewWrite creates a new write tool.
func NewWrite(workDir string) *Write {
	return &Write{workDir: workDir}
}

func (w *Write) Name() string {
	return "write"
}

func (w *Write) Description() string {
	return `Write content to a file. Creates the file if it doesn't exist, overwrites if it does.
Creates parent directories as needed.
Use for creating new files or completely replacing existing content.`
}

func (w *Write) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the file to write"
			},
			"content": {
				"type": "string",
				"description": "Content to write to the file"
			}
		},
		"required": ["path", "content"]
	}`)
}

type writeParams struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (w *Write) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p writeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return &Result{Content: fmt.Sprintf("invalid params: %v", err), IsError: true}, nil
	}

	if p.Path == "" {
		return &Result{Content: "path is required", IsError: true}, nil
	}

	path := p.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(w.workDir, path)
	}

	// Create parent directories
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &Result{Content: fmt.Sprintf("failed to create directory: %v", err), IsError: true}, nil
	}

	// Write file
	if err := os.WriteFile(path, []byte(p.Content), 0644); err != nil {
		return &Result{Content: fmt.Sprintf("failed to write file: %v", err), IsError: true}, nil
	}

	return &Result{Content: fmt.Sprintf("wrote %d bytes to %s", len(p.Content), p.Path)}, nil
}
