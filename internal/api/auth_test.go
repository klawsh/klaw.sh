package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testAuthStore(t *testing.T) *AuthStore {
	t.Helper()
	tmpDir := t.TempDir()
	s := &AuthStore{
		path: filepath.Join(tmpDir, "api-keys.json"),
	}
	return s
}

func TestAuthStore_CreateAndValidate(t *testing.T) {
	s := testAuthStore(t)

	key, err := s.Create("test-key")
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}

	if !strings.HasPrefix(key, "klk_") {
		t.Errorf("key should start with klk_, got %q", key)
	}
	if len(key) != 36 { // klk_ (4) + 32 hex chars
		t.Errorf("expected 36 chars, got %d", len(key))
	}

	if !s.Validate(key) {
		t.Error("validate should return true for created key")
	}

	if s.Validate("klk_invalid") {
		t.Error("validate should return false for invalid key")
	}
}

func TestAuthStore_Revoke(t *testing.T) {
	s := testAuthStore(t)

	key, _ := s.Create("to-revoke")

	if !s.Revoke(key) {
		t.Error("revoke should return true for existing key")
	}

	if s.Validate(key) {
		t.Error("revoked key should not validate")
	}

	if s.Revoke("klk_nonexistent") {
		t.Error("revoke should return false for unknown key")
	}
}

func TestAuthStore_List(t *testing.T) {
	s := testAuthStore(t)

	_, _ = s.Create("key-one")
	_, _ = s.Create("key-two")

	keys := s.List()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}

	// Keys should be masked
	for _, k := range keys {
		if !strings.HasSuffix(k.Key, "...") {
			t.Errorf("key should be masked, got %q", k.Key)
		}
	}
}

func TestAuthStore_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "api-keys.json")

	// Create and save
	s1 := &AuthStore{path: path}
	key, _ := s1.Create("persist-test")

	// Reload
	s2 := &AuthStore{path: path}
	if err := s2.load(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	if !s2.Validate(key) {
		t.Error("key should persist after reload")
	}
}

func TestAuthStore_Middleware(t *testing.T) {
	s := testAuthStore(t)
	key, _ := s.Create("middleware-test")

	handler := s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// No auth → 401
	req := httptest.NewRequest("GET", "/api/v1/run", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	// Invalid key → 401
	req = httptest.NewRequest("GET", "/api/v1/run", nil)
	req.Header.Set("Authorization", "Bearer klk_invalid")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	// Valid key → 200
	req = httptest.NewRequest("GET", "/api/v1/run", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Health check skips auth → 200
	req = httptest.NewRequest("GET", "/api/v1/health", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 for health, got %d", w.Code)
	}

	_ = os.RemoveAll(s.path)
}
