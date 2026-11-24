package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/sidecar"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

type sidecarNoCallClient struct {
	fakeClient
	chatSyncCalls int
}

func (c *sidecarNoCallClient) ChatSync(context.Context, ollama.ChatRequest) (ollama.ChatResponse, error) {
	c.chatSyncCalls++
	return ollama.ChatResponse{Content: "condensed"}, nil
}

func TestNewUserPromptNotHijackedByStalePendingTodos(t *testing.T) {
	client := &captureChatClient{replies: []string{
		"go1.24",
	}}
	ag, _, reg := newTurnAgent(t, client)
	ag.sess.AddUser("finish the old refactor")
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "edit stale.go", Status: tools.TodoStatusPending},
		{ID: "2", Content: "run stale tests", Status: tools.TodoStatusPending},
	})
	ag.mu.Lock()
	ag.currentGoal = "finish the old refactor"
	ag.mu.Unlock()
	ag.PersistWorkState()

	if err := ag.Run(context.Background(), "what Go version is this?"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("model calls = %d, want 1 (stale TODOs must not hijack new request)", client.calls)
	}
	for _, m := range client.requests[0].Messages {
		if strings.Contains(m.Content, "Continue top to bottom") || strings.Contains(m.Content, "edit stale.go") {
			t.Fatalf("stale TODO progress leaked onto wire during unrelated request: %q", m.Content)
		}
	}
	if !ag.CanResume() {
		t.Fatal("expected paused checklist to remain resumable via Enter")
	}
	if got := ag.resumeSnapshot().Goal; got != "finish the old refactor" {
		t.Fatalf("paused resume goal = %q, want %q", got, "finish the old refactor")
	}
}

func TestExplicitContinueUnpausesChecklistAndBlockedMission(t *testing.T) {
	t.Run("typed continue", func(t *testing.T) {
		client := &captureChatClient{replies: []string{
			`<invoke name="TodoWrite">{"todos":[{"id":"1","content":"apply patch","status":"completed"}]}</invoke>`,
			"patch applied",
		}}
		ag, _, reg := newTurnAgent(t, client)
		ag.sess.AddUser("apply the patch")
		ag.StartMission("apply the patch")
		seedChecklist(t, reg, []tools.TodoItem{
			{ID: "1", Content: "apply patch", Status: tools.TodoStatusPending},
		})
		ag.PersistWorkState()

		ag.PauseRestoredWork()
		if ag.MissionActive() {
			t.Fatal("expected restored mission to be paused before explicit continuation")
		}

		// A fresh unrelated request must neither reactivate the mission nor run old TODOs.
		sideClient := &captureChatClient{replies: []string{"side answer"}}
		ag.client = sideClient
		if err := ag.Run(context.Background(), "what time is it?"); err != nil {
			t.Fatalf("side request failed: %v", err)
		}
		if ag.MissionActive() {
			t.Fatal("unrelated request must not reactivate paused mission")
		}
		if sideClient.calls != 1 {
			t.Fatalf("side request calls = %d, want 1", sideClient.calls)
		}

		// Explicit "continue" reactivates both the checklist and the mission.
		ag.client = client
		if err := ag.Run(context.Background(), "continue"); err != nil {
			t.Fatalf("continue failed: %v", err)
		}
		if !ag.MissionActive() {
			t.Fatal("explicit continue should reactivate paused mission")
		}
		if ag.PendingTodoCount() != 0 {
			t.Fatalf("expected TODO to complete after continue, got %d pending", ag.PendingTodoCount())
		}
	})

	t.Run("RunContinuation", func(t *testing.T) {
		client := &captureChatClient{replies: []string{
			`<invoke name="TodoWrite">{"todos":[{"id":"1","content":"finish task","status":"completed"}]}</invoke>`,
			"all done",
		}}
		ag, _, reg := newTurnAgent(t, client)
		ag.sess.AddUser("finish the migration")
		ag.StartMission("finish the migration")
		seedChecklist(t, reg, []tools.TodoItem{
			{ID: "1", Content: "finish task", Status: tools.TodoStatusPending},
		})
		ag.PersistWorkState()
		ag.PauseRestoredWork()

		if err := ag.RunContinuation(context.Background()); err != nil {
			t.Fatalf("RunContinuation failed: %v", err)
		}
		if !ag.MissionActive() {
			t.Fatal("RunContinuation should reactivate paused mission")
		}
		if ag.PendingTodoCount() != 0 {
			t.Fatalf("expected TODO to complete after RunContinuation, got %d pending", ag.PendingTodoCount())
		}
	})
}

func TestTodoWriteInTurnActivatesChecklistEnforcement(t *testing.T) {
	client := &captureChatClient{replies: []string{
		`<invoke name="TodoWrite">{"todos":[{"id":"1","content":"write output.txt","status":"in_progress"}]}</invoke>`,
		"still thinking about it",
		`<invoke name="TodoWrite">{"todos":[{"id":"1","content":"write output.txt","status":"completed"}]}</invoke>`,
		"wrote output.txt",
	}}
	ag, _, reg := newTurnAgent(t, client)
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "stale step", Status: tools.TodoStatusPending},
	})

	if err := ag.Run(context.Background(), "create output.txt"); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if client.calls != 4 {
		t.Fatalf("expected checklist written in-turn to enforce completion before stopping; got %d calls", client.calls)
	}
}

