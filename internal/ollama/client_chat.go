package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

// isThinkingUnsupportedBody detects Ollama's 400 when the model cannot run with "think": true.
func isThinkingUnsupportedBody(body string) bool {
	b := strings.ToLower(body)
	if !strings.Contains(b, "thinking") {
		return false
	}
	return strings.Contains(b, "does not support") || strings.Contains(b, "not support")
}

// postChat calls /api/chat; on 400 "does not support thinking" it retries once with think disabled.
func (c *HTTPClient) postChat(ctx context.Context, req ChatRequest) (*http.Response, error) {
	attempt := req
	for {
		payload, err := json.Marshal(attempt)
		if err != nil {
			return nil, fmt.Errorf("marshal chat request: %w", err)
		}
		resp, err := doPOSTWithRetry(ctx, c.http, func() (*http.Request, error) {
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(payload))
			if err != nil {
				return nil, err
			}
			httpReq.Header.Set("Content-Type", "application/json")
			return httpReq, nil
		})
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		bodyStr := readLimitedBody(resp.Body, 32*1024)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusBadRequest && attempt.Think && isThinkingUnsupportedBody(bodyStr) {
			c.markThinkUnsupported(attempt.Model)
			attempt.Think = false
			continue
		}
		return nil, mapChatError(resp.StatusCode, bodyStr)
	}
}

func (c *HTTPClient) Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	if req.Model == "" {
		return nil, errors.New("chat model is required")
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("chat requires at least one message")
	}
	streamRequested := req.Stream
	if !streamRequested {
		req.Stream = false
	} else {
		req.Stream = true
	}
	if req.KeepAlive == 0 {
		req.KeepAlive = -1
	}
	c.applyThinkSessionOverride(&req)
	resp, err := c.postChat(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ollama chat: %w", err)
	}
	if !streamRequested {
		return decodeSingleChatResponse(resp.Body)
	}
	return streamChatResponse(ctx, resp.Body), nil
}

func decodeSingleChatResponse(body io.ReadCloser) (<-chan Chunk, error) {
	raw, err := io.ReadAll(io.LimitReader(body, 32*1024*1024))
	_ = body.Close()
	if err != nil {
		return nil, fmt.Errorf("read chat response: %w", err)
	}
	var parsed chatResponseLine
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}
	ch := make(chan Chunk, 1)
	ch <- Chunk{Delta: parsed.Message.Content, Thinking: parsed.Message.Thinking, Done: true}
	close(ch)
	return ch, nil
}

func streamChatResponse(ctx context.Context, body io.ReadCloser) <-chan Chunk {
	ch := make(chan Chunk, 8)
	go func() {
		defer close(ch)
		defer body.Close()
		scanner := newStreamScanner(body)
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
			ch <- Chunk{Delta: parsed.Message.Content, Thinking: parsed.Message.Thinking, Done: parsed.Done}
			if parsed.Done {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- Chunk{Err: fmt.Errorf("read chat stream: %w", err), Done: true}
		}
	}()
	return ch
}

func (c *HTTPClient) ChatSync(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	req.Stream = false
	stream, err := c.Chat(ctx, req)
	if err != nil {
		return ChatResponse{}, err
	}
	var content, thinking strings.Builder
	for chunk := range stream {
		if chunk.Err != nil {
			return ChatResponse{}, chunk.Err
		}
		content.WriteString(chunk.Delta)
		thinking.WriteString(chunk.Thinking)
		if chunk.Done {
			break
		}
	}
	out := stripThinkBlocks(content.String())
	return ChatResponse{Content: out, Thinking: strings.TrimSpace(thinking.String())}, nil
}

// Compiled once; ChatSync may redact thinking blocks on every non-streaming reply.
var thinkBlockRE = regexp.MustCompile("(?is)<think>.*?</think>")

func stripThinkBlocks(text string) string {
	return strings.TrimSpace(thinkBlockRE.ReplaceAllString(text, ""))
}
