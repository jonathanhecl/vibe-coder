package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// TestChatStreamingUsageChunk verifies the trailing usage-only chunk (sent
// when stream_options include_usage is honored) reaches the caller on the
// final Done chunk even though it carries no choices.
func TestChatStreamingUsageChunk(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.StreamOptions == nil || !req.StreamOptions.IncludeUsage {
			t.Errorf("expected stream_options.include_usage on a streaming request")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		send := func(chunk string) {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}

		send(`{"choices":[{"index":0,"delta":{"content":"Hi"}}]}`)
		send(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		send(`{"choices":[],"usage":{"prompt_tokens":42,"completion_tokens":7}}`)
		send(`[DONE]`)
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	ch, err := client.Chat(context.Background(), ollama.ChatRequest{
		Model:    "usage-model",
		Messages: []ollama.Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	var content strings.Builder
	var done *ollama.Chunk
	doneCount := 0
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
		if chunk.Done {
			doneCount++
			c := chunk
			done = &c
			continue
		}
		content.WriteString(chunk.Delta)
	}

	if content.String() != "Hi" {
		t.Fatalf("unexpected content: %q", content.String())
	}
	if doneCount != 1 || done == nil {
		t.Fatalf("expected exactly one Done chunk, got %d", doneCount)
	}
	if done.Usage == nil {
		t.Fatal("expected usage on the Done chunk")
	}
	if done.Usage.PromptTokens != 42 || done.Usage.CompletionTokens != 7 {
		t.Fatalf("unexpected usage: %+v", done.Usage)
	}
}

// TestChatStreamingTimingsFromLlamaCpp covers the one place a llama.cpp server
// reports generation time: its non-standard timings block on the include_usage
// chunk. Standard OpenAI usage carries token counts only, so without this the
// footer has nothing to turn into a tokens/s figure.
func TestChatStreamingTimingsFromLlamaCpp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		send := func(chunk string) {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}

		send(`{"choices":[{"index":0,"delta":{"content":"Hi"}}]}`)
		send(`{"choices":[{"index":0,"delta":{},"finish_reason":"length"}],` +
			`"usage":{"prompt_tokens":58,"completion_tokens":16,"total_tokens":74},` +
			`"timings":{"cache_n":53,"prompt_n":5,"prompt_ms":27.1,` +
			`"predicted_n":16,"predicted_ms":158.6,"predicted_per_second":100.9}}`)
		send(`[DONE]`)
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	ch, err := client.Chat(context.Background(), ollama.ChatRequest{
		Model:    "timings-model",
		Messages: []ollama.Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	var done *ollama.Chunk
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
		if chunk.Done {
			c := chunk
			done = &c
		}
	}
	if done == nil || done.Usage == nil {
		t.Fatal("expected usage on the Done chunk")
	}
	if done.Usage.CompletionTokens != 16 || done.Usage.PromptTokens != 58 {
		t.Fatalf("unexpected token counts: %+v", done.Usage)
	}
	// 16 tokens over 158.6 ms is 100.9 tok/s, the rate the server reported.
	if got := float64(done.Usage.CompletionTokens) / done.Usage.EvalDuration.Seconds(); got < 100 || got > 102 {
		t.Fatalf("eval duration %v yields %.1f tok/s, want ~100.9 (from predicted_ms)", done.Usage.EvalDuration, got)
	}
	if done.Usage.PromptEvalDuration.Milliseconds() != 27 {
		t.Fatalf("prompt eval duration = %v, want 27ms", done.Usage.PromptEvalDuration)
	}
}

// A server that reports counts without timings must keep producing zero
// durations: the footer then omits the speed instead of inventing one.
func TestChatStreamingWithoutTimingsHasNoEvalDuration(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	ch, err := client.Chat(context.Background(), ollama.ChatRequest{
		Model:    "plain-model",
		Messages: []ollama.Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	var done *ollama.Chunk
	for chunk := range ch {
		if chunk.Done {
			c := chunk
			done = &c
		}
	}
	if done == nil || done.Usage == nil {
		t.Fatal("expected usage on the Done chunk")
	}
	if done.Usage.EvalDuration != 0 || done.Usage.PromptEvalDuration != 0 {
		t.Fatalf("durations appeared without timings: %+v", done.Usage)
	}
}

// TestChatStreamOptionsFallback verifies a 400 complaining about
// stream_options triggers one retry without the field, and that the model is
// remembered so later requests skip it from the start.
func TestChatStreamOptionsFallback(t *testing.T) {
	var sawStreamOptions []bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		sawStreamOptions = append(sawStreamOptions, req.StreamOptions != nil)

		if req.StreamOptions != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unrecognized request argument supplied: stream_options"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	ch, err := client.Chat(context.Background(), ollama.ChatRequest{
		Model:    "legacy-model",
		Messages: []ollama.Message{{Role: "user", Content: "Hello"}},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	var content strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
		content.WriteString(chunk.Delta)
	}
	if content.String() != "ok" {
		t.Fatalf("unexpected content: %q", content.String())
	}
	if len(sawStreamOptions) != 2 || !sawStreamOptions[0] || sawStreamOptions[1] {
		t.Fatalf("expected stream_options then fallback without it, got %v", sawStreamOptions)
	}
	if !client.isStreamOptionsUnsupported("legacy-model") {
		t.Fatal("expected the model to be remembered as stream_options-unsupported")
	}

	// A second chat for the same model must not send stream_options at all.
	sawStreamOptions = nil
	ch, err = client.Chat(context.Background(), ollama.ChatRequest{
		Model:    "legacy-model",
		Messages: []ollama.Message{{Role: "user", Content: "Again"}},
	})
	if err != nil {
		t.Fatalf("second Chat failed: %v", err)
	}
	for range ch {
	}
	if len(sawStreamOptions) != 1 || sawStreamOptions[0] {
		t.Fatalf("expected one request without stream_options, got %v", sawStreamOptions)
	}
}

func TestToolsUnsupportedReporting(t *testing.T) {
	client := New("http://dummy/v1")
	if client.ToolsUnsupported("m") {
		t.Fatal("expected tools supported before any 400")
	}
	client.markToolsUnsupported("m")
	if !client.ToolsUnsupported("m") {
		t.Fatal("expected tools unsupported after mark")
	}
}
