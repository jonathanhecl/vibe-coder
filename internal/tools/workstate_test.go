package tools

import "testing"

func TestTodoStoreReplace(t *testing.T) {
	t.Parallel()

	store := &TodoStore{}
	store.Replace([]TodoItem{
		{ID: "1", Content: "first", Status: TodoStatusPending},
		{ID: "2", Content: "second", Status: TodoStatusCompleted},
	})
	snap := store.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 items, got %d", len(snap))
	}
	// Replace is a full swap, not a merge: unknown ids from before must drop.
	store.Replace([]TodoItem{{ID: "3", Content: "third", Status: TodoStatusPending}})
	snap = store.Snapshot()
	if len(snap) != 1 || snap[0].ID != "3" {
		t.Fatalf("expected only item 3, got %#v", snap)
	}
}
