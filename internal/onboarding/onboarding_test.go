package onboarding

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// TestRunFirstRunDefaultPathUsesInstalledRecommendedWithoutPull verifies the
// first run configures the host and the main model only, reusing the installed
// recommended model instead of pulling it.
func TestRunFirstRunDefaultPathUsesInstalledRecommendedWithoutPull(t *testing.T) {
	// Pin the RAM-based recommendation so the expected model is deterministic.
	t.Setenv("VIBE_CODER_RAM_GB", "12")
	recommended := recommendedModel()

	var mu sync.Mutex
	pullCalls := 0
	showCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"0.8.0"}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen3.5:4b"},{"name":"qwen3.5:1.7b"}]}`))
		case "/api/show":
			showCalls++
			data, _ := io.ReadAll(r.Body)
			payload := string(data)
			switch {
			case strings.Contains(payload, `"model":"qwen3.5:4b"`):
				_, _ = w.Write([]byte(`{"model":"qwen3.5:4b","capabilities":["tools"]}`))
			case strings.Contains(payload, `"model":"qwen3.5:1.7b"`):
				_, _ = w.Write([]byte(`{"model":"qwen3.5:1.7b","capabilities":["vision"]}`))
			default:
				http.NotFound(w, r)
			}
		case "/api/pull":
			mu.Lock()
			pullCalls++
			mu.Unlock()
			_, _ = w.Write([]byte(`{"status":"done"}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tmp := t.TempDir()
	cfg := &config.Config{
		ConfigDir:  tmp,
		ConfigFile: filepath.Join(tmp, "vibe-coder.env"),
		OllamaHost: defaultOllamaHost,
	}

	in := bytes.NewBufferString(strings.Join([]string{
		"c",     // manual host
		srv.URL, // host value
		"",      // main model: recommended
	}, "\n") + "\n")
	out := &bytes.Buffer{}

	if err := RunFirstRun(context.Background(), cfg, "test", in, out); err != nil {
		t.Fatalf("RunFirstRun: %v", err)
	}
	if cfg.OllamaHost != srv.URL {
		t.Fatalf("host mismatch: got %q", cfg.OllamaHost)
	}
	if cfg.Model != recommended {
		t.Fatalf("model mismatch: got %q, want %q", cfg.Model, recommended)
	}
	if cfg.SidecarModel != "" {
		t.Fatalf("expected no sidecar model on first run, got %q", cfg.SidecarModel)
	}
	if cfg.DecisionModel != "" {
		t.Fatalf("expected no decision model on first run, got %q", cfg.DecisionModel)
	}

	mu.Lock()
	gotPulls := pullCalls
	mu.Unlock()
	if gotPulls != 0 {
		t.Fatalf("expected no pull for the installed recommended model, got %d", gotPulls)
	}
	if showCalls == 0 {
		t.Fatal("expected /api/show calls to resolve per-model tool support")
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Installed models (2)") {
		t.Fatalf("expected the installed-model list, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "[1] qwen3.5:4b") || !strings.Contains(rendered, "[2] qwen3.5:1.7b") {
		t.Fatalf("expected the numbered model list, got:\n%s", rendered)
	}
	if got := strings.Count(rendered, "Installed models ("); got != 1 {
		t.Fatalf("expected the model list exactly once, got %d", got)
	}
	if strings.Contains(rendered, "Sidecar model choice") || strings.Contains(rendered, "Decision model choice") {
		t.Fatalf("first run must not ask for other roles, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "/setmodel") {
		t.Fatalf("expected the new-commands hint, got:\n%s", rendered)
	}
}

func TestNormalizeHost(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"http://mac-mini.local:11434": "http://mac-mini.local:11434",
		"https://ollama.example":      "https://ollama.example",
		"mac-mini.local:11434":        "http://mac-mini.local:11434",
		"192.168.1.50:11434":          "http://192.168.1.50:11434",
		"  mac-mini.local  ":          "http://mac-mini.local:11434",
		"http://mac-mini.local":       "http://mac-mini.local:11434",
		"":                            "",
	}
	for in, want := range cases {
		if got := normalizeHost(in); got != want {
			t.Fatalf("normalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunFirstRunAcceptsHostURLAtChoice(t *testing.T) {
	t.Setenv("VIBE_CODER_RAM_GB", "12")
	recommended := recommendedModel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"0.8.0"}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen3.5:4b"}]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{"model":"qwen3.5:4b","capabilities":["tools"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tmp := t.TempDir()
	cfg := &config.Config{
		ConfigDir:  tmp,
		ConfigFile: filepath.Join(tmp, "vibe-coder.env"),
	}

	// Paste the URL directly at the host choice prompt (no "c" needed), then
	// accept the recommended main model.
	in := bytes.NewBufferString(strings.Join([]string{
		srv.URL,
		"",
	}, "\n") + "\n")
	out := &bytes.Buffer{}

	if err := RunFirstRun(context.Background(), cfg, "test", in, out); err != nil {
		t.Fatalf("RunFirstRun: %v", err)
	}
	if cfg.OllamaHost != srv.URL {
		t.Fatalf("host mismatch: got %q", cfg.OllamaHost)
	}
	if cfg.Model != recommended {
		t.Fatalf("model mismatch: got %q, want %q", cfg.Model, recommended)
	}
}

func TestRunFirstRunCustomMainModelPulls(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	pulled := make([]string, 0, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"0.8.0"}`))
		case "/api/tags":
			// No tool-capable models: the custom route should still work.
			_, _ = w.Write([]byte(`{"models":[{"name":"tiny","capabilities":["vision"]}]}`))
		case "/api/pull":
			data, _ := io.ReadAll(r.Body)
			if strings.Contains(string(data), `"model":"main-custom:7b"`) {
				mu.Lock()
				pulled = append(pulled, "main-custom:7b")
				mu.Unlock()
			}
			_, _ = w.Write([]byte(`{"status":"done"}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tmp := t.TempDir()
	cfg := &config.Config{
		ConfigDir:  tmp,
		ConfigFile: filepath.Join(tmp, "vibe-coder.env"),
	}

	in := bytes.NewBufferString(strings.Join([]string{
		"c",              // manual host
		srv.URL,          // host value
		"c",              // custom main model
		"main-custom:7b", // custom main model name
	}, "\n") + "\n")
	out := &bytes.Buffer{}

	if err := RunFirstRun(context.Background(), cfg, "test", in, out); err != nil {
		t.Fatalf("RunFirstRun: %v", err)
	}
	if cfg.Model != "main-custom:7b" {
		t.Fatalf("model mismatch: got %q", cfg.Model)
	}
	if cfg.SidecarModel != "" || cfg.DecisionModel != "" {
		t.Fatalf("first run must not bind other roles: sidecar=%q decision=%q", cfg.SidecarModel, cfg.DecisionModel)
	}

	mu.Lock()
	got := strings.Join(pulled, ",")
	mu.Unlock()
	if got != "main-custom:7b" {
		t.Fatalf("unexpected pull sequence: %q", got)
	}
}

func TestRunFirstRunSelectsMainFromListAndSavesRegistry(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"0.8.0"}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[` +
				`{"name":"main:7b","capabilities":["tools"]},` +
				`{"name":"side:3b","capabilities":["tools"]},` +
				`{"name":"decision:2b"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	tmp := t.TempDir()
	cfg := &config.Config{
		ConfigDir:  tmp,
		ConfigFile: filepath.Join(tmp, "vibe-coder.env"),
	}

	// Host is pasted at the choice prompt; the main model is picked by its
	// list number.
	in := bytes.NewBufferString(strings.Join([]string{
		srv.URL, // host
		"1",     // main: main:7b
	}, "\n") + "\n")
	out := &bytes.Buffer{}

	if err := RunFirstRun(context.Background(), cfg, "test", in, out); err != nil {
		t.Fatalf("RunFirstRun: %v", err)
	}
	if cfg.Model != "main:7b" {
		t.Fatalf("model mismatch: got %q", cfg.Model)
	}
	if cfg.SidecarModel != "" || cfg.DecisionModel != "" {
		t.Fatalf("first run must not bind other roles: sidecar=%q decision=%q", cfg.SidecarModel, cfg.DecisionModel)
	}

	data, err := os.ReadFile(cfg.ConfigFile)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	saved := string(data)
	for _, want := range []string{"ROLE_MAIN_MODEL=main:7b", "PROVIDER_OLLAMA_ENDPOINT=" + srv.URL} {
		if !strings.Contains(saved, want) {
			t.Fatalf("expected %q in saved config, got:\n%s", want, saved)
		}
	}
	for _, unwanted := range []string{"SIDECAR_MODEL", "DECISION_MODEL"} {
		if strings.Contains(saved, unwanted) {
			t.Fatalf("did not expect %q in saved config, got:\n%s", unwanted, saved)
		}
	}
}

func TestRunFirstRunInterruptReturnsErrInterrupted(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfg := &config.Config{
		ConfigDir:  tmp,
		ConfigFile: filepath.Join(tmp, "vibe-coder.env"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := RunFirstRun(ctx, cfg, "test", bytes.NewBufferString(""), &bytes.Buffer{})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", err)
	}
}
