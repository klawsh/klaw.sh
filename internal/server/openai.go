package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/eachlabs/klaw/internal/agent"
	"github.com/eachlabs/klaw/internal/channel"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/tool"
	"github.com/google/uuid"
)

// ── OpenAI-compatible request/response types ─────────────────

type chatRequest struct {
	Model       string       `json:"model"`
	Messages    []apiMessage `json:"messages"`
	Stream      bool         `json:"stream"`
	Temperature *float64     `json:"temperature,omitempty"`
	MaxTokens   *int         `json:"max_tokens,omitempty"`
	SessionID   string       `json:"session_id,omitempty"`
}

type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

// Streaming chunk types
type streamChunk struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []streamChoice `json:"choices"`
}

type streamChoice struct {
	Index        int          `json:"index"`
	Delta        deltaContent `json:"delta"`
	FinishReason *string      `json:"finish_reason"`
}

type deltaContent struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// Non-streaming response types
type completionResponse struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []completionChoice `json:"choices"`
	Usage   usageInfo          `json:"usage"`
}

type completionChoice struct {
	Index        int        `json:"index"`
	Message      apiMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type usageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ── Handler ──────────────────────────────────────────────────

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method_not_allowed", "Only POST is allowed")
		return
	}

	// Parse request
	var req chatRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", "Invalid JSON: "+err.Error())
		return
	}

	if len(req.Messages) == 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", "messages array is required")
		return
	}

	// Resolve model → provider mapping
	modelID := req.Model
	if modelID == "" {
		modelID = s.cfg.DefaultModel
	}
	mapping, ok := s.cfg.Models[modelID]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "invalid_request_error", "model_not_found",
			fmt.Sprintf("Model '%s' not found. Use GET /v1/models to list available models.", modelID))
		return
	}

	// Resolve provider
	prov, ok := s.providers[mapping.Provider]
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "server_error", "internal_error",
			fmt.Sprintf("Provider '%s' not configured", mapping.Provider))
		return
	}

	// Convert messages: separate system prompt, prior history, and last user message
	systemPrompt, priorHistory, lastUserContent := convertMessages(req.Messages)

	if lastUserContent == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "invalid_request", "Last message must be from user role")
		return
	}

	// Build full system prompt: base + per-model skills + client system message
	fullSystemPrompt := s.systemPrompt + s.skillsIndexForModel(mapping)
	if systemPrompt != "" {
		fullSystemPrompt = fullSystemPrompt + "\n\n" + systemPrompt
	}

	// Acquire concurrency slot
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		writeAPIError(w, http.StatusTooManyRequests, "rate_limit_error", "rate_limit_exceeded", "Too many concurrent requests")
		return
	}

	requestID := fmt.Sprintf("chatcmpl-%s", uuid.New().String()[:12])

	// Session support: if session_id provided, use/resume persistent history.
	// Also check X-Session-ID header as alternative.
	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = r.Header.Get("X-Session-ID")
	}

	// Determine initial history for this request
	var initialHistory []provider.Message
	var sess *session

	if sessionID != "" {
		sess = s.sessions.get(sessionID)
		sess.mu.Lock()
		defer sess.mu.Unlock()
		// Session history takes priority — client messages are ignored except last user msg.
		// Session already contains the full conversation including tool results.
		initialHistory = sess.history
	} else {
		// Stateless: client sends full history each time
		initialHistory = priorHistory
	}

	// Resolve tools: session requests get per-session workDir so files go under the session directory
	tools := s.tools
	if sess != nil {
		tools = tool.DefaultRegistry(sess.filesDir())
	}

	if req.Stream {
		s.handleStreamingRequest(w, r, prov, fullSystemPrompt, initialHistory, lastUserContent, modelID, requestID, sess, tools)
	} else {
		s.handleNonStreamingRequest(w, r, prov, fullSystemPrompt, initialHistory, lastUserContent, modelID, requestID, sess, tools)
	}
}

// ── Streaming ────────────────────────────────────────────────

