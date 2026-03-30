package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type cacheEntry struct {
	content   string
	fetchedAt time.Time
}

// SkillCache fetches and caches SKILL.md content by URL with a TTL.
type SkillCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	ttl     time.Duration
	client  *http.Client
}

// NewSkillCache creates a cache with the given TTL.
func NewSkillCache(ttl time.Duration) *SkillCache {
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	return &SkillCache{
		entries: make(map[string]cacheEntry),
		ttl:     ttl,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Fetch returns the SKILL.md content for a URL, using cache if fresh.
func (c *SkillCache) Fetch(ctx context.Context, url string) (string, error) {
	// Check cache
	c.mu.RLock()
	if entry, ok := c.entries[url]; ok && time.Since(entry.fetchedAt) < c.ttl {
		c.mu.RUnlock()
		return entry.content, nil
	}
	c.mu.RUnlock()

	// Fetch from URL
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("invalid skill URL: %w", err)
	}
	req.Header.Set("User-Agent", "klaw-skill-loader/1.0")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch skill: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("skill URL returned HTTP %d", resp.StatusCode)
	}

	// Limit to 512KB
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", fmt.Errorf("failed to read skill content: %w", err)
	}

	content := string(body)

	// Store in cache
	c.mu.Lock()
	c.entries[url] = cacheEntry{content: content, fetchedAt: time.Now()}
	c.mu.Unlock()

	return content, nil
}

// Invalidate removes a URL from the cache.
func (c *SkillCache) Invalidate(url string) {
	c.mu.Lock()
	delete(c.entries, url)
	c.mu.Unlock()
}
