package agent

import (
	"errors"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// checklistTTL is how long a pending checklist may sit untouched before
// continuing it requires an explicit user decision. The clock runs from the
// last meaningful checklist edit or an explicit approval — never from session
// activity, so unrelated turns do not keep stale work alive.
const checklistTTL = 5 * time.Hour

// ErrStaleChecklist reports that a pending checklist needs review before any
// continuation may run. Noninteractive paths surface it as an error; the
// interactive REPL turns it into a Continue/Discard/Later prompt.
var ErrStaleChecklist = errors.New("the pending checklist needs review before it can continue; open an interactive session to continue or discard it")

// StaleChecklist reports whether pending TODOs require explicit review: the
// checklist has meaningful pending or in-progress items and its last edit (or
// approval) is older than checklistTTL — or was never recorded, as with lists
// persisted before the timestamp existed.
func (a *Agent) StaleChecklist(now time.Time) bool {
	tw := a.todoWriteTool()
	if tw == nil {
		return false
	}
	items, updatedAt := tw.Store().SnapshotState()
	if !updatedAt.IsZero() && now.Sub(updatedAt) < checklistTTL {
		return false
	}
	for _, item := range items {
		if isMeaningfulTodoContent(item.Content) && (item.Status == tools.TodoStatusPending || item.Status == tools.TodoStatusInProgress) {
			return true
		}
	}
	return false
}

// ResolveStaleChecklist records the user's decision without touching the
// transcript or notes. Discard clears the checklist, an active or blocked
// mission, and the resume cursor while keeping completed missions and the
// conversation; continue stamps the checklist fresh so the TTL restarts from
// this approval. It returns false when nothing was stale.
func (a *Agent) ResolveStaleChecklist(now time.Time, discard bool) bool {
	if !a.StaleChecklist(now) {
		return false
	}
	store := a.todoWriteTool().Store()
	if discard {
		store.Reset()
		m := a.MissionSnapshot()
		if m.Status == tools.MissionStatusActive || m.Status == tools.MissionStatusBlocked {
			a.mission.Restore(tools.Mission{})
		}
		a.mu.Lock()
		a.resume = nil
		a.currentGoal = ""
		a.checklistPaused = false
		a.mu.Unlock()
		if a.perm != nil {
			a.perm.SetUnattended(false)
		}
	} else {
		store.Touch(now)
	}
	a.PersistWorkState()
	return true
}
