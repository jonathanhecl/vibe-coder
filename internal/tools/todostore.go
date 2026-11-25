package tools

import (
	"slices"
	"strings"
	"sync"
	"time"
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
	// updatedAt is the last time the checklist contents actually changed (or
	// was explicitly approved/restored). It drives the stale-checklist review:
	// a pending list untouched for hours asks before it resumes.
	updatedAt time.Time
}

// Snapshot returns a deep copy of the current TODO list. Safe to mutate.
func (s *TodoStore) Snapshot() []TodoItem {
	items, _ := s.SnapshotState()
	return items
}

// SnapshotState returns a deep copy of the current TODO list plus the
// timestamp of its last meaningful change. A zero timestamp means the list
// was never stamped (e.g. restored from a legacy snapshot).
func (s *TodoStore) SnapshotState() ([]TodoItem, time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.items), s.updatedAt
}

// Reset clears the store. Used by tests and slash-commands; the agent
// loop never calls it on its own.
func (s *TodoStore) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = nil
	s.updatedAt = time.Time{}
}

// Restore swaps the store contents and timestamp in one critical section.
// Used when reloading a persisted checklist so its original edit time — not
// the restore time — decides whether it needs review.
func (s *TodoStore) Restore(items []TodoItem, updatedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = normaliseAll(items)
	s.updatedAt = updatedAt
}

// Touch stamps the checklist as freshly approved without changing its items.
// An explicit "continue" on a stale checklist uses this so the TTL restarts
// from the user's decision rather than the last edit.
func (s *TodoStore) Touch(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updatedAt = now.UTC()
}

// Replace swaps the store contents with the given items in one critical
// section. Used when restoring a persisted checklist (e.g. after --resume).
func (s *TodoStore) Replace(items []TodoItem) {
	if s == nil {
		return
	}
	s.apply(false, items)
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
	before := slices.Clone(s.items)
	defer func() {
		if !slices.Equal(before, s.items) {
			s.updatedAt = time.Now().UTC()
		}
	}()
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
