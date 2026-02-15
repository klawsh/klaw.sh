// Package skill provides marketplace functionality for discovering skills.
package skill

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// MarketplaceConfig holds marketplace configuration
type MarketplaceConfig struct {
	BaseURL string // Default: https://skills.sh
}

// Marketplace provides access to the skills.sh registry
type Marketplace struct {
	config MarketplaceConfig
	client *http.Client
}

// MarketplaceSkill represents a skill in the marketplace
type MarketplaceSkill struct {
	Name        string   `json:"name"`
	Org         string   `json:"org"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      string   `json:"author"`
	Downloads   int      `json:"downloads"`
	Stars       int      `json:"stars"`
	Tags        []string `json:"tags"`
	Categories  []string `json:"categories"`
	URL         string   `json:"url"`
	Verified    bool     `json:"verified"`
	Featured    bool     `json:"featured"`
}

// MarketplaceCategory represents a skill category
type MarketplaceCategory struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Count       int    `json:"count"`
	Icon        string `json:"icon"`
}

// SearchResult holds search results
type SearchResult struct {
	Skills     []MarketplaceSkill `json:"skills"`
	Total      int                `json:"total"`
	Page       int                `json:"page"`
	PerPage    int                `json:"per_page"`
	Categories []string           `json:"categories"`
}

// NewMarketplace creates a new marketplace client
func NewMarketplace(config MarketplaceConfig) *Marketplace {
	if config.BaseURL == "" {
		config.BaseURL = "https://skills.sh"
	}
	return &Marketplace{
		config: config,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// GetCategories returns all skill categories
func (m *Marketplace) GetCategories() ([]MarketplaceCategory, error) {
	// For now, return hardcoded categories until skills.sh API is available
	// This will be replaced with actual API call
	categories := []MarketplaceCategory{
		{Name: "Web Browsing", Slug: "browser", Description: "Browse websites, take screenshots, interact with pages", Icon: "🌐", Count: 5},
		{Name: "Web Search", Slug: "search", Description: "Search the web and retrieve information", Icon: "🔍", Count: 8},
		{Name: "Code Execution", Slug: "code", Description: "Execute code in various languages", Icon: "💻", Count: 12},
		{Name: "Git & GitHub", Slug: "git", Description: "Git operations and GitHub integration", Icon: "📦", Count: 7},
		{Name: "Docker & Containers", Slug: "docker", Description: "Container management and orchestration", Icon: "🐳", Count: 4},
		{Name: "APIs & HTTP", Slug: "api", Description: "Make HTTP requests and integrate with APIs", Icon: "🔌", Count: 15},
		{Name: "Database", Slug: "database", Description: "Query and manage databases", Icon: "🗄️", Count: 6},
		{Name: "Communication", Slug: "communication", Description: "Slack, email, and messaging", Icon: "💬", Count: 9},
		{Name: "Productivity", Slug: "productivity", Description: "Calendar, notes, and task management", Icon: "📅", Count: 11},
		{Name: "AI & ML", Slug: "ai", Description: "AI models, embeddings, and ML tools", Icon: "🤖", Count: 8},
		{Name: "Files & Storage", Slug: "storage", Description: "File management and cloud storage", Icon: "📁", Count: 6},
		{Name: "Scraping", Slug: "scraping", Description: "Web scraping and data extraction", Icon: "🕷️", Count: 4},
	}
	return categories, nil
}

// GetFeatured returns featured skills
func (m *Marketplace) GetFeatured() ([]MarketplaceSkill, error) {
	// Featured skills - will be replaced with API call
	skills := []MarketplaceSkill{
		{
			Name:        "agent-browser",
			Org:         "vercel-labs",
			Version:     "1.0.0",
			Description: "AI-powered browser automation with screenshots, clicking, and form filling",
			Author:      "Vercel",
			Downloads:   15420,
			Stars:       342,
			Tags:        []string{"browser", "automation", "screenshots"},
			Categories:  []string{"browser", "scraping"},
			URL:         "https://skills.sh/vercel-labs/agent-browser",
			Verified:    true,
			Featured:    true,
		},
		{
			Name:        "exa-search",
			Org:         "exa-labs",
			Version:     "2.1.0",
			Description: "Powerful semantic web search using Exa's neural search API",
			Author:      "Exa",
			Downloads:   12350,
			Stars:       287,
			Tags:        []string{"search", "semantic", "web"},
			Categories:  []string{"search"},
			URL:         "https://skills.sh/exa-labs/exa-search",
			Verified:    true,
			Featured:    true,
		},
		{
			Name:        "firecrawl",
			Org:         "mendable",
			Version:     "1.5.0",
			Description: "Crawl and scrape websites with AI-powered extraction",
			Author:      "Mendable",
			Downloads:   9870,
			Stars:       234,
			Tags:        []string{"scraping", "crawling", "extraction"},
			Categories:  []string{"scraping", "browser"},
			URL:         "https://skills.sh/mendable/firecrawl",
			Verified:    true,
			Featured:    true,
		},
		{
			Name:        "postgres",
			Org:         "mcp-servers",
			Version:     "1.0.0",
			Description: "Query PostgreSQL databases with schema inspection",
			Author:      "MCP Community",
			Downloads:   8450,
			Stars:       198,
			Tags:        []string{"database", "postgresql", "sql"},
			Categories:  []string{"database"},
			URL:         "https://skills.sh/mcp-servers/postgres",
			Verified:    true,
			Featured:    true,
		},
		{
			Name:        "slack",
			Org:         "mcp-servers",
			Version:     "1.2.0",
			Description: "Send and read Slack messages, manage channels",
			Author:      "MCP Community",
			Downloads:   7230,
			Stars:       176,
			Tags:        []string{"slack", "messaging", "communication"},
			Categories:  []string{"communication"},
			URL:         "https://skills.sh/mcp-servers/slack",
			Verified:    true,
			Featured:    true,
		},
		{
			Name:        "github",
			Org:         "mcp-servers",
			Version:     "1.3.0",
			Description: "GitHub integration - issues, PRs, repos, and more",
			Author:      "MCP Community",
			Downloads:   11200,
			Stars:       265,
			Tags:        []string{"github", "git", "issues", "prs"},
			Categories:  []string{"git"},
			URL:         "https://skills.sh/mcp-servers/github",
			Verified:    true,
			Featured:    true,
		},
	}
	return skills, nil
}

// Search searches for skills
func (m *Marketplace) Search(query string, category string) (*SearchResult, error) {
	// Get all skills (featured + more)
	featured, _ := m.GetFeatured()

	// Additional skills for search
	allSkills := append(featured, []MarketplaceSkill{
		{Name: "puppeteer", Org: "mcp-servers", Version: "1.0.0", Description: "Browser automation with Puppeteer", Tags: []string{"browser"}, Categories: []string{"browser"}, URL: "https://skills.sh/mcp-servers/puppeteer"},
		{Name: "playwright", Org: "mcp-servers", Version: "1.0.0", Description: "Browser automation with Playwright", Tags: []string{"browser"}, Categories: []string{"browser"}, URL: "https://skills.sh/mcp-servers/playwright"},
		{Name: "tavily", Org: "tavily", Version: "1.0.0", Description: "AI-powered web search", Tags: []string{"search"}, Categories: []string{"search"}, URL: "https://skills.sh/tavily/tavily"},
		{Name: "brave-search", Org: "mcp-servers", Version: "1.0.0", Description: "Brave Search API", Tags: []string{"search"}, Categories: []string{"search"}, URL: "https://skills.sh/mcp-servers/brave-search"},
		{Name: "sqlite", Org: "mcp-servers", Version: "1.0.0", Description: "SQLite database operations", Tags: []string{"database"}, Categories: []string{"database"}, URL: "https://skills.sh/mcp-servers/sqlite"},
		{Name: "mysql", Org: "mcp-servers", Version: "1.0.0", Description: "MySQL database operations", Tags: []string{"database"}, Categories: []string{"database"}, URL: "https://skills.sh/mcp-servers/mysql"},
		{Name: "redis", Org: "mcp-servers", Version: "1.0.0", Description: "Redis cache and data store", Tags: []string{"database", "cache"}, Categories: []string{"database"}, URL: "https://skills.sh/mcp-servers/redis"},
		{Name: "notion", Org: "mcp-servers", Version: "1.0.0", Description: "Notion workspace integration", Tags: []string{"productivity"}, Categories: []string{"productivity"}, URL: "https://skills.sh/mcp-servers/notion"},
		{Name: "linear", Org: "mcp-servers", Version: "1.0.0", Description: "Linear issue tracking", Tags: []string{"productivity"}, Categories: []string{"productivity"}, URL: "https://skills.sh/mcp-servers/linear"},
		{Name: "jira", Org: "atlassian", Version: "1.0.0", Description: "Jira issue management", Tags: []string{"productivity"}, Categories: []string{"productivity"}, URL: "https://skills.sh/atlassian/jira"},
		{Name: "openai", Org: "mcp-servers", Version: "1.0.0", Description: "OpenAI API integration", Tags: []string{"ai"}, Categories: []string{"ai"}, URL: "https://skills.sh/mcp-servers/openai"},
		{Name: "anthropic", Org: "mcp-servers", Version: "1.0.0", Description: "Anthropic Claude API", Tags: []string{"ai"}, Categories: []string{"ai"}, URL: "https://skills.sh/mcp-servers/anthropic"},
		{Name: "s3", Org: "aws", Version: "1.0.0", Description: "AWS S3 storage operations", Tags: []string{"storage", "aws"}, Categories: []string{"storage"}, URL: "https://skills.sh/aws/s3"},
		{Name: "gcs", Org: "google", Version: "1.0.0", Description: "Google Cloud Storage", Tags: []string{"storage", "gcp"}, Categories: []string{"storage"}, URL: "https://skills.sh/google/gcs"},
		{Name: "email", Org: "mcp-servers", Version: "1.0.0", Description: "Send and receive emails", Tags: []string{"email"}, Categories: []string{"communication"}, URL: "https://skills.sh/mcp-servers/email"},
		{Name: "discord", Org: "mcp-servers", Version: "1.0.0", Description: "Discord bot integration", Tags: []string{"discord"}, Categories: []string{"communication"}, URL: "https://skills.sh/mcp-servers/discord"},
		{Name: "twitter", Org: "mcp-servers", Version: "1.0.0", Description: "Twitter/X API integration", Tags: []string{"twitter", "social"}, Categories: []string{"communication"}, URL: "https://skills.sh/mcp-servers/twitter"},
	}...)

	// Filter by query
	var filtered []MarketplaceSkill
	queryLower := strings.ToLower(query)

	for _, skill := range allSkills {
		// Category filter
		if category != "" {
			found := false
			for _, cat := range skill.Categories {
				if cat == category {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		// Query filter
		if query != "" {
			match := false
			if strings.Contains(strings.ToLower(skill.Name), queryLower) {
				match = true
			}
			if strings.Contains(strings.ToLower(skill.Org), queryLower) {
				match = true
			}
			if strings.Contains(strings.ToLower(skill.Description), queryLower) {
				match = true
			}
			if strings.Contains(strings.ToLower(skill.Author), queryLower) {
				match = true
			}
			for _, tag := range skill.Tags {
				if strings.Contains(strings.ToLower(tag), queryLower) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}

		filtered = append(filtered, skill)
	}

	// Sort by downloads
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Downloads > filtered[j].Downloads
	})

	return &SearchResult{
		Skills:  filtered,
		Total:   len(filtered),
		Page:    1,
		PerPage: 20,
	}, nil
}

// GetSkill gets details for a specific skill
func (m *Marketplace) GetSkill(org, name string) (*MarketplaceSkill, error) {
	url := fmt.Sprintf("%s/api/skills/%s/%s", m.config.BaseURL, org, name)

	resp, err := m.client.Get(url)
	if err != nil {
		// Fallback to search
		result, err := m.Search(name, "")
		if err != nil {
			return nil, err
		}
		for _, skill := range result.Skills {
			if skill.Name == name && skill.Org == org {
				return &skill, nil
			}
		}
		return nil, fmt.Errorf("skill not found: %s/%s", org, name)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Fallback
		result, _ := m.Search(name, "")
		for _, skill := range result.Skills {
			if skill.Name == name {
				return &skill, nil
			}
		}
		return nil, fmt.Errorf("skill not found")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var skill MarketplaceSkill
	if err := json.Unmarshal(body, &skill); err != nil {
		return nil, err
	}

	return &skill, nil
}

// FormatSkillCard formats a skill for display
func FormatSkillCard(skill MarketplaceSkill) string {
	var sb strings.Builder

	// Name and verification
	verified := ""
	if skill.Verified {
		verified = " ✓"
	}
	sb.WriteString(fmt.Sprintf("📦 %s/%s%s\n", skill.Org, skill.Name, verified))

	// Description
	sb.WriteString(fmt.Sprintf("   %s\n", skill.Description))

	// Stats
	if skill.Downloads > 0 || skill.Stars > 0 {
		sb.WriteString(fmt.Sprintf("   ⬇️ %d downloads  ⭐ %d stars\n", skill.Downloads, skill.Stars))
	}

	// Tags
	if len(skill.Tags) > 0 {
		sb.WriteString(fmt.Sprintf("   🏷️  %s\n", strings.Join(skill.Tags, ", ")))
	}

	return sb.String()
}
