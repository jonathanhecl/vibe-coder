package main

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

