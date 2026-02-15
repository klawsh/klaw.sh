package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// WebFetch fetches content from URLs
type WebFetch struct {
	client *http.Client
}

// NewWebFetch creates a new web fetch tool
func NewWebFetch() *WebFetch {
	return &WebFetch{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (t *WebFetch) Name() string {
	return "web_fetch"
}

func (t *WebFetch) Description() string {
	return `Fetch content from a URL. Use this to:
- Read web pages and articles
- Check website content
- Get data from APIs (GET requests)

Returns the text content of the page (HTML tags stripped for readability).
For complex web pages that require JavaScript, this may not work - use browser tools instead.`
}

func (t *WebFetch) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"url": {
				"type": "string",
				"description": "The URL to fetch"
			},
			"raw": {
				"type": "boolean",
				"description": "Return raw HTML instead of stripped text (default: false)"
			}
		},
		"required": ["url"]
	}`)
}

type webFetchParams struct {
	URL string `json:"url"`
	Raw bool   `json:"raw"`
}

func (t *WebFetch) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p webFetchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return &Result{Content: fmt.Sprintf("Invalid parameters: %v", err), IsError: true}, nil
	}

	if p.URL == "" {
		return &Result{Content: "URL is required", IsError: true}, nil
	}

	// Ensure URL has scheme
	if !strings.HasPrefix(p.URL, "http://") && !strings.HasPrefix(p.URL, "https://") {
		p.URL = "https://" + p.URL
	}

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, "GET", p.URL, nil)
	if err != nil {
		return &Result{Content: fmt.Sprintf("Failed to create request: %v", err), IsError: true}, nil
	}

	// Set user agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Klaw/1.0; +https://github.com/eachlabs/klaw)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	// Execute request
	resp, err := t.client.Do(req)
	if err != nil {
		return &Result{Content: fmt.Sprintf("Failed to fetch URL: %v", err), IsError: true}, nil
	}
	defer resp.Body.Close()

	// Check status
	if resp.StatusCode != http.StatusOK {
		return &Result{Content: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, resp.Status), IsError: true}, nil
	}

	// Read body (limit to 1MB)
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return &Result{Content: fmt.Sprintf("Failed to read response: %v", err), IsError: true}, nil
	}

	content := string(body)

	// Strip HTML if not raw mode
	if !p.Raw {
		content = stripHTML(content)
		content = cleanText(content)

		// Truncate if too long
		if len(content) > 50000 {
			content = content[:50000] + "\n\n[Content truncated - too long]"
		}
	}

	return &Result{Content: fmt.Sprintf("Fetched %s (%d bytes):\n\n%s", p.URL, len(body), content)}, nil
}

// stripHTML removes HTML tags and extracts text content
func stripHTML(html string) string {
	// Remove script and style elements
	reScript := regexp.MustCompile(`(?is)<script.*?</script>`)
	html = reScript.ReplaceAllString(html, "")

	reStyle := regexp.MustCompile(`(?is)<style.*?</style>`)
	html = reStyle.ReplaceAllString(html, "")

	// Remove HTML comments
	reComment := regexp.MustCompile(`(?s)<!--.*?-->`)
	html = reComment.ReplaceAllString(html, "")

	// Replace common block elements with newlines
	reBlock := regexp.MustCompile(`(?i)<(br|p|div|h[1-6]|li|tr)[^>]*>`)
	html = reBlock.ReplaceAllString(html, "\n")

	// Remove all remaining HTML tags
	reTag := regexp.MustCompile(`<[^>]+>`)
	html = reTag.ReplaceAllString(html, "")

	// Decode common HTML entities
	html = strings.ReplaceAll(html, "&nbsp;", " ")
	html = strings.ReplaceAll(html, "&amp;", "&")
	html = strings.ReplaceAll(html, "&lt;", "<")
	html = strings.ReplaceAll(html, "&gt;", ">")
	html = strings.ReplaceAll(html, "&quot;", "\"")
	html = strings.ReplaceAll(html, "&#39;", "'")

	return html
}

// cleanText cleans up whitespace
func cleanText(text string) string {
	// Replace multiple whitespace with single space
	reSpace := regexp.MustCompile(`[ \t]+`)
	text = reSpace.ReplaceAllString(text, " ")

	// Replace multiple newlines with double newline
	reNewline := regexp.MustCompile(`\n\s*\n\s*\n+`)
	text = reNewline.ReplaceAllString(text, "\n\n")

	// Trim lines
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}

	return strings.Join(cleaned, "\n")
}

// WebSearch performs web searches
type WebSearch struct {
	client *http.Client
}

// NewWebSearch creates a new web search tool
func NewWebSearch() *WebSearch {
	return &WebSearch{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (t *WebSearch) Name() string {
	return "web_search"
}

func (t *WebSearch) Description() string {
	return `Search the web using DuckDuckGo. Use this to:
