package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
	"github.com/jonathanhecl/vibe-coder/internal/watcher"
)

type coverageUI struct {
	decision tui.Decision
}

func (c *coverageUI) StartESCMonitor(func()) error { return nil }
func (c *coverageUI) StopESCMonitor()              {}
func (c *coverageUI) StreamAssistant(string)       {}
func (c *coverageUI) EndAssistant()                {}
func (c *coverageUI) StreamThinking(string)        {}
func (c *coverageUI) EndThinking()                 {}
func (c *coverageUI) StartWaiting(string)          {}
func (c *coverageUI) StopWaiting()                 {}
func (c *coverageUI) ShowToolCall(string, map[string]any) {
}
func (c *coverageUI) ShowToolResult(string, string, bool) {
}
func (c *coverageUI) ShowTodos([]tui.TodoItem) {}
func (c *coverageUI) AskPermission(string, map[string]any) tui.Decision {
	return c.decision
}

type coverageRAG struct{}

func (coverageRAG) QueryText(context.Context, string, int) (string, error) {
	return "[RAG Context]\nfile:chunk", nil
}

type coverageClient struct {
	chatCalls atomic.Int32
	pullCalls atomic.Int32
	reply     string
}

func (c *coverageClient) Tags(context.Context) ([]ollama.Model, error) { return nil, nil }
func (c *coverageClient) Version(context.Context) (string, error)      { return "0.0.0", nil }
func (c *coverageClient) Pull(context.Context, string, func(ollama.PullEvent)) error {
	c.pullCalls.Add(1)
	return nil
}
func (c *coverageClient) ChatSync(context.Context, ollama.ChatRequest) (ollama.ChatResponse, error) {
	return ollama.ChatResponse{Content: "ok"}, nil
}
func (c *coverageClient) Chat(context.Context, ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	call := c.chatCalls.Add(1)
	if c.reply == "" && call == 1 {
		return nil, errors.New("model not found")
	}
	ch := make(chan ollama.Chunk, 2)
	text := c.reply
	if text == "" {
		text = "done"
	}
	ch <- ollama.Chunk{Delta: text, Done: true}
	close(ch)
	return ch, nil
}

type cancelClient struct{}

func (cancelClient) Tags(context.Context) ([]ollama.Model, error) { return nil, nil }
func (cancelClient) Version(context.Context) (string, error)      { return "0.0.0", nil }
func (cancelClient) Pull(context.Context, string, func(ollama.PullEvent)) error {
	return nil
}
func (cancelClient) ChatSync(context.Context, ollama.ChatRequest) (ollama.ChatResponse, error) {
	return ollama.ChatResponse{}, nil
}
func (cancelClient) Chat(context.Context, ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Err: context.Canceled, Done: true}
	close(ch)
	return ch, nil
}

type errorStreamClient struct{}

func (errorStreamClient) Tags(context.Context) ([]ollama.Model, error) { return nil, nil }
func (errorStreamClient) Version(context.Context) (string, error)      { return "0.0.0", nil }
func (errorStreamClient) Pull(context.Context, string, func(ollama.PullEvent)) error {
	return errors.New("pull failed")
}
func (errorStreamClient) ChatSync(context.Context, ollama.ChatRequest) (ollama.ChatResponse, error) {
	return ollama.ChatResponse{}, nil
}
func (errorStreamClient) Chat(context.Context, ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Err: errors.New("stream failed"), Done: true}
	close(ch)
	return ch, nil
}

type pullProgressClient struct {
	coverageClient
}

func (p *pullProgressClient) Pull(ctx context.Context, model string, cb func(ollama.PullEvent)) error {
	if cb != nil {
		cb(ollama.PullEvent{Status: "pulling", Completed: 5, Total: 10})
		cb(ollama.PullEvent{Status: "done"})
	}
	p.pullCalls.Add(1)
	return nil
}

func newCoverageAgent(t *testing.T, client ollama.Client, decision tui.Decision, yes bool) *Agent {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 4096,
		MaxTokens:     128,
		Temperature:   0.2,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
		RAG:           true,
		RAGTopK:       3,
	}
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: yes})
	sess := session.New(cfg)
	return New(cfg, client, reg, perm, sess, &coverageUI{decision: decision})
}

func TestSettersAndSimpleHelpers(t *testing.T) {
	c := &coverageClient{}
	ag := newCoverageAgent(t, c, tui.DecisionAllowOnce, true)

	ag.SetRAG(coverageRAG{})
	w := watcher.New(ag.cfg.Cwd)
	defer w.Close()
	ag.SetWatcher(w)

	ag.EnterPlanMode()
	if !ag.InPlanMode() {
		t.Fatal("expected plan mode on")
	}
	ag.ExitPlanMode()
	if ag.InPlanMode() {
		t.Fatal("expected plan mode off")
	}
	if asString(123) != "" || asString("abc") != "abc" {
		t.Fatal("asString conversion mismatch")
	}
}

func TestTryAutoPullModelAndChatRetry(t *testing.T) {
	c := &coverageClient{}
	ag := newCoverageAgent(t, c, tui.DecisionAllowOnce, true)

	ok := ag.tryAutoPullModel(context.Background())
	if !ok || c.pullCalls.Load() != 1 {
		t.Fatalf("expected auto pull success, ok=%t pullCalls=%d", ok, c.pullCalls.Load())
	}

	reply, err := ag.chatOnce(context.Background(), "hello")
	if err != nil {
		t.Fatalf("chatOnce should recover after pull: %v", err)
	}
	if !strings.Contains(reply, "done") {
		t.Fatalf("unexpected reply: %q", reply)
	}
}

