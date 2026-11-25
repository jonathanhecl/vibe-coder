package tools

import (
	"testing"
	"time"
)

// A restored checklist keeps its persisted edit time: rewriting identical
// items must not restart the stale-review clock, while a real change must.
func TestTodoStoreRestorePreservesTimestampAcrossNoOpReplace(t *testing.T) {
	store := &TodoStore{}
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	items := []TodoItem{{ID: "1", Content: "finish step", Status: TodoStatusPending}}

	store.Restore(items, fixed)
	store.Replace(items)
	if _, got := store.SnapshotState(); !got.Equal(fixed) {
		t.Fatalf("no-op Replace restamped the checklist: %v, want %v", got, fixed)
	}

	store.Replace([]TodoItem{{ID: "1", Content: "finish step", Status: TodoStatusCompleted}})
	_, got := store.SnapshotState()
	if got.Equal(fixed) || got.IsZero() {
		t.Fatalf("a real checklist change did not restamp it: %v", got)
	}
}
