package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func newMissionAgent(t *testing.T) *Agent {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "test",
		ContextWindow: 32768,
		MaxTokens:     128,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
		StateDir:      filepath.Join(tmp, "state"),
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	return New(cfg, fakeClient{}, reg, perm, sess, &fakeUI{})
}

func TestIsIterationCapErr(t *testing.T) {
	t.Parallel()
	if !IsIterationCapErr(fmt.Errorf("iteration cap reached (50)")) {
		t.Fatal("expected iteration cap error to be recognized")
	}
	if IsIterationCapErr(nil) {
		t.Fatal("nil is not an iteration cap error")
	}
	if IsIterationCapErr(fmt.Errorf("some other error")) {
		t.Fatal("unrelated error must not match")
	}
}

func TestPendingTodoCount(t *testing.T) {
	ag := newMissionAgent(t)
	ag.todoWriteTool().Store().Replace([]tools.TodoItem{
		{ID: "1", Content: "pending", Status: tools.TodoStatusPending},
		{ID: "2", Content: "done", Status: tools.TodoStatusCompleted},
		{ID: "3", Content: "active", Status: tools.TodoStatusInProgress},
		{ID: "4", Content: "dropped", Status: tools.TodoStatusCancelled},
	})
	if got := ag.PendingTodoCount(); got != 2 {
		t.Fatalf("expected 2 pending items, got %d", got)
	}
}

func TestMissionToolsAreRegisteredAndDriveLifecycle(t *testing.T) {
	ag := newMissionAgent(t)
	if ag.MissionActive() {
		t.Fatal("no mission should be active initially")
	}
	start := ag.reg.Get("MissionStart")
	complete := ag.reg.Get("MissionComplete")
	if start == nil || complete == nil {
		t.Fatal("mission tools must be registered by agent.New")
	}

	if res := start.Execute(context.Background(), map[string]any{"goal": "process the dataset"}); res.IsError {
		t.Fatalf("MissionStart: %s", res.Output)
	}
	if !ag.MissionActive() {
		t.Fatal("mission should be active after MissionStart")
	}

	prompt := ag.MissionContinuationPrompt()
	if !strings.HasPrefix(prompt, missionPromptPrefix) {
		t.Fatalf("continuation prompt must carry the mission prefix, got %q", prompt)
	}
	if got := ag.MissionSnapshot().Turns; got != 1 {
		t.Fatalf("expected 1 continuation turn, got %d", got)
	}

	if res := complete.Execute(context.Background(), map[string]any{"summary": "done"}); res.IsError {
		t.Fatalf("MissionComplete: %s", res.Output)
	}
	if ag.MissionActive() {
		t.Fatal("mission should be inactive after MissionComplete")
	}
}

func TestMissionWorkStateRoundTrip(t *testing.T) {
	ag := newMissionAgent(t)
	ag.reg.Get("MissionStart").Execute(context.Background(), map[string]any{"goal": "long job"})
	ag.MissionContinuationPrompt()

	raw := ag.captureWorkState()
	if len(raw) == 0 {
		t.Fatal("expected non-empty work state with an active mission")
	}

	ag.BlockActiveMission("interrupted")
	ag.applyWorkState(raw)

	m := ag.MissionSnapshot()
	if m.Status != tools.MissionStatusActive || m.Goal != "long job" || m.Turns != 1 {
		t.Fatalf("unexpected restored mission: %#v", m)
	}
}

func TestMissionPromptBlockAdvertisesGoalAndInstructions(t *testing.T) {
	ag := newMissionAgent(t)
	ag.reg.Get("MissionStart").Execute(context.Background(), map[string]any{"goal": "paint 200 icons"})
	block := ag.missionPromptBlock()
	if !strings.Contains(block, "paint 200 icons") {
		t.Fatalf("mission block should include the goal, got %q", block)
	}
	if !strings.Contains(block, "MissionComplete") || !strings.Contains(block, "MissionBlocked") {
		t.Fatalf("mission block should explain how to end, got %q", block)
	}
}

func TestTurnToolCallsCounter(t *testing.T) {
	ag := newMissionAgent(t)
	ag.resetTurnToolCalls()
	if ag.ToolCallsThisTurn() != 0 {
		t.Fatalf("expected 0 tool calls after reset, got %d", ag.ToolCallsThisTurn())
	}
	ag.noteToolExecuted()
	ag.noteToolExecuted()
	if got := ag.ToolCallsThisTurn(); got != 2 {
		t.Fatalf("expected 2 tool calls, got %d", got)
	}
}

func TestMissionMakesPermissionsUnattended(t *testing.T) {
	ag := newMissionAgent(t)
	if ag.perm.Unattended() {
		t.Fatal("permissions should not be unattended before a mission")
	}
	ag.reg.Get("MissionStart").Execute(context.Background(), map[string]any{"goal": "run unattended"})

	file := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ag.executeTool(context.Background(), ag.reg.Get("Read"), "Read", map[string]any{"file_path": file}, toolExecutionMode{}); err != nil {
		t.Fatalf("executeTool: %v", err)
	}
	if !ag.perm.Unattended() {
		t.Fatal("expected unattended permissions while a mission is active")
	}
}

func TestWorkStateCaptureApplyRoundTrip(t *testing.T) {
	ag := newMissionAgent(t)
	ag.todoWriteTool().Store().Replace([]tools.TodoItem{
		{ID: "1", Content: "step one", Status: tools.TodoStatusPending},
	})

	raw := ag.captureWorkState()
	if len(raw) == 0 {
		t.Fatal("expected non-empty work state")
	}
	ag.todoWriteTool().Store().Reset()
	if ag.PendingTodoCount() != 0 {
		t.Fatal("expected reset checklist to be empty")
	}
	ag.applyWorkState(raw)
	if got := ag.PendingTodoCount(); got != 1 {
		t.Fatalf("expected restored checklist with 1 pending item, got %d", got)
	}

	ag.todoWriteTool().Store().Reset()
	if blob := ag.captureWorkState(); blob != nil {
		t.Fatalf("expected nil work state when empty, got %s", blob)
	}
}
