package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestModelSupportsVision(t *testing.T) {
	t.Parallel()
	m := Model{Name: "llava", Capabilities: []string{"vision", "completion"}}
	if !m.SupportsVision() {
		t.Fatal("expected vision support")
	}
	if (Model{Name: "x", Capabilities: []string{"completion"}}).SupportsVision() {
		t.Fatal("did not expect vision support")
	}
	if (Model{}).SupportsVision() {
		t.Fatal("did not expect vision support for empty model")
	}
}

func TestLookupVision(t *testing.T) {
	t.Parallel()
	byModel := map[string]bool{"llava:latest": true, "qwen3.5:9b": false}
	for _, tc := range []struct {
		name      string
		available bool
		known     bool
	}{
		{"llava", true, true},
		{"llava:latest", true, true},
		{"LLAVA", true, true},
		{"qwen3.5:9b", false, true},
		{"qwen3.5", false, true},
		{"missing", false, false},
		{"", false, false},
	} {
		available, known := LookupVision(byModel, tc.name)
		if available != tc.available || known != tc.known {
			t.Fatalf("LookupVision(%q) = (%t,%t), want (%t,%t)",
				tc.name, available, known, tc.available, tc.known)
		}
	}
	if _, known := LookupVision(map[string]bool{"m:1": true, "m:2": false}, "m"); known {
		t.Fatal("expected ambiguous base to be unknown")
	}
}

func TestMatchModelToleratesTags(t *testing.T) {
	t.Parallel()
	models := []Model{{Name: "llava:latest"}, {Name: "qwen3.5:9b"}}
	if m := MatchModel(models, "llava"); m == nil || m.Name != "llava:latest" {
		t.Fatalf("expected llava match, got %+v", m)
	}
	if m := MatchModel(models, "QWEN3.5"); m == nil || m.Name != "qwen3.5:9b" {
		t.Fatalf("expected qwen match, got %+v", m)
	}
	if m := MatchModel(models, "missing"); m != nil {
		t.Fatalf("expected nil, got %+v", m)
	}
}

func TestMessageSerializesImagesWhenPresent(t *testing.T) {
	t.Parallel()
	with := Message{Role: "user", Content: "see this", Images: []string{"aGVsbG8="}}
	raw, err := json.Marshal(with)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"images":["aGVsbG8="]`) {
		t.Fatalf("expected images field, got %s", raw)
	}
	without := Message{Role: "user", Content: "plain"}
	raw, err = json.Marshal(without)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "images") {
		t.Fatalf("expected no images field, got %s", raw)
	}
}

func TestChatSyncFallsBackToStreamingOnEmpty(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/x-ndjson")
		if n == 1 {
			// Moondream-style: empty non-streaming body.
			_, _ = w.Write([]byte(`{"model":"m","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop"}`))
			return
		}
		_, _ = w.Write([]byte(`{"message":{"content":"red "},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"content":"circle"},"done":true}` + "\n"))
	}))
	defer srv.Close()

	resp, err := NewHTTP(srv.URL).ChatSync(context.Background(), ChatRequest{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("chatsync: %v", err)
	}
	if resp.Content != "red circle" {
		t.Fatalf("expected streamed fallback content, got %q", resp.Content)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected exactly 2 calls, got %d", got)
	}
}

func TestChatSyncEmptyTwiceStaysEmpty(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"model":"m","message":{"role":"assistant","content":""},"done":true}`))
	}))
	defer srv.Close()

	resp, err := NewHTTP(srv.URL).ChatSync(context.Background(), ChatRequest{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("chatsync: %v", err)
	}
	if resp.Content != "" {
		t.Fatalf("expected empty content, got %q", resp.Content)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected exactly 2 calls (no loop), got %d", got)
	}
}
