package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type WebSearchTool struct{}

func NewWebSearchTool() *WebSearchTool { return &WebSearchTool{} }

func (t *WebSearchTool) Name() string        { return "WebSearch" }
func (t *WebSearchTool) Description() string { return "Search the web using DuckDuckGo HTML." }
func (t *WebSearchTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
				},
				"required": []string{"query"},
			},
		},
	}
}

// ddgUserAgent is a generic browser User-Agent. DuckDuckGo serves a bot challenge
// (no result markup) for the default Go client User-Agent, which makes parsing return zero hits.
const ddgUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func (t *WebSearchTool) Execute(ctx context.Context, params map[string]any) Result {
	query, ok := params["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return errResult("query is required")
	}
	searchURL := "https://duckduckgo.com/html/?q=" + url.QueryEscape(query)
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return errResult(fmt.Sprintf("search request build failed: %v", err))
	}
	req.Header.Set("User-Agent", ddgUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return errResult(fmt.Sprintf("search request failed: %v", err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return errResult(fmt.Sprintf("read search response: %v", err))
	}
	results := parseDDGResults(string(body))
	if len(results) == 0 {
		return Result{Output: "No results found."}
	}
	if len(results) > 10 {
		results = results[:10]
	}
	return Result{Output: strings.Join(results, "\n")}
}

func parseDDGResults(html string) []string {
	re := regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	matches := re.FindAllStringSubmatch(html, -1)
	results := make([]string, 0, len(matches))
	reTags := regexp.MustCompile(`(?s)<[^>]+>`)
	for _, m := range matches {
		title := strings.TrimSpace(reTags.ReplaceAllString(m[2], " "))
		title = strings.Join(strings.Fields(title), " ")
		link := strings.TrimSpace(m[1])
		if title == "" || link == "" {
			continue
		}
		results = append(results, fmt.Sprintf("- %s | %s", title, link))
	}
	return results
}
