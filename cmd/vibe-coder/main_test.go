package main

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestVersionFlagSmoke(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("go", "run", "./cmd/vibe-coder", "--version")
	cmd.Dir = filepath.Clean("../..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run --version failed: %v\noutput: %s", err, string(out))
	}

	got := strings.TrimSpace(string(out))
	if got != "vibe-coder dev" {
		t.Fatalf("unexpected version output: %q", got)
	}
}

func TestOneShotPromptSmoke(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/chat":
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = w.Write([]byte(`{"message":{"content":"response"},"done":true}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cmd := exec.Command("go", "run", "./cmd/vibe-coder",
		"--ollama-host", srv.URL,
		"-m", "llama3.2:3b",
		"-p", "say hi",
	)
	cmd.Dir = filepath.Clean("../..")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run -p failed: %v\noutput: %s", err, string(out))
	}

	got := strings.TrimSpace(string(out))
	if got != "response" {
		t.Fatalf("unexpected one-shot output: %q", got)
	}
}

func TestStartupBanner(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:        "llama3.2:3b",
		SidecarModel: "",
		OllamaHost:   "http://localhost:11434",
	}
	out := startupBanner(cfg, "session-123")
	if !strings.Contains(out, "vibe-coder") {
		t.Fatalf("missing app name banner: %q", out)
	}
	if !strings.Contains(out, "Session started: session-123") {
		t.Fatalf("missing session id: %q", out)
	}
	if !strings.Contains(out, "Model: llama3.2:3b") {
		t.Fatalf("missing model line: %q", out)
	}
	if !strings.Contains(out, "Sidecar: (disabled)") {
		t.Fatalf("missing sidecar line: %q", out)
	}
	if !strings.Contains(out, "Ollama host: http://localhost:11434") {
		t.Fatalf("missing host line: %q", out)
	}
}

func TestExtractPersistDirective(t *testing.T) {
	t.Parallel()

	args, persist := extractPersistDirective([]string{
		"-model", "qwen2.5-coder:7b",
		"-sidecar", "llama3.2:3b",
		"-ollama-host", "http://192.168.1.50:11434",
		"/save",
	})
	if !persist {
		t.Fatal("expected persist directive to be detected")
	}
	if strings.Join(args, " ") != "-model qwen2.5-coder:7b -sidecar llama3.2:3b -ollama-host http://192.168.1.50:11434" {
		t.Fatalf("unexpected args after filter: %v", args)
	}
}
