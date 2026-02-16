// Package session provides conversation persistence for klaw chat.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eachlabs/klaw/internal/config"
	"github.com/eachlabs/klaw/internal/provider"
)

// Session represents a persistent chat session.
type Session struct {
	ID           string             `json:"id"`
	Name         string             `json:"name,omitempty"`
	Model        string             `json:"model"`
	Provider     string             `json:"provider"`
	Agent        string             `json:"agent,omitempty"`
	SystemPrompt string             `json:"system_prompt,omitempty"`
	WorkDir      string             `json:"work_dir,omitempty"`
	Messages     []provider.Message `json:"messages"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

// Manager handles session persistence with debounced saving.
type Manager struct {
	session     *Session
	dir         string
	mu          sync.Mutex
	dirty       bool
	lastSave    time.Time
	debounceMin time.Duration
}

// NewManager creates a new session manager.
func NewManager() *Manager {
	return &Manager{
		dir:         config.SessionsDir(),
		debounceMin: 2 * time.Second,
	}
}

// generateID creates a session ID in format: YYYYMMDD-HHMMSS-<4-char-uuid>
func generateID() string {
	now := time.Now()
	datePart := now.Format("20060102-150405")

	// Generate 2 random bytes (4 hex chars)
	b := make([]byte, 2)
	_, _ = rand.Read(b)
	randPart := hex.EncodeToString(b)

	return fmt.Sprintf("%s-%s", datePart, randPart)
}

// New creates a new session with the given parameters.
func (m *Manager) New(model, providerName, agent, systemPrompt, workDir string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	m.session = &Session{
		ID:           generateID(),
		Model:        model,
		Provider:     providerName,
		Agent:        agent,
		SystemPrompt: systemPrompt,
		WorkDir:      workDir,
		Messages:     make([]provider.Message, 0),
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	return m.session
}

// SetName sets the session name.
func (m *Manager) SetName(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.session != nil {
		m.session.Name = name
		m.dirty = true
	}
}

// Load loads an existing session by ID.
func (m *Manager) Load(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	path := filepath.Join(m.dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session not found: %s", id)
		}
		return nil, fmt.Errorf("failed to read session: %w", err)
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("failed to parse session: %w", err)
	}

	m.session = &session
	return m.session, nil
}

// Session returns the current session.
func (m *Manager) Session() *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.session
}

// SetMessages updates the session messages.
func (m *Manager) SetMessages(messages []provider.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.session != nil {
		m.session.Messages = messages
		m.session.UpdatedAt = time.Now()
		m.dirty = true
	}
}

// Save saves the session to disk with debouncing.
// It will skip saving if less than debounceMin has passed since last save.
func (m *Manager) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.session == nil || !m.dirty {
		return nil
	}

	// Check debounce
	if time.Since(m.lastSave) < m.debounceMin {
		return nil
	}

	return m.saveInternal()
}

// ForceSave saves the session immediately, ignoring debounce.
func (m *Manager) ForceSave() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.session == nil {
		return nil
	}

	return m.saveInternal()
}

// saveInternal performs the actual save operation. Caller must hold the lock.
func (m *Manager) saveInternal() error {
	// Ensure directory exists
	if err := os.MkdirAll(m.dir, 0755); err != nil {
		return fmt.Errorf("failed to create sessions dir: %w", err)
	}

	path := filepath.Join(m.dir, m.session.ID+".json")
	data, err := json.MarshalIndent(m.session, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal session: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write session: %w", err)
	}

	m.dirty = false
	m.lastSave = time.Now()
	return nil
}

// List returns all sessions sorted by updated time (newest first).
func (m *Manager) List() ([]*Session, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sessions []*Session
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		path := filepath.Join(m.dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}

		sessions = append(sessions, &sess)
	}

	// Sort by updated time, newest first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	return sessions, nil
}

// Delete removes a session by ID.
func (m *Manager) Delete(id string) error {
	path := filepath.Join(m.dir, id+".json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("session not found: %s", id)
		}
		return fmt.Errorf("failed to delete session: %w", err)
	}
	return nil
}

// Messages returns the current session's messages.
func (m *Manager) Messages() []provider.Message {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.session == nil {
		return nil
	}
	return m.session.Messages
}
