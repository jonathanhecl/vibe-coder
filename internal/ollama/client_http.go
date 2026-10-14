package ollama

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Connection-establishment timeouts. The overall client Timeout stays at zero
// on purpose: a local model can legitimately stream for minutes, so bounding
// the whole request would cut long generations. Bounding dial, TLS handshake
// and response headers instead makes a dead or unreachable endpoint fail in
// seconds while leaving long streams intact.
const (
	defaultDialTimeout           = 10 * time.Second
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultResponseHeaderTimeout = 60 * time.Second
)

func NewHTTP(baseURL string) Client {
	return NewHTTPWithAPIKey(baseURL, "")
}

// NewHTTPWithAPIKey builds a native Ollama client. When apiKey is non-empty
// every request carries an Authorization: Bearer header, which a self-hosted
// or proxied deployment may require. Ollama itself ignores the header.
func NewHTTPWithAPIKey(baseURL, apiKey string) Client {
	return &HTTPClient{
		baseURL: NormalizeURL(baseURL),
		apiKey:  strings.TrimSpace(apiKey),
		http: &http.Client{
			Timeout:   defaultHTTPTimeout,
			Transport: defaultTransport(),
		},
		thinkDisabledModels: make(map[string]bool),
		toolsDisabledModels: make(map[string]bool),
	}
}

// defaultTransport bounds connection establishment without capping the whole
// request, so an unreachable endpoint fails fast while long generations keep
// streaming.
func defaultTransport() *http.Transport {
	return transportWithHeaderTimeout(defaultResponseHeaderTimeout)
}

// transportWithHeaderTimeout builds the transport with an explicit
// response-header timeout, so tests can exercise the bound quickly.
func transportWithHeaderTimeout(d time.Duration) *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   defaultDialTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: d,
	}
}

// applyAuth adds the configured provider API key to a request.
func (c *HTTPClient) applyAuth(req *http.Request) {
	if c.apiKey == "" || req == nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
}

func (c *HTTPClient) applyThinkSessionOverride(req *ChatRequest) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.thinkDisabledModels[model] {
		req.Think = ThinkOff()
	}
}

func (c *HTTPClient) markThinkUnsupported(model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.thinkDisabledModels == nil {
		c.thinkDisabledModels = make(map[string]bool)
	}
	c.thinkDisabledModels[model] = true
}

// markToolsUnsupported remembers that Ollama rejected tools for this model
// so later turns skip the doomed 400 round trip (mirrors think handling).
func (c *HTTPClient) markToolsUnsupported(model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.toolsDisabledModels == nil {
		c.toolsDisabledModels = make(map[string]bool)
	}
	c.toolsDisabledModels[model] = true
}

// ToolsUnsupported reports whether this model already rejected native tools
// in-process (a 400 tool error on a previous turn), so the agent knows the
// XML envelope protocol is required even when capability detection claimed
// support.
func (c *HTTPClient) ToolsUnsupported(model string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.toolsDisabledModels[strings.TrimSpace(model)]
}

// applyToolsSessionOverride strips tools for models already known to
// reject them in-process.
func (c *HTTPClient) applyToolsSessionOverride(req *ChatRequest) {
	model := strings.TrimSpace(req.Model)
	if model == "" || len(req.Tools) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.toolsDisabledModels[model] {
		req.Tools = nil
	}
}

func newStreamScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), maxStreamBuffer)
	return scanner
}

func readLimitedBody(r io.Reader, limit int64) string {
	// Error bodies are diagnostic only; return best effort text for messages.
	body, _ := io.ReadAll(io.LimitReader(r, limit))
	return strings.TrimSpace(string(body))
}

func mapChatError(statusCode int, body string) error {
	bodyLower := strings.ToLower(body)
	switch statusCode {
	case http.StatusNotFound:
		return fmt.Errorf("Model not found. Run: ollama pull X")
	case http.StatusBadRequest:
		if strings.Contains(bodyLower, "tool") {
			return fmt.Errorf("Model does not support function calling")
		}
		if strings.Contains(bodyLower, "context") {
			return fmt.Errorf("Context window exceeded")
		}
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return fmt.Errorf("http %d", statusCode)
	}
	return fmt.Errorf("http %d: %s", statusCode, trimmed)
}
