package tools

import (
	"context"
	"errors"
	"fmt"
	"html"
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

const maxFetchRedirects = 5

// errFetchNotRetryable aborts the request (redirect chain violations and
// private destinations must not fall through to another fetch attempt).
var errFetchNotRetryable = errors.New("fetch blocked by network policy")

func validateFetchURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("only http/https URLs are allowed")
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("url is missing a host")
	}
	if isPrivateHost(parsed.Hostname()) {
		return fmt.Errorf("private or localhost URLs are blocked")
	}
	return nil
}

// guardedDialContext pins fetches to public addresses at connection time, so
// a hostname re-resolving to a private IP (DNS rebinding) cannot connect even
// though the URL check passed earlier.
func guardedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	addrs, err := (&net.Resolver{}).LookupIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range addrs {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return nil, fmt.Errorf("%w: %s resolves to private address %s", errFetchNotRetryable, host, ip)
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%w: no addresses for %s", errFetchNotRetryable, host)
	}
	return (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, net.JoinHostPort(addrs[0].String(), port))
}

func newWebFetchClient() *http.Client {
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         guardedDialContext,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxFetchRedirects {
				return fmt.Errorf("%w: too many redirects (limit %d)", errFetchNotRetryable, maxFetchRedirects)
			}
			if err := validateFetchURL(req.URL.String()); err != nil {
				return fmt.Errorf("%w: redirect to %s: %v", errFetchNotRetryable, req.URL.Redacted(), err)
			}
			return nil
		},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, params map[string]any) Result {
	rawURL, ok := params["url"].(string)
	if !ok || strings.TrimSpace(rawURL) == "" {
		return errResult("url is required")
	}
	rawURL = strings.TrimSpace(rawURL)
	if err := validateFetchURL(rawURL); err != nil {
		return errResult(err.Error())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return errResult(fmt.Sprintf("build request: %v", err))
	}
	// A browser-like UA and Accept set: many sites (e.g. Wikipedia) answer the
	// Go default client with HTTP 403, which would make pages unfetchable.
	req.Header.Set("User-Agent", ddgUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := newWebFetchClient().Do(req)
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

func htmlToText(htmlDoc string) string {
	// Strip script/style/noscript blocks (with their contents) and HTML
	// comments before removing the remaining tags. Script content must go:
	// leaving it in floods the model context with JavaScript instead of prose.
	reScript := regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	reStyle := regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	reNoscript := regexp.MustCompile(`(?is)<noscript\b[^>]*>.*?</noscript\s*>`)
	reComment := regexp.MustCompile(`(?s)<!--.*?-->`)
	reTag := regexp.MustCompile(`(?s)<[^>]+>`)
	text := reScript.ReplaceAllString(htmlDoc, " ")
	text = reStyle.ReplaceAllString(text, " ")
	text = reNoscript.ReplaceAllString(text, " ")
	text = reComment.ReplaceAllString(text, " ")
	text = reTag.ReplaceAllString(text, " ")
	text = html.UnescapeString(text)
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
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast()
}
