package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Grep searches for patterns in files.
type Grep struct {
	workDir string
}

// NewGrep creates a new grep tool.
func NewGrep(workDir string) *Grep {
	return &Grep{workDir: workDir}
}

func (g *Grep) Name() string {
	return "grep"
}

func (g *Grep) Description() string {
	return `Search for a pattern in files.
Returns matching lines with file paths and line numbers.
Use for finding code, function definitions, imports, etc.`
}

func (g *Grep) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "Regular expression pattern to search for"
			},
			"path": {
				"type": "string",
				"description": "File or directory to search (default: current directory)"
			},
			"glob": {
				"type": "string",
				"description": "Glob pattern to filter files (e.g., '*.go')"
			},
			"case_insensitive": {
				"type": "boolean",
				"description": "Case-insensitive search (default: false)"
			}
		},
		"required": ["pattern"]
	}`)
}

type grepParams struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path"`
	Glob            string `json:"glob"`
	CaseInsensitive bool   `json:"case_insensitive"`
}

type grepMatch struct {
	File    string
	Line    int
	Content string
}

func (g *Grep) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p grepParams
	if err := json.Unmarshal(params, &p); err != nil {
		return &Result{Content: fmt.Sprintf("invalid params: %v", err), IsError: true}, nil
	}

	if p.Pattern == "" {
		return &Result{Content: "pattern is required", IsError: true}, nil
	}

	// Compile regex
	regexPattern := p.Pattern
	if p.CaseInsensitive {
		regexPattern = "(?i)" + regexPattern
	}
	re, err := regexp.Compile(regexPattern)
	if err != nil {
		return &Result{Content: fmt.Sprintf("invalid regex: %v", err), IsError: true}, nil
	}

	searchPath := g.workDir
	if p.Path != "" {
		if filepath.IsAbs(p.Path) {
			searchPath = p.Path
		} else {
			searchPath = filepath.Join(g.workDir, p.Path)
		}
	}

	var matches []grepMatch
	maxMatches := 100

	err = filepath.Walk(searchPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			// Skip hidden directories
			if info != nil && info.IsDir() {
				name := info.Name()
				if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Check glob filter
		if p.Glob != "" {
			matched, _ := filepath.Match(p.Glob, info.Name())
			if !matched {
				return nil
			}
		}

		// Skip binary files (simple heuristic)
		if isBinaryFile(info.Name()) {
			return nil
		}

		// Search file
		fileMatches, _ := searchFile(path, re)
		for _, m := range fileMatches {
			relPath, _ := filepath.Rel(g.workDir, path)
			matches = append(matches, grepMatch{
				File:    relPath,
				Line:    m.Line,
				Content: m.Content,
			})
			if len(matches) >= maxMatches {
				return filepath.SkipAll
			}
		}
		return nil
	})

	if err != nil && err != filepath.SkipAll {
		return &Result{Content: fmt.Sprintf("search error: %v", err), IsError: true}, nil
	}

	if len(matches) == 0 {
		return &Result{Content: "no matches found"}, nil
	}

	var result strings.Builder
	for _, m := range matches {
		content := m.Content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		fmt.Fprintf(&result, "%s:%d: %s\n", m.File, m.Line, content)
	}

	if len(matches) >= maxMatches {
		result.WriteString(fmt.Sprintf("\n... (limited to %d matches)", maxMatches))
	}

	return &Result{Content: result.String()}, nil
}

func searchFile(path string, re *regexp.Regexp) ([]grepMatch, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var matches []grepMatch
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if re.MatchString(line) {
			matches = append(matches, grepMatch{
				Line:    lineNum,
				Content: strings.TrimSpace(line),
			})
		}
	}

	return matches, scanner.Err()
}

func isBinaryFile(name string) bool {
	binaryExtensions := map[string]bool{
		".exe": true, ".dll": true, ".so": true, ".dylib": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
		".pdf": true, ".zip": true, ".tar": true, ".gz": true,
		".bin": true, ".dat": true, ".db": true, ".sqlite": true,
		".woff": true, ".woff2": true, ".ttf": true, ".eot": true,
		".mp3": true, ".mp4": true, ".wav": true, ".avi": true,
	}
	ext := strings.ToLower(filepath.Ext(name))
	return binaryExtensions[ext]
}