func TestAskUserQuestionNeverMaskedAndCarriesConfirmedChoice(t *testing.T) {
	t.Run("excluded from masking", func(t *testing.T) {
		ag, sess := newWireTestAgent(t, 4096, 2)
		longChoice := `{"q1":"2"}` + "\n\n[assistant-hints]\nUser confirmed choice (do not re-ask):\n- Download mirror?: [2] Mirror B\n" + strings.Repeat(" ", 420) + "[/assistant-hints]"
		sess.AddAssistantToolCalls("", []ollama.MessageToolCall{{
			Function: ollama.MessageToolCallFunction{Name: "AskUserQuestion", Arguments: map[string]any{"prompt": "Download mirror?"}},
		}})
		sess.AddToolResult("AskUserQuestion", longChoice)
		for i := 0; i < 12; i++ {
			addNativeToolTurn(sess, strings.Repeat("r", 900))
		}
		hist := sess.MessagesReadOnly()
		for _, idx := range toolTurnStarts(hist) {
			if idx == 0 {
				t.Fatal("AskUserQuestion turn must not count toward toolTurnStarts")
			}
		}
		msgs, _ := ag.buildOllamaMessages(context.Background(), "SYS")
		foundChoice := false
		for _, m := range msgs {
			if m.Role == "tool" && m.ToolName == "AskUserQuestion" {
				if strings.HasPrefix(m.Content, "[elided]") {
					t.Fatalf("AskUserQuestion result was elided: %q", m.Content)
				}
				if strings.Contains(m.Content, "Mirror B") {
					foundChoice = true
				}
			}
		}
		if !foundChoice {
			t.Fatal("expected AskUserQuestion choice to stay verbatim on wire")
		}
		masked, _ := maskToolResults(hist, len(hist))
		for _, m := range masked {
			if m.ToolName == "AskUserQuestion" && strings.HasPrefix(m.Content, "[elided]") {
				t.Fatalf("maskToolResults elided AskUserQuestion even directly: %q", m.Content)
			}
		}
	})

	t.Run("stored with hints and exempt from sidecar condensation", func(t *testing.T) {
		ag, _, _ := newTurnAgent(t, &fakeClient{})
		ag.cfg.SidecarModel = "sidecar"
		scClient := &sidecarNoCallClient{}
		pool := sidecar.New(ag.cfg, scClient)
		ag.SetSidecar(pool)
		var out bytes.Buffer
		ag.reg.Register(tools.NewAskUserQuestionToolWithIO(strings.NewReader("2\n"), &out))
		ask := ag.reg.Get("AskUserQuestion")
		res, _, err := ag.executeTool(context.Background(), ask, "AskUserQuestion", map[string]any{
			"prompt":  "Which download source?",
			"options": []any{"Slow source", map[string]any{"label": "Fast mirror", "description": strings.Repeat("CDN HTTP/2 ", 300)}},
		}, toolExecutionMode{structured: true})
		if err != nil {
			t.Fatalf("executeTool: %v", err)
		}
		if scClient.chatSyncCalls != 0 {
			t.Fatalf("expected AskUserQuestion to skip sidecar condensation, got %d ChatSync calls", scClient.chatSyncCalls)
		}
		var parsed map[string]string
		if err := json.Unmarshal([]byte(res.Output), &parsed); err != nil || parsed["q1"] != "2" {
			t.Fatalf("expected raw JSON answer {\"q1\":\"2\"}, got %q (err=%v)", res.Output, err)
		}
		msgs := ag.sess.MessagesReadOnly()
		last := msgs[len(msgs)-1]
		if !strings.Contains(last.Content, "User confirmed choice (do not re-ask):") ||
			!strings.Contains(last.Content, "Fast mirror") ||
			!strings.Contains(last.Content, "Which download source?") {
			t.Fatalf("session observation missing confirmed choice details: %q", last.Content)
		}
	})
}

func TestRuntimeStateNoteAnchorsFollowUpGoalEvenWithActiveMissionAndNotes(t *testing.T) {
	ag, _, _ := newTurnAgent(t, &fakeClient{})
	ag.StartMission("download dataset")
	ag.mu.Lock()
	ag.currentGoal = "download the lightweight JSON mirror only"
	ag.mu.Unlock()

	notesTool := ag.reg.Get("SessionNotes")
	if notesTool == nil {
		t.Fatal("SessionNotes missing")
	}
	if _, _, err := ag.executeTool(context.Background(), notesTool, "SessionNotes", map[string]any{
		"action":  "upsert",
		"id":      "options",
		"kind":    "decision",
		"content": "Initial mirrors: A, B, C",
	}, toolExecutionMode{structured: true}); err != nil {
		t.Fatalf("upsert note: %v", err)
	}

	note := ag.runtimeStateNote()
	notesIdx := strings.Index(note, "# Session Working Notes")
	goalIdx := strings.Index(note, "# Current user goal")
	if notesIdx < 0 || goalIdx < 0 {
		t.Fatalf("expected both notes and current user goal in runtimeStateNote, got:\n%s", note)
	}
	if goalIdx < notesIdx {
		t.Fatalf("expected # Current user goal after # Session Working Notes, got:\n%s", note)
	}
	if !strings.Contains(note, "download the lightweight JSON mirror only") {
		t.Fatalf("expected follow-up choice in goal anchor, got:\n%s", note)
	}
}
