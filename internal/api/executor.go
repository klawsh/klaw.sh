package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/tool"
	"github.com/google/uuid"
)

// RunRequest is the incoming POST /api/v1/run body.
type RunRequest struct {
	Model          string         `json:"model"`
	Prompt         string         `json:"prompt"`
	SkillURL       string         `json:"skill_url,omitempty"`
	Tools          []string       `json:"tools,omitempty"`
	Context        map[string]any `json:"context,omitempty"`
	TimeoutSeconds int            `json:"timeout_seconds,omitempty"`
	MaxIterations  int            `json:"max_iterations,omitempty"`
}

// UsageInfo tracks token usage for a task.
type UsageInfo struct {
	Model        string  `json:"model"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	ToolCalls    int     `json:"tool_calls"`
	Iterations   int     `json:"iterations"`
	DurationMs   int64   `json:"duration_ms"`
	Cost         float64 `json:"cost"`
}

// TaskState tracks a running or completed task for polling.
type TaskState struct {
	ID        string        `json:"id"`
	Status    string        `json:"status"`
	Events    []SSEEvent    `json:"-"`
	Result    *ParsedOutput `json:"result,omitempty"`
	Usage     *UsageInfo    `json:"usage,omitempty"`
	Error     *TaskError    `json:"error,omitempty"`
	StartedAt time.Time     `json:"started_at"`
	EndedAt   *time.Time    `json:"ended_at,omitempty"`
}

// SSEEvent is a single event stored for replay.
type SSEEvent struct {
	ID    int    `json:"id"`
	Event string `json:"event"`
	Data  any    `json:"data"`
}

// TaskError represents a task failure.
type TaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ExecutorConfig holds executor initialization parameters.
type ExecutorConfig struct {
	Provider   provider.Provider
	Workers    int
	MaxIter    int
	MaxTimeout time.Duration
	SkillTTL   time.Duration
}

// Executor runs creative agent tasks with SSE streaming.
type Executor struct {
	provider   provider.Provider
	skillCache *SkillCache
	sem        chan struct{}
	tasks      map[string]*TaskState
	tasksMu    sync.RWMutex
	maxIter    int
	maxTimeout time.Duration
}

// NewExecutor creates an executor.
func NewExecutor(cfg ExecutorConfig) *Executor {
	workers := cfg.Workers
	if workers <= 0 {
		workers = 50
	}
	maxIter := cfg.MaxIter
	if maxIter <= 0 {
		maxIter = 50
	}
	maxTimeout := cfg.MaxTimeout
	if maxTimeout <= 0 {
		maxTimeout = 10 * time.Minute
	}

	e := &Executor{
		provider:   cfg.Provider,
		skillCache: NewSkillCache(cfg.SkillTTL),
		sem:        make(chan struct{}, workers),
		tasks:      make(map[string]*TaskState),
		maxIter:    maxIter,
		maxTimeout: maxTimeout,
	}

	// Background cleanup of completed tasks (every 5 min, evict >30 min old)
	go e.cleanupLoop()

	return e
}

// ActiveCount returns the number of currently running tasks.
func (e *Executor) ActiveCount() int {
	return len(e.sem)
}

// GetTask returns the state of a task by ID.
func (e *Executor) GetTask(taskID string) (*TaskState, bool) {
	e.tasksMu.RLock()
	defer e.tasksMu.RUnlock()
	t, ok := e.tasks[taskID]
	return t, ok
}

// Run executes a task with SSE streaming to the ResponseWriter.
func (e *Executor) Run(ctx context.Context, w http.ResponseWriter, req *RunRequest) {
	// Validate
	if err := validateRequest(req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Apply defaults
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 300
	}
	if req.TimeoutSeconds > int(e.maxTimeout.Seconds()) {
		req.TimeoutSeconds = int(e.maxTimeout.Seconds())
	}
	if req.MaxIterations <= 0 {
		req.MaxIterations = e.maxIter
	}
	if req.MaxIterations > 100 {
		req.MaxIterations = 100
	}

	// Acquire worker slot
	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	default:
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many concurrent requests"})
		return
	}

	// Setup SSE
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming not supported"})
		return
	}

	taskID := fmt.Sprintf("task_%s", uuid.New().String()[:12])

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Task-ID", taskID)
	w.WriteHeader(http.StatusOK)

	// Task state for polling
	now := time.Now()
	state := &TaskState{ID: taskID, Status: "running", StartedAt: now}
	e.tasksMu.Lock()
	e.tasks[taskID] = state
	e.tasksMu.Unlock()

	eventSeq := 0
	writeEvent := func(event string, data any) {
		eventSeq++
		sseEvent := SSEEvent{ID: eventSeq, Event: event, Data: data}

		e.tasksMu.Lock()
		state.Events = append(state.Events, sseEvent)
		e.tasksMu.Unlock()

		writeSSEEvent(w, flusher, event, data)
	}

	// Run the agent
	result, usage, taskErr := e.runAgent(ctx, req, taskID, writeEvent)

	endTime := time.Now()
	e.tasksMu.Lock()
	state.EndedAt = &endTime
	state.Usage = usage
	if taskErr != nil {
		state.Status = "failed"
		state.Error = taskErr
		writeEvent("error", map[string]any{
			"task_id": taskID,
			"status":  "failed",
			"error":   taskErr,
			"usage":   usage,
		})
	} else {
		state.Status = "completed"
		state.Result = result
		writeEvent("done", map[string]any{
			"task_id": taskID,
			"status":  "completed",
			"result":  result,
			"usage":   usage,
		})
	}
	e.tasksMu.Unlock()
}

func (e *Executor) runAgent(ctx context.Context, req *RunRequest, taskID string, writeEvent func(string, any)) (*ParsedOutput, *UsageInfo, *TaskError) {
	start := time.Now()

	writeEvent("status", map[string]string{
		"task_id": taskID,
		"status":  "running",
		"message": "Agent started",
	})

	// Timeout context
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Fetch skill
	var skillContent string
	if req.SkillURL != "" {
		var err error
		skillContent, err = e.skillCache.Fetch(ctx, req.SkillURL)
		if err != nil {
			return nil, nil, &TaskError{Code: "skill_fetch_failed", Message: err.Error()}
		}
	}

	// Build system prompt
	systemPrompt := buildSystemPrompt(skillContent, req.Context)

	// Create temp workspace
	tmpDir, err := os.MkdirTemp("", "klaw-task-"+taskID+"-")
	if err != nil {
		return nil, nil, &TaskError{Code: "workspace_error", Message: err.Error()}
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Build tool registry with per-task env
	env := envFromContext(req.Context)
	registry := tool.APIRegistry(tmpDir, env)

	// Filter tools if specified
	if len(req.Tools) > 0 {
		registry = registry.Filter(req.Tools)
	}

	// Build tool definitions for provider
	allTools := registry.All()
	toolDefs := make([]provider.ToolDefinition, len(allTools))
	for i, t := range allTools {
		toolDefs[i] = provider.ToolDefinition{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.Schema(),
		}
	}

	// Agent loop
	messages := []provider.Message{
		{Role: "user", Content: req.Prompt},
	}

	usage := &UsageInfo{Model: req.Model}
	var lastText string

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for iteration := 0; iteration < req.MaxIterations; iteration++ {
		usage.Iterations = iteration + 1

		// Check context
		if ctx.Err() != nil {
			return nil, usage, &TaskError{Code: "timeout", Message: "agent exceeded maximum execution time"}
		}

		// Stream from provider
		events, err := e.provider.Stream(ctx, &provider.ChatRequest{
			System:    systemPrompt,
			Messages:  messages,
			Tools:     toolDefs,
			MaxTokens: 16384,
		})
		if err != nil {
			return nil, usage, &TaskError{Code: "provider_error", Message: err.Error()}
		}

		var textContent strings.Builder
		var toolCalls []provider.ToolCall
		var streamErr error

		for event := range events {
			switch event.Type {
			case "text":
				textContent.WriteString(event.Text)
				writeEvent("text", map[string]string{
					"task_id": taskID,
					"content": event.Text,
				})
			case "tool_use":
				toolCalls = append(toolCalls, *event.ToolUse)
			case "error":
				streamErr = event.Error
			case "stop":
				if event.Usage != nil {
					usage.InputTokens += event.Usage.InputTokens
					usage.OutputTokens += event.Usage.OutputTokens
				}
			}
		}

		if streamErr != nil {
			return nil, usage, &TaskError{Code: "stream_error", Message: streamErr.Error()}
		}

		// Add assistant message to history
		messages = append(messages, provider.Message{
			Role:      "assistant",
			Content:   textContent.String(),
			ToolCalls: toolCalls,
		})

		// No tool calls → done
		if len(toolCalls) == 0 {
			lastText = textContent.String()
			break
		}

		// Execute tools in parallel
		type toolExec struct {
			tc       provider.ToolCall
			result   *tool.Result
			duration time.Duration
		}
		results := make([]toolExec, len(toolCalls))

		for i, tc := range toolCalls {
			results[i].tc = tc
			writeEvent("tool_call", map[string]any{
				"task_id": taskID,
				"tool":    tc.Name,
				"input":   json.RawMessage(tc.Input),
			})
		}

		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						results[idx].result = &tool.Result{
							Content: fmt.Sprintf("tool panicked: %v", r),
							IsError: true,
						}
					}
				}()

				tc := results[idx].tc
				toolStart := time.Now()

				t, ok := registry.Get(tc.Name)
				if !ok {
					results[idx].result = &tool.Result{
						Content: fmt.Sprintf("unknown tool: %s", tc.Name),
						IsError: true,
					}
					results[idx].duration = time.Since(toolStart)
					return
				}

				toolCtx, toolCancel := context.WithTimeout(ctx, 2*time.Minute)
				defer toolCancel()

				res, err := t.Execute(toolCtx, tc.Input)
				if err != nil {
					results[idx].result = &tool.Result{
						Content: fmt.Sprintf("tool error: %v", err),
						IsError: true,
					}
				} else {
					results[idx].result = res
				}
				results[idx].duration = time.Since(toolStart)
			}(i)
		}
		wg.Wait()

		// Emit results in order and append to history
		for _, r := range results {
			usage.ToolCalls++

			preview := r.result.Content
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}

			writeEvent("tool_result", map[string]any{
				"task_id":     taskID,
				"tool":        r.tc.Name,
				"duration_ms": r.duration.Milliseconds(),
				"is_error":    r.result.IsError,
				"preview":     preview,
			})

			// Detect artifacts
			for _, art := range detectArtifacts(r.result.Content) {
				writeEvent("artifact", map[string]any{
					"task_id":  taskID,
					"artifact": art,
				})
			}

			messages = append(messages, provider.Message{
				Role: "user",
				ToolResult: &provider.ToolResult{
					ToolUseID: r.tc.ID,
					Content:   r.result.Content,
					IsError:   r.result.IsError,
				},
			})
		}
	}

	usage.DurationMs = time.Since(start).Milliseconds()

	// Parse final output
	parsed := ParseAgentOutput(lastText)
	return &parsed, usage, nil
}

// --- Helpers ---

func validateRequest(req *RunRequest) error {
	if req.Prompt == "" {
		return fmt.Errorf("prompt is required")
	}
	if req.TimeoutSeconds < 0 {
		return fmt.Errorf("timeout_seconds must be non-negative")
	}
	if req.MaxIterations < 0 {
		return fmt.Errorf("max_iterations must be non-negative")
	}
	return nil
}

func writeSSEEvent(w http.ResponseWriter, f http.Flusher, event string, data any) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, jsonData)
	f.Flush()
}

func buildSystemPrompt(skill string, ctx map[string]any) string {
	var parts []string

	parts = append(parts, `You are a klaw creative agent. You have access to tools for completing tasks.
Be direct, efficient, and thorough. Execute tasks step by step.`)

	if skill != "" {
		parts = append(parts, "---\n\n"+skill)
	}

	if len(ctx) > 0 {
		section := buildContextSection(ctx)
		if section != "" {
			parts = append(parts, "---\n\n# Context\n\n"+section)
		}
	}

	return strings.Join(parts, "\n\n")
}

func buildContextSection(ctx map[string]any) string {
	var lines []string
	var memories []string

	for key, val := range ctx {
		// Skip sensitive keys
		if key == "eachlabs_api_key" {
			continue
		}

		// Extract memories separately
		if key == "memories" {
			if memList, ok := val.([]any); ok {
				for _, m := range memList {
					if s, ok := m.(string); ok {
						memories = append(memories, s)
					}
				}
			}
			continue
		}

		// Render value
		switch v := val.(type) {
		case string:
			lines = append(lines, fmt.Sprintf("%s: %s", formatKey(key), v))
		case []any:
			strs := make([]string, 0, len(v))
			for _, item := range v {
				strs = append(strs, fmt.Sprintf("%v", item))
			}
			lines = append(lines, fmt.Sprintf("%s: %s", formatKey(key), strings.Join(strs, ", ")))
		default:
			lines = append(lines, fmt.Sprintf("%s: %v", formatKey(key), v))
		}
	}

	result := strings.Join(lines, "\n")

	if len(memories) > 0 {
		result += "\n\n# Memories from Previous Runs\n\n"
		for _, m := range memories {
			result += "- " + m + "\n"
		}
	}

	return result
}

func formatKey(key string) string {
	words := strings.Split(key, "_")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

func envFromContext(ctx map[string]any) map[string]string {
	env := make(map[string]string)
	if key, ok := ctx["eachlabs_api_key"].(string); ok && key != "" {
		env["EACHLABS_API_KEY"] = key
	}
	return env
}

var artifactURLPattern = regexp.MustCompile(`https?://[^\s"'\)]+\.(png|jpg|jpeg|gif|webp|mp4|webm|mp3|wav|pdf|svg)`)
var eachlabsURLPattern = regexp.MustCompile(`https?://[^\s"'\)]*eachlabs\.ai[^\s"'\)]*`)

func detectArtifacts(content string) []Artifact {
	seen := make(map[string]bool)
	var artifacts []Artifact

	for _, match := range eachlabsURLPattern.FindAllString(content, -1) {
		if seen[match] {
			continue
		}
		seen[match] = true
		artifacts = append(artifacts, Artifact{
			Type: inferType(match),
			URL:  match,
		})
	}

	for _, match := range artifactURLPattern.FindAllString(content, -1) {
		if seen[match] {
			continue
		}
		seen[match] = true
		artifacts = append(artifacts, Artifact{
			Type: inferType(match),
			URL:  match,
		})
	}

	return artifacts
}

func inferType(url string) string {
	lower := strings.ToLower(url)
	switch {
	case strings.HasSuffix(lower, ".mp4") || strings.HasSuffix(lower, ".webm"):
		return "video"
	case strings.HasSuffix(lower, ".mp3") || strings.HasSuffix(lower, ".wav"):
		return "audio"
	case strings.HasSuffix(lower, ".pdf"):
		return "document"
	default:
		return "image"
	}
}

func (e *Executor) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		e.tasksMu.Lock()
		now := time.Now()
		for id, task := range e.tasks {
			if task.EndedAt != nil && now.Sub(*task.EndedAt) > 30*time.Minute {
				delete(e.tasks, id)
			}
		}
		e.tasksMu.Unlock()
	}
}
