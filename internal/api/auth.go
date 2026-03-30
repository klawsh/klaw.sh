package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/eachlabs/klaw/internal/config"
)

// APIKey represents a stored API key.
type APIKey struct {
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// AuthStore manages API keys on disk at ~/.klaw/api-keys.json.
type AuthStore struct {
	mu   sync.RWMutex
	keys []APIKey
	path string
}

// NewAuthStore loads or creates the key store.
func NewAuthStore() (*AuthStore, error) {
	s := &AuthStore{
		path: filepath.Join(config.StateDir(), "api-keys.json"),
	}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load API keys: %w", err)
	}
	return s, nil
}

// Create generates a new API key with the given name.
func (s *AuthStore) Create(name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate key: %w", err)
	}

	key := "klk_" + hex.EncodeToString(b)

	s.keys = append(s.keys, APIKey{
		Key:       key,
		Name:      name,
		CreatedAt: time.Now(),
	})

	if err := s.save(); err != nil {
		return "", err
	}

	return key, nil
}

// Revoke removes a key. Returns true if found and removed.
func (s *AuthStore) Revoke(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, k := range s.keys {
		if k.Key == key {
			s.keys = append(s.keys[:i], s.keys[i+1:]...)
			_ = s.save()
			return true
		}
	}
	return false
}

// List returns all stored keys with masked key strings.
func (s *AuthStore) List() []APIKey {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]APIKey, len(s.keys))
	for i, k := range s.keys {
		masked := k.Key
		if len(masked) > 8 {
			masked = masked[:8] + "..."
		}
		result[i] = APIKey{
			Key:       masked,
			Name:      k.Name,
			CreatedAt: k.CreatedAt,
		}
	}
	return result
}

// Validate checks if a key is valid.
func (s *AuthStore) Validate(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, k := range s.keys {
		if k.Key == key {
			return true
		}
	}
	return false
}

// Middleware returns an http.Handler that enforces Bearer token auth.
func (s *AuthStore) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip health check
		if r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}

		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if token == "" || token == auth {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid Authorization header"})
			return
		}

		if !s.Validate(token) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid API key"})
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *AuthStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.keys)
}

func (s *AuthStore) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.keys, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0600)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
