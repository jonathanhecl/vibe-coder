package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

type fakeClient struct{}

func (fakeClient) Tags(context.Context) ([]ollama.Model, error) { return nil, nil }
func (fakeClient) Version(context.Context) (string, error)      { return "0.0.0", nil }
func (fakeClient) Chat(_ context.Context, _ ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Delta: "ok", Done: true}
	close(ch)
	return ch, nil
}

type fakeUI struct {
	calls int
}

func (f *fakeUI) StartESCMonitor(func()) error { return nil }
func (f *fakeUI) StopESCMonitor()              {}
func (f *fakeUI) StreamAssistant(string)       {}
func (f *fakeUI) EndAssistant()                {}
func (f *fakeUI) ShowToolCall(string, map[string]any) {
	f.calls++
}
func (f *fakeUI) ShowToolResult(string, string, bool) {}
func (f *fakeUI) AskPermission(string, map[string]any) tui.Decision {
	return tui.DecisionAllowOnce
}

func TestRunGlobOnce(t *testing.T) {
	tmp := t.TempDir()
	internalDir := filepath.Join(tmp, "internal")
	if err := os.MkdirAll(internalDir, 0o755); err != nil {
		t.Fatalf("mkdir internal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(internalDir, "a.go"), []byte("package internal"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Chdir(tmp)

	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		MaxTokens:     128,
		Temperature:   0.2,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	ui := &fakeUI{}
	ag := New(cfg, fakeClient{}, reg, perm, sess, ui)

	err := ag.Run(context.Background(), "list .go files in ./internal using Glob")
	if err != nil {
		t.Fatalf("run agent: %v", err)
	}
	if ui.calls != 1 {
		t.Fatalf("expected exactly one tool call, got %d", ui.calls)
	}
	if sess.MessageCount() < 2 {
		t.Fatalf("expected session messages to be appended")
	}
}

func TestInferGlob(t *testing.T) {
	name, params, ok := inferSingleToolCall("list .go files in ./internal using Glob")
	if !ok || name != "Glob" {
		t.Fatalf("expected glob inference")
	}
	if !strings.Contains(params["pattern"].(string), "*.go") {
		t.Fatalf("unexpected pattern: %v", params["pattern"])
	}
}

func TestDetectParallelTasks(t *testing.T) {
	t.Parallel()
	tasks, ok := detectParallelTasks("1. summarize repo\n2. list TODOs")
	if !ok || len(tasks) != 2 {
		t.Fatalf("expected parallel tasks from numbered input, got ok=%t len=%d", ok, len(tasks))
	}
}
