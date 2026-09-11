package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPRequestGET(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	res := NewHTTPRequestTool().Execute(context.Background(), map[string]any{"url": srv.URL + "/status"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "HTTP 200") || !strings.Contains(res.Output, `{"ok":true}`) {
		t.Fatalf("unexpected output: %s", res.Output)
	}
}

func TestHTTPRequestPOSTWithHeadersAndBody(t *testing.T) {
	t.Parallel()
	var gotBody, gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Test")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	res := NewHTTPRequestTool().Execute(context.Background(), map[string]any{
		"url":     srv.URL,
		"method":  "POST",
		"headers": map[string]any{"X-Test": "yes"},
		"body":    `{"prompt":"hello"}`,
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if gotHeader != "yes" || gotBody != `{"prompt":"hello"}` {
		t.Fatalf("server did not receive request: header=%q body=%q", gotHeader, gotBody)
	}
	if !strings.Contains(res.Output, "HTTP 202") {
		t.Fatalf("expected 202 in output, got %s", res.Output)
	}
}

func TestHTTPRequestErrorStatusIsMarked(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	res := NewHTTPRequestTool().Execute(context.Background(), map[string]any{"url": srv.URL})
	if !res.IsError {
		t.Fatalf("expected 500 to be an error result, got %s", res.Output)
	}
	if !strings.Contains(res.Output, "HTTP 500") {
		t.Fatalf("expected status in output, got %s", res.Output)
	}
}

func TestHTTPRequestRejectsNonHTTPScheme(t *testing.T) {
	t.Parallel()
	res := NewHTTPRequestTool().Execute(context.Background(), map[string]any{"url": "file:///etc/passwd"})
	if !res.IsError {
		t.Fatal("expected non-http scheme to be rejected")
	}
}
