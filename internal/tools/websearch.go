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

func (t *WebSearchTool) Execute(_ context.Context, params map[string]any) Result {
	query, ok := params["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return errResult("query is required")
	}
	searchURL := "https://duckduckgo.com/html/?q=" + url.QueryEscape(query)
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(searchURL)
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
