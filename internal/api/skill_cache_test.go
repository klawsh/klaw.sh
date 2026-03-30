package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSkillCache_FetchAndCache(t *testing.T) {
	var fetchCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# Test Skill\nDo things."))
	}))
	defer srv.Close()

	cache := NewSkillCache(5 * time.Minute)
	ctx := context.Background()

	// First fetch
	content, err := cache.Fetch(ctx, srv.URL+"/skill.md")
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if content != "# Test Skill\nDo things." {
		t.Errorf("unexpected content: %q", content)
	}
	if fetchCount.Load() != 1 {
		t.Errorf("expected 1 fetch, got %d", fetchCount.Load())
	}

	// Second fetch — should be cached
	content2, err := cache.Fetch(ctx, srv.URL+"/skill.md")
	if err != nil {
		t.Fatalf("cached fetch failed: %v", err)
	}
	if content2 != content {
		t.Error("cached content should match")
	}
	if fetchCount.Load() != 1 {
		t.Errorf("expected still 1 fetch (cached), got %d", fetchCount.Load())
	}
}

func TestSkillCache_TTLExpiry(t *testing.T) {
	var fetchCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		_, _ = w.Write([]byte("skill content"))
	}))
	defer srv.Close()

	cache := NewSkillCache(50 * time.Millisecond) // Very short TTL
	ctx := context.Background()

	_, _ = cache.Fetch(ctx, srv.URL)
	time.Sleep(100 * time.Millisecond)
	_, _ = cache.Fetch(ctx, srv.URL)

	if fetchCount.Load() != 2 {
		t.Errorf("expected 2 fetches after TTL expiry, got %d", fetchCount.Load())
	}
}

func TestSkillCache_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cache := NewSkillCache(5 * time.Minute)
	_, err := cache.Fetch(context.Background(), srv.URL)

	if err == nil {
		t.Error("expected error for 404")
	}
}

func TestSkillCache_Invalidate(t *testing.T) {
	var fetchCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		_, _ = w.Write([]byte("content"))
	}))
	defer srv.Close()

	cache := NewSkillCache(5 * time.Minute)
	ctx := context.Background()

	_, _ = cache.Fetch(ctx, srv.URL)
	cache.Invalidate(srv.URL)
	_, _ = cache.Fetch(ctx, srv.URL)

	if fetchCount.Load() != 2 {
		t.Errorf("expected 2 fetches after invalidate, got %d", fetchCount.Load())
	}
}
