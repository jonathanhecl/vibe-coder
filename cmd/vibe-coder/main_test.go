package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

type emptyRetryClient struct{}

func (emptyRetryClient) Tags(context.Context) ([]ollama.Model, error) { return nil, nil }
func (emptyRetryClient) Version(context.Context) (string, error)      { return "0.0.0", nil }
func (emptyRetryClient) Pull(context.Context, string, func(ollama.PullEvent)) error {
	return nil
}
func (emptyRetryClient) ChatSync(context.Context, ollama.ChatRequest) (ollama.ChatResponse, error) {
	return ollama.ChatResponse{}, nil
}
func (emptyRetryClient) Chat(context.Context, ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Thinking: "plan", Done: true}
	close(ch)
	return ch, nil
}

func newMainTestAgent(t *testing.T, client ollama.Client) (*agent.Agent, *tools.TodoWriteTool) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 4096,
		MaxTokens:     128,
		Temperature:   0.2,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	tw, _ := reg.Get("TodoWrite").(*tools.TodoWriteTool)
	perm := permissions.NewManager(&config.Config{YesMode: true})
	ui := tui.NewPlain()
	t.Cleanup(ui.Stop)
	return agent.New(cfg, client, reg, perm, sess, ui), tw
}

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

func TestRunAgentWithEmptyRetryStopsAfterCap(t *testing.T) {
	ag, tw := newMainTestAgent(t, emptyRetryClient{})
	if tw == nil {
		t.Fatal("TodoWrite tool not available")
	}
	seed := tw.Execute(context.Background(), map[string]any{
		"todos": []any{
			map[string]any{"id": "step-1", "content": "Create startup script", "status": "pending"},
		},
	})
	if seed.IsError {
		t.Fatalf("failed to seed todo store: %s", seed.Output)
	}

	err := runAgentWithEmptyRetry(context.Background(), ag, tui.NewPlain(), "continue")
	if err == nil {
		t.Fatal("expected empty-response retry wrapper to return an error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "empty assistant response") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRetryContextBuilderAddsMarkers(t *testing.T) {
	ag, _ := newMainTestAgent(t, emptyRetryClient{})
	input := ag.BuildEmptyResponseRetryInput("do task", true)
	if !strings.Contains(input, "[retry_context]") {
		t.Fatalf("expected retry_context marker, got %q", input)
	}
	if !strings.Contains(input, "State appears unchanged") {
		t.Fatalf("expected repeated-state hint, got %q", input)
	}
	if !strings.Contains(input, "do task") {
		t.Fatalf("expected original input retained, got %q", input)
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
		"-save",
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
