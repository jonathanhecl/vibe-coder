package onboarding

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestRunFirstRunDefaultPathUsesInstalledRecommendedWithoutPull(t *testing.T) {
	t.Parallel()

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
		ConfigDir:    tmp,
		ConfigFile:   filepath.Join(tmp, "config.env"),
		OllamaHost:   defaultOllamaHost,
		Model:        "",
		SidecarModel: "",
	}

	in := bytes.NewBufferString(strings.Join([]string{
		"c",     // manual host
		srv.URL, // host value
		"",      // primary model: recommended
		"",      // sidecar: disabled
	}, "\n") + "\n")
	out := &bytes.Buffer{}

	if err := RunFirstRun(context.Background(), cfg, "test", in, out); err != nil {
		t.Fatalf("RunFirstRun: %v", err)
	}
	if cfg.OllamaHost != srv.URL {
		t.Fatalf("host mismatch: got %q", cfg.OllamaHost)
	}
	if cfg.Model != defaultModel {
		t.Fatalf("model mismatch: got %q", cfg.Model)
	}
	if !cfg.SidecarDisabled {
		t.Fatal("expected sidecar disabled by default")
	}
	if cfg.SidecarModel != "" {
		t.Fatalf("expected empty sidecar model, got %q", cfg.SidecarModel)
	}

	mu.Lock()
	gotPulls := pullCalls
	mu.Unlock()
	if gotPulls != 0 {
		t.Fatalf("expected no pull for installed recommended model, got %d", gotPulls)
	}
	if showCalls == 0 {
		t.Fatal("expected /api/show calls to resolve per-model tool support")
	}
	rendered := out.String()
	if strings.Contains(rendered, "Installed models from host") {
		t.Fatalf("did not expect full installed model list in output, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Selectable tool-capable models (1)") {
		t.Fatalf("expected selectable model list in output, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "[1] qwen3.5:4b") {
		t.Fatalf("expected selectable model entry for qwen3.5:4b, got:\n%s", rendered)
	}
}

func TestRunFirstRunCustomModelAndCustomSidecarPullsBoth(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	pulled := make([]string, 0, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"0.8.0"}`))
		case "/api/tags":
			// No tool-capable models: custom route should still work.
			_, _ = w.Write([]byte(`{"models":[{"name":"tiny","capabilities":["vision"]}]}`))
		case "/api/pull":
			data, _ := io.ReadAll(r.Body)
			payload := string(data)
			switch {
			case strings.Contains(payload, `"model":"main-custom:7b"`):
				mu.Lock()
				pulled = append(pulled, "main-custom:7b")
				mu.Unlock()
			case strings.Contains(payload, `"model":"sidecar-custom:3b"`):
				mu.Lock()
				pulled = append(pulled, "sidecar-custom:3b")
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
		ConfigFile: filepath.Join(tmp, "config.env"),
	}

	in := bytes.NewBufferString(strings.Join([]string{
		"c",                 // manual host
		srv.URL,             // host value
		"c",                 // custom primary
		"main-custom:7b",    // custom primary model
		"c",                 // custom sidecar
		"sidecar-custom:3b", // custom sidecar model
	}, "\n") + "\n")
	out := &bytes.Buffer{}

	if err := RunFirstRun(context.Background(), cfg, "test", in, out); err != nil {
		t.Fatalf("RunFirstRun: %v", err)
	}
	if cfg.Model != "main-custom:7b" {
		t.Fatalf("model mismatch: got %q", cfg.Model)
	}
	if cfg.SidecarModel != "sidecar-custom:3b" {
		t.Fatalf("sidecar mismatch: got %q", cfg.SidecarModel)
	}
	if cfg.SidecarDisabled {
		t.Fatal("expected sidecar enabled for custom sidecar")
	}

	mu.Lock()
	got := strings.Join(pulled, ",")
	mu.Unlock()
	if got != "main-custom:7b,sidecar-custom:3b" {
		t.Fatalf("unexpected pull sequence: %q", got)
	}
}

func TestRunFirstRunInterruptReturnsErrInterrupted(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfg := &config.Config{
		ConfigDir:  tmp,
		ConfigFile: filepath.Join(tmp, "config.env"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := RunFirstRun(ctx, cfg, "test", bytes.NewBufferString(""), &bytes.Buffer{})
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("expected ErrInterrupted, got %v", err)
	}
}
