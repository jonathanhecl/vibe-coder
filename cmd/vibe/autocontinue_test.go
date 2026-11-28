package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// newAutoContinueAgent builds the real agent around a scripted client and
// returns the pieces finishTurn needs.
func newAutoContinueAgent(t *testing.T, client ollama.Client, auto bool) (*agent.Agent, *tools.TodoWriteTool, *config.Config, *session.Session, tui.UI) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "fake:latest",
		ContextWindow: 4096,
		Temperature:   0.2,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
		StateDir:      filepath.Join(tmp, "state"),
		AutoContinue:  auto,
	}
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	tw, _ := reg.Get("TodoWrite").(*tools.TodoWriteTool)
	perm := permissions.NewManager(&config.Config{YesMode: true})
	sess := session.New(cfg)
	ui := tui.NewPlain()
	t.Cleanup(ui.Stop)
	return agent.New(cfg, client, reg, perm, sess, ui), tw, cfg, sess, ui
}

func seedPendingTodo(t *testing.T, tw *tools.TodoWriteTool) {
	t.Helper()
	res := tw.Execute(context.Background(), map[string]any{
		"todos": []any{
			map[string]any{"id": "step-1", "content": "Finish the migration", "status": "pending"},
		},
	})
	if res.IsError {
		t.Fatalf("failed to seed todo store: %s", res.Output)
	}
}

// Only stops a continuation can pick up stay eligible; everything that needs a
// human goes back to the prompt.
func TestAutoContinueEligibility(t *testing.T) {
	ag, tw, _, _, _ := newAutoContinueAgent(t, emptyRetryClient{}, true)

	if autoContinueEligible(ag, nil) {
		t.Fatal("a finished turn with nothing pending must not continue")
	}

	seedPendingTodo(t, tw)
	if !autoContinueEligible(ag, nil) {
		t.Fatal("a pending checklist is continuable")
	}

	ag.EnterPlanMode()
	if autoContinueEligible(ag, nil) {
		t.Fatal("plan mode is a conversation with the user, not unattended work")
	}
	ag.ExitPlanMode()

	ag.StartMission("ship the migration")
	ag.BlockActiveMission("needs user input")
	if autoContinueEligible(ag, nil) {
		t.Fatal("a paused goal asked for the user and must not be overridden")
	}
}

// A step-cap stop is progress, not a failure: it continues when a checkpoint,
// pending work or real tool activity can be resumed.
func TestAutoContinueEligibleOnIterationCap(t *testing.T) {
	ag, tw, _, _, _ := newAutoContinueAgent(t, emptyRetryClient{}, true)
	capErr := fmt.Errorf("iteration cap reached (50): this turn used all of its steps without finishing. Nothing was lost: press Enter to continue")
	if !agent.IsIterationCapErr(capErr) {
		t.Fatal("fixture is not an iteration-cap error")
	}
	if autoContinueEligible(ag, capErr) {
		t.Fatal("an idle capped turn with nothing pending or resumable must not continue")
	}
	seedPendingTodo(t, tw)
	if !autoContinueEligible(ag, capErr) {
		t.Fatal("a capped turn with pending work is continuable")
	}

	if autoContinueEligible(ag, agent.ErrTurnCancelled) {
		t.Fatal("Ctrl+C must always stop the loop")
	}
	if autoContinueEligible(ag, fmt.Errorf("network unreachable")) {
		t.Fatal("a hard failure must reach the user, not loop")
	}
}

// A continuation that produces no progress is not worth another request: the
// loop hands control back after two idle turns instead of asking the same model
// the same thing again.
func TestFinishTurnAutoContinuePausesWhenIdle(t *testing.T) {
	ag, tw, cfg, sess, ui := newAutoContinueAgent(t, emptyRetryClient{}, true)
	seedPendingTodo(t, tw)

	runs := 0
	err := finishTurnWith(context.Background(), cfg, sess, ag, ui, nil, func() error {
		runs++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runs != autoContinueIdleTurns {
		t.Fatalf("continuations=%d, want %d idle turns", runs, autoContinueIdleTurns)
	}
	if !cfg.AutoContinue {
		t.Fatal("the idle guard pauses the loop, it does not turn the mode off")
	}
}

// Without /autocontinue nothing runs on its own.
func TestFinishTurnWithoutAutoContinueReturnsToPrompt(t *testing.T) {
	ag, tw, cfg, sess, ui := newAutoContinueAgent(t, emptyRetryClient{}, false)
	seedPendingTodo(t, tw)

	runs := 0
	if err := finishTurnWith(context.Background(), cfg, sess, ag, ui, nil, func() error {
		runs++
		return nil
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runs != 0 {
		t.Fatalf("continuations=%d, want 0 when auto-continue is off", runs)
	}
}

// A model that never finishes cannot loop forever: the turn cap stops the
// prompt and turns the mode off so control really comes back.
func TestFinishTurnAutoContinueStopsAtTurnCap(t *testing.T) {
	prev := autoContinueTurnCap
	autoContinueTurnCap = 3
	t.Cleanup(func() { autoContinueTurnCap = prev })

	ag, tw, cfg, sess, ui := newAutoContinueAgent(t, emptyRetryClient{}, true)
	seedPendingTodo(t, tw)

	// Each turn spends its step budget again: productive enough to continue,
	// never finished, so only the cap can stop the prompt.
	stepCap := fmt.Errorf("iteration cap reached (50): this turn used all of its steps without finishing")
	runs := 0
	err := finishTurnWith(context.Background(), cfg, sess, ag, ui, nil, func() error {
		runs++
		return stepCap
	})
	if !agent.IsIterationCapErr(err) {
		t.Fatalf("expected the step-cap stop to reach the caller, got %v", err)
	}
	if runs != autoContinueTurnCap {
		t.Fatalf("continuations=%d, want the cap %d", runs, autoContinueTurnCap)
	}
	if cfg.AutoContinue {
		t.Fatal("hitting the cap must turn the mode off so the prompt returns")
	}
}