func (s *Server) handleStreamingRequest(
	w http.ResponseWriter, r *http.Request,
	prov provider.Provider,
	systemPrompt string,
	priorHistory []provider.Message,
	lastUserContent string,
	modelID, requestID string,
	sess *session,
	tools *tool.Registry,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "server_error", "internal_error", "Streaming not supported")
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Request-ID", requestID)
	w.WriteHeader(http.StatusOK)

	// Send initial role chunk
	writeSSE(w, flusher, streamChunk{
		ID:      requestID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   modelID,
		Choices: []streamChoice{{
			Index: 0,
			Delta: deltaContent{Role: "assistant"},
		}},
	})

	// Create HTTPChannel and agent
	ch := NewHTTPChannel(r.Context(), requestID)

	ag := agent.New(agent.Config{
		Provider:       prov,
		Channel:        ch,
		Tools:          tools,
		Memory:         s.memory,
		SystemPrompt:   systemPrompt,
		InitialHistory: priorHistory,
		MaxTokens:      8192,
		MaxIterations:  50,
	})

	// Push the last user message so RunOnce can receive it
	ch.PushUserMessage(&channel.Message{
		ID:      requestID,
		Role:    "user",
		Content: lastUserContent,
	})

	// Run agent in background
	agentDone := make(chan error, 1)
	go func() {
		agentDone <- ag.RunOnce(r.Context())
		// Persist history to session if session-based
		if sess != nil {
			sess.history = ag.History()
			sess.save()
		}
		ch.Stop()
	}()

	// Heartbeat ticker for long-running tool executions
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	// Stream SSE chunks from agent output
	for {
		select {
		case msg, ok := <-ch.outgoing:
			if !ok {
				// Channel closed — agent done. Send finish + [DONE].
				sendFinish(w, flusher, requestID, modelID, "stop")
				return
			}
			heartbeat.Reset(15 * time.Second)
			s.processMessage(w, flusher, msg, requestID, modelID)

		case <-heartbeat.C:
			// SSE comment to keep connection alive
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()

		case <-r.Context().Done():
			// Client disconnected
			ch.cancel()
			return
		}
	}
}

func (s *Server) processMessage(
	w http.ResponseWriter, flusher http.Flusher,
	msg *channel.Message,
	requestID, modelID string,
) {
	switch {
	case msg.IsDone:
		// Don't send here — the channel close in the main loop handles finish.
		return

	case msg.Role == "error":
		writeSSE(w, flusher, streamChunk{
			ID:      requestID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   modelID,
			Choices: []streamChoice{{
				Index: 0,
				Delta: deltaContent{Content: "\n\n[ERROR] " + msg.Content},
			}},
		})

	case msg.IsPartial && msg.Content != "":
		// Strip box drawing characters for clean API output
		content := cleanToolOutput(msg.Content)
		if content == "" {
			return
		}
		writeSSE(w, flusher, streamChunk{
			ID:      requestID,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   modelID,
			Choices: []streamChoice{{
				Index: 0,
				Delta: deltaContent{Content: content},
			}},
		})
	}
}

// ── Non-streaming ────────────────────────────────────────────

