package ollama

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTagsVersionAndChatStream(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"llama3.2:3b"}]}`))
		case "/api/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"0.6.0"}`))
		case "/api/chat":
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte(`{"message":{"content":"hello "},"done":false}` + "\n"))
			_, _ = w.Write([]byte(`{"message":{"content":"world"},"done":true}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewHTTP(srv.URL)

	ctx := context.Background()
	models, err := client.Tags(ctx)
	if err != nil {
		t.Fatalf("tags failed: %v", err)
	}
	if len(models) != 1 || models[0].Name != "llama3.2:3b" {
		t.Fatalf("unexpected models: %#v", models)
	}

	version, err := client.Version(ctx)
	if err != nil {
		t.Fatalf("version failed: %v", err)
	}
	if version != "0.6.0" {
		t.Fatalf("unexpected version: %q", version)
	}

	stream, err := client.Chat(ctx, ChatRequest{
		Model: "llama3.2:3b",
		Messages: []Message{
			{Role: "user", Content: "hello"},
		},
		Stream: true,
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}

	var b strings.Builder
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
		b.WriteString(chunk.Delta)
	}
	if b.String() != "hello world" {
		t.Fatalf("unexpected stream content: %q", b.String())
	}
}

func TestChatStreamSplitsThinkingAndContent(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"message":{"thinking":"reasoning... "},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"thinking":"more"},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"content":"answer"},"done":true}` + "\n"))
	}))
	defer srv.Close()

	client := NewHTTP(srv.URL)
	stream, err := client.Chat(context.Background(), ChatRequest{
		Model:    "qwen3.5:9b",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Stream:   true,
		Think:    true,
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}

	var content, thinking strings.Builder
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		content.WriteString(chunk.Delta)
		thinking.WriteString(chunk.Thinking)
	}
	if content.String() != "answer" {
		t.Fatalf("unexpected content: %q", content.String())
	}
	if thinking.String() != "reasoning... more" {
		t.Fatalf("unexpected thinking: %q", thinking.String())
	}
}

func TestChatErrorMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    string
	}{
		{
			name:       "not found maps to pull hint",
			statusCode: http.StatusNotFound,
			body:       `{"error":"not found"}`,
			wantErr:    "Model not found. Run: ollama pull X",
		},
		{
			name:       "bad request tool maps to no tool support",
			statusCode: http.StatusBadRequest,
			body:       `{"error":"tool support missing"}`,
			wantErr:    "Model does not support function calling",
		},
		{
			name:       "bad request context maps to context exceeded",
			statusCode: http.StatusBadRequest,
			body:       `{"error":"context length exceeded"}`,
			wantErr:    "Context window exceeded",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/chat" {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(tc.statusCode)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			client := NewHTTP(srv.URL)
			_, err := client.Chat(context.Background(), ChatRequest{
				Model: "llama3.2:3b",
				Messages: []Message{
					{Role: "user", Content: "hello"},
				},
			})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("unexpected error: got %q want contains %q", err.Error(), tc.wantErr)
			}
		})
	}
}

