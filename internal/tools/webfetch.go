package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type WebFetchTool struct{}

func NewWebFetchTool() *WebFetchTool { return &WebFetchTool{} }

func (t *WebFetchTool) Name() string        { return "WebFetch" }
func (t *WebFetchTool) Description() string { return "Fetch and extract webpage text." }
func (t *WebFetchTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{"type": "string"},
				},
				"required": []string{"url"},
			},
		},
	}
}

func (t *WebFetchTool) Execute(_ context.Context, params map[string]any) Result {
	rawURL, ok := params["url"].(string)
	if !ok || strings.TrimSpace(rawURL) == "" {
		return errResult("url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errResult(fmt.Sprintf("invalid url: %v", err))
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errResult("only http/https URLs are allowed")
	}
	if isPrivateHost(parsed.Hostname()) {
		return errResult("private or localhost URLs are blocked")
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return errResult(fmt.Sprintf("fetch url: %v", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errResult(fmt.Sprintf("non-200 status: %d", resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return errResult(fmt.Sprintf("read response body: %v", err))
	}
	text := htmlToText(string(body))
	if len(text) > 50*1024 {
		text = text[:50*1024]
	}
	return Result{Output: strings.TrimSpace(text)}
}

func htmlToText(html string) string {
	reScript := regexp.MustCompile(`(?is)<script.*?>.*?</script>`)
	reStyle := regexp.MustCompile(`(?is)<style.*?>.*?</style>`)
	reTag := regexp.MustCompile(`(?s)<[^>]+>`)
	text := reScript.ReplaceAllString(html, " ")
	text = reStyle.ReplaceAllString(text, " ")
	text = reTag.ReplaceAllString(text, " ")
	text = strings.ReplaceAll(text, "\t", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	reSpaces := regexp.MustCompile(`\s+`)
	return reSpaces.ReplaceAllString(text, " ")
}

func isPrivateHost(host string) bool {
	if host == "" {
		return true
	}
	lower := strings.ToLower(host)
	if lower == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		addrs, err := net.LookupIP(host)
		if err != nil || len(addrs) == 0 {
			return false
		}
		for _, addr := range addrs {
			if addr.IsLoopback() || addr.IsPrivate() || addr.IsUnspecified() || addr.IsLinkLocalUnicast() {
				return true
			}
		}
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast()
}
