package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/eachlabs/klaw/internal/memory"
	"github.com/eachlabs/klaw/internal/provider"
	"github.com/eachlabs/klaw/internal/skill"
	"github.com/eachlabs/klaw/internal/tool"
)

// OpenAIConfig holds gateway configuration.
type OpenAIConfig struct {
	Enabled       bool                    `toml:"enabled"`
	AuthRequired  bool                    `toml:"auth_required"`
	APIKeys       []string                `toml:"api_keys"`
	DefaultModel  string                  `toml:"default_model"`
	Models        map[string]ModelMapping `toml:"models"`
	CORSOrigins   []string                `toml:"cors_origins"`
	MaxConcurrent int                     `toml:"max_concurrent"`
}

// ModelMapping maps an OpenAI model ID to a klaw agent + provider + skills.
type ModelMapping struct {
	Agent    string   `toml:"agent"`
	Provider string   `toml:"provider"`
	Skills   []string // skill names; "all" or empty = all installed skills
}

// ServerConfig holds host/port for the HTTP server.
type ServerConfig struct {
	Host string
	Port int
}

// Server is the OpenAI-compatible HTTP gateway.
type Server struct {
	cfg          OpenAIConfig
	serverCfg    ServerConfig
	providers    map[string]provider.Provider
	tools        *tool.Registry
	memory       memory.Memory
	systemPrompt string // base system prompt (without skills)
	skillLoader  *skill.SkillLoader
	sem          chan struct{}
	sessions     *sessionPool
}

// New creates a new gateway server.
func New(cfg OpenAIConfig, serverCfg ServerConfig, providers map[string]provider.Provider, tools *tool.Registry, mem memory.Memory, systemPrompt string, skillLoader *skill.SkillLoader) *Server {
	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 20
	}
	return &Server{
		cfg:          cfg,
		serverCfg:    serverCfg,
		providers:    providers,
		tools:        tools,
		memory:       mem,
		systemPrompt: systemPrompt,
		skillLoader:  skillLoader,
		sem:          make(chan struct{}, maxConcurrent),
		sessions:     newSessionPool(),
	}
}

// skillsIndexForModel builds the skills system prompt section for a specific model.
func (s *Server) skillsIndexForModel(mapping ModelMapping) string {
	if s.skillLoader == nil {
		return ""
	}

	// Determine which skills to include
	skillNames := mapping.Skills

	// Empty or contains "all" → use all installed skills
	if len(skillNames) == 0 || (len(skillNames) == 1 && skillNames[0] == "all") {
		all, err := s.skillLoader.ListSkills()
		if err != nil {
			return ""
		}
		skillNames = all
	}

	if len(skillNames) == 0 {
		return ""
	}

	return s.skillLoader.GetSkillsIndex(skillNames)
}

// Start runs the HTTP server. Blocks until context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	handler := s.corsMiddleware(s.authMiddleware(mux))

	addr := fmt.Sprintf("%s:%d", s.serverCfg.Host, s.serverCfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		BaseContext: func(l net.Listener) context.Context {
			return ctx
		},
	}

	// Graceful shutdown
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	return srv.ListenAndServe()
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := "*"
		if len(s.cfg.CORSOrigins) > 0 {
			origin = s.cfg.CORSOrigins[0]
			// Check if the request origin matches any allowed origin
			reqOrigin := r.Header.Get("Origin")
			for _, allowed := range s.cfg.CORSOrigins {
				if allowed == "*" || allowed == reqOrigin {
					origin = reqOrigin
					break
				}
			}
		}

		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.AuthRequired {
			next.ServeHTTP(w, r)
			return
		}

		auth := r.Header.Get("Authorization")
		if auth == "" {
			writeAPIError(w, http.StatusUnauthorized, "authentication_error", "invalid_api_key", "Missing Authorization header")
			return
		}

		token := auth
		if len(auth) > 7 && auth[:7] == "Bearer " {
			token = auth[7:]
		}

		valid := false
		for _, key := range s.cfg.APIKeys {
			if key == token {
				valid = true
				break
			}
		}
		if !valid {
			writeAPIError(w, http.StatusUnauthorized, "authentication_error", "invalid_api_key", "Invalid API key")
			return
		}

		next.ServeHTTP(w, r)
	})
}
