package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type WebSearchTool struct{}

func NewWebSearchTool() *WebSearchTool { return &WebSearchTool{} }

func (t *WebSearchTool) Name() string { return "WebSearch" }
func (t *WebSearchTool) Description() string {
	return "Search the web using DuckDuckGo HTML with a Bing HTML fallback."
}
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
	query = strings.TrimSpace(query)

	// DuckDuckGo is the preferred, privacy-friendly backend, but it sometimes
	// answers automated clients with a challenge page (HTTP 202, no result
	// markup). Fall back to Bing HTML so search keeps working when that happens.
	results, ddgErr := searchDuckDuckGo(ctx, query)
	if len(results) == 0 {
		if bingResults, bingErr := searchBing(ctx, query); len(bingResults) > 0 {
			results = bingResults
		} else if ddgErr != nil && bingErr != nil {
			return errResult(fmt.Sprintf("search failed: duckduckgo: %v; bing: %v", ddgErr, bingErr))
		}
	}
	if len(results) == 0 {
		return Result{Output: "No results found."}
	}
	if len(results) > 10 {
		results = results[:10]
	}
	return Result{Output: strings.Join(results, "\n")}
}

func newSearchRequest(ctx context.Context, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ddgUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	return req, nil
}

func fetchSearchHTML(ctx context.Context, rawURL string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := newSearchRequest(ctx, rawURL)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func searchDuckDuckGo(ctx context.Context, query string) ([]string, error) {
	body, err := fetchSearchHTML(ctx, "https://duckduckgo.com/html/?q="+url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	return parseDDGResults(body), nil
}

func searchBing(ctx context.Context, query string) ([]string, error) {
	body, err := fetchSearchHTML(ctx, "https://www.bing.com/search?q="+url.QueryEscape(query))
	if err != nil {
		return nil, err
	}
	return parseBingResults(body), nil
}

func parseDDGResults(htmlDoc string) []string {
	re := regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	matches := re.FindAllStringSubmatch(htmlDoc, -1)
	results := make([]string, 0, len(matches))
	reTags := regexp.MustCompile(`(?s)<[^>]+>`)
	for _, m := range matches {
		title := html.UnescapeString(strings.TrimSpace(reTags.ReplaceAllString(m[2], " ")))
		title = strings.Join(strings.Fields(title), " ")
		link := normalizeDDGLink(m[1])
		if title == "" || link == "" {
			continue
		}
		results = append(results, fmt.Sprintf("- %s | %s", title, link))
	}
	return results
}

// normalizeDDGLink turns a DuckDuckGo result href into a real absolute URL.
// DDG returns protocol-relative redirect links such as
// "//duckduckgo.com/l/?uddg=<encoded target>"; leaving them as-is makes the
// result unusable because WebFetch only accepts absolute http/https URLs.
func normalizeDDGLink(raw string) string {
	link := html.UnescapeString(strings.TrimSpace(raw))
	if link == "" {
		return ""
	}
	if strings.HasPrefix(link, "//") {
		link = "https:" + link
	}
	parsed, err := url.Parse(link)
	if err != nil {
		return link
	}
	if !strings.HasSuffix(strings.ToLower(parsed.Host), "duckduckgo.com") {
		return link
	}
	// url.Query() already percent-decodes once, which is exactly the encoding
	// DuckDuckGo uses for uddg; do not decode a second time.
	target := strings.TrimSpace(parsed.Query().Get("uddg"))
	if target == "" {
		return link
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	return link
}

// parseBingResults extracts the title/anchor of each organic result. Bing
// returns absolute redirect links (bing.com/ck/a?...&u=a1<base64url>); the
// real destination is decoded so the model can WebFetch it directly.
func parseBingResults(htmlDoc string) []string {
	chunks := strings.Split(htmlDoc, `class="b_algo"`)
	if len(chunks) < 2 {
		return nil
	}
	reAnchor := regexp.MustCompile(`(?is)<h2[^>]*>\s*<a[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	reTags := regexp.MustCompile(`(?s)<[^>]+>`)
	results := make([]string, 0, 10)
	for _, chunk := range chunks[1:] {
		m := reAnchor.FindStringSubmatch(chunk)
		if m == nil {
			continue
		}
		link := decodeBingLink(html.UnescapeString(strings.TrimSpace(m[1])))
		title := html.UnescapeString(strings.Join(strings.Fields(reTags.ReplaceAllString(m[2], " ")), " "))
		if title == "" || link == "" {
			continue
		}
		results = append(results, fmt.Sprintf("- %s | %s", title, link))
		if len(results) >= 10 {
			break
		}
	}
	return results
}

// decodeBingLink unwraps a Bing redirect (u=a1<base64url>) into the real URL.
// Anything that is not a recognizable Bing redirect is returned unchanged.
func decodeBingLink(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || !strings.HasSuffix(strings.ToLower(parsed.Host), "bing.com") {
		return raw
	}
	enc := parsed.Query().Get("u")
	if !strings.HasPrefix(enc, "a1") {
		return raw
	}
	encoded := strings.TrimRight(enc[2:], "=")
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 {
		return raw
	}
	if target := strings.TrimSpace(string(decoded)); strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return target
	}
	return raw
}