- Find information on any topic
- Get current news and events
- Research questions you don't know the answer to

Returns search results with titles, URLs, and snippets.`
}

func (t *WebSearch) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "The search query"
			},
			"max_results": {
				"type": "integer",
				"description": "Maximum number of results to return (default: 5)"
			}
		},
		"required": ["query"]
	}`)
}

type webSearchParams struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

func (t *WebSearch) Execute(ctx context.Context, params json.RawMessage) (*Result, error) {
	var p webSearchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return &Result{Content: fmt.Sprintf("Invalid parameters: %v", err), IsError: true}, nil
	}

	if p.Query == "" {
		return &Result{Content: "Query is required", IsError: true}, nil
	}

	if p.MaxResults <= 0 {
		p.MaxResults = 5
	}

	// Use DuckDuckGo HTML search (no API key needed)
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", strings.ReplaceAll(p.Query, " ", "+"))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return &Result{Content: fmt.Sprintf("Failed to create request: %v", err), IsError: true}, nil
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Klaw/1.0)")

	resp, err := t.client.Do(req)
	if err != nil {
		return &Result{Content: fmt.Sprintf("Search failed: %v", err), IsError: true}, nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &Result{Content: fmt.Sprintf("Failed to read response: %v", err), IsError: true}, nil
	}

	// Parse results from HTML
	results := parseSearchResults(string(body), p.MaxResults)

	if len(results) == 0 {
		return &Result{Content: fmt.Sprintf("No results found for: %s", p.Query)}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for: %s\n\n", p.Query))

	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.Title))
		sb.WriteString(fmt.Sprintf("   URL: %s\n", r.URL))
		sb.WriteString(fmt.Sprintf("   %s\n\n", r.Snippet))
	}

	return &Result{Content: sb.String()}, nil
}

type searchResult struct {
	Title   string
	URL     string
	Snippet string
}

func parseSearchResults(html string, maxResults int) []searchResult {
	var results []searchResult

	// Find result links (DuckDuckGo HTML format)
	// Pattern: <a class="result__a" href="...">title</a>
	reLink := regexp.MustCompile(`<a[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>([^<]*)</a>`)
	reSnippet := regexp.MustCompile(`<a[^>]*class="result__snippet"[^>]*>([^<]*(?:<[^>]*>[^<]*)*)</a>`)

	linkMatches := reLink.FindAllStringSubmatch(html, maxResults)
	snippetMatches := reSnippet.FindAllStringSubmatch(html, maxResults)

	for i, match := range linkMatches {
		if i >= maxResults {
			break
		}

		result := searchResult{
			URL:   match[1],
			Title: strings.TrimSpace(match[2]),
		}

		if i < len(snippetMatches) {
			result.Snippet = stripHTML(snippetMatches[i][1])
			result.Snippet = strings.TrimSpace(result.Snippet)
		}

		if result.Title != "" && result.URL != "" {
			results = append(results, result)
		}
	}

	return results
}
