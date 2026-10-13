package slash

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

func decisionTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "model": "srv-model"})
		case "/v1/systemone":
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			qs := req["questions"].(map[string]any)
			answers := map[string]any{}
			for name, q := range qs {
				switch q.(map[string]any)["type"] {
				case "choice":
					crit := q.(map[string]any)["criteria"].(map[string]any)
					var first string
					for k := range crit {
						if first == "" || k < first {
							first = k
						}
					}
					answers[name] = map[string]any{"type": "choice", "choice": first, "confidence": 0.9}
				case "noul":
					answers[name] = map[string]any{"type": "noul", "noul": 0.1, "confidence": 0.8}
				default:
					answers[name] = map[string]any{"type": "score", "score": 0, "confidence": 0.9}
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"model": "srv-model", "answers": answers})
		default:
			http.NotFound(w, r)
		}
	}))
}

func newDecisionTestCtx(t *testing.T, endpoint string) (*Ctx, *config.Config, *bytes.Buffer) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:            "qwen3.5:4b",
		ContextWindow:    32768,
		Cwd:              tmp,
		SessionsDir:      tmp,
		ConfigDir:        tmp,
		ConfigFile:       filepath.Join(tmp, "vibe-coder.env"),
		DecisionEndpoint: endpoint,
	}
	out := &bytes.Buffer{}
	return &Ctx{
		Cfg:     cfg,
		Session: session.New(cfg),
		Out:     out,
		Models:  &fakeModelLister{models: pickerTestModels()},
	}, cfg, out
}

func TestDecisionStatusAndUsage(t *testing.T) {
	lastDecisionCheck = nil
	ctx, _, out := newDecisionTestCtx(t, "http://example:8765")
	if _, _, err := Dispatch(ctx, "/decision"); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{"Decision model:", "endpoint: http://example:8765", "assisted mode: off", "/decision test"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in output, got:\n%s", want, rendered)
		}
	}
}

func TestDecisionNotConfigured(t *testing.T) {
	ctx, _, out := newDecisionTestCtx(t, "")
	if _, _, err := Dispatch(ctx, "/decision status"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "not configured") {
		t.Fatalf("expected not-configured status, got %q", out.String())
	}
}

func TestDecisionEndpointSetAndClear(t *testing.T) {
	srv := decisionTestServer(t)
	defer srv.Close()
	lastDecisionCheck = nil

	ctx, cfg, out := newDecisionTestCtx(t, "")
	if _, _, err := Dispatch(ctx, "/decision endpoint "+srv.URL); err != nil {
		t.Fatal(err)
	}
	if cfg.DecisionEndpoint != srv.URL {
		t.Fatalf("expected endpoint set, got %q", cfg.DecisionEndpoint)
	}
	if !strings.Contains(out.String(), "Decision backend: systemone-server") {
		t.Fatalf("expected backend line, got %q", out.String())
	}

	if _, _, err := Dispatch(ctx, "/decision endpoint ollama"); err != nil {
		t.Fatal(err)
	}
	if cfg.DecisionEndpoint != "" {
		t.Fatalf("expected endpoint cleared, got %q", cfg.DecisionEndpoint)
	}
}

func TestDecisionEndpointInvalid(t *testing.T) {
	ctx, cfg, out := newDecisionTestCtx(t, "")
	if _, _, err := Dispatch(ctx, "/decision endpoint example.com"); err != nil {
		t.Fatal(err)
	}
	if cfg.DecisionEndpoint != "" {
		t.Fatalf("expected endpoint unchanged, got %q", cfg.DecisionEndpoint)
	}
	if !strings.Contains(out.String(), "must start with http://") {
		t.Fatalf("expected invalid-endpoint message, got %q", out.String())
	}
}

func TestDecisionAssistedToggle(t *testing.T) {
	ctx, cfg, _ := newDecisionTestCtx(t, "http://example:8765")
	if _, _, err := Dispatch(ctx, "/decision assisted on"); err != nil {
		t.Fatal(err)
	}
	if !cfg.AssistedYes {
		t.Fatal("expected assisted mode on")
	}
	if _, _, err := Dispatch(ctx, "/decision assisted off"); err != nil {
		t.Fatal(err)
	}
	if cfg.AssistedYes {
		t.Fatal("expected assisted mode off")
	}
}

func TestDecisionOff(t *testing.T) {
	ctx, cfg, _ := newDecisionTestCtx(t, "http://example:8765")
	cfg.DecisionModel = "tev1:0.8b"
	cfg.AssistedYes = true
	if _, _, err := Dispatch(ctx, "/decision off"); err != nil {
		t.Fatal(err)
	}
	if cfg.DecisionInUse() || cfg.AssistedYes {
		t.Fatalf("expected decision disabled, got %+v", cfg)
	}
}

func TestDecisionTestSubcommand(t *testing.T) {
	srv := decisionTestServer(t)
	defer srv.Close()
	lastDecisionCheck = nil

	ctx, _, out := newDecisionTestCtx(t, srv.URL)
	if _, _, err := Dispatch(ctx, "/decision test"); err != nil {
		t.Fatal(err)
	}
	rendered := out.String()
	for _, want := range []string{"Backend: systemone-server", "AskSafety (ls -la): action=allow", "DecideBool (is raining?): false", "Decision test complete."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in output, got:\n%s", want, rendered)
		}
	}
}

func TestDecisionModelArg(t *testing.T) {
	srv := decisionTestServer(t)
	defer srv.Close()

	ctx, cfg, out := newDecisionTestCtx(t, "")
	// Point the Ollama fallback at the test server so Check succeeds.
	cfg.OllamaHost = srv.URL
	if _, _, err := Dispatch(ctx, "/decision tev1:0.8b"); err != nil {
		t.Fatal(err)
	}
	if cfg.DecisionModel != "tev1:0.8b" {
		t.Fatalf("expected model set, got %q", cfg.DecisionModel)
	}
	if !strings.Contains(out.String(), "Decision model set to: tev1:0.8b") {
		t.Fatalf("expected set message, got %q", out.String())
	}
}
