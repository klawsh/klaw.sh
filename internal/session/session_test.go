package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eachlabs/klaw/internal/provider"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	return &Manager{
		dir:         dir,
		debounceMin: 2 * time.Second,
	}
}

func TestGenerateID(t *testing.T) {
	id := generateID()

	// Format: YYYYMMDD-HHMMSS-xxxx
	parts := strings.Split(id, "-")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts, got %d: %q", len(parts), id)
	}
	if len(parts[0]) != 8 {
		t.Errorf("date part should be 8 chars: %q", parts[0])
	}
	if len(parts[1]) != 6 {
		t.Errorf("time part should be 6 chars: %q", parts[1])
	}
	if len(parts[2]) != 4 {
		t.Errorf("random part should be 4 chars: %q", parts[2])
	}

	// Uniqueness
	id2 := generateID()
	if id == id2 {
		t.Error("two generated IDs should be different")
	}
}

func TestManager_NewSession(t *testing.T) {
	m := newTestManager(t)
	sess := m.New("model", "anthropic", "default", "sys prompt", "/work")

	if sess.ID == "" {
		t.Error("session ID should not be empty")
	}
	if sess.Model != "model" {
		t.Errorf("model = %q", sess.Model)
	}
	if sess.Provider != "anthropic" {
		t.Errorf("provider = %q", sess.Provider)
	}
	if sess.Agent != "default" {
		t.Errorf("agent = %q", sess.Agent)
	}
	if sess.SystemPrompt != "sys prompt" {
		t.Errorf("system_prompt = %q", sess.SystemPrompt)
	}
	if sess.WorkDir != "/work" {
		t.Errorf("work_dir = %q", sess.WorkDir)
	}
	if len(sess.Messages) != 0 {
		t.Error("messages should be empty initially")
	}
	if sess.CreatedAt.IsZero() {
		t.Error("created_at should be set")
	}
}

func TestManager_SetName(t *testing.T) {
	m := newTestManager(t)
	m.New("model", "prov", "", "", "")

	m.SetName("test session")

	sess := m.Session()
	if sess.Name != "test session" {
		t.Errorf("name = %q, want 'test session'", sess.Name)
	}
}

func TestManager_SetName_NoSession(t *testing.T) {
	m := newTestManager(t)
	// Should not panic
	m.SetName("test")
}

func TestManager_SetMessages(t *testing.T) {
	m := newTestManager(t)
	m.New("model", "prov", "", "", "")

	msgs := []provider.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	m.SetMessages(msgs)

	if len(m.Messages()) != 2 {
		t.Errorf("expected 2 messages, got %d", len(m.Messages()))
	}
}

func TestManager_ForceSaveAndLoad(t *testing.T) {
	m := newTestManager(t)
	sess := m.New("model", "anthropic", "agent", "sys", "/work")
	m.SetMessages([]provider.Message{
		{Role: "user", Content: "hello"},
	})

	if err := m.ForceSave(); err != nil {
		t.Fatalf("ForceSave error: %v", err)
	}

	// Verify file exists
	path := filepath.Join(m.dir, sess.ID+".json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session file not found: %v", err)
	}

	// Load it back
	m2 := &Manager{dir: m.dir, debounceMin: 2 * time.Second}
	loaded, err := m2.Load(sess.ID)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if loaded.ID != sess.ID {
		t.Errorf("loaded ID = %q, want %q", loaded.ID, sess.ID)
	}
	if loaded.Model != "model" {
		t.Errorf("loaded model = %q", loaded.Model)
	}
	if len(loaded.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(loaded.Messages))
	}
	if loaded.Messages[0].Content != "hello" {
		t.Errorf("loaded message content = %q", loaded.Messages[0].Content)
	}
}

func TestManager_Save_Debounce(t *testing.T) {
	m := newTestManager(t)
	m.debounceMin = 1 * time.Second // short for testing
	m.New("model", "prov", "", "", "")
	m.SetMessages([]provider.Message{{Role: "user", Content: "msg"}})

	// First save should work
	if err := m.Save(); err != nil {
		t.Fatalf("first Save error: %v", err)
	}

	// Immediate second save should be debounced (skipped)
	m.mu.Lock()
	m.dirty = true
	m.mu.Unlock()

	err := m.Save()
	if err != nil {
		t.Fatalf("second Save error: %v", err)
	}

	// Verify the file still exists (first save worked)
	sess := m.Session()
	path := filepath.Join(m.dir, sess.ID+".json")
	if _, err := os.Stat(path); err != nil {
		t.Error("session file should exist after first save")
	}
}

