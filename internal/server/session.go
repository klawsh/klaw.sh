package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/provider"
)

// session holds persistent conversation state for a client session.
// Each session gets its own directory: {baseDir}/{id}/
//   - history.json — conversation history
//   - files/       — agent output files (images, videos, etc.)
type session struct {
	mu      sync.Mutex
	id      string
	history []provider.Message
	baseDir string // parent directory for all sessions
}

// sessionPool manages active sessions backed by filesystem.
type sessionPool struct {
	mu       sync.RWMutex
	sessions map[string]*session
	dir      string
}

func newSessionPool() *sessionPool {
	dir := filepath.Join(config.StateDir(), "sessions", "http")
	os.MkdirAll(dir, 0755)
	return &sessionPool{
		sessions: make(map[string]*session),
		dir:      dir,
	}
}

// get returns an existing session or loads/creates one.
func (sp *sessionPool) get(id string) *session {
	sp.mu.RLock()
	s, ok := sp.sessions[id]
	sp.mu.RUnlock()
	if ok {
		return s
	}

	sp.mu.Lock()
	defer sp.mu.Unlock()

	// Double-check after write lock
	if s, ok := sp.sessions[id]; ok {
		return s
	}

	s = &session{
		id:      id,
		baseDir: sp.dir,
	}

	// Create session directory and files subdirectory
	os.MkdirAll(s.filesDir(), 0755)

	// Try to load from disk
	s.load()

	sp.sessions[id] = s
	return s
}

// sessionDir returns the session's own directory.
func (s *session) sessionDir() string {
	return filepath.Join(s.baseDir, s.id)
}

// filesDir returns the directory where agent output files are stored.
func (s *session) filesDir() string {
	return filepath.Join(s.baseDir, s.id, "files")
}

// historyPath returns the path to the session's history file.
func (s *session) historyPath() string {
	return filepath.Join(s.baseDir, s.id, "history.json")
}

// load reads session history from disk.
func (s *session) load() {
	// Try new path first
	data, err := os.ReadFile(s.historyPath())
	if err != nil {
		// Try legacy path (flat {id}.json)
		legacyPath := filepath.Join(s.baseDir, s.id+".json")
		data, err = os.ReadFile(legacyPath)
		if err != nil {
			s.history = make([]provider.Message, 0)
			return
		}
		// Migrate: save to new location and remove legacy file
		if json.Unmarshal(data, &s.history) == nil {
			os.MkdirAll(s.sessionDir(), 0755)
			s.save()
			os.Remove(legacyPath)
			return
		}
	}
	if err := json.Unmarshal(data, &s.history); err != nil {
		s.history = make([]provider.Message, 0)
	}
}

// save writes session history to disk.
func (s *session) save() error {
	os.MkdirAll(s.sessionDir(), 0755)
	data, err := json.Marshal(s.history)
	if err != nil {
		return err
	}
	return os.WriteFile(s.historyPath(), data, 0644)
}
