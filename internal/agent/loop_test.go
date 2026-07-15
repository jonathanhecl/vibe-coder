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

type sequenceClient struct {
	fakeClient
	replies []string
	calls   int
}

func (c *sequenceClient) Chat(context.Context, ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	if c.calls >= len(c.replies) {
		return nil, context.Canceled
	}
	reply := c.replies[c.calls]
	c.calls++
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Delta: reply, Done: true}
	close(ch)
	return ch, nil
}

func TestFileEditCompletionNote(t *testing.T) {
	note := fileEditCompletionNote("Write", map[string]any{"file_path": "scripts/run_comfyui.bat"})
	if !strings.Contains(note, "Treat this step as completed") {
		t.Fatalf("expected completion anchor note, got %q", note)
	}
	if !strings.Contains(note, "scripts/run_comfyui.bat") {
		t.Fatalf("expected file path in note, got %q", note)
	}
	if got := fileEditCompletionNote("Read", map[string]any{"file_path": "a.txt"}); got != "" {
		t.Fatalf("expected empty note for non-edit tool, got %q", got)
	}
}

func (fakeClient) Tags(context.Context) ([]ollama.Model, error) { return nil, nil }
func (fakeClient) Version(context.Context) (string, error)      { return "0.0.0", nil }
func (fakeClient) ChatSync(context.Context, ollama.ChatRequest) (ollama.ChatResponse, error) {
	return ollama.ChatResponse{Content: "summary"}, nil
}
func (fakeClient) Pull(context.Context, string, func(ollama.PullEvent)) error { return nil }
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
func (f *fakeUI) SetPlanMode(bool)             {}
func (f *fakeUI) StreamAssistant(string)       {}
func (f *fakeUI) EndAssistant()                {}
func (f *fakeUI) StreamThinking(string)        {}
func (f *fakeUI) EndThinking()                 {}
func (f *fakeUI) StartWaiting(string)          {}
func (f *fakeUI) StopWaiting()                 {}
func (f *fakeUI) ShowToolCall(string, map[string]any) {
	f.calls++
}
func (f *fakeUI) ShowToolResult(string, string, bool, map[string]any) {}
func (f *fakeUI) ShowTodos([]tui.TodoItem)                            {}
func (f *fakeUI) AskPermission(string, map[string]any) tui.Decision {
	return tui.DecisionAllowOnce
}
func (f *fakeUI) GetInput(string) (string, error) { return "", nil }
func (f *fakeUI) Stop()                           {}
func (f *fakeUI) CollapseAssistantOutput()        {}

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

func TestRunStoresAssistantToolRequestBeforeObservation(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.ToSlash(filepath.Join(tmp, "auto.py"))
	if err := os.WriteFile(filePath, []byte("print('hello')\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg := &config.Config{
		Model:         "test-model",
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
	client := &sequenceClient{replies: []string{
		`<invoke name="Read">{"file_path":"` + filePath + `"}</invoke>`,
		"The file prints hello.",
	}}
	ag := New(cfg, client, reg, perm, sess, ui)

	if err := ag.Run(context.Background(), "analyze auto.py"); err != nil {
		t.Fatalf("run agent: %v", err)
	}
	msgs := sess.Messages()
	if len(msgs) != 4 {
		t.Fatalf("expected user, assistant tool request, tool result, assistant final; got %d messages: %#v", len(msgs), msgs)
	}
	if msgs[1].Role != "assistant" || !strings.Contains(msgs[1].Content, `<invoke name="Read">`) {
		t.Fatalf("expected assistant tool request at message 2, got %#v", msgs[1])
	}
	if msgs[2].Role != "user" || !strings.Contains(msgs[2].Content, "[tool_result name=Read]") {
		t.Fatalf("expected Read observation at message 3, got %#v", msgs[2])
	}
	if msgs[3].Role != "assistant" || msgs[3].Content != "The file prints hello." {
		t.Fatalf("expected final assistant response at message 4, got %#v", msgs[3])
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

func TestPlanModeWriteGuard(t *testing.T) {
	tmp := t.TempDir()
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
	ag.EnterPlanMode()

	if ag.isWriteAllowedInPlan(map[string]any{"file_path": filepath.Join(tmp, "notes.txt")}) {
		t.Fatalf("write outside .vibe-coder/plans should be blocked in plan mode")
	}
	allowed := filepath.Join(tmp, ".vibe-coder", "plans", "a.txt")
	if !ag.isWriteAllowedInPlan(map[string]any{"file_path": allowed}) {
		t.Fatalf("write inside .vibe-coder/plans should be allowed in plan mode")
	}
}
