package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Glob finds files matching a pattern.
type Glob struct {
	workDir string
}

// NewGlob creates a new glob tool.
func NewGlob(workDir string) *Glob {
	return &Glob{workDir: workDir}
}

func (g *Glob) Name() string {
	return "glob"
}

func (g *Glob) Description() string {
	return `Find files matching a glob pattern.
Supports patterns like "**/*.go", "src/**/*.ts", "*.md".
Returns matching file paths relative to the working directory.`
}

func (g *Glob) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "Glob pattern to match files (e.g., '**/*.go')"
			},
			"path": {
				"type": "string",
				"description": "Directory to search in (default: current directory)"
			}
		},
		"required": ["pattern"]
	}`)
}

type globParams struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

func (g *Glob) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p globParams
	if err := json.Unmarshal(params, &p); err != nil {
		return &Result{Content: fmt.Sprintf("invalid params: %v", err), IsError: true}, nil
	}

	if p.Pattern == "" {
		return &Result{Content: "pattern is required", IsError: true}, nil
	}

	searchDir := g.workDir
	if p.Path != "" {
		if filepath.IsAbs(p.Path) {
			searchDir = p.Path
		} else {
			searchDir = filepath.Join(g.workDir, p.Path)
		}
	}

	var matches []string

	// Handle ** patterns manually
	if strings.Contains(p.Pattern, "**") {
		err := filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip errors
			}
			if info.IsDir() {
				// Skip hidden directories and common ignore patterns
				name := info.Name()
				if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}

			relPath, _ := filepath.Rel(searchDir, path)
			if matchGlob(p.Pattern, relPath) {
				matches = append(matches, relPath)
			}
			return nil
		})
		if err != nil {
			return &Result{Content: fmt.Sprintf("error walking directory: %v", err), IsError: true}, nil
		}
	} else {
		// Simple glob
		pattern := filepath.Join(searchDir, p.Pattern)
		found, err := filepath.Glob(pattern)
		if err != nil {
			return &Result{Content: fmt.Sprintf("invalid pattern: %v", err), IsError: true}, nil
		}
		for _, f := range found {
			relPath, _ := filepath.Rel(searchDir, f)
			matches = append(matches, relPath)
		}
	}

	// Sort and limit results
	sort.Strings(matches)
	if len(matches) > 500 {
		matches = matches[:500]
	}

	if len(matches) == 0 {
		return &Result{Content: "no files matched"}, nil
	}

	return &Result{Content: strings.Join(matches, "\n")}, nil
}

// matchGlob handles ** patterns
func matchGlob(pattern, path string) bool {
	// Normalize separators
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)

	// Handle ** pattern
	if strings.Contains(pattern, "**") {
		parts := strings.Split(pattern, "**")
		if len(parts) == 2 {
			prefix := strings.TrimSuffix(parts[0], "/")
			suffix := strings.TrimPrefix(parts[1], "/")

			// Check prefix match
			if prefix != "" && !strings.HasPrefix(path, prefix) {
				return false
			}

			// Check suffix match (typically a file pattern like "*.go")
			if suffix != "" {
				matched, _ := filepath.Match(suffix, filepath.Base(path))
				return matched
			}
			return true
		}
	}

	// Fall back to standard glob
	matched, _ := filepath.Match(pattern, path)
	return matched
}
