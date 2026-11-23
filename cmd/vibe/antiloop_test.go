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
	fakeMissionStart = `<invoke name="MissionStart">{"goal":"keep doing busywork"}</invoke>`
	fakeNoopCall     = `<invoke name="Bash">{"command":"true"}</invoke>`
	fakeMissionDone  = `<invoke name="MissionComplete">{"summary":"call cap reached"}</invoke>`
	// hardChatCap bounds a broken guard so a regression cannot spin unbounded
	// in the test; the mission then completes (and the assertion fails).
	hardChatCap = 200
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
		n := f.calls.Add(1)
		if f.mode == "mixed-progress" {
			// Verify the corrective prompt actually reached the model, not just
			// the stored session: n=11 is the first model call after the nudged
			// continuation (n=1 start, n=2-4 first turn, n=5-7 stalled turn 1,
			// n=8-10 stalled turn 2). Later calls may carry the same message in
			// history, so only this call proves it was sent.
			var request ollama.ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			for _, message := range request.Messages {
				if strings.Contains(message.Content, "The last two turns produced no new progress") && n == 11 {
					f.nudgesOnWire.Add(1)
				}
			}
		}
		content := ""
		switch {
		case n > hardChatCap:
			content = fakeMissionDone
		case f.mode == "empty":
			if n == 1 {
				content = fakeMissionStart
			}
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
			if n == 1 {
				content = fakeMissionStart
			} else {
				// Alternate a stable read, a stable check and a plain-text turn
				// ender. Every (call, output) pair repeats from the second turn
				// on, so no single-turn guard can ever stop this.
				switch (n - 2) % 3 {
				case 0:
					content = `<invoke name="Bash">{"command":"cat evidence.txt"}</invoke>`
				case 1:
					content = `<invoke name="Bash">{"command":"sh verify.sh"}</invoke>`
				default:
					content = "continuing work"
				}
			}
		case f.mode == "productive-reads":
			if n == 1 {
				content = fakeMissionStart
			} else if n == 12 {
				content = `<invoke name="MissionComplete">{"summary":"Reviewed all evidence"}</invoke>`
			} else if n%2 == 0 {
				// Read-only commands whose output changes every time are real
				// evidence: each one advances the run, so the stall guard must
				// never fire no matter how many turns pass.
				content = fmt.Sprintf(`<invoke name="Bash">{"command":"printf 'finding %d'"}</invoke>`, n)
			} else {
				content = "continuing review"
			}
		case f.mode == "loop-inspect":
			if n == 1 {
				content = fakeMissionStart
			} else {
				// Different commands and outputs still perform no concrete work.
				content = fmt.Sprintf(`<invoke name="Bash">{"command":"printf 'check %d'"}</invoke>`, n)
			}
		default: // loop-tool
			if n == 1 {
				content = fakeMissionStart
			} else {
				content = fakeNoopCall
			}
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
	t.Helper()
	tmp := t.TempDir()
	// Bash inherits the process working directory, so keep every fixture and
	// state-changing fake command inside the test's temporary workspace.
	t.Chdir(tmp)
	cfg := &config.Config{
		Model:         "fake:latest",
		ContextWindow: 4096,
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

// TestRunPromptPausesRepeatedToolLoop covers the mission-loop pause: a model
// that declares a mission and then repeats the same no-op tool call must be
// stopped by the no-progress guard, not spin forever.
func TestRunPromptPausesRepeatedToolLoop(t *testing.T) {
	srv, fake := newFakeOllama(t, "loop-tool")
	ag, ui := newHTTPTestAgent(t, srv.URL)

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
	if calls := fake.calls.Load(); calls > 12 {
		t.Fatalf("no-progress guard fired too late: %d chat calls", calls)
	}
}

// Different read-only commands evade the identical-call guard, but they still
// cannot keep an autonomous mission busy for an hour without doing real work.
func TestRunPromptPausesInspectionOnlyLoop(t *testing.T) {
	srv, fake := newFakeOllama(t, "loop-inspect")
	ag, ui := newHTTPTestAgent(t, srv.URL)

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

// A mission that alternates different tool calls can still spin: every
// (call, output) pair repeats identically each turn, so neither the repeated-
// call guard nor the inspection guard fires. The cross-turn evidence tracker
// must count three turns with no new progress, nudge once, then pause.
func TestRunPromptPausesAcrossTurnsWithoutNewProgress(t *testing.T) {
	srv, fake := newFakeOllama(t, "mixed-progress")
	ag, ui, sess := newHTTPTestAgentWithSession(t, srv.URL)
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
	if calls := fake.calls.Load(); calls != 13 {
		t.Fatalf("expected exactly 13 chat calls (mission start + four 3-call turns), got %d", calls)
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

// The mirror of the stall guard: a mission doing productive read-only work —
// every command returns new output — must keep running past the three-turn
// boundary and end on its own MissionComplete, not on a pause.
func TestRunPromptContinuesWithNewReadOnlyEvidence(t *testing.T) {
	srv, fake := newFakeOllama(t, "productive-reads")
	ag, ui := newHTTPTestAgent(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runPrompt(ctx, ag, ui, "continue"); err != nil {
		t.Fatalf("runPrompt returned an error instead of completing: %v", err)
	}

	snap := ag.MissionSnapshot()
	if snap.Status != tools.MissionStatusCompleted {
		t.Fatalf("expected mission status %q, got %q (%q)", tools.MissionStatusCompleted, snap.Status, snap.Summary)
	}
	if snap.Summary != "Reviewed all evidence" {
		t.Fatalf("expected the model's own completion summary, got %q", snap.Summary)
	}
	if calls := fake.calls.Load(); calls != 12 {
		t.Fatalf("expected exactly 12 chat calls (5 productive continuation turns), got %d", calls)
	}
}

// TestRunPromptPausesEmptyResponses covers the other runaway mode: a mission
// that keeps producing empty replies must also pause (via the empty/stall
// guards) instead of looping.
func TestRunPromptPausesEmptyResponses(t *testing.T) {
	srv, fake := newFakeOllama(t, "empty")
	ag, ui := newHTTPTestAgent(t, srv.URL)

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
