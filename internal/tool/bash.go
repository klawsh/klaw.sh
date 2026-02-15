package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Bash executes shell commands.
type Bash struct {
	workDir string
}

// NewBash creates a new bash tool.
func NewBash(workDir string) *Bash {
	return &Bash{workDir: workDir}
}

func (b *Bash) Name() string {
	return "bash"
}

func (b *Bash) Description() string {
	return `Execute a bash command. Use for running shell commands, git operations, package management, etc.
The command runs in the current working directory.
Returns stdout/stderr combined. Exit code 0 = success.`
}

func (b *Bash) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The bash command to execute"
			},
			"timeout": {
				"type": "integer",
				"description": "Timeout in seconds (default: 120)"
			}
		},
		"required": ["command"]
	}`)
}

type bashParams struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

func (b *Bash) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p bashParams
	if err := json.Unmarshal(params, &p); err != nil {
		return &Result{Content: fmt.Sprintf("invalid params: %v", err), IsError: true}, nil
	}

	if p.Command == "" {
		return &Result{Content: "command is required", IsError: true}, nil
	}

	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 120
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", p.Command)
	cmd.Dir = b.workDir
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}

	// Trim and limit output
	output = strings.TrimSpace(output)
	if len(output) > 30000 {
		output = output[:30000] + "\n... (output truncated)"
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return &Result{Content: fmt.Sprintf("command timed out after %ds\n%s", timeout, output), IsError: true}, nil
		}
		return &Result{Content: fmt.Sprintf("exit status %v\n%s", err, output), IsError: true}, nil
	}

	if output == "" {
		output = "(no output)"
	}

	return &Result{Content: output}, nil
}
