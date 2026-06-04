package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

func validateTodoContent(incoming []TodoItem, merge bool, existing []TodoItem) error {
	existingIDs := map[string]struct{}{}
	if merge {
		for _, it := range existing {
			existingIDs[it.ID] = struct{}{}
		}
	}
	for i, it := range incoming {
		content := strings.TrimSpace(it.Content)
		if content == "" {
			if merge {
				if _, ok := existingIDs[it.ID]; ok {
					continue
				}
			}
			return fmt.Errorf("item %d content must be descriptive text", i)
		}
		if !containsLetter(content) {
			return fmt.Errorf("item %d content must be descriptive text, not a placeholder like %q", i, content)
		}
	}
	return nil
}

func containsLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
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
