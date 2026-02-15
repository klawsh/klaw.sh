package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Edit performs string replacement in files.
type Edit struct {
	workDir string
}

// NewEdit creates a new edit tool.
func NewEdit(workDir string) *Edit {
	return &Edit{workDir: workDir}
}

func (e *Edit) Name() string {
	return "edit"
}

func (e *Edit) Description() string {
	return `Edit a file by replacing a specific string with new content.
The old_string must match exactly (including whitespace and indentation).
Use for making targeted changes to existing files.
The old_string must be unique in the file unless replace_all is true.`
}

func (e *Edit) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the file to edit"
			},
			"old_string": {
				"type": "string",
				"description": "The exact string to find and replace"
			},
			"new_string": {
				"type": "string",
				"description": "The string to replace it with"
			},
			"replace_all": {
				"type": "boolean",
				"description": "Replace all occurrences (default: false, fails if not unique)"
			}
		},
		"required": ["path", "old_string", "new_string"]
	}`)
}

type editParams struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

func (e *Edit) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p editParams
	if err := json.Unmarshal(params, &p); err != nil {
		return &Result{Content: fmt.Sprintf("invalid params: %v", err), IsError: true}, nil
	}

	if p.Path == "" {
		return &Result{Content: "path is required", IsError: true}, nil
	}
	if p.OldString == "" {
		return &Result{Content: "old_string is required", IsError: true}, nil
	}
	if p.OldString == p.NewString {
		return &Result{Content: "old_string and new_string must be different", IsError: true}, nil
	}

	path := p.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(e.workDir, path)
	}

	// Read existing content
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Result{Content: fmt.Sprintf("file not found: %s", p.Path), IsError: true}, nil
		}
		return &Result{Content: fmt.Sprintf("failed to read file: %v", err), IsError: true}, nil
	}

	text := string(content)

	// Count occurrences
	count := strings.Count(text, p.OldString)

	if count == 0 {
		return &Result{Content: fmt.Sprintf("old_string not found in %s", p.Path), IsError: true}, nil
	}

	if count > 1 && !p.ReplaceAll {
		return &Result{
			Content: fmt.Sprintf("old_string found %d times in %s. Use replace_all=true to replace all, or make old_string more specific.", count, p.Path),
			IsError: true,
		}, nil
	}

	// Perform replacement
	var newText string
	if p.ReplaceAll {
		newText = strings.ReplaceAll(text, p.OldString, p.NewString)
	} else {
		newText = strings.Replace(text, p.OldString, p.NewString, 1)
	}

	// Write back
	if err := os.WriteFile(path, []byte(newText), 0644); err != nil {
		return &Result{Content: fmt.Sprintf("failed to write file: %v", err), IsError: true}, nil
	}

	if p.ReplaceAll && count > 1 {
		return &Result{Content: fmt.Sprintf("replaced %d occurrences in %s", count, p.Path)}, nil
	}
	return &Result{Content: fmt.Sprintf("edited %s", p.Path)}, nil
}
