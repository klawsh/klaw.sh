package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/eachlabs/klaw/internal/provider"
)

// ServerConfig holds API server configuration.
type ServerConfig struct {
	Host       string
	Port       int
	Workers    int
	MaxTimeout time.Duration
	NoAuth     bool
}

// Server is the Creative Agent HTTP API server.
type Server struct {
	executor *Executor
	auth     *AuthStore
	cfg      ServerConfig
	server   *http.Server
}

// NewServer creates a new API server.
func NewServer(cfg ServerConfig, prov provider.Provider, auth *AuthStore) *Server {
	workers := cfg.Workers
	if workers <= 0 {
		workers = 50
	}
	maxTimeout := cfg.MaxTimeout
	if maxTimeout <= 0 {
		maxTimeout = 10 * time.Minute
	}

	executor := NewExecutor(ExecutorConfig{
		Provider:   prov,
		Workers:    workers,
		MaxIter:    50,
		MaxTimeout: maxTimeout,
	})

	return &Server{
		executor: executor,
		auth:     auth,
		cfg:      cfg,
	}
}

// Start runs the HTTP server. Blocks until context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/run", s.handleRun)
	mux.HandleFunc("/api/v1/tasks/", s.handleGetTask)
	mux.HandleFunc("/api/v1/health", s.handleHealth)

	var handler http.Handler = mux
	if !s.cfg.NoAuth && s.auth != nil {
		handler = s.auth.Middleware(handler)
	}
	handler = s.corsMiddleware(handler)

	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	s.server = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 15 * time.Minute,
		IdleTimeout:  60 * time.Second,
		BaseContext: func(l net.Listener) context.Context {
			return ctx
		},
	}

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutdownCtx)
	}()

	return s.server.ListenAndServe()
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "only POST is allowed"})
		return
	}

	var req RunRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	s.executor.Run(r.Context(), w, &req)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "only GET is allowed"})
		return
	}

	taskID := strings.TrimPrefix(r.URL.Path, "/api/v1/tasks/")
	if taskID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task_id required"})
		return
	}

	task, ok := s.executor.GetTask(taskID)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "task not found"})
		return
	}

	// Support event replay with ?after=N
	afterStr := r.URL.Query().Get("after")
	after := 0
	if afterStr != "" {
		after, _ = strconv.Atoi(afterStr)
	}

	// Filter events
	var events []SSEEvent
	for _, e := range task.Events {
		if e.ID > after {
			events = append(events, e)
		}
	}

	resp := map[string]any{
		"task_id":    task.ID,
		"status":     task.Status,
		"started_at": task.StartedAt,
		"events":     events,
	}

	if task.Result != nil {
		resp["result"] = task.Result
	}
	if task.Usage != nil {
		resp["usage"] = task.Usage
	}
	if task.Error != nil {
		resp["error"] = task.Error
	}
	if task.EndedAt != nil {
		resp["ended_at"] = task.EndedAt
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"active_tasks": s.executor.ActiveCount(),
		"max_workers":  s.cfg.Workers,
	})
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Expose-Headers", "X-Task-ID")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
