package main

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
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
	if !strings.Contains(got, "Session started:") {
		t.Fatalf("expected startup banner in one-shot output, got: %q", got)
	}
	if !strings.Contains(got, "Model: llama3.2:3b") {
		t.Fatalf("expected model line in one-shot output, got: %q", got)
	}
	if !strings.Contains(got, "response") {
		t.Fatalf("expected one-shot model response text, got: %q", got)
	}
	if !strings.Contains(got, "responded in") {
		t.Fatalf("expected response duration line in one-shot output, got: %q", got)
	}
}

func TestStartupBanner(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:        "llama3.2:3b",
		SidecarModel: "",
		OllamaHost:   "http://localhost:11434",
	}
	out := startupBanner(cfg, "session-123", tui.Style{})
	if !strings.Contains(out, "vibe-coder") {
		t.Fatalf("missing app name banner: %q", out)
	}
	if !strings.Contains(out, "Session started: session-123") {
		t.Fatalf("missing session id: %q", out)
	}
	if !strings.Contains(out, "Model: llama3.2:3b") {
		t.Fatalf("missing model line: %q", out)
	}
	if !strings.Contains(out, "Sidecar:") || !strings.Contains(out, "SIDECAR_MODEL") {
		t.Fatalf("missing sidecar line: %q", out)
	}
	if !strings.Contains(out, "Ollama host: http://localhost:11434") {
		t.Fatalf("missing host line: %q", out)
	}
}

func TestExtractPersistDirective(t *testing.T) {
	t.Parallel()

	args, persist := extractPersistDirective([]string{
		"-model", "qwen3.5:9b",
		"-sidecar", "qwen3.5:4b",
		"-ollama-host", "http://192.168.1.50:11434",
		"/save",
	})
	if !persist {
		t.Fatal("expected persist directive to be detected")
	}
	if strings.Join(args, " ") != "-model qwen3.5:9b -sidecar qwen3.5:4b -ollama-host http://192.168.1.50:11434" {
		t.Fatalf("unexpected args after filter: %v", args)
	}
}

func TestShouldContinueInteractiveAfterPrompt(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Prompt: "hello", Interactive: true}
	if !shouldContinueInteractiveAfterPrompt(cfg, true, true) {
		t.Fatal("expected interactive continuation when prompt is set and both streams are TTY")
	}
	if shouldContinueInteractiveAfterPrompt(cfg, true, false) {
		t.Fatal("expected no continuation when stdout is not a TTY")
	}
	if shouldContinueInteractiveAfterPrompt(cfg, false, true) {
		t.Fatal("expected no continuation when stdin is not a TTY")
	}
	if shouldContinueInteractiveAfterPrompt(&config.Config{Prompt: "", Interactive: true}, true, true) {
		t.Fatal("expected no continuation with empty prompt")
	}
	if shouldContinueInteractiveAfterPrompt(&config.Config{Prompt: "hello", Interactive: false}, true, true) {
		t.Fatal("expected no continuation when interactive mode is disabled")
	}
	if shouldContinueInteractiveAfterPrompt(nil, true, true) {
		t.Fatal("expected no continuation with nil config")
	}
}

func TestPlanTaskFromSlash(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{in: "/plan", want: "", ok: false},
		{in: "/plan off", want: "", ok: false},
		{in: "/plan exit", want: "", ok: false},
		{in: "/plan cancel", want: "", ok: false},
		{in: "/plan revisar arquitectura", want: "revisar arquitectura", ok: true},
		{in: "   /plan   fix login   ", want: "fix login", ok: true},
		{in: "/approve", want: "", ok: false},
	}

	for _, tc := range cases {
		got, ok := planTaskFromSlash(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("planTaskFromSlash(%q) => (%q,%t), want (%q,%t)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
