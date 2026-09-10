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
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/logger"
)

// isThinkingUnsupportedBody detects Ollama's 400 when the model cannot run with "think": true.
func isThinkingUnsupportedBody(body string) bool {
	b := strings.ToLower(body)
	if !strings.Contains(b, "thinking") {
		return false
	}
	return strings.Contains(b, "does not support") || strings.Contains(b, "not support")
}

// isToolsUnsupportedBody detects Ollama's 400 when the model cannot run with "tools".
func isToolsUnsupportedBody(body string) bool {
	b := strings.ToLower(body)
	if !strings.Contains(b, "tool") {
		return false
	}
	return strings.Contains(b, "does not support") || strings.Contains(b, "not support")
}

// thinkForLog renders the think setting for diagnostics.
func thinkForLog(t *ThinkSetting) string {
	if t == nil {
		return "unset"
	}
	if t.Level != "" {
		return t.Level
	}
	if t.Enabled {
		return "true"
	}
	return "false"
}

// postChat calls /api/chat; on 400 "does not support thinking" it retries
// once with think disabled, and on 400 tool errors it retries once without
// tools (marking the model in-process so later turns skip the doomed trip).
func (c *HTTPClient) postChat(ctx context.Context, req ChatRequest) (*http.Response, error) {
	attempt := req
	for {
		payload, err := json.Marshal(attempt)
		if err != nil {
			return nil, fmt.Errorf("marshal chat request: %w", err)
		}
		logger.Infof("Ollama API POST calling /api/chat (attempt with think=%s tools=%d)", thinkForLog(attempt.Think), len(attempt.Tools))
		resp, err := doPOSTWithRetry(ctx, c.http, func() (*http.Request, error) {
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(payload))
			if err != nil {
				return nil, err
			}
			httpReq.Header.Set("Content-Type", "application/json")
			return httpReq, nil
		})
		if err != nil {
			logger.Errorf("Ollama API POST call failed: %v", err)
			return nil, err
		}
		if resp.StatusCode == http.StatusOK {
			logger.Infof("Ollama API POST succeeded with status 200")
			return resp, nil
		}
		bodyStr := readLimitedBody(resp.Body, 32*1024)
		_ = resp.Body.Close()
		logger.Errorf("Ollama API POST failed: status=%d, body=%q", resp.StatusCode, bodyStr)
		if resp.StatusCode == http.StatusBadRequest && attempt.Think.IsActive() && isThinkingUnsupportedBody(bodyStr) {
			logger.Infof("Model doesn't support thinking, retrying with think disabled")
			c.markThinkUnsupported(attempt.Model)
			attempt.Think = ThinkOff()
			continue
		}
		if resp.StatusCode == http.StatusBadRequest && len(attempt.Tools) > 0 && isToolsUnsupportedBody(bodyStr) {
			logger.Infof("Model doesn't support tools, retrying without tools")
			c.markToolsUnsupported(attempt.Model)
			attempt.Tools = nil
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
	logger.Infof("Ollama Chat request: model=%s, stream=%t, message_count=%d", req.Model, req.Stream, len(req.Messages))
	if len(req.Messages) > 0 {
		lastMsg := req.Messages[len(req.Messages)-1]
		logger.Infof("Last user/system message role=%s: %q", lastMsg.Role, lastMsg.Content)
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
	c.applyToolsSessionOverride(&req)
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
	ch <- Chunk{Delta: parsed.Message.Content, Thinking: parsed.Message.Thinking, ToolCalls: parsed.Message.ToolCalls, Done: true}
	close(ch)
	return ch, nil
}

// streamStallTimeout bounds how long the chat stream may go without
// delivering any bytes after a 200 OK. A model stuck loading or wedged
// mid-generation would otherwise leave the agent waiting silently until the
// 15-minute chat timeout with no progress feedback.
var streamStallTimeout = 5 * time.Minute

func streamChatResponse(ctx context.Context, body io.ReadCloser) <-chan Chunk {
	ch := make(chan Chunk, 8)
	go func() {
		defer close(ch)
		defer body.Close()
		// Unblock a wedged scanner.Scan when the caller cancels: the HTTP
		// transport normally aborts the read itself, but an explicit close
		// guarantees Ctrl+C and timeouts always interrupt the wait.
		stopWatch := context.AfterFunc(ctx, func() { _ = body.Close() })
		defer stopWatch()

		// Read lines on a separate goroutine so the consumer can enforce
		// a stall timeout and react to cancellation while Scan blocks.
		lines := make(chan string, 16)
		scanDone := make(chan error, 1)
		go func() {
			defer close(lines)
			scanner := newStreamScanner(body)
			for scanner.Scan() {
				select {
				case lines <- scanner.Text():
				case <-ctx.Done():
					return
				}
			}
			select {
			case scanDone <- scanner.Err():
			case <-ctx.Done():
			}
		}()

		// streamStallTimeout bounds how long the stream may go without
		// delivering any bytes. Without it a wedged model leaves the agent
		// waiting silently until the 15-minute chat timeout.
		stall := time.NewTimer(streamStallTimeout)
		defer stall.Stop()
		resetStall := func() {
			if !stall.Stop() {
				select {
				case <-stall.C:
				default:
				}
			}
			stall.Reset(streamStallTimeout)
		}
		chunkCount := 0
		for {
			select {
			case <-ctx.Done():
				logger.Errorf("Ollama chat stream context cancelled: %v", ctx.Err())
				ch <- Chunk{Err: ctx.Err(), Done: true}
				return
			case <-stall.C:
				logger.Errorf("Ollama chat stream stalled: no data for %s", streamStallTimeout)
				_ = body.Close()
				ch <- Chunk{Err: fmt.Errorf("ollama stream stalled: no data for %s (model may be overloaded; retry with --no-think)", streamStallTimeout), Done: true}
				return
			case line, ok := <-lines:
				if !ok {
					err := <-scanDone
					if err != nil {
						logger.Errorf("Ollama chat stream scanner failed: %v", err)
						ch <- Chunk{Err: fmt.Errorf("read chat stream: %w", err), Done: true}
					} else {
						logger.Infof("Ollama chat stream closed cleanly: chunk_count=%d", chunkCount)
					}
					return
				}
				resetStall()
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var parsed chatResponseLine
				if err := json.Unmarshal([]byte(line), &parsed); err != nil {
					logger.Errorf("Ollama chat stream unmarshal failed: %v, raw line: %q", err, line)
					ch <- Chunk{Err: fmt.Errorf("decode chat stream line: %w", err), Done: true}
					return
				}
				if parsed.Error != "" {
					logger.Errorf("Ollama chat stream error: %s", parsed.Error)
					ch <- Chunk{Err: errors.New(parsed.Error), Done: true}
					return
				}
				chunkCount++
				// Ollama streams content deltas per line and delivers the complete
				// tool_calls list on the final (done) message. Forward them as-is;
				// the consumer keeps the last non-empty set.
				ch <- Chunk{Delta: parsed.Message.Content, Thinking: parsed.Message.Thinking, ToolCalls: parsed.Message.ToolCalls, Done: parsed.Done}
				if parsed.Done {
					logger.Infof("Ollama chat stream done: chunk_count=%d", chunkCount)
					return
				}
			}
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
	content, thinking, toolCalls, streamErr := drainStream(stream)
	if streamErr != nil {
		return ChatResponse{}, streamErr
	}
	// Some models (e.g. moondream builds) return an empty body on the
	// non-streaming path while streaming the same turn fine. Retry once via
	// streaming instead of surfacing a bogus empty reply. A turn carrying
	// native tool calls is never "empty", even with blank visible text.
	if strings.TrimSpace(content.String()) == "" && len(toolCalls) == 0 && ctx.Err() == nil {
		logger.Infof("ChatSync got empty non-streaming reply; retrying via streaming")
		sreq := req
		sreq.Stream = true
		stream, err := c.Chat(ctx, sreq)
		if err != nil {
			return ChatResponse{}, err
		}
		content, thinking, toolCalls, streamErr = drainStream(stream)
		if streamErr != nil {
			return ChatResponse{}, streamErr
		}
	}
	out := stripThinkBlocks(content.String())
	return ChatResponse{Content: out, Thinking: strings.TrimSpace(thinking.String()), ToolCalls: toolCalls}, nil
}

// drainStream collects a chat channel, stopping at the first error or Done.
// Tool calls can arrive split across several chunks, so they are
// accumulated (with duplicate suppression) instead of last-write-wins.
func drainStream(stream <-chan Chunk) (content, thinking strings.Builder, toolCalls []MessageToolCall, err error) {
	for chunk := range stream {
		if chunk.Err != nil {
			return content, thinking, toolCalls, chunk.Err
		}
		content.WriteString(chunk.Delta)
		thinking.WriteString(chunk.Thinking)
		toolCalls = MergeToolCalls(toolCalls, chunk.ToolCalls)
		if chunk.Done {
			break
		}
	}
	return content, thinking, toolCalls, nil
}

// Compiled once; ChatSync may redact thinking blocks on every non-streaming reply.
var thinkBlockRE = regexp.MustCompile("(?is)<think>.*?</think>")

func stripThinkBlocks(text string) string {
	return strings.TrimSpace(thinkBlockRE.ReplaceAllString(text, ""))
}
