package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxHTTPResponseBytes bounds how much of an HTTP response body is returned to
// the model. Large binary/document responses are truncated with a clear note.
const maxHTTPResponseBytes = 128 * 1024

// HTTPRequestTool performs a generic HTTP request. It is deliberately
// domain-agnostic: it is the reliable alternative to shelling out to curl for
// any JSON/REST API (including local ones such as ComfyUI), with no coupling to
// a specific service. It is a Network-tier tool, so it asks for permission
// unless yes-mode or an unattended mission is active.
type HTTPRequestTool struct{}

func NewHTTPRequestTool() *HTTPRequestTool { return &HTTPRequestTool{} }

func (t *HTTPRequestTool) Name() string { return "HTTPRequest" }
func (t *HTTPRequestTool) Description() string {
	return "Perform a generic HTTP request (method, url, headers, body) and return the status, " +
		"content type and response body. Use it for REST/JSON APIs, including local services, " +
		"instead of shelling out to curl. It is domain-agnostic: no service-specific knowledge is assumed."
}
func (t *HTTPRequestTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":        map[string]any{"type": "string", "description": "Absolute http(s) URL."},
					"method":     map[string]any{"type": "string", "description": "HTTP method (default GET)."},
					"headers":    map[string]any{"type": "object", "description": "Optional request headers as a string map."},
					"body":       map[string]any{"type": "string", "description": "Optional request body (e.g. a JSON string)."},
					"timeout_ms": map[string]any{"type": "integer", "description": "Timeout in milliseconds (default 30000, max 600000)."},
				},
				"required": []string{"url"},
			},
		},
	}
}

func (t *HTTPRequestTool) Execute(ctx context.Context, params map[string]any) Result {
	rawURL := strings.TrimSpace(asStringParam(params, "url"))
	if rawURL == "" {
		return errResult("url is required")
	}
	lower := strings.ToLower(rawURL)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return errResult("only http(s) URLs are allowed")
	}
	method := strings.ToUpper(strings.TrimSpace(asStringParam(params, "method")))
	if method == "" {
		method = http.MethodGet
	}

	timeoutMS := 30000
	switch v := params["timeout_ms"].(type) {
	case float64:
		timeoutMS = int(v)
	case int:
		timeoutMS = v
	case string:
		if parsed, err := strconv.Atoi(v); err == nil {
			timeoutMS = parsed
		}
	}
	if timeoutMS <= 0 {
		timeoutMS = 30000
	}
	if timeoutMS > 600000 {
		timeoutMS = 600000
	}
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()

	body := asStringParam(params, "body")
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, rawURL, reader)
	if err != nil {
		return errResult("build request: " + err.Error())
	}
	for key, value := range headerParams(params) {
		req.Header.Set(key, value)
	}

	client := &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return errResult("request failed: " + err.Error())
	}
	defer resp.Body.Close()

	data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxHTTPResponseBytes+1))
	truncated := len(data) > maxHTTPResponseBytes
	if truncated {
		data = data[:maxHTTPResponseBytes]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "HTTP %d %s\n", resp.StatusCode, http.StatusText(resp.StatusCode))
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		fmt.Fprintf(&b, "content-type: %s\n", ct)
	}
	b.WriteString("\n")
	b.Write(data)
	if truncated {
		fmt.Fprintf(&b, "\n\n... (response truncated at %d bytes)", maxHTTPResponseBytes)
	}
	if readErr != nil {
		fmt.Fprintf(&b, "\n(read error: %v)", readErr)
	}
	result := Result{Output: strings.TrimRight(b.String(), "\n")}
	if resp.StatusCode >= 400 {
		result.IsError = true
	}
	return result
}

// headerParams converts the JSON headers object into a string map, ignoring
// non-string values so a malformed header never panics the tool.
func headerParams(params map[string]any) map[string]string {
	raw, ok := params["headers"].(map[string]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		if s, ok := value.(string); ok {
			out[key] = s
		}
	}
	return out
}
