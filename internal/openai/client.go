package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/logger"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

const (
	defaultHTTPTimeout = 0 // Context deadlines govern streaming timeouts
	maxStreamBuffer    = 1024 * 1024
	streamStallTimeout = 5 * time.Minute
)

// Option configures the Client.
type Option func(*Client)

// WithAPIKey sets the optional Authorization Bearer token.
func WithAPIKey(key string) Option {
	return func(c *Client) {
		c.apiKey = strings.TrimSpace(key)
	}
}

// WithHTTPClient overrides the default HTTP transport.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		if httpClient != nil {
			c.http = httpClient
		}
	}
}

// Client implements ollama.Client for OpenAI-compatible (/v1) servers.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client

	mu                  sync.Mutex
	toolsDisabledModels map[string]bool
}

// New creates an OpenAI-compatible client.
// If baseURL does not end with /v1, /v1 is appended.
func New(baseURL string, opts ...Option) *Client {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}

	c := &Client{
		baseURL: base,
		http: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
		toolsDisabledModels: make(map[string]bool),
	}

	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) markToolsUnsupported(model string) {
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

func (c *Client) isToolsUnsupported(model string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.toolsDisabledModels[strings.TrimSpace(model)]
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return req, nil
}

func isToolError(body string) bool {
	low := strings.ToLower(body)
	return strings.Contains(low, "tool") || strings.Contains(low, "function")
}

// Chat initiates a streaming chat completion turn.
func (c *Client) Chat(ctx context.Context, req ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	if req.Model == "" {
		return nil, errors.New("chat model is required")
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("messages list is empty")
	}

	attempt := req
	attempt.Stream = true
	if c.isToolsUnsupported(attempt.Model) {
		attempt.Tools = nil
	}

	for {
		wireReq, err := buildChatRequest(attempt)
		if err != nil {
			return nil, fmt.Errorf("build chat request: %w", err)
		}

		payload, err := json.Marshal(wireReq)
		if err != nil {
			return nil, fmt.Errorf("marshal chat request: %w", err)
		}

		httpReq, err := c.newRequest(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}

		resp, err := c.http.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("openai chat: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			return c.streamChatResponse(ctx, resp.Body), nil
		}

		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		_ = resp.Body.Close()
		bodyStr := string(bodyBytes)

		if resp.StatusCode == http.StatusBadRequest && len(attempt.Tools) > 0 && isToolError(bodyStr) {
			logger.Infof("OpenAI model %q does not support tools; retrying without tools", attempt.Model)
			c.markToolsUnsupported(attempt.Model)
			attempt.Tools = nil
			continue
		}

		return nil, fmt.Errorf("openai chat failed (%s): %s", resp.Status, strings.TrimSpace(bodyStr))
	}
}

func (c *Client) streamChatResponse(ctx context.Context, body io.ReadCloser) <-chan ollama.Chunk {
	ch := make(chan ollama.Chunk, 16)

	go func() {
		defer close(ch)
		defer body.Close()

		stopWatch := context.AfterFunc(ctx, func() { _ = body.Close() })
		defer stopWatch()

		scanner := bufio.NewScanner(body)
		scanner.Buffer(make([]byte, 0, 4096), maxStreamBuffer)

		accumulator := newStreamAccumulator()
		doneEmitted := false

		stall := time.NewTimer(streamStallTimeout)
		defer stall.Stop()

		for scanner.Scan() {
			if !stall.Stop() {
				select {
				case <-stall.C:
				default:
				}
			}
			stall.Reset(streamStallTimeout)

			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			if !strings.HasPrefix(line, "data:") {
				continue
			}

			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				if !doneEmitted {
					toolCalls := accumulator.buildToolCalls()
					ch <- ollama.Chunk{
						ToolCalls: toolCalls,
						Done:      true,
					}
					doneEmitted = true
				}
				return
			}

			var chunk chatCompletionChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			if chunk.Error != nil && chunk.Error.Message != "" {
				ch <- ollama.Chunk{
					Err:  errors.New(chunk.Error.Message),
					Done: true,
				}
				return
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]
			delta := choice.Delta
			accumulator.accumulate(delta)

			thinking := delta.ReasoningContent
			if thinking == "" {
				thinking = delta.Reasoning
			}

			isDone := choice.FinishReason != "" && choice.FinishReason != "null"
			var toolCalls []ollama.MessageToolCall
			if isDone {
				toolCalls = accumulator.buildToolCalls()
			}

			outChunk := ollama.Chunk{
				Delta:     delta.Content,
				Thinking:  thinking,
				ToolCalls: toolCalls,
				Done:      isDone,
			}

			select {
			case ch <- outChunk:
			case <-ctx.Done():
				return
			}

			if isDone {
				doneEmitted = true
			}
		}

		if !doneEmitted {
			toolCalls := accumulator.buildToolCalls()
			ch <- ollama.Chunk{
				ToolCalls: toolCalls,
				Done:      true,
			}
		}
	}()

	return ch
}

