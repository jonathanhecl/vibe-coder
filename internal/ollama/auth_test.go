package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestHTTPClientSendsAPIKeyOnEveryRequest(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("Authorization")
		mu.Unlock()
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{"model":"m","capabilities":["completion"]}`))
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"0.6.0"}`))
		case "/api/pull":
			_, _ = w.Write([]byte(`{"status":"success"}` + "\n"))
		case "/api/chat":
			_, _ = w.Write([]byte(`{"message":{"content":"ok"},"done":true}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client, ok := NewHTTPWithAPIKey(srv.URL, " secret-key ").(*HTTPClient)
	if !ok {
		t.Fatal("expected the native HTTP client")
	}
	ctx := context.Background()

	if _, err := client.Tags(ctx); err != nil {
		t.Fatalf("tags: %v", err)
	}
	if _, err := client.Show(ctx, "m"); err != nil {
		t.Fatalf("show: %v", err)
	}
	if _, err := client.Version(ctx); err != nil {
		t.Fatalf("version: %v", err)
	}
	if err := client.Pull(ctx, "m", nil); err != nil {
		t.Fatalf("pull: %v", err)
	}
	stream, err := client.Chat(ctx, ChatRequest{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatalf("chat chunk: %v", chunk.Err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/api/tags", "/api/show", "/api/version", "/api/pull", "/api/chat"} {
		if got := seen[path]; got != "Bearer secret-key" {
			t.Fatalf("%s authorization = %q, want %q", path, got, "Bearer secret-key")
		}
	}
}

func TestHTTPClientWithoutAPIKeySendsNoAuthorization(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer srv.Close()

	if _, err := NewHTTP(srv.URL).Tags(context.Background()); err != nil {
		t.Fatalf("tags: %v", err)
	}
	if got != "" {
		t.Fatalf("unexpected authorization header: %q", got)
	}
}
