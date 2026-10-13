package decision

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func answersBody(t *testing.T, answers map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{"model": "m", "answers": answers})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func soServer(t *testing.T, handler func(w http.ResponseWriter, req map[string]any)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("bad request json: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		handler(w, req)
	}))
}

func TestRequestBodyIncludesModelAndQuestion(t *testing.T) {
	srv := soServer(t, func(w http.ResponseWriter, req map[string]any) {
		if req["model"] != "tev1:0.8b" {
			t.Errorf("expected model field, got %v", req["model"])
		}
		q := req["questions"].(map[string]any)["decision"].(map[string]any)
		if q["type"] != "noul" {
			t.Errorf("expected noul question, got %v", q["type"])
		}
		w.Write(answersBody(t, map[string]any{
			"decision": map[string]any{"type": "noul", "noul": 0.9, "confidence": 0.9},
		}))
	})
	defer srv.Close()

	c := New(srv.URL, "tev1:0.8b")
	ok, err := c.DecideBool(context.Background(), "state", "question?")
	if err != nil || !ok {
		t.Fatalf("DecideBool = %v, %v", ok, err)
	}
}

func TestEndpointNormalization(t *testing.T) {
	for _, raw := range []string{
		"http://x:1", "http://x:1/", "http://x:1/v1", "http://x:1/v1/", "http://x:1/v1/systemone", "http://x:1/v1/systemone/",
	} {
		if got := normalizeEndpoint(raw); got != "http://x:1" {
			t.Fatalf("normalizeEndpoint(%q) = %q", raw, got)
		}
	}
}

func TestBearerHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing bearer header: %q", r.Header.Get("Authorization"))
		}
		w.Write(answersBody(t, map[string]any{
			"decision": map[string]any{"type": "noul", "noul": 0.2},
		}))
	}))
	defer srv.Close()

	c := New(srv.URL, "m", WithAPIKey("secret"))
	if _, err := c.DecideBool(context.Background(), "s", "q"); err != nil {
		t.Fatal(err)
	}
}

func TestDecideChoiceValidation(t *testing.T) {
	srv := soServer(t, func(w http.ResponseWriter, req map[string]any) {
		w.Write(answersBody(t, map[string]any{
			"decision": map[string]any{"type": "choice", "choice": "beta", "confidence": 0.8},
		}))
	})
	defer srv.Close()
	c := New(srv.URL, "m")
	opt, idx, err := c.DecideChoice(context.Background(), "s", "q", "alpha", "beta", "gamma")
	if err != nil || opt != "beta" || idx != 1 {
		t.Fatalf("DecideChoice = %q,%d,%v", opt, idx, err)
	}
}