func (s *Server) handleNonStreamingRequest(
	w http.ResponseWriter, r *http.Request,
	prov provider.Provider,
	systemPrompt string,
	priorHistory []provider.Message,
	lastUserContent string,
	modelID, requestID string,
	sess *session,
	tools *tool.Registry,
) {
	ch := NewHTTPChannel(r.Context(), requestID)

	ag := agent.New(agent.Config{
		Provider:       prov,
		Channel:        ch,
		Tools:          tools,
		Memory:         s.memory,
		SystemPrompt:   systemPrompt,
		InitialHistory: priorHistory,
		MaxTokens:      8192,
		MaxIterations:  50,
	})

	// Push the last user message so RunOnce can receive it
	ch.PushUserMessage(&channel.Message{
		ID:      requestID,
		Role:    "user",
		Content: lastUserContent,
	})

	agentDone := make(chan error, 1)
	go func() {
		agentDone <- ag.RunOnce(r.Context())
		// Persist history to session if session-based
		if sess != nil {
			sess.history = ag.History()
			sess.save()
		}
		ch.Stop()
	}()

	// Collect all output
	var fullContent strings.Builder
	for msg := range ch.outgoing {
		if msg.IsPartial && msg.Content != "" {
			content := cleanToolOutput(msg.Content)
			fullContent.WriteString(content)
		}
	}

	// Check for agent error
	var agentErr error
	select {
	case agentErr = <-agentDone:
	default:
	}

	finishReason := "stop"
	if agentErr != nil {
		fullContent.WriteString("\n\n[ERROR] " + agentErr.Error())
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(completionResponse{
		ID:      requestID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelID,
		Choices: []completionChoice{{
			Index:        0,
			Message:      apiMessage{Role: "assistant", Content: fullContent.String()},
			FinishReason: finishReason,
		}},
	})
}

// ── Helpers ──────────────────────────────────────────────────

// convertMessages splits OpenAI messages into system prompt, prior history, and last user message.
// The last user message is extracted so it can be pushed via PushUserMessage to the HTTPChannel.
func convertMessages(msgs []apiMessage) (systemPrompt string, priorHistory []provider.Message, lastUserContent string) {
	var systemParts []string
	var allHistory []provider.Message

	for _, m := range msgs {
		switch m.Role {
		case "system":
			systemParts = append(systemParts, m.Content)
		case "user":
			allHistory = append(allHistory, provider.Message{Role: "user", Content: m.Content})
		case "assistant":
			allHistory = append(allHistory, provider.Message{Role: "assistant", Content: m.Content})
		// "tool", "function" roles are ignored — klaw manages its own tools
		}
	}

	systemPrompt = strings.Join(systemParts, "\n\n")

	// Split: everything except last user message → priorHistory, last user → lastUserContent
	// Walk backwards to find the last user message
	for i := len(allHistory) - 1; i >= 0; i-- {
		if allHistory[i].Role == "user" {
			lastUserContent = allHistory[i].Content
			priorHistory = append(allHistory[:i], allHistory[i+1:]...)
			return
		}
	}

	// No user message found
	return
}

// cleanToolOutput strips box-drawing prefixes from tool output messages.
func cleanToolOutput(content string) string {
	// Tool start: "\n╭─ bash: ls\n"
	if strings.HasPrefix(content, "\n╭─ ") || strings.HasPrefix(content, "╭─ ") {
		trimmed := strings.TrimPrefix(content, "\n")
		trimmed = strings.TrimPrefix(trimmed, "╭─ ")
		trimmed = strings.TrimSuffix(trimmed, "\n")
		if trimmed != "" {
			return "[Tool: " + trimmed + "]\n"
		}
		return ""
	}

	// Tool end: "╰─\n"
	if strings.HasPrefix(content, "╰─") {
		return ""
	}

	// Tool output lines: "│ some output\n"
	if strings.HasPrefix(content, "│ ") {
		lines := strings.Split(content, "\n")
		var cleaned []string
		for _, line := range lines {
			if strings.HasPrefix(line, "│ ") {
				cleaned = append(cleaned, strings.TrimPrefix(line, "│ "))
			} else if strings.HasPrefix(line, "│") {
				cleaned = append(cleaned, strings.TrimPrefix(line, "│"))
			} else if line == "╰─" {
				// skip closing marker
			} else if line != "" {
				cleaned = append(cleaned, line)
			}
		}
		result := strings.Join(cleaned, "\n")
		if result != "" {
			return result + "\n"
		}
		return ""
	}

	// Compaction message — filter out internal messages
	if strings.Contains(content, "Compacting context...") {
		return ""
	}

	return content
}

func sendFinish(w http.ResponseWriter, flusher http.Flusher, requestID, modelID, reason string) {
	finishReason := reason
	writeSSE(w, flusher, streamChunk{
		ID:      requestID,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   modelID,
		Choices: []streamChoice{{
			Index:        0,
			Delta:        deltaContent{},
			FinishReason: &finishReason,
		}},
	})
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, chunk streamChunk) {
	data, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func writeAPIError(w http.ResponseWriter, status int, errType, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(apiError{
		Error: apiErrorBody{
			Message: message,
			Type:    errType,
			Code:    code,
		},
	})
}
