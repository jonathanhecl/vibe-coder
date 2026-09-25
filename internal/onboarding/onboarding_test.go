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
		ConfigFile:   filepath.Join(tmp, "vibe-coder.env"),
		OllamaHost:   defaultOllamaHost,
		Model:        "",
		SidecarModel: "",
	}

	in := bytes.NewBufferString(strings.Join([]string{
		"c",     // manual host
		srv.URL, // host value
		"",      // primary model: recommended
		"",      // sidecar: disabled
		"",      // JEV Style: disabled
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
	if cfg.JevstyleModel != "" {
		t.Fatalf("expected empty JEV Style model, got %q", cfg.JevstyleModel)
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
	if strings.Contains(rendered, "Selectable tool-capable models") {
		t.Fatalf("did not expect a per-role model list, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Installed models (2)") {
		t.Fatalf("expected a single installed-model list, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "[1] qwen3.5:4b") || !strings.Contains(rendered, "[2] qwen3.5:1.7b") {
		t.Fatalf("expected the numbered model list, got:\n%s", rendered)
	}
	if got := strings.Count(rendered, "Installed models ("); got != 1 {
		t.Fatalf("expected the model list exactly once, got %d", got)
	}
}

func TestNormalizeHost(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"http://mac-mini.local:11434": "http://mac-mini.local:11434",
		"https://ollama.example":      "https://ollama.example",
		"mac-mini.local:11434":        "http://mac-mini.local:11434",
		"192.168.1.50:11434":          "http://192.168.1.50:11434",
		"  mac-mini.local  ":          "http://mac-mini.local",
		"":                            "",
	}
	for in, want := range cases {
		if got := normalizeHost(in); got != want {
			t.Fatalf("normalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunFirstRunAcceptsHostURLAtChoice(t *testing.T) {
	t.Parallel()

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

	// Paste the URL directly at the host choice prompt (no "c" needed),
	// then accept the recommended model and disable the optional roles.
	in := bytes.NewBufferString(strings.Join([]string{
		srv.URL,
		"",
		"",
		"",
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
		ConfigFile: filepath.Join(tmp, "vibe-coder.env"),
	}

	in := bytes.NewBufferString(strings.Join([]string{
		"c",                 // manual host
		srv.URL,             // host value
		"c",                 // custom primary
		"main-custom:7b",    // custom primary model
		"c",                 // custom sidecar
		"sidecar-custom:3b", // custom sidecar model
		"",                  // JEV Style: disabled
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

func TestRunFirstRunSelectsAllRolesFromSingleList(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			_, _ = w.Write([]byte(`{"version":"0.8.0"}`))
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[` +
				`{"name":"main:7b","capabilities":["tools"]},` +
				`{"name":"side:3b","capabilities":["tools"]},` +
				`{"name":"jev:2b"}]}`))
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

	// Host is pasted at the choice prompt; the three roles are picked by the
	// same list numbers (1, 2, 3) from the single listing.
	in := bytes.NewBufferString(strings.Join([]string{
		srv.URL, // host
		"1",     // primary: main:7b
		"2",     // sidecar: side:3b
		"3",     // JEV Style: jev:2b
	}, "\n") + "\n")
	out := &bytes.Buffer{}

	if err := RunFirstRun(context.Background(), cfg, "test", in, out); err != nil {
		t.Fatalf("RunFirstRun: %v", err)
	}
	if cfg.Model != "main:7b" {
		t.Fatalf("model mismatch: got %q", cfg.Model)
	}
	if cfg.SidecarModel != "side:3b" || cfg.SidecarDisabled {
		t.Fatalf("sidecar mismatch: got %q disabled=%v", cfg.SidecarModel, cfg.SidecarDisabled)
	}
	if cfg.JevstyleModel != "jev:2b" {
		t.Fatalf("jevstyle mismatch: got %q", cfg.JevstyleModel)
	}

	rendered := out.String()
	if got := strings.Count(rendered, "Installed models ("); got != 1 {
		t.Fatalf("expected the model list exactly once, got %d:\n%s", got, rendered)
	}
	if !strings.Contains(rendered, "JEV Style") {
		t.Fatalf("expected JEV Style in the summary, got:\n%s", rendered)
	}

	data, err := os.ReadFile(cfg.ConfigFile)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	for _, want := range []string{"MODEL=main:7b", "SIDECAR_MODEL=side:3b", "JEVSTYLE_MODEL=jev:2b"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("expected %q in saved config, got:\n%s", want, data)
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
