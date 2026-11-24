package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

const (
	fakeNoopCall = `<invoke name="Bash">{"command":"true"}</invoke>`
	// hardChatCap bounds a broken guard so a regression cannot spin unbounded
	// in the test; the test then hits the cap and fails assertions.
	hardChatCap = 400
)

// fakeOllama is an in-process Ollama stub that speaks just enough of the HTTP
// API and returns a scripted reply for every chat turn. It lets these tests
// exercise the real client/agent/mission-loop stack without a model.
type fakeOllama struct {
	mode         string
	calls        atomic.Int64
	nudgesOnWire atomic.Int64
}

func newFakeOllama(t *testing.T, mode string) (*httptest.Server, *fakeOllama) {
	t.Helper()
	f := &fakeOllama{mode: mode}
	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"version":"0.0.0-fake"}`)
	})
	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"models":[{"name":"fake:latest","model":"fake:latest","capabilities":["completion"]}]}`)
	})
	mux.HandleFunc("/api/show", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"model":"fake:latest","capabilities":["completion"]}`)
	})
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		var request ollama.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !request.Stream {
			writeJSON(w, `{"model":"fake:latest","message":{"role":"assistant","content":"summary"},"done":true}`)
			return
		}
		n := f.calls.Add(1)
		if f.mode == "mixed-progress" {
			// Verify the corrective prompt actually reached the model, not just
			// the stored session: Turn 1 runs 50 steps (n=1..50, records initial
			// sh verify.sh evidence), Turn 2 stalls (n=51..100, stalled=1),
			// Turn 3 stalls (n=101..150, stalled=2 -> nudge on n=151!).
			for _, message := range request.Messages {
				if strings.Contains(message.Content, "The last two turns produced no new progress") && n == 151 {
					f.nudgesOnWire.Add(1)
				}
			}
		}
		content := ""
		switch {
		case n > hardChatCap:
			content = "call cap reached"
		case f.mode == "empty":
			// Return empty replies until stalled/empty guard pauses the goal.
		case f.mode == "ok":
			content = "done"
		case f.mode == "stuck":
			// The same call every time: the no-progress guard is the only thing
			// that can stop this turn.
			content = fakeNoopCall
		case f.mode == "busy":
			// A different state-changing call every time: real work, so only the
			// per-turn step budget can end this turn.
			content = fmt.Sprintf(`<invoke name="Bash">{"command":"printf x > .busy-%d"}</invoke>`, n)
		case f.mode == "mixed-progress":
			// Alternate a stable read and a stable check every step so
			// neither the repeated-call guard nor the inspection guard fires,
			// exercising cross-turn stall detection across MaxIterations turns.
			if n%2 == 1 {
				content = `<invoke name="Bash">{"command":"cat evidence.txt"}</invoke>`
			} else {
				content = `<invoke name="Bash">{"command":"sh verify.sh"}</invoke>`
			}
		case f.mode == "productive-reads":
			// Read-only commands whose output changes every time still perform
			// no concrete work, so the inspection guard pauses the goal.
			content = fmt.Sprintf(`<invoke name="Bash">{"command":"printf 'finding %d'"}</invoke>`, n)
		case f.mode == "loop-inspect":
			// Different commands and outputs still perform no concrete work.
			content = fmt.Sprintf(`<invoke name="Bash">{"command":"printf 'check %d'"}</invoke>`, n)
		default: // loop-tool
			content = fakeNoopCall
		}
		payload, _ := json.Marshal(map[string]any{
			"model":   "fake:latest",
			"message": map[string]any{"role": "assistant", "content": content},
			"done":    true,
		})
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write(append(payload, '\n'))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, f
}

// newHTTPTestAgent wires the real Ollama HTTP client into an agent so the
// streaming NDJSON path is exercised end to end.
func newHTTPTestAgent(t *testing.T, host string) (*agent.Agent, tui.UI) {
	ag, ui, _ := newHTTPTestAgentWithSession(t, host)
	return ag, ui
}

func newHTTPTestAgentWithSession(t *testing.T, host string) (*agent.Agent, tui.UI, *session.Session) {
	return newHTTPTestAgentWithContext(t, host, 4096)
}

func newHTTPTestAgentWithContext(t *testing.T, host string, contextWindow int) (*agent.Agent, tui.UI, *session.Session) {
	t.Helper()
	tmp := t.TempDir()
	// Bash inherits the process working directory, so keep every fixture and
	// state-changing fake command inside the test's temporary workspace.
	t.Chdir(tmp)
	cfg := &config.Config{
		Model:         "fake:latest",
		ContextWindow: contextWindow,
		MaxTokens:     128,
		Temperature:   0.2,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
		StateDir:      filepath.Join(tmp, "state"),
		// Force the XML fallback path explicitly (known, not tool-capable).
		ToolsKnown:     true,
		ToolsSupported: false,
	}
	client := ollama.NewHTTP(host)
	sess := session.New(cfg)
	sess.SetClient(client)
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	ui := tui.NewPlain()
	t.Cleanup(ui.Stop)
	return agent.New(cfg, client, reg, perm, sess, ui), ui, sess
}

// TestRunPromptPausesRepeatedToolLoop covers the autonomous-goal pause: an
// active goal that repeats the same no-op tool call must be stopped by the
// no-progress guard, not spin forever.
func TestRunPromptPausesRepeatedToolLoop(t *testing.T) {
	srv, fake := newFakeOllama(t, "loop-tool")
	ag, ui := newHTTPTestAgent(t, srv.URL)
	ag.StartMission("keep doing busywork")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runPrompt(ctx, ag, ui, "continue"); err != nil {
		t.Fatalf("runPrompt returned an error instead of pausing: %v", err)
	}

	snap := ag.MissionSnapshot()
	if snap.Status != tools.MissionStatusBlocked {
		t.Fatalf("expected mission status %q, got %q", tools.MissionStatusBlocked, snap.Status)
	}
	if !strings.Contains(strings.ToLower(snap.Summary), "repeated") {
		t.Fatalf("pause reason should mention the repeated tool call, got %q", snap.Summary)
	}
	if calls := fake.calls.Load(); calls > 10 {
		t.Fatalf("no-progress guard fired too late: %d chat calls", calls)
	}
}

// Different read-only commands evade the identical-call guard, but they still
// cannot keep an autonomous goal busy for an hour without doing real work.
func TestRunPromptPausesInspectionOnlyLoop(t *testing.T) {
	srv, fake := newFakeOllama(t, "loop-inspect")
	ag, ui := newHTTPTestAgent(t, srv.URL)
	ag.StartMission("keep inspecting")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runPrompt(ctx, ag, ui, "continue"); err != nil {
		t.Fatalf("runPrompt returned an error instead of pausing: %v", err)
	}

	snap := ag.MissionSnapshot()
	if snap.Status != tools.MissionStatusBlocked {
		t.Fatalf("expected mission status %q, got %q", tools.MissionStatusBlocked, snap.Status)
	}
	if !strings.Contains(strings.ToLower(snap.Summary), "inspection") {
		t.Fatalf("pause reason should identify the inspection-only loop, got %q", snap.Summary)
	}
	if calls := fake.calls.Load(); calls > 16 {
		t.Fatalf("inspection guard fired too late: %d chat calls", calls)
	}
}

// A goal that alternates different tool calls can still spin: every
// (call, output) pair repeats identically each turn, so neither the repeated-
// call guard nor the inspection guard fires. The cross-turn evidence tracker
// must count three turns with no new progress, nudge once, then pause.
func TestRunPromptPausesAcrossTurnsWithoutNewProgress(t *testing.T) {
	srv, fake := newFakeOllama(t, "mixed-progress")
	// Use a roomier window so compaction does not archive Turn 3's nudge
	// before the assertion inspects sess.MessagesReadOnly().
	ag, ui, sess := newHTTPTestAgentWithContext(t, srv.URL, 65536)
	ag.StartMission("finish task")
	if err := os.WriteFile("evidence.txt", []byte("fixed evidence\n"), 0o644); err != nil {
		t.Fatalf("write evidence.txt: %v", err)
	}
	if err := os.WriteFile("verify.sh", []byte("printf 'tests passed\\n'\n"), 0o644); err != nil {
		t.Fatalf("write verify.sh: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runPrompt(ctx, ag, ui, "continue"); err != nil {
		t.Fatalf("runPrompt returned an error instead of pausing: %v", err)
	}

	snap := ag.MissionSnapshot()
	if snap.Status != tools.MissionStatusBlocked {
		t.Fatalf("expected mission status %q, got %q", tools.MissionStatusBlocked, snap.Status)
	}
	if !strings.Contains(strings.ToLower(snap.Summary), "no new evidence") {
		t.Fatalf("pause reason should mention the missing new evidence, got %q", snap.Summary)
	}
	if calls := fake.calls.Load(); calls != 200 {
		t.Fatalf("expected exactly 200 chat calls (4x50 MaxIterations turns across stalled window), got %d", calls)
	}
	if !ag.CanResume() {
		t.Fatal("a stalled mission must stay resumable with Enter")
	}
	if state := sess.WorkState(); !strings.Contains(string(state), "no new evidence") {
		t.Fatalf("serialized work state should carry the stall reason, got %s", state)
	}
	nudges := 0
	for _, msg := range sess.MessagesReadOnly() {
		if strings.Contains(msg.Content, "The last two turns produced no new progress") {
			nudges++
		}
	}
	if nudges != 1 {
		t.Fatalf("expected exactly one corrective continuation prompt, got %d", nudges)
	}
	if got := fake.nudgesOnWire.Load(); got != 1 {
		t.Fatalf("expected the corrective prompt on the model wire exactly once, got %d", got)
	}
}

// Reads alone are never concrete progress — even when every command returns
// new output. An autonomous goal that only opens new references without writing
// must pause instead of looking busy forever.
func TestRunPromptContinuesWithNewReadOnlyEvidence(t *testing.T) {
	srv, fake := newFakeOllama(t, "productive-reads")
	ag, ui := newHTTPTestAgent(t, srv.URL)
	ag.StartMission("review and edit")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runPrompt(ctx, ag, ui, "continue"); err != nil {
		t.Fatalf("runPrompt returned an error instead of pausing: %v", err)
	}

	snap := ag.MissionSnapshot()
	if snap.Status != tools.MissionStatusBlocked {
		t.Fatalf("expected mission status %q, got %q (%q)", tools.MissionStatusBlocked, snap.Status, snap.Summary)
	}
	if calls := fake.calls.Load(); calls > 20 {
		t.Fatalf("read-only mission fired too late: %d chat calls", calls)
	}
}

// TestRunPromptAutoCompletesGoalWithoutMissionComplete verifies that an active
// autonomous goal finishes cleanly when the model replies in plain text with
// no pending TODO items remaining (no MissionComplete tool call needed).
func TestRunPromptAutoCompletesGoalWithoutMissionComplete(t *testing.T) {
	srv, fake := newFakeOllama(t, "ok")
	ag, ui := newHTTPTestAgent(t, srv.URL)
	ag.StartMission("answer concisely")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runPrompt(ctx, ag, ui, "answer concisely"); err != nil {
		t.Fatalf("runPrompt returned error: %v", err)
	}
	snap := ag.MissionSnapshot()
	if snap.Status != tools.MissionStatusCompleted {
		t.Fatalf("expected goal status %q, got %q", tools.MissionStatusCompleted, snap.Status)
	}
	if calls := fake.calls.Load(); calls != 1 {
		t.Fatalf("expected 1 chat call for direct goal completion, got %d", calls)
	}
}

// TestRunPromptPausesEmptyResponses covers the other runaway mode: an active
// goal that keeps producing empty replies must also pause (via the empty/stall
// guards) instead of looping.
func TestRunPromptPausesEmptyResponses(t *testing.T) {
	srv, fake := newFakeOllama(t, "empty")
	ag, ui := newHTTPTestAgent(t, srv.URL)
	ag.StartMission("produce an answer")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runPrompt(ctx, ag, ui, "continue"); err != nil {
		t.Fatalf("runPrompt returned an error instead of pausing: %v", err)
	}
	if snap := ag.MissionSnapshot(); snap.Status != tools.MissionStatusBlocked {
		t.Fatalf("expected mission status %q, got %q", tools.MissionStatusBlocked, snap.Status)
	}
	if calls := fake.calls.Load(); calls > hardChatCap/2 {
		t.Fatalf("empty-response guard fired too late: %d chat calls", calls)
	}
}

// A continuation turn must not store an empty user message: the checkpoint is
// runtime context, and Enter is only the explicit signal to start that turn.
func TestContinuationPromptStoresNoEmptyUserMessage(t *testing.T) {
	srv, _ := newFakeOllama(t, "ok")
	ag, ui, sess := newHTTPTestAgentWithSession(t, srv.URL)
	sess.AddUser("finish the migration")
	ag.StartMission("finish the migration")
	ag.BlockActiveMission("needs a decision")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runContinuationPrompt(ctx, ag, ui); err != nil {
		t.Fatalf("continuation failed: %v", err)
	}
	for _, msg := range sess.MessagesReadOnly() {
		if msg.Role == "user" && strings.TrimSpace(msg.Content) == "" {
			t.Fatal("continuation stored an empty user message")
		}
	}
}

// A plain turn (no mission) that gets stuck repeating one tool call used to end
// with a nil error: runPrompt swallowed the no-progress stop and the user was
// dropped back to the prompt with no answer and no reason. The stop must reach
// the caller so it can be reported.
func TestRunPromptReportsRepeatedToolLoopOutsideMission(t *testing.T) {
	srv, _ := newFakeOllama(t, "stuck")
	ag, ui := newHTTPTestAgent(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := runPrompt(ctx, ag, ui, "fix the file")
	if !agent.IsNoProgressErr(err) {
		t.Fatalf("runPrompt returned %v, want the repeated-call stop to reach the caller", err)
	}
	if !strings.Contains(err.Error(), "press Enter to continue") {
		t.Errorf("the stop does not say how to resume: %v", err)
	}
}

// The per-turn step cap was swallowed the same way: the turn vanished at step
// 50 with nothing printed. It must be reported, and it must say the session can
// continue.
func TestRunPromptReportsIterationCapOutsideMission(t *testing.T) {
	srv, _ := newFakeOllama(t, "busy")
	ag, ui := newHTTPTestAgent(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err := runPrompt(ctx, ag, ui, "keep working")
	if !agent.IsIterationCapErr(err) {
		t.Fatalf("runPrompt returned %v, want the step cap to reach the caller", err)
	}
	if !strings.Contains(err.Error(), "press Enter to continue") {
		t.Errorf("the step cap does not say how to resume: %v", err)
	}
}

func TestStartupAutoResumePausesRestoredMission(t *testing.T) {
	srv, fake := newFakeOllama(t, "ok")
	ag, ui, _ := newHTTPTestAgentWithSession(t, srv.URL)
	ag.StartMission("old background goal")
	ag.PersistWorkState()

	// Startup auto-resume pauses leftover missions before handling a fresh question.
	ag.PauseRestoredWork()
	if ag.MissionActive() {
		t.Fatal("expected restored mission to start paused")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runPrompt(ctx, ag, ui, "what is 2+2?"); err != nil {
		t.Fatalf("runPrompt failed: %v", err)
	}
	if calls := fake.calls.Load(); calls != 1 {
		t.Fatalf("expected 1 chat call on new question while old mission stays paused, got %d", calls)
	}
	if !ag.CanResume() {
		t.Fatal("expected paused mission to remain resumable via Enter")
	}
	if got := ag.MissionSnapshot().Goal; got != "old background goal" {
		t.Fatalf("paused mission goal mutated to %q", got)
	}
}

func TestGoalResumeWithContinuePreservesOriginalMissionGoal(t *testing.T) {
	srv, _ := newFakeOllama(t, "ok")
	ag, ui, sess := newHTTPTestAgentWithSession(t, srv.URL)
	ag.StartMission("finish the migration")
	ag.BlockActiveMission("Paused on session restore")

	cfg := &config.Config{
		Goal:   true,
		Prompt: "continue",
	}
	shouldCont, err := runInitialPrompt(context.Background(), cfg, ag, sess, ui, true, nil)
	if err != nil {
		t.Fatalf("runInitialPrompt failed: %v", err)
	}
	if shouldCont {
		t.Fatal("expected non-interactive one-shot continuation to stop after completion")
	}
	snap := ag.MissionSnapshot()
	if snap.Goal != "finish the migration" {
		t.Fatalf("mission goal overwritten by continue prompt: got %q", snap.Goal)
	}
	if snap.Status != tools.MissionStatusCompleted {
		t.Fatalf("expected completed mission, got %q", snap.Status)
	}
}
