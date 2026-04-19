package ollama

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatValidationAndNonStreamPath(t *testing.T) {
	t.Parallel()
	client := NewHTTP("http://example.com").(*HTTPClient)

	if _, err := client.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "x"}}}); err == nil {
		t.Fatal("expected missing model validation error")
	}
	if _, err := client.Chat(context.Background(), ChatRequest{Model: "m"}); err == nil {
		t.Fatal("expected missing messages validation error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"message":{"content":"hello <think>hidden</think>world"},"done":true}`))
	}))
	defer srv.Close()

	client = NewHTTP(srv.URL).(*HTTPClient)
	out, err := client.ChatSync(context.Background(), ChatRequest{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("chat sync failed: %v", err)
	}
	if strings.Contains(strings.ToLower(out.Content), "hidden") {
		t.Fatalf("think block should be stripped: %q", out.Content)
	}
}

func TestPullAndErrorBranches(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pull":
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte(`{"status":"pulling","completed":5,"total":10}` + "\n"))
			_, _ = w.Write([]byte(`{"status":"error","error":"boom"}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewHTTP(srv.URL)
	events := 0
	err := client.Pull(context.Background(), "model", func(PullEvent) { events++ })
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected pull error from event, got: %v", err)
	}
	if events == 0 {
		t.Fatal("expected progress callback to run")
	}
}

func TestChatStreamDecodeErrorAndMapFallbacks(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("{not-json}\n"))
	}))
	defer srv.Close()

	client := NewHTTP(srv.URL)
	stream, err := client.Chat(context.Background(), ChatRequest{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "x"}},
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("chat should start stream: %v", err)
	}
	gotErr := false
	for ch := range stream {
		if ch.Err != nil {
			gotErr = true
		}
	}
	if !gotErr {
		t.Fatal("expected decode error from invalid NDJSON")
	}

	if msg := mapChatError(http.StatusInternalServerError, ""); msg.Error() != "http 500" {
		t.Fatalf("unexpected empty-body fallback: %v", msg)
	}
	if msg := mapChatError(http.StatusInternalServerError, "oops"); !strings.Contains(msg.Error(), "oops") {
		t.Fatalf("unexpected non-empty fallback: %v", msg)
	}
	if out := stripThinkBlocks("a <think>x</think> b"); out != "a  b" {
		t.Fatalf("unexpected think strip output: %q", out)
	}
}

func TestPullHTTPErrorAndTagsVersionErrors(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/pull":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = fmt.Fprint(w, `bad gateway`)
		case "/api/tags":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `boom`)
		case "/api/version":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = fmt.Fprint(w, `boom`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewHTTP(srv.URL)
	if err := client.Pull(context.Background(), "m", nil); err == nil {
		t.Fatal("expected pull status error")
	}
	if _, err := client.Tags(context.Background()); err == nil {
		t.Fatal("expected tags status error")
	}
	if _, err := client.Version(context.Background()); err == nil {
		t.Fatal("expected version status error")
	}
}

