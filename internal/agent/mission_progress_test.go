package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// The evidence window dedupes repeated (call, output) pairs across turns while
// still crediting genuinely new output, and checklist items completed before
// the run are baseline, not progress.
func TestMissionProgressTrackerDedupesEvidence(t *testing.T) {
	ag := newMissionAgent(t)
	ag.todoWriteTool().Store().Replace([]tools.TodoItem{
		{ID: "1", Content: "already done before this run", Status: tools.TodoStatusCompleted},
	})
	ctx, p := ag.TrackMissionProgress(context.Background())

	if p.Advanced(ag) {
		t.Fatal("checklist items completed before the run are baseline, not new progress")
	}

	observeMissionTool(ctx, "Bash", map[string]any{"command": "go test ./..."}, tools.Result{Output: "ok\tmodule/pkg\t0.123s"}, "call", "out")
	if !p.Advanced(ag) {
		t.Fatal("the first go test result should count as progress")
	}
	observeMissionTool(ctx, "Bash", map[string]any{"command": "go test ./..."}, tools.Result{Output: "ok\tmodule/pkg\t(cached)"}, "call", "out")
	if p.Advanced(ag) {
		t.Fatal("a cached go test line differs only in timing and must dedupe to the same evidence")
	}
	observeMissionTool(ctx, "Bash", map[string]any{"command": "go test ./..."}, tools.Result{Output: "ok\tmodule/other\t0.200s"}, "call", "out2")
	if !p.Advanced(ag) {
		t.Fatal("a result for a different package is new evidence")
	}
	observeMissionTool(ctx, "Bash", map[string]any{"command": "go test ./..."}, tools.Result{Output: "ok\tmodule/other\t0.512s"}, "call", "out2")
	if p.Advanced(ag) {
		t.Fatal("repeating the same package result with a different timing must not count again")
	}
}

// A checklist item completed during the run is progress exactly once; the same
// item on later turns must not keep resetting the stall counter.
func TestMissionProgressCountsNewCompletedTodoOnce(t *testing.T) {
	ag := newMissionAgent(t)
	ag.todoWriteTool().Store().Replace([]tools.TodoItem{
		{ID: "1", Content: "baseline done", Status: tools.TodoStatusCompleted},
		{ID: "2", Content: "still open", Status: tools.TodoStatusPending},
	})
	_, p := ag.TrackMissionProgress(context.Background())

	ag.todoWriteTool().Store().Replace([]tools.TodoItem{
		{ID: "1", Content: "baseline done", Status: tools.TodoStatusCompleted},
		{ID: "2", Content: "still open", Status: tools.TodoStatusCompleted},
	})
	if !p.Advanced(ag) {
		t.Fatal("a checklist item completed during the run should count as progress")
	}
	if p.Advanced(ag) {
		t.Fatal("the same completed item must not count as progress twice")
	}
}

// Completed checklist items are tracked outside the bounded tool-evidence
// window: enough tool churn to evict every stored (call, output) pair must not
// make an old completed item look new again on a later turn.
func TestMissionProgressDoesNotEvictCompletedTodoWithToolEvidence(t *testing.T) {
	ag := newMissionAgent(t)
	ag.todoWriteTool().Store().Replace([]tools.TodoItem{
		{ID: "1", Content: "baseline done", Status: tools.TodoStatusCompleted},
	})
	_, p := ag.TrackMissionProgress(context.Background())

	for i := 0; i <= missionEvidenceLimit; i++ {
		p.observe(fmt.Sprintf("tool evidence %d", i))
	}
	ag.todoWriteTool().Store().Reset()
	if !p.Advanced(ag) {
		t.Fatal("the overflowing tool evidence itself should still count as progress once")
	}

	ag.todoWriteTool().Store().Replace([]tools.TodoItem{
		{ID: "1", Content: "baseline done", Status: tools.TodoStatusCompleted},
	})
	if p.Advanced(ag) {
		t.Fatal("an item completed before the run must not reappear as progress after tool-evidence eviction")
	}
}
