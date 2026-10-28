package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// newTurnAgent builds an agent with the default tool registry over a temp dir,
// which is enough to seed a checklist and register a probe tool.
func newTurnAgent(t *testing.T, client ollama.Client) (*Agent, *fakeUI, *tools.Registry) {
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
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: true, Cwd: tmp})
	sess := session.New(cfg)
	ui := &fakeUI{}
	return New(cfg, client, reg, perm, sess, ui), ui, reg
}

// seedChecklist writes the given items into the TodoWrite store.
func seedChecklist(t *testing.T, reg *tools.Registry, items []tools.TodoItem) {
	t.Helper()
	tw, ok := reg.Get("TodoWrite").(*tools.TodoWriteTool)
	if !ok {
		t.Fatal("TodoWrite tool missing from the registry")
	}
	tw.Store().Replace(items)
}

// A turn that stops must say what is still open: that is the difference between
// "the task is done" and "the agent gave up somewhere in the middle".
func TestPendingWorkSummaryNamesOpenChecklistItems(t *testing.T) {
	ag, _, reg := newTurnAgent(t, &fakeClient{})
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "unzip the archive", Status: tools.TodoStatusInProgress},
		{ID: "2", Content: "read Follow me.txt", Status: tools.TodoStatusPending},
		{ID: "3", Content: "write the report", Status: tools.TodoStatusPending},
		{ID: "4", Content: "already done", Status: tools.TodoStatusCompleted},
	})

	got := ag.PendingWorkSummary()
	for _, want := range []string{"3 TODO items pending", `in progress "unzip the archive"`, `pending "read Follow me.txt"`, "and 1 more", `send "continue" to resume`} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary %q is missing %q", got, want)
		}
	}
	if strings.Contains(got, "already done") {
		t.Fatalf("summary must not name completed items: %q", got)
	}
}

func TestPendingWorkSummaryIsEmptyWhenNothingIsOpen(t *testing.T) {
	ag, _, reg := newTurnAgent(t, &fakeClient{})
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "done", Status: tools.TodoStatusCompleted},
	})
	if got := ag.PendingWorkSummary(); got != "" {
		t.Fatalf("expected no summary for a finished checklist, got %q", got)
	}
}

// A Ctrl+C used to be replayed as an answer: the cancelled stream returns
// "[Cancelled by user]", that counts as visible text, and a pending checklist
// sends the loop back for another iteration until the step cap. The user pressed
// Ctrl+C again and again while nothing appeared to stop.
func TestCancelledTurnStopsOnTheSpot(t *testing.T) {
	client := &sequenceClient{replies: []string{cancelledReply, cancelledReply, cancelledReply}}
	ag, _, reg := newTurnAgent(t, client)
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "keep going", Status: tools.TodoStatusPending},
	})

	err := ag.Run(context.Background(), "do the work")
	if !IsTurnCancelledErr(err) {
		t.Fatalf("expected a cancelled turn, got %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("a cancelled turn must not ask the model again: model turns=%d, want 1", client.calls)
	}
	if IsIterationCapErr(err) {
		t.Fatal("a cancelled turn must not surface as the step cap")
	}
}

// The same guard for a context cancelled before the first request: nothing to
// replay, and the caller still gets the cancellation signal.
func TestCancelledContextEndsTheTurn(t *testing.T) {
	ag, _, _ := newTurnAgent(t, &fakeClient{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := ag.Run(ctx, "do the work")
	if !IsTurnCancelledErr(err) {
		t.Fatalf("expected a cancelled turn, got %v", err)
	}
}

// probeTool records when it ran so a test can compare that moment with the tool
// card the UI was told to open.
type probeTool struct {
	ui *orderUI
}

func (probeTool) Name() string        { return "Probe" }
func (probeTool) Description() string { return "test probe" }
func (probeTool) Schema() tools.Schema {
	return tools.Schema{Type: "function", Function: tools.FunctionSchema{Name: "Probe"}}
}
func (p probeTool) Execute(context.Context, map[string]any) tools.Result {
	p.ui.events = append(p.ui.events, "exec")
	return tools.Result{Output: "probe ok"}
}

// orderUI records the order of the calls that matter for the silence bug.
type orderUI struct {
	fakeUI
	events []string
}

func (u *orderUI) ShowToolCall(name string, _ map[string]any) {
	u.events = append(u.events, "card:"+name)
}
func (u *orderUI) ShowToolResult(name string, _ string, _ bool, _ map[string]any) {
	u.events = append(u.events, "result:"+name)
}

// The tool card must open before the tool runs: a Bash that spends two minutes
// timing out prints nothing at all otherwise, which is what made the agent look
// frozen right after its answer.
func TestToolCardOpensBeforeTheToolRuns(t *testing.T) {
	ag, _, reg := newTurnAgent(t, &fakeClient{})
	ui := &orderUI{}
	ag.ui = ui
	reg.Register(probeTool{ui: ui})

	tool := reg.Get("Probe")
	if tool == nil {
		t.Fatal("probe tool was not registered")
	}
	if _, _, err := ag.executeTool(context.Background(), tool, "Probe", map[string]any{}, toolExecutionMode{}); err != nil {
		t.Fatalf("probe tool failed: %v", err)
	}

	want := []string{"card:Probe", "exec", "result:Probe"}
	if strings.Join(ui.events, ",") != strings.Join(want, ",") {
		t.Fatalf("tool card order = %v, want %v", ui.events, want)
	}
}

// The cancellation signal must never be mistaken for a hard failure, otherwise
// the REPL prints "error: ..." for the user's own Ctrl+C.
func TestCancelledTurnIsNotAnError(t *testing.T) {
	if !IsTurnCancelledErr(ErrTurnCancelled) {
		t.Fatal("ErrTurnCancelled must be recognised")
	}
	if IsTurnCancelledErr(errors.New("boom")) {
		t.Fatal("an unrelated error must not look like a cancellation")
	}
	if IsNoProgressErr(ErrTurnCancelled) || IsIterationCapErr(ErrTurnCancelled) {
		t.Fatal("a cancellation must not be reported as another stop reason")
	}
}
