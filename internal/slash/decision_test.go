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

func TestDecisionRemovedModelRoutes(t *testing.T) {
	ctx, cfg, out := newDecisionTestCtx(t, "http://example:8765")
	cfg.DecisionModel = "tev1:0.8b"
	cfg.AssistedYes = true

	for _, line := range []string{"/decision endpoint http://other:9000", "/decision off", "/decision tev1:0.8b"} {
		if _, _, err := Dispatch(ctx, line); err != nil {
			t.Fatalf("%s: %v", line, err)
		}
	}
	if cfg.DecisionEndpoint != "http://example:8765" {
		t.Fatalf("expected the decision endpoint untouched, got %q", cfg.DecisionEndpoint)
	}
	if cfg.DecisionModel != "tev1:0.8b" {
		t.Fatalf("expected the decision model untouched, got %q", cfg.DecisionModel)
	}
	if !cfg.AssistedYes {
		t.Fatal("expected assisted mode untouched")
	}
	rendered := out.String()
	if !strings.Contains(rendered, "bound with the provider commands now") {
		t.Fatalf("expected the provider-commands hint, got %q", rendered)
	}
	if !strings.Contains(rendered, "Unknown /decision action") {
		t.Fatalf("expected the unknown-action message, got %q", rendered)
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
