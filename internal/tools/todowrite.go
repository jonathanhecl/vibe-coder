package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Todo statuses recognised by the renderer. Anything else is normalised to
// "pending" so a hallucinated value never breaks the UI.
const (
	TodoStatusPending    = "pending"
	TodoStatusInProgress = "in_progress"
	TodoStatusCompleted  = "completed"
	TodoStatusCancelled  = "cancelled"
)

// TodoItem is a single entry in the agent-managed task list. The shape
// mirrors what Cursor's "To-dos" panel exposes so the model can use
// familiar field names (id, content, status).
type TodoItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

// TodoStore is a goroutine-safe holder for the current TODO list.
//
// One instance lives inside a TodoWriteTool and is shared with the agent
// loop, which reads Snapshot() after each call to push the list into the
// UI. Lifecycles match a single Run() turn: the agent decides whether to
// reset between turns (we keep it sticky so multi-turn plans can be
// followed across iterations).
type TodoStore struct {
	mu    sync.RWMutex
	items []TodoItem
}

// Snapshot returns a deep copy of the current TODO list. Safe to mutate.
func (s *TodoStore) Snapshot() []TodoItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]TodoItem, len(s.items))
	copy(out, s.items)
	return out
}

// Reset clears the store. Used by tests and slash-commands; the agent
// loop never calls it on its own.
func (s *TodoStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = nil
}

// apply replaces or merges the list in one critical section. When merge
// is true, items with matching IDs are updated in place (preserving
// position) and unknown IDs are appended. Otherwise the incoming list
// fully replaces the store.
//
// Returns the resulting snapshot for convenience.
func (s *TodoStore) apply(merge bool, incoming []TodoItem) []TodoItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !merge || len(s.items) == 0 {
		s.items = normaliseAll(incoming)
		out := make([]TodoItem, len(s.items))
		copy(out, s.items)
		return out
	}
	index := make(map[string]int, len(s.items))
	for i, it := range s.items {
		index[it.ID] = i
	}
	for _, it := range incoming {
		it = normaliseOne(it)
		if pos, ok := index[it.ID]; ok {
			// Merge-in-place: only override fields the caller actually
			// supplied. Empty content keeps the previous content (handy
			// when the model just toggles the status).
			cur := s.items[pos]
			if strings.TrimSpace(it.Content) != "" {
				cur.Content = it.Content
			}
			if it.Status != "" {
				cur.Status = it.Status
			}
			s.items[pos] = cur
			continue
		}
		s.items = append(s.items, it)
		index[it.ID] = len(s.items) - 1
	}
	out := make([]TodoItem, len(s.items))
	copy(out, s.items)
	return out
}

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

	final := t.store.apply(merge, items)
	return Result{Output: renderTodosForModel(final)}
}

// decodeTodos accepts the loose JSON shape the model usually emits
// (slice of map[string]any) as well as the strongly typed []TodoItem we
// use in tests.
func decodeTodos(raw any) ([]TodoItem, error) {
	switch v := raw.(type) {
	case []TodoItem:
		return v, nil
	case []any:
		out := make([]TodoItem, 0, len(v))
		for i, entry := range v {
			m, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("item %d is not an object", i)
			}
			id, _ := m["id"].(string)
			content, _ := m["content"].(string)
			status, _ := m["status"].(string)
			if strings.TrimSpace(id) == "" {
				return nil, fmt.Errorf("item %d missing id", i)
			}
			// content may be empty when merging an existing item to flip its
			// status; the merge logic preserves the previous content in that
			// case. We still require a non-empty id so we can match.
			out = append(out, TodoItem{ID: id, Content: content, Status: status})
		}
		return out, nil
	}
	// Fallback: the parser produced a JSON-looking string; try to unmarshal it.
	if s, ok := raw.(string); ok {
		var parsed []TodoItem
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			return parsed, nil
		}
	}
	return nil, fmt.Errorf("expected an array of {id, content, status}")
}

// renderTodosForModel produces the deterministic plain-text summary that
// goes into the model's context. The TUI uses ShowTodos for the rich
// version; this output is the source of truth the model reads back later.
func renderTodosForModel(items []TodoItem) string {
	if len(items) == 0 {
		return "TODO list is empty."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "TODOs (%d):\n", len(items))
	for _, it := range items {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", statusGlyphASCII(it.Status), it.ID, it.Content)
	}
	return strings.TrimRight(b.String(), "\n")
}

func statusGlyphASCII(status string) string {
	switch status {
	case TodoStatusInProgress:
		return "~"
	case TodoStatusCompleted:
		return "x"
	case TodoStatusCancelled:
		return "-"
	default:
		return " "
	}
}

func normaliseOne(it TodoItem) TodoItem {
	switch it.Status {
	case TodoStatusInProgress, TodoStatusCompleted, TodoStatusCancelled, TodoStatusPending:
		// already normalised
	default:
		it.Status = TodoStatusPending
	}
	return it
}

func normaliseAll(in []TodoItem) []TodoItem {
	out := make([]TodoItem, len(in))
	for i, it := range in {
		out[i] = normaliseOne(it)
	}
	return out
}
