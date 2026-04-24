package tools

import (
	"context"
	"strings"
	"testing"
)

func TestTodoWriteReplacesByDefault(t *testing.T) {
	t.Parallel()
	tool := NewTodoWriteTool()
	res := tool.Execute(context.Background(), map[string]any{
		"todos": []any{
			map[string]any{"id": "a", "content": "first", "status": "pending"},
			map[string]any{"id": "b", "content": "second", "status": "in_progress"},
		},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	got := tool.Store().Snapshot()
	if len(got) != 2 || got[0].ID != "a" || got[1].Status != "in_progress" {
		t.Fatalf("unexpected snapshot: %+v", got)
	}

	// A second call without merge wipes the previous list.
	tool.Execute(context.Background(), map[string]any{
		"todos": []any{
			map[string]any{"id": "z", "content": "fresh", "status": "pending"},
		},
	})
	got = tool.Store().Snapshot()
	if len(got) != 1 || got[0].ID != "z" {
		t.Fatalf("expected replace, got %+v", got)
	}
}

func TestTodoWriteMergeAllowsEmptyContentForExistingID(t *testing.T) {
	t.Parallel()
	tool := NewTodoWriteTool()
	res := tool.Execute(context.Background(), map[string]any{
		"todos": []any{
			map[string]any{"id": "a", "content": "Investigate project structure", "status": "pending"},
		},
	})
	if res.IsError {
		t.Fatalf("unexpected seed error: %s", res.Output)
	}

	res = tool.Execute(context.Background(), map[string]any{
		"merge": true,
		"todos": []any{
			map[string]any{"id": "a", "content": "", "status": "completed"},
		},
	})
	if res.IsError {
		t.Fatalf("unexpected merge error: %s", res.Output)
	}

	got := tool.Store().Snapshot()
	if len(got) != 1 || got[0].Content != "Investigate project structure" || got[0].Status != TodoStatusCompleted {
		t.Fatalf("unexpected merged snapshot: %+v", got)
	}
}

func TestTodoWriteMergesByID(t *testing.T) {
	t.Parallel()
	tool := NewTodoWriteTool()
	tool.Execute(context.Background(), map[string]any{
		"todos": []any{
			map[string]any{"id": "a", "content": "first", "status": "pending"},
			map[string]any{"id": "b", "content": "second", "status": "pending"},
		},
	})
	tool.Execute(context.Background(), map[string]any{
		"merge": true,
		"todos": []any{
			map[string]any{"id": "b", "content": "", "status": "in_progress"},
			map[string]any{"id": "c", "content": "third", "status": "pending"},
		},
	})

	got := tool.Store().Snapshot()
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %d (%+v)", len(got), got)
	}
	if got[1].ID != "b" || got[1].Status != "in_progress" || got[1].Content != "second" {
		t.Fatalf("merge should preserve content when empty, got %+v", got[1])
	}
	if got[2].ID != "c" || got[2].Content != "third" {
		t.Fatalf("merge should append unknown ids, got %+v", got[2])
	}
}

func TestTodoWriteValidatesInput(t *testing.T) {
	t.Parallel()
	tool := NewTodoWriteTool()
	cases := map[string]map[string]any{
		"missing todos":                     {},
		"empty array":                       {"todos": []any{}},
		"item not object":                   {"todos": []any{"oops"}},
		"missing id":                        {"todos": []any{map[string]any{"content": "x", "status": "pending"}}},
		"numeric placeholder content":       {"todos": []any{map[string]any{"id": "1", "content": "1", "status": "pending"}}},
		"empty content without merge match": {"todos": []any{map[string]any{"id": "a", "content": "", "status": "pending"}}},
	}
	for name, params := range cases {
		params := params
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			res := tool.Execute(context.Background(), params)
			if !res.IsError {
				t.Fatalf("expected error, got: %s", res.Output)
			}
		})
	}
}

func TestTodoWriteNormalisesUnknownStatus(t *testing.T) {
	t.Parallel()
	tool := NewTodoWriteTool()
	tool.Execute(context.Background(), map[string]any{
		"todos": []any{
			map[string]any{"id": "a", "content": "x", "status": "weird"},
		},
	})
	got := tool.Store().Snapshot()
	if got[0].Status != TodoStatusPending {
		t.Fatalf("expected normalisation to pending, got %q", got[0].Status)
	}
}

func TestRenderTodosForModelIsStable(t *testing.T) {
	t.Parallel()
	out := renderTodosForModel([]TodoItem{
		{ID: "a", Content: "first", Status: TodoStatusCompleted},
		{ID: "b", Content: "second", Status: TodoStatusInProgress},
	})
	if !strings.Contains(out, "[x] a: first") {
		t.Fatalf("missing completed marker: %q", out)
	}
	if !strings.Contains(out, "[~] b: second") {
		t.Fatalf("missing in-progress marker: %q", out)
	}
}
