package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	mode  string
	calls atomic.Int64
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
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, _ *http.Request) {
		n := f.calls.Add(1)
		content := ""
		switch {
		case n > hardChatCap:
			content = fakeMissionDone
		case f.mode == "empty":
			if n == 1 {
				content = fakeMissionStart
			}
		case f.mode == "stuck":
			// The same call every time: the no-progress guard is the only thing
			// that can stop this turn.
			content = fakeNoopCall
		case f.mode == "busy":
			// A different call every time: real work, so only the per-turn step
			// budget can end this turn.
			content = fmt.Sprintf(`<invoke name="Bash">{"command":"echo step-%d"}</invoke>`, n)
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
	t.Helper()
	tmp := t.TempDir()
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
	return agent.New(cfg, client, reg, perm, sess, ui), ui
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
	if !strings.Contains(err.Error(), `send "continue" to resume`) {
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
	if !strings.Contains(err.Error(), `send "continue" to resume`) {
		t.Errorf("the step cap does not say how to resume: %v", err)
	}
}
