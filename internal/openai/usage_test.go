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