func TestAnswerValidationFailures(t *testing.T) {
	cases := []struct {
		name    string
		answers map[string]any
		call    func(c *Client) error
	}{
		{"missing answer key", map[string]any{"other": map[string]any{"type": "noul", "noul": 0.5}},
			func(c *Client) error { _, e := c.DecideBool(context.Background(), "s", "q"); return e }},
		{"invalid choice", map[string]any{"decision": map[string]any{"type": "choice", "choice": "nope"}},
			func(c *Client) error { _, _, e := c.DecideChoice(context.Background(), "s", "q", "a", "b"); return e }},
		{"invalid score", map[string]any{"decision": map[string]any{"type": "score", "score": 7}},
			func(c *Client) error { _, e := c.IsCommandDangerous(context.Background(), "ls"); return e }},
		{"invalid noul", map[string]any{"decision": map[string]any{"type": "noul", "noul": 1.4}},
			func(c *Client) error { _, e := c.DecideBool(context.Background(), "s", "q"); return e }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := soServer(t, func(w http.ResponseWriter, req map[string]any) {
				w.Write(answersBody(t, tc.answers))
			})
			defer srv.Close()
			if err := tc.call(New(srv.URL, "m")); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestErrorBodies(t *testing.T) {
	t.Run("ollama shape", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"model not found"}`))
		}))
		defer srv.Close()
		_, err := New(srv.URL, "m").DecideBool(context.Background(), "s", "q")
		if err == nil || !strings.Contains(err.Error(), "model not found") || !strings.Contains(err.Error(), "500") {
			t.Fatalf("expected parsed error, got %v", err)
		}
	})
	t.Run("nested message shape", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(503)
			w.Write([]byte(`{"error":{"message":"server busy"}}`))
		}))
		defer srv.Close()
		_, err := New(srv.URL, "m").DecideBool(context.Background(), "s", "q")
		if err == nil || !strings.Contains(err.Error(), "server busy") {
			t.Fatalf("expected nested error, got %v", err)
		}
	})
	t.Run("raw fallback clipped", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(400)
			w.Write([]byte(strings.Repeat("x", 2000)))
		}))
		defer srv.Close()
		_, err := New(srv.URL, "m").DecideBool(context.Background(), "s", "q")
		if err == nil || len(err.Error()) > 600 {
			t.Fatalf("expected clipped error, got len %d", len(err.Error()))
		}
	})
}

func healthAndShowServer(t *testing.T, caps []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			http.NotFound(w, r)
		case "/api/show":
			json.NewEncoder(w).Encode(map[string]any{"capabilities": caps})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestCheckSystemOneServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "model": "srv-model"})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	info, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Backend != "systemone-server" || info.Model != "srv-model" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if c.Model() != "srv-model" {
		t.Fatalf("expected learned model, got %q", c.Model())
	}
}

func TestCheckOllama(t *testing.T) {
	srv := healthAndShowServer(t, []string{"decision", "vision"})
	defer srv.Close()
	info, err := New(srv.URL, "tev1:0.8b").Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Backend != "ollama" || info.Model != "tev1:0.8b" || !info.Vision {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestCheckOllamaNoDecisionCapability(t *testing.T) {
	srv := healthAndShowServer(t, []string{"vision"})
	defer srv.Close()
	_, err := New(srv.URL, "plain:latest").Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not advertise the 'decision' capability") {
		t.Fatalf("expected capability error, got %v", err)
	}
}

func TestCheckOllamaEmptyModel(t *testing.T) {
	srv := healthAndShowServer(t, nil)
	defer srv.Close()
	_, err := New(srv.URL, "").Check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "a decision model name is required for an Ollama endpoint") {
		t.Fatalf("expected model-required error, got %v", err)
	}
}

func TestCheckHealthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "m").Check(context.Background()); err == nil {
		t.Fatal("expected health failure")
	}
}

func TestSafetyDecisionThresholds(t *testing.T) {
	if !(SafetyDecision{Action: "allow"}).Allowed() {
		t.Fatal("Allowed")
	}
	if !(SafetyDecision{Action: "block"}).Blocked() {
		t.Fatal("Blocked")
	}
	if !(SafetyDecision{Action: "review"}).NeedsReview() {
		t.Fatal("NeedsReview")
	}
	if !(SafetyDecision{Confidence: 0.7}).Confident() {
		t.Fatal("Confident at 0.7")
	}
	if (SafetyDecision{Confidence: 0.69}).Confident() {
		t.Fatal("not confident below 0.7")
	}
}

func TestAskSafety(t *testing.T) {
	srv := soServer(t, func(w http.ResponseWriter, req map[string]any) {
		qs := req["questions"].(map[string]any)
		if _, ok := qs["action"]; !ok {
			t.Error("missing action question")
		}
		if _, ok := qs["risk"]; !ok {
			t.Error("missing risk question")
		}
		w.Write(answersBody(t, map[string]any{
			"action": map[string]any{"type": "choice", "choice": "allow", "confidence": 0.95},
			"risk":   map[string]any{"type": "score", "score": 0, "confidence": 0.9},
		}))
	})
	defer srv.Close()

	d, err := New(srv.URL, "m").AskSafety(context.Background(), "Bash", "ls -la")
	if err != nil {
		t.Fatal(err)
	}
	if !d.Allowed() || !d.Confident() || d.RiskScore != 0 {
		t.Fatalf("unexpected decision: %+v", d)
	}
}

func TestDisambiguatePathCapsCandidates(t *testing.T) {
	srv := soServer(t, func(w http.ResponseWriter, req map[string]any) {
		q := req["questions"].(map[string]any)["decision"].(map[string]any)
		crit := q["criteria"].(map[string]any)
		if len(crit) != MaxOptions {
			t.Errorf("expected %d candidates, got %d", MaxOptions, len(crit))
		}
		w.Write(answersBody(t, map[string]any{
			"decision": map[string]any{"type": "choice", "choice": "p0", "confidence": 0.5},
		}))
	})
	defer srv.Close()
	cands := make([]string, 40)
	for i := range cands {
		cands[i] = "p" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + "/" + string(rune('0'+i%10))
	}
	cands[0] = "p0"
	got, ok, err := New(srv.URL, "m").DisambiguatePath(context.Background(), "hint", cands)
	if err != nil || !ok || got != "p0" {
		t.Fatalf("DisambiguatePath = %q,%v,%v", got, ok, err)
	}
}

func TestDangerousThreshold(t *testing.T) {
	srv := soServer(t, func(w http.ResponseWriter, req map[string]any) {
		w.Write(answersBody(t, map[string]any{
			"decision": map[string]any{"type": "score", "score": 3, "confidence": 0.9},
		}))
	})
	defer srv.Close()
	dangerous, err := New(srv.URL, "m").IsCommandDangerous(context.Background(), "rm -rf /")
	if err != nil || !dangerous {
		t.Fatalf("expected dangerous, %v %v", dangerous, err)
	}
}

func TestEnabledAndModelFallback(t *testing.T) {
	c := New("", "m")
	if c.Enabled() {
		t.Fatal("expected disabled without endpoint")
	}
	c = New("http://x", "")
	if c.Model() != "http://x" {
		t.Fatalf("expected endpoint fallback, got %q", c.Model())
	}
}

func TestWithTimeoutAndHTTPClient(t *testing.T) {
	c := New("http://x", "m", WithTimeout(5*time.Second), WithHTTPClient(&http.Client{Timeout: time.Second}))
	if c.timeout != 5*time.Second {
		t.Fatalf("expected 5s timeout, got %v", c.timeout)
	}
}
