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

func TestThinkSettingMarshal(t *testing.T) {
	t.Parallel()
	// Unset (nil) omits the field entirely.
	raw, err := json.Marshal(ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "think") {
		t.Fatalf("expected no think field, got %s", raw)
	}
	// Explicit false IS sent (this was silently dropped before).
	raw, err = json.Marshal(ChatRequest{Model: "m", Think: ThinkOff()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"think":false`) {
		t.Fatalf("expected explicit false, got %s", raw)
	}
	// Levels marshal as strings.
	raw, err = json.Marshal(ChatRequest{Model: "m", Think: ThinkLevel("low")})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"think":"low"`) {
		t.Fatalf("expected level string, got %s", raw)
	}
}

func TestNormalizeThinkLevel(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		norm string
		ok   bool
	}{
		"": {"", true}, "off": {"off", true}, "LOW": {"low", true}, "Medium": {"medium", true},
		"high": {"high", true}, "max": {"max", true}, "on": {"on", true},
		"false": {"off", true}, "true": {"on", true}, "no": {"off", true}, "yes": {"on", true},
		"ultra": {"", false}, "0": {"", false},
	}
	for in, want := range cases {
		norm, ok := NormalizeThinkLevel(in)
		if norm != want.norm || ok != want.ok {
			t.Fatalf("NormalizeThinkLevel(%q) = (%q,%t), want (%q,%t)", in, norm, ok, want.norm, want.ok)
		}
	}
}

func TestResolveThinkSetting(t *testing.T) {
	t.Parallel()
	asWire := func(s *ThinkSetting) string {
		if s == nil {
			return "unset"
		}
		raw, _ := json.Marshal(s)
		return string(raw)
	}
	cases := []struct {
		name             string
		level            string
		noThink          bool
		known, supported bool
		want             string
	}{
		{"explicit level wins over nothing", "low", false, false, false, `"low"`},
		{"explicit level wins over no-think", "high", true, false, false, `"high"`},
		{"off level", "off", false, false, false, `false`},
		{"legacy no-think", "", true, false, false, `false`},
		{"known non-thinker omits", "", false, true, false, "unset"},
		{"default stays explicit true", "", false, false, false, `true`},
		{"known thinker default true", "", false, true, true, `true`},
	}
	for _, tc := range cases {
		if got := asWire(ResolveThinkSetting(tc.level, tc.noThink, tc.known, tc.supported)); got != tc.want {
			t.Fatalf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}
}

func TestModelSupportsThinking(t *testing.T) {
	t.Parallel()
	if !(Model{Capabilities: []string{"thinking"}}).SupportsThinking() {
		t.Fatal("expected thinking support")
	}
	if (Model{Capabilities: []string{"vision"}}).SupportsThinking() {
		t.Fatal("did not expect thinking support")
	}
}

func TestChatRetriesLevelWithoutThink(t *testing.T) {
	t.Parallel()
	var bodies int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&bodies, 1)
		if n == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"\"m\" does not support thinking"}`))
			return
		}
		var req ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode retry: %v", err)
		}
		if req.Think == nil || req.Think.IsActive() {
			t.Errorf("expected retried request with think disabled, got %+v", req.Think)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"message":{"content":"ok"},"done":true}` + "\n"))
	}))
	defer srv.Close()

	stream, err := NewHTTP(srv.URL).Chat(context.Background(), ChatRequest{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Think:    ThinkLevel("high"),
	})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	var b strings.Builder
	for chunk := range stream {
		if chunk.Err != nil {
			t.Fatalf("chunk: %v", chunk.Err)
		}
		b.WriteString(chunk.Delta)
	}
	if b.String() != "ok" {
		t.Fatalf("unexpected content %q", b.String())
	}
	if got := atomic.LoadInt32(&bodies); got != 2 {
		t.Fatalf("expected 2 calls, got %d", got)
	}
}