func TestTryAutoPullModelDeniedAndCancelledChat(t *testing.T) {
	denyAgent := newCoverageAgent(t, &coverageClient{}, tui.DecisionDeny, false)
	if denyAgent.tryAutoPullModel(context.Background()) {
		t.Fatal("expected auto pull denied")
	}

	cancelAgent := newCoverageAgent(t, cancelClient{}, tui.DecisionAllowOnce, true)
	reply, err := cancelAgent.chatOnce(context.Background(), "hello")
	if err != nil {
		t.Fatalf("chatOnce canceled should not error: %v", err)
	}
	if reply != "[Cancelled by user]" {
		t.Fatalf("unexpected cancel reply: %q", reply)
	}
}

func TestRunAddsRAGContextAndHandlesUnknownXMLTool(t *testing.T) {
	c := &coverageClient{reply: `<invoke name="MissingTool">{"x":1}</invoke>`}
	ag := newCoverageAgent(t, c, tui.DecisionAllowOnce, true)
	ag.SetRAG(coverageRAG{})
	if err := ag.Run(context.Background(), "just chat"); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	// user input + rag context + assistant output
	if ag.sess.MessageCount() < 3 {
		t.Fatalf("expected rag-enriched session, got %d messages", ag.sess.MessageCount())
	}
}

func TestRunInferredToolMissingAndXMLWriteBlockedInPlan(t *testing.T) {
	// Inferred tool missing branch.
	c1 := &coverageClient{reply: "unused"}
	ag1 := newCoverageAgent(t, c1, tui.DecisionAllowOnce, true)
	ag1.reg = tools.NewRegistry() // no tools registered
	if err := ag1.Run(context.Background(), "use glob to list files"); err == nil {
		t.Fatal("expected missing inferred tool error")
	}

	// XML write blocked by plan mode branch.
	c2 := &coverageClient{reply: `<invoke name="Write">{"file_path":"outside.txt","contents":"x"}</invoke>`}
	ag2 := newCoverageAgent(t, c2, tui.DecisionAllowOnce, true)
	ag2.EnterPlanMode()
	if err := ag2.Run(context.Background(), "chat"); err != nil {
		t.Fatalf("run with plan-mode block should not error: %v", err)
	}
}

func TestRunXMLPermissionDeniedAndParallelAgentsPath(t *testing.T) {
	// XML permission denied branch.
	c1 := &coverageClient{reply: `<invoke name="Write">{"file_path":"C:/tmp/a.txt","contents":"x"}</invoke>`}
	ag1 := newCoverageAgent(t, c1, tui.DecisionDeny, false)
	if err := ag1.Run(context.Background(), "chat"); err != nil {
		t.Fatalf("run with denied xml tool should not error: %v", err)
	}

	// ParallelAgents branch with registered tool.
	c2 := &coverageClient{reply: "subagent-output"}
	ag2 := newCoverageAgent(t, c2, tui.DecisionAllowOnce, true)
	sub := tools.NewSubAgentTool(ag2.cfg, c2)
	ag2.reg.Register(sub)
	ag2.reg.Register(tools.NewParallelAgentsTool(sub))
	if err := ag2.Run(context.Background(), "1. summarize\n2. list todos"); err != nil {
		t.Fatalf("parallel agents run failed: %v", err)
	}
}

func TestRunXMLWriteExecutesAndDetectParallelAndBranch(t *testing.T) {
	tmp := t.TempDir()
	c := &coverageClient{reply: `<invoke name="Write">{"file_path":"` + filepath.Join(tmp, "note.txt") + `","contents":"ok"}</invoke>`}
	ag := newCoverageAgent(t, c, tui.DecisionAllowOnce, true)
	if err := ag.Run(context.Background(), "chat"); err != nil {
		t.Fatalf("xml write run failed: %v", err)
	}

	tasks, ok := detectParallelTasks("summarize this and list TODOs")
	if !ok || len(tasks) != 2 {
		t.Fatalf("expected 'and' parallel detection, ok=%t tasks=%d", ok, len(tasks))
	}
}

func TestChatOnceErrorAndTryAutoPullFailure(t *testing.T) {
	ag := newCoverageAgent(t, errorStreamClient{}, tui.DecisionAllowOnce, true)
	if _, err := ag.chatOnce(context.Background(), "hello"); err == nil {
		t.Fatal("expected chatOnce to fail on repeated stream errors")
	}
	if ag.tryAutoPullModel(context.Background()) {
		t.Fatal("expected auto pull to fail when client.Pull errors")
	}
}

func TestTryAutoPullModelWithProgressCallback(t *testing.T) {
	c := &pullProgressClient{}
	ag := newCoverageAgent(t, c, tui.DecisionAllowOnce, true)
	if !ag.tryAutoPullModel(context.Background()) {
		t.Fatal("expected pull with progress to succeed")
	}
	if c.pullCalls.Load() != 1 {
		t.Fatalf("unexpected pull calls: %d", c.pullCalls.Load())
	}
}

func TestInferSingleToolCallMarkdownPattern(t *testing.T) {
	name, params, ok := inferSingleToolCall("use glob to list .md files in internal")
	if !ok || name != "Glob" {
		t.Fatalf("expected glob inference for markdown, got ok=%t name=%s", ok, name)
	}
	if params["pattern"] != "*.md" {
		t.Fatalf("expected markdown pattern, got %#v", params["pattern"])
	}
}

