package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestChatRequestKeepAliveMarshal pins the wire contract for keep_alive:
// 0 omits the field (the provider applies its own default), -1 and any
// positive value are sent verbatim.
func TestChatRequestKeepAliveMarshal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		alive int
		want  string
	}{
		{name: "zero omits the field", alive: 0, want: ""},
		{name: "minus one is sent", alive: -1, want: `"keep_alive":-1`},
		{name: "positive is sent", alive: 300, want: `"keep_alive":300`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(ChatRequest{
				Model:     "m",
				Messages:  []Message{{Role: "user", Content: "hi"}},
				KeepAlive: tc.alive,
			})
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if tc.want == "" {
				if strings.Contains(string(raw), `"keep_alive"`) {
					t.Fatalf("expected keep_alive to be omitted, got %s", raw)
				}
				return
			}
			if !strings.Contains(string(raw), tc.want) {
				t.Fatalf("expected %s in %s", tc.want, raw)
			}
		})
	}
}

// TestChatPreservesKeepAlive guards against the old unconditional
// rewrite of 0 to -1 in Chat, which pinned every model forever.
func TestChatPreservesKeepAlive(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		alive      int
		wantInBody bool
		wantValue  string
	}{
		{name: "zero is not rewritten to minus one", alive: 0, wantInBody: false},
		{name: "minus one passes through", alive: -1, wantInBody: true, wantValue: "-1"},
		{name: "worker value passes through", alive: 300, wantInBody: true, wantValue: "300"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				body = string(raw)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ok"},"done":true}`))
			}))
			defer srv.Close()

			client := NewHTTP(srv.URL)
			if _, err := client.Chat(context.Background(), ChatRequest{
				Model:     "m",
				Messages:  []Message{{Role: "user", Content: "hi"}},
				Stream:    false,
				KeepAlive: tc.alive,
			}); err != nil {
				t.Fatalf("chat: %v", err)
			}

			if tc.wantInBody {
				if !strings.Contains(body, `"keep_alive":`+tc.wantValue) {
					t.Fatalf("expected keep_alive:%s in body, got %s", tc.wantValue, body)
				}
				return
			}
			if strings.Contains(body, `"keep_alive"`) {
				t.Fatalf("expected keep_alive omitted in body, got %s", body)
			}
		})
	}
}
