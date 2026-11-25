package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// staleWorkStateJSON builds a persisted work-state snapshot holding one
// pending TODO, a mission, and one session note. version 2 omits the
// checklist timestamp, reproducing snapshots written before it existed.
func staleWorkStateJSON(t *testing.T, version int, updatedAt *time.Time, missionStatus string) []byte {
	t.Helper()
	snap := workStateSnapshot{
		Version: version,
		Todos: []tools.TodoItem{
			{ID: "1", Content: "finish the migration", Status: tools.TodoStatusPending},
		},
		Mission: &tools.Mission{Goal: "finish the migration", Status: missionStatus},
		Notes: []tools.SessionNote{
			{ID: "note", Kind: "fact", Content: "keep me", Evidence: "fixture"},
		},
	}
	if version >= 3 && updatedAt != nil {
		snap.TodosUpdatedAt = updatedAt
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return raw
}

func TestStaleChecklistRespectsEditAgeAndLegacyStamp(t *testing.T) {
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ag, _, _ := newTurnAgent(t, &fakeClient{})
	ag.RestoreWorkState(staleWorkStateJSON(t, 3, &stamp, tools.MissionStatusBlocked))

	if !ag.StaleChecklist(stamp.Add(5 * time.Hour)) {
		t.Fatal("a checklist exactly at the TTL must need review")
	}
	if ag.StaleChecklist(stamp.Add(5*time.Hour - time.Nanosecond)) {
		t.Fatal("a checklist just inside the TTL must not need review")
	}
	if ag.StaleChecklist(stamp.Add(-time.Hour)) {
		t.Fatal("a recently edited checklist must not need review")
	}

	legacy, _, _ := newTurnAgent(t, &fakeClient{})
	legacy.RestoreWorkState(staleWorkStateJSON(t, 2, nil, tools.MissionStatusBlocked))
	if !legacy.StaleChecklist(stamp) {
		t.Fatal("a legacy checklist without a timestamp must need one review")
	}
}

func TestStaleChecklistBlocksContinuationWithoutModelCall(t *testing.T) {
	client := &captureChatClient{replies: []string{"must never run"}}
	ag, _, _ := newTurnAgent(t, client)
	ag.RestoreWorkState(staleWorkStateJSON(t, 2, nil, tools.MissionStatusBlocked))

	if err := ag.RunContinuation(context.Background()); !errors.Is(err, ErrStaleChecklist) {
		t.Fatalf("RunContinuation error = %v, want ErrStaleChecklist", err)
	}
	if err := ag.Run(context.Background(), "continue"); !errors.Is(err, ErrStaleChecklist) {
		t.Fatalf("Run(continue) error = %v, want ErrStaleChecklist", err)
	}
	if len(client.requests) != 0 {
		t.Fatalf("stale continuation reached the model %d times", len(client.requests))
	}
}

func TestResolveStaleChecklistContinueApprovesAndPersists(t *testing.T) {
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	now := stamp.Add(5 * time.Hour)
	ag, _, _ := newTurnAgent(t, &fakeClient{})
	ag.RestoreWorkState(staleWorkStateJSON(t, 3, &stamp, tools.MissionStatusBlocked))

	if !ag.ResolveStaleChecklist(now, false) {
		t.Fatal("expected the stale checklist to resolve")
	}
	if ag.StaleChecklist(now) {
		t.Fatal("an approved checklist must not stay stale")
	}
	if got := ag.PendingTodoCount(); got != 1 {
		t.Fatalf("pending todos = %d, want the checklist preserved", got)
	}
	if m := ag.MissionSnapshot(); m.Status != tools.MissionStatusBlocked || m.Goal != "finish the migration" {
		t.Fatalf("mission not preserved: %+v", m)
	}

	restored, _, _ := newTurnAgent(t, &fakeClient{})
	restored.RestoreWorkState(ag.sess.WorkState())
	if restored.StaleChecklist(now) {
		t.Fatal("the approval timestamp did not survive capture/restore")
	}
	if !restored.StaleChecklist(now.Add(5 * time.Hour)) {
		t.Fatal("the checklist must go stale again exactly one TTL after approval")
	}
}

func TestResolveStaleChecklistDiscardClearsWorkKeepsNotesAndTranscript(t *testing.T) {
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	now := stamp.Add(5 * time.Hour)
	ag, _, _ := newTurnAgent(t, &fakeClient{})
	ag.RestoreWorkState(staleWorkStateJSON(t, 3, &stamp, tools.MissionStatusBlocked))
	ag.sess.AddUser("keep transcript")

	if !ag.ResolveStaleChecklist(now, true) {
		t.Fatal("expected discard to resolve the stale checklist")
	}
	if got := ag.PendingTodoCount(); got != 0 {
		t.Fatalf("pending todos after discard = %d, want 0", got)
	}
	if ag.CanResume() {
		t.Fatal("discard must clear the resume cursor")
	}
	if m := ag.MissionSnapshot(); m.Status == tools.MissionStatusActive || m.Status == tools.MissionStatusBlocked {
		t.Fatalf("active/blocked mission should be cleared, got %+v", m)
	}
	if got := ag.sess.MessageCount(); got != 1 {
		t.Fatalf("transcript messages = %d, want the user prompt kept", got)
	}

	restored, _, _ := newTurnAgent(t, &fakeClient{})
	restored.RestoreWorkState(ag.sess.WorkState())
	if restored.PendingTodoCount() != 0 || restored.CanResume() {
		t.Fatal("discarded work came back after capture/restore")
	}
	if m := restored.MissionSnapshot(); m.Status == tools.MissionStatusActive || m.Status == tools.MissionStatusBlocked {
		t.Fatalf("mission should stay cleared, got %+v", m)
	}
	if n, ok := restored.notes.Get("note"); !ok || n.Content != "keep me" {
		t.Fatalf("session note not preserved: %+v (found=%v)", n, ok)
	}
}

func TestResolveStaleChecklistDiscardKeepsCompletedMission(t *testing.T) {
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	now := stamp.Add(5 * time.Hour)
	ag, _, _ := newTurnAgent(t, &fakeClient{})
	ag.RestoreWorkState(staleWorkStateJSON(t, 3, &stamp, tools.MissionStatusCompleted))

	if !ag.ResolveStaleChecklist(now, true) {
		t.Fatal("expected discard to resolve the stale checklist")
	}
	if m := ag.MissionSnapshot(); m.Status != tools.MissionStatusCompleted || m.Goal != "finish the migration" {
		t.Fatalf("completed mission should be retained, got %+v", m)
	}
}

// An unrelated question that hit an empty model reply retries with scaffolding;
// while the checklist is stale that scaffolding must not smuggle its contents
// or push the model toward the pending work awaiting review.
func TestStaleChecklistExcludedFromUnrelatedRetry(t *testing.T) {
	ag, _, _ := newTurnAgent(t, &fakeClient{})
	ag.RestoreWorkState(staleWorkStateJSON(t, 2, nil, tools.MissionStatusBlocked))

	got := ag.BuildEmptyResponseRetryInput("answer a different question", true)
	if !strings.Contains(got, "answer a different question") {
		t.Fatalf("retry input lost the original request: %q", got)
	}
	if strings.Contains(got, "finish the migration") || strings.Contains(got, "Pick the next pending TODO") {
		t.Fatalf("stale checklist leaked into unrelated retry input: %q", got)
	}
}