func TestManager_Save_NilSession(t *testing.T) {
	m := newTestManager(t)
	// No session created — should not error
	if err := m.Save(); err != nil {
		t.Errorf("Save with nil session should not error: %v", err)
	}
}

func TestManager_ForceSave_NilSession(t *testing.T) {
	m := newTestManager(t)
	if err := m.ForceSave(); err != nil {
		t.Errorf("ForceSave with nil session should not error: %v", err)
	}
}

func TestManager_Load_NotFound(t *testing.T) {
	m := newTestManager(t)
	_, err := m.Load("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManager_List(t *testing.T) {
	m := newTestManager(t)

	// Create 3 sessions
	s1 := m.New("m1", "p1", "", "", "")
	_ = m.ForceSave()
	time.Sleep(10 * time.Millisecond) // ensure different timestamps

	s2 := m.New("m2", "p2", "", "", "")
	_ = m.ForceSave()
	time.Sleep(10 * time.Millisecond)

	s3 := m.New("m3", "p3", "", "", "")
	_ = m.ForceSave()

	sessions, err := m.List()
	if err != nil {
		t.Fatalf("List error: %v", err)
	}

	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}

	// Should be sorted newest first
	if sessions[0].ID != s3.ID {
		t.Errorf("expected newest first, got %q", sessions[0].ID)
	}

	_ = s1
	_ = s2
}

func TestManager_List_EmptyDir(t *testing.T) {
	m := newTestManager(t)
	sessions, err := m.List()
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestManager_List_NonexistentDir(t *testing.T) {
	m := &Manager{dir: "/nonexistent/path/sessions", debounceMin: 2 * time.Second}
	sessions, err := m.List()
	if err != nil {
		t.Fatalf("List should not error for nonexistent dir: %v", err)
	}
	if sessions != nil {
		t.Error("expected nil sessions for nonexistent dir")
	}
}

func TestManager_Delete(t *testing.T) {
	m := newTestManager(t)
	sess := m.New("model", "prov", "", "", "")
	_ = m.ForceSave()

	if err := m.Delete(sess.ID); err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	// Verify file is gone
	path := filepath.Join(m.dir, sess.ID+".json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("session file should be deleted")
	}
}

func TestManager_Delete_NotFound(t *testing.T) {
	m := newTestManager(t)
	err := m.Delete("nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestManager_Messages_NilSession(t *testing.T) {
	m := newTestManager(t)
	msgs := m.Messages()
	if msgs != nil {
		t.Error("expected nil messages for nil session")
	}
}

func TestManager_Session_NilSession(t *testing.T) {
	m := newTestManager(t)
	sess := m.Session()
	if sess != nil {
		t.Error("expected nil session")
	}
}

func TestSession_CostFields(t *testing.T) {
	m := newTestManager(t)
	sess := m.New("model", "prov", "", "", "")

	// Set cost fields
	sess.TotalInputTokens = 5000
	sess.TotalOutputTokens = 2000
	sess.TotalCost = 1.23

	_ = m.ForceSave()

	// Load and verify
	m2 := &Manager{dir: m.dir, debounceMin: 2 * time.Second}
	loaded, err := m2.Load(sess.ID)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if loaded.TotalInputTokens != 5000 {
		t.Errorf("input tokens = %d, want 5000", loaded.TotalInputTokens)
	}
	if loaded.TotalOutputTokens != 2000 {
		t.Errorf("output tokens = %d, want 2000", loaded.TotalOutputTokens)
	}
	if loaded.TotalCost != 1.23 {
		t.Errorf("cost = %f, want 1.23", loaded.TotalCost)
	}
}

func TestSession_JSONSerialization(t *testing.T) {
	sess := &Session{
		ID:                "20260303-120000-abcd",
		Name:              "test",
		Model:             "claude-sonnet-4-20250514",
		Provider:          "anthropic",
		TotalInputTokens:  1000,
		TotalOutputTokens: 500,
		TotalCost:         0.018,
		Messages: []provider.Message{
			{Role: "user", Content: "hello"},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var loaded Session
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if loaded.ID != sess.ID {
		t.Errorf("ID mismatch: %q", loaded.ID)
	}
	if loaded.TotalInputTokens != 1000 {
		t.Errorf("input tokens: %d", loaded.TotalInputTokens)
	}
	if loaded.TotalCost != 0.018 {
		t.Errorf("cost: %f", loaded.TotalCost)
	}
}
