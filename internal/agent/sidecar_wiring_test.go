package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/sidecar"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// stubClient is the smallest fake we need for wiring tests; only ChatSync
// is exercised through the sidecar.Pool.
type stubClient struct {
	calls int32
	reply string
	err   error
}

func (s *stubClient) Chat(ctx context.Context, req ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	return nil, errors.New("not used")
}
func (s *stubClient) ChatSync(ctx context.Context, req ollama.ChatRequest) (ollama.ChatResponse, error) {
	atomic.AddInt32(&s.calls, 1)
	if s.err != nil {
		return ollama.ChatResponse{}, s.err
	}
	return ollama.ChatResponse{Content: s.reply}, nil
}
func (s *stubClient) Tags(ctx context.Context) ([]ollama.Model, error) {
	return nil, errors.New("not used")
}
func (s *stubClient) Version(ctx context.Context) (string, error) {
	return "", errors.New("not used")
}
func (s *stubClient) Pull(ctx context.Context, model string, p func(ollama.PullEvent)) error {
	return errors.New("not used")
}

// recordingUI captures the last tool result hint so wiring tests can
// assert what the user actually sees.
type recordingUI struct {
	results []string
}

func (r *recordingUI) StartESCMonitor(func()) error        { return nil }
func (r *recordingUI) StopESCMonitor()                     {}
func (r *recordingUI) SetPlanMode(bool)                    {}
func (r *recordingUI) StreamAssistant(string)              {}
func (r *recordingUI) EndAssistant()                       {}
func (r *recordingUI) StreamThinking(string)               {}
func (r *recordingUI) EndThinking()                        {}
func (r *recordingUI) StartWaiting(string)                 {}
func (r *recordingUI) StopWaiting()                        {}
func (r *recordingUI) ShowToolCall(string, map[string]any) {}
func (r *recordingUI) ShowToolResult(name, output string, isError bool, _ map[string]any) {
	r.results = append(r.results, output)
}
func (r *recordingUI) ShowTodos([]tui.TodoItem) {}
func (r *recordingUI) AskPermission(string, map[string]any) tui.Decision {
	return tui.DecisionDeny
}
func (r *recordingUI) GetInput(string) (string, error) { return "", nil }
func (r *recordingUI) Stop()                           {}
func (r *recordingUI) CollapseAssistantOutput()        {}

func newTestAgent(t *testing.T, sideModel string, client ollama.Client) (*Agent, *recordingUI) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:          tmp,
		SessionsDir:  filepath.Join(tmp, "_sessions"),
		Model:        "main",
		SidecarModel: sideModel,
	}
	if err := os.MkdirAll(cfg.SessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	ui := &recordingUI{}
	a := New(cfg, client, tools.NewRegistry(), permissions.NewManager(cfg), session.New(cfg), ui)
	return a, ui
}

func TestRecordToolObservationSummarisesNonReadOutputWhenSidecarEnabled(t *testing.T) {
	t.Parallel()
	sc := &stubClient{reply: "- big file\n- many lines"}
	a, _ := newTestAgent(t, "qwen3.5:9b", sc)
	a.SetSidecar(sidecar.New(a.cfg, sc, sidecar.WithSummariseThreshold(10)))

	a.recordToolObservation(context.Background(), "Bash", strings.Repeat("payload\n", 500), "")

	msgs := a.sess.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message in session, got %d", len(msgs))
	}
	body := msgs[0].Content
	if !strings.Contains(body, "[sidecar-summary tool=Bash") {
		t.Fatalf("expected summarised observation, got: %q", body)
	}
	if !strings.Contains(body, "big file") {
		t.Fatalf("expected summary content, got: %q", body)
	}
	if atomic.LoadInt32(&sc.calls) != 1 {
		t.Fatalf("expected 1 sidecar call, got %d", sc.calls)
	}
}

func TestRecordToolObservationKeepsLargeReadOutputVerbatim(t *testing.T) {
	t.Parallel()
	sc := &stubClient{reply: "should not be used"}
	a, _ := newTestAgent(t, "qwen3.5:9b", sc)
	a.SetSidecar(sidecar.New(a.cfg, sc, sidecar.WithSummariseThreshold(10)))

	body := strings.Repeat("source-line\n", 500)
	a.recordToolObservation(context.Background(), "Read", body, "")

	msgs := a.sess.Messages()
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, body) {
		t.Fatalf("expected large Read output to remain verbatim")
	}
	if atomic.LoadInt32(&sc.calls) != 0 {
		t.Fatalf("Read output should not be sent to sidecar")
	}
}

