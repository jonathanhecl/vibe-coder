package ollama

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
	"time"
)

const (
	defaultHTTPTimeout = 5 * time.Minute
	maxStreamBuffer    = 1024 * 1024
)

type Client interface {
	Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error)
	Tags(ctx context.Context) ([]Model, error)
	Version(ctx context.Context) (string, error)
}

type HTTPClient struct {
	baseURL string
	http    *http.Client
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatOptions struct {
	NumCtx      int     `json:"num_ctx,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

type ChatRequest struct {
	Model    string      `json:"model"`
	Messages []Message   `json:"messages"`
	Stream   bool        `json:"stream"`
	Options  ChatOptions `json:"options"`
	KeepAlive int        `json:"keep_alive"`
}

type Chunk struct {
	Delta string
	Done  bool
	Err   error
}

type Model struct {
	Name string `json:"name"`
}

type tagsResponse struct {
	Models []Model `json:"models"`
}

type versionResponse struct {
	Version string `json:"version"`
}

type chatResponseLine struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Done  bool   `json:"done"`
	Error string `json:"error"`
}

func NewHTTP(baseURL string) Client {
	return &HTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

func (c *HTTPClient) Tags(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("build tags request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama tags: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return nil, fmt.Errorf("ollama tags failed: %s (%s)", resp.Status, strings.TrimSpace(string(body)))
	}

	var out tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode tags response: %w", err)
	}
	return out.Models, nil
}

func (c *HTTPClient) Version(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/version", nil)
	if err != nil {
		return "", fmt.Errorf("build version request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return "", fmt.Errorf("ollama version failed: %s (%s)", resp.Status, strings.TrimSpace(string(body)))
	}

	var out versionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode version response: %w", err)
	}
	return out.Version, nil
}

func (c *HTTPClient) Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	if req.Model == "" {
		return nil, errors.New("chat model is required")
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("chat requires at least one message")
	}

	if !req.Stream {
		req.Stream = true
	}
	if req.KeepAlive == 0 {
		req.KeepAlive = -1
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal chat request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama chat: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		return nil, fmt.Errorf("ollama chat: %w", mapChatError(resp.StatusCode, string(body)))
	}

	ch := make(chan Chunk)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 4096), maxStreamBuffer)
		for scanner.Scan() {
			select {
			case <-ctx.Done():
				ch <- Chunk{Err: ctx.Err(), Done: true}
				return
			default:
			}

			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			var parsed chatResponseLine
			if err := json.Unmarshal([]byte(line), &parsed); err != nil {
				ch <- Chunk{Err: fmt.Errorf("decode chat stream line: %w", err), Done: true}
				return
			}
			if parsed.Error != "" {
				ch <- Chunk{Err: errors.New(parsed.Error), Done: true}
				return
			}

			ch <- Chunk{
				Delta: parsed.Message.Content,
				Done:  parsed.Done,
			}
			if parsed.Done {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- Chunk{Err: fmt.Errorf("read chat stream: %w", err), Done: true}
		}
	}()

	return ch, nil
}

func mapChatError(statusCode int, body string) error {
	bodyLower := strings.ToLower(body)
	switch statusCode {
	case http.StatusNotFound:
		return errors.New("Model not found. Run: ollama pull X")
	case http.StatusBadRequest:
		if strings.Contains(bodyLower, "tool") {
			return errors.New("Model does not support function calling")
		}
		if strings.Contains(bodyLower, "context") {
			return errors.New("Context window exceeded")
		}
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return fmt.Errorf("http %d", statusCode)
	}
	return fmt.Errorf("http %d: %s", statusCode, trimmed)
}

