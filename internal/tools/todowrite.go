package tools

import (
	"context"
)

// TodoWriteTool lets the model maintain a Cursor-style TODO list during a
// turn so the user has a live view of the plan and its progress.
//
// Invocation contract (mirrors what Cursor exposes):
//
//	{"merge": true, "todos": [
//	    {"id": "step-1", "content": "Read main.go", "status": "in_progress"},
//	    {"id": "step-2", "content": "Run tests",     "status": "pending"}
//	]}
//
// merge defaults to false (full replace). Return value is a plain text
// summary; the rich rendering happens in the TUI through ShowTodos.
type TodoWriteTool struct {
	store *TodoStore
}

// NewTodoWriteTool constructs a tool with its own store.
func NewTodoWriteTool() *TodoWriteTool {
	return &TodoWriteTool{store: &TodoStore{}}
}

// Store exposes the underlying store so the agent can push snapshots into
// the UI after every Execute.
func (t *TodoWriteTool) Store() *TodoStore { return t.store }

func (t *TodoWriteTool) Name() string { return "TodoWrite" }
func (t *TodoWriteTool) Description() string {
	return "Maintain a live TODO list of the steps you plan to take during this turn. " +
		"Use it whenever the user request needs 3+ distinct steps so they can see your plan and progress. " +
		"After creating or updating the TODO list, immediately continue executing the first pending step with the appropriate tool call (Read, Write, Bash, etc.). Do not stop after planning. " +
		"Status rules: 'pending' = not started yet; 'in_progress' = currently working on it; 'completed' = the step is fully done and verified; only mark 'completed' after the tool call for that step has returned successfully."
}

func (t *TodoWriteTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"merge": map[string]any{
						"type":        "boolean",
						"description": "If true, merge the incoming todos into the existing list by id; otherwise replace it.",
					},
					"todos": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id":      map[string]any{"type": "string"},
								"content": map[string]any{"type": "string"},
								"status": map[string]any{
									"type": "string",
									"enum": []string{
										TodoStatusPending,
										TodoStatusInProgress,
										TodoStatusCompleted,
										TodoStatusCancelled,
									},
								},
							},
							"required": []string{"id", "content", "status"},
						},
					},
				},
				"required": []string{"todos"},
			},
		},
	}
}

func (t *TodoWriteTool) Execute(_ context.Context, params map[string]any) Result {
	merge := false
	if v, ok := params["merge"].(bool); ok {
		merge = v
	}
	raw, ok := params["todos"]
	if !ok {
		return errResult("todos is required")
	}
	items, err := decodeTodos(raw)
	if err != nil {
		return errResult("todos: " + err.Error())
	}
	if len(items) == 0 {
		return errResult("todos must contain at least one item")
	}
	if err := validateTodoContent(items, merge, t.store.Snapshot()); err != nil {
		return errResult("todos: " + err.Error())
	}

	final := t.store.apply(merge, items)
	return Result{Output: renderTodosForModel(final)}
}