func TestRecordToolObservationSkipsSummariseForSmallOutput(t *testing.T) {
	t.Parallel()
	sc := &stubClient{reply: "should not be used"}
	a, _ := newTestAgent(t, "qwen3.5:9b", sc)
	a.SetSidecar(sidecar.New(a.cfg, sc, sidecar.WithSummariseThreshold(10_000)))

	a.recordToolObservation(context.Background(), "Read", "short", "")

	msgs := a.sess.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "[tool_result name=Read]") {
		t.Fatalf("expected raw tool_result envelope, got: %q", msgs[0].Content)
	}
	if atomic.LoadInt32(&sc.calls) != 0 {
		t.Fatalf("sidecar should not be called for small output")
	}
}

func TestRecordToolObservationFallsBackOnSidecarError(t *testing.T) {
	t.Parallel()
	sc := &stubClient{err: errors.New("boom")}
	a, _ := newTestAgent(t, "qwen3.5:9b", sc)
	a.SetSidecar(sidecar.New(a.cfg, sc, sidecar.WithSummariseThreshold(10)))

	a.recordToolObservation(context.Background(), "Bash", strings.Repeat("z", 4096), "")

	msgs := a.sess.Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "[tool_result name=Bash]") {
		t.Fatalf("failed sidecar should keep raw observation, got: %q", msgs[0].Content)
	}
}

func TestRescuePathParamUsesSidecarOnAmbiguity(t *testing.T) {
	t.Parallel()
	sc := &stubClient{reply: "PICK: 2"}
	a, _ := newTestAgent(t, "qwen3.5:9b", sc)
	a.SetSidecar(sidecar.New(a.cfg, sc))

	// Seed two candidates with the same basename, in different dirs.
	tmp := a.cfg.Cwd
	mkfile := func(rel string) string {
		full := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("package x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return full
	}
	a.paths.add(mkfile("alpha/config.go"))
	a.paths.add(mkfile("beta/config.go"))

	params := map[string]any{"file_path": "config.go"}
	a.currentGoal = "edit the beta config"
	a.rescuePathParam(context.Background(), "Read", params)

	got, _ := params["file_path"].(string)
	want := filepath.Join(tmp, "beta", "config.go")
	// Sidecar picked candidate #2 in lexicographic order, which is "beta".
	if got != want {
		t.Fatalf("expected disambiguation to pick %q, got %q", want, got)
	}
	if atomic.LoadInt32(&sc.calls) != 1 {
		t.Fatalf("expected exactly 1 sidecar call, got %d", sc.calls)
	}
}

func TestRescuePathParamSkipsSidecarWhenSinglyResolvable(t *testing.T) {
	t.Parallel()
	sc := &stubClient{reply: "should not be invoked"}
	a, _ := newTestAgent(t, "qwen3.5:9b", sc)
	a.SetSidecar(sidecar.New(a.cfg, sc))

	tmp := a.cfg.Cwd
	full := filepath.Join(tmp, "AGENTS.md")
	if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	params := map[string]any{"file_path": "AGENTS.md"}
	a.rescuePathParam(context.Background(), "Read", params)

	got, _ := params["file_path"].(string)
	if got != full {
		t.Fatalf("expected cwd resolve to %q, got %q", full, got)
	}
	if atomic.LoadInt32(&sc.calls) != 0 {
		t.Fatalf("sidecar should not be called when cwd resolves cleanly")
	}
}

// Sanity check that the public Pool surface stays callable through the
// Agent — guards against accidental rename of SetSidecar.
func TestAgentSetSidecarIsPublic(t *testing.T) {
	t.Parallel()
	a, _ := newTestAgent(t, "", &stubClient{})
	a.SetSidecar(sidecar.New(a.cfg, &stubClient{}))
	if a.side == nil {
		t.Fatal("SetSidecar did not store the pool")
	}
	_ = fmt.Sprintf("%v", a.side.Enabled())
}
