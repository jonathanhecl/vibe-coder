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

func TestTaskSnapshotRestoreRoundTrip(t *testing.T) {
	before := SnapshotTasks()
	t.Cleanup(func() { RestoreTasks(before) })

	RestoreTasks([]Task{
		{ID: "task-3", Content: "three", Status: "pending"},
		{ID: "task-7", Content: "seven", Status: "completed"},
	})
	snap := SnapshotTasks()
	if len(snap) != 2 || snap[0].ID != "task-3" || snap[1].ID != "task-7" {
		t.Fatalf("unexpected snapshot: %#v", snap)
	}

	// A new task must not collide with the restored ids.
	result := NewTaskCreateTool().Execute(nil, map[string]any{"content": "new"})
	if result.IsError {
		t.Fatalf("create task: %s", result.Output)
	}
	restored := SnapshotTasks()
	if len(restored) != 3 {
		t.Fatalf("expected 3 tasks after create, got %d", len(restored))
	}
	for _, task := range restored {
		if task.ID == "task-3" || task.ID == "task-7" {
			continue
		}
		if task.ID != "task-8" {
			t.Fatalf("expected new id past restored sequence (task-8), got %q", task.ID)
		}
	}
}