// ChatSync performs a non-streaming chat completion request.
func (c *Client) ChatSync(ctx context.Context, req ollama.ChatRequest) (ollama.ChatResponse, error) {
	if req.Model == "" {
		return ollama.ChatResponse{}, errors.New("chat model is required")
	}

	attempt := req
	attempt.Stream = false
	if c.isToolsUnsupported(attempt.Model) {
		attempt.Tools = nil
	}

	for {
		wireReq, err := buildChatRequest(attempt)
		if err != nil {
			return ollama.ChatResponse{}, fmt.Errorf("build chat sync request: %w", err)
		}

		payload, err := json.Marshal(wireReq)
		if err != nil {
			return ollama.ChatResponse{}, fmt.Errorf("marshal chat sync request: %w", err)
		}

		httpReq, err := c.newRequest(ctx, http.MethodPost, "/chat/completions", bytes.NewReader(payload))
		if err != nil {
			return ollama.ChatResponse{}, err
		}

		resp, err := c.http.Do(httpReq)
		if err != nil {
			return ollama.ChatResponse{}, fmt.Errorf("openai chat sync: %w", err)
		}

		bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024))
		_ = resp.Body.Close()
		if err != nil {
			return ollama.ChatResponse{}, fmt.Errorf("read response body: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			var parsed chatCompletionResponse
			if err := json.Unmarshal(bodyBytes, &parsed); err != nil {
				return ollama.ChatResponse{}, fmt.Errorf("decode chat sync response: %w", err)
			}
			if len(parsed.Choices) == 0 {
				return ollama.ChatResponse{}, nil
			}
			return parseResponseChoice(parsed.Choices[0]), nil
		}

		bodyStr := string(bodyBytes)
		if resp.StatusCode == http.StatusBadRequest && len(attempt.Tools) > 0 && isToolError(bodyStr) {
			logger.Infof("OpenAI model %q does not support tools; retrying without tools", attempt.Model)
			c.markToolsUnsupported(attempt.Model)
			attempt.Tools = nil
			continue
		}

		return ollama.ChatResponse{}, fmt.Errorf("openai chat sync failed (%s): %s", resp.Status, strings.TrimSpace(bodyStr))
	}
}

// Tags lists available models by querying GET /v1/models.
func (c *Client) Tags(ctx context.Context) ([]ollama.Model, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai list models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openai list models failed (%s): %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var parsed modelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}

	out := make([]ollama.Model, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		name := strings.TrimSpace(m.ID)
		if name == "" {
			continue
		}
		caps := []string{"tools", "thinking"}
		nameLower := strings.ToLower(name)
		if strings.Contains(nameLower, "vision") || strings.Contains(nameLower, "vl") || strings.Contains(nameLower, "omni") || strings.Contains(nameLower, "4o") {
			caps = append(caps, "vision")
		}
		out = append(out, ollama.Model{
			Name:              name,
			Capabilities:      caps,
			CapabilitiesKnown: true,
		})
	}
	return out, nil
}

// Show reports capability metadata for the requested model.
func (c *Client) Show(_ context.Context, model string) (ollama.Model, error) {
	name := strings.TrimSpace(model)
	caps := []string{"tools", "thinking"}
	nameLower := strings.ToLower(name)
	if strings.Contains(nameLower, "vision") || strings.Contains(nameLower, "vl") || strings.Contains(nameLower, "omni") || strings.Contains(nameLower, "4o") {
		caps = append(caps, "vision")
	}
	return ollama.Model{
		Name:              name,
		Capabilities:      caps,
		CapabilitiesKnown: true,
	}, nil
}

// Version checks connectivity by performing a lightweight call to /v1/models.
func (c *Client) Version(ctx context.Context) (string, error) {
	httpReq, err := c.newRequest(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return "", err
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openai server check: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return "OpenAI-compatible (/v1)", nil
	}
	return "", fmt.Errorf("openai server responded with status %s", resp.Status)
}

// Pull is a no-op for external/remote OpenAI-compatible endpoints.
func (c *Client) Pull(_ context.Context, _ string, _ func(ollama.PullEvent)) error {
	return errors.New("model pulling is not supported for OpenAI-compatible endpoints")
}
