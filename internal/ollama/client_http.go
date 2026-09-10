package ollama

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func NewHTTP(baseURL string) Client {
	return &HTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
		thinkDisabledModels: make(map[string]bool),
		toolsDisabledModels: make(map[string]bool),
	}
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
