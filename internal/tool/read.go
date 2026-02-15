package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Read reads file contents.
type Read struct {
	workDir string
}

// NewRead creates a new read tool.
func NewRead(workDir string) *Read {
	return &Read{workDir: workDir}
}

func (r *Read) Name() string {
	return "read"
}

func (r *Read) Description() string {
	return `Read the contents of a file. Returns the file content with line numbers.
Use absolute paths or paths relative to the working directory.`
}

func (r *Read) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the file to read"
			},
			"offset": {
				"type": "integer",
				"description": "Line number to start from (1-indexed)"
			},
			"limit": {
				"type": "integer",
				"description": "Maximum number of lines to read"
			}
		},
		"required": ["path"]
	}`)
}

type readParams struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func (r *Read) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p readParams
	if err := json.Unmarshal(params, &p); err != nil {
		return &Result{Content: fmt.Sprintf("invalid params: %v", err), IsError: true}, nil
	}

	if p.Path == "" {
		return &Result{Content: "path is required", IsError: true}, nil
	}

	path := p.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.workDir, path)
	}

	// Check if file exists
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Result{Content: fmt.Sprintf("file not found: %s", p.Path), IsError: true}, nil
		}
		return &Result{Content: fmt.Sprintf("cannot access file: %v", err), IsError: true}, nil
	}

	if info.IsDir() {
		return &Result{Content: fmt.Sprintf("%s is a directory, not a file", p.Path), IsError: true}, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return &Result{Content: fmt.Sprintf("failed to read file: %v", err), IsError: true}, nil
	}

	lines := strings.Split(string(content), "\n")

	// Apply offset
	offset := p.Offset
	if offset < 1 {
		offset = 1
	}
	if offset > len(lines) {
		return &Result{Content: fmt.Sprintf("offset %d exceeds file length %d", offset, len(lines)), IsError: true}, nil
	}

	// Apply limit
	limit := p.Limit
	if limit <= 0 {
		limit = 2000
	}

	startIdx := offset - 1
	endIdx := startIdx + limit
	if endIdx > len(lines) {
		endIdx = len(lines)
	}

	// Format with line numbers
	var result strings.Builder
	for i := startIdx; i < endIdx; i++ {
		line := lines[i]
		// Truncate long lines
		if len(line) > 2000 {
			line = line[:2000] + "..."
		}
		fmt.Fprintf(&result, "%6d\t%s\n", i+1, line)
	}

	return &Result{Content: result.String()}, nil
}
