package tools

import "testing"

func TestMissionStoreLifecycle(t *testing.T) {
	t.Parallel()

	store := NewMissionStore()
	if store.Active() {
		t.Fatal("new store must not be active")
	}
	store.Start("process 200 items")
	if !store.Active() {
		t.Fatal("mission should be active after Start")
	}
	store.IncrementTurns()
	store.IncrementTurns()
	if got := store.Snapshot().Turns; got != 2 {
		t.Fatalf("expected 2 turns, got %d", got)
	}
	m := store.Complete("all done")
	if m.Status != MissionStatusCompleted || m.Summary != "all done" {
		t.Fatalf("unexpected complete mission: %#v", m)
	}
	if store.Active() {
		t.Fatal("completed mission must not be active")
	}
}

func TestMissionRestore(t *testing.T) {
	t.Parallel()

	store := NewMissionStore()
	store.Restore(Mission{Goal: "resume me", Status: MissionStatusActive, Turns: 5})
	if !store.Active() {
		t.Fatal("restored active mission should be active")
	}
	if got := store.Snapshot(); got.Goal != "resume me" || got.Turns != 5 {
		t.Fatalf("unexpected restored mission: %#v", got)
	}
}
