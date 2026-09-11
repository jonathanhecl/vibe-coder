package agent

import (
	"encoding/json"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// workStateVersion is bumped when the persisted work-state shape changes.
const workStateVersion = 1

// workStateSnapshot is the task-agnostic durable state of an autonomous run:
// the TODO checklist the model maintains plus any explicit tasks. Keeping it
// generic means it works for any long process (image batches, migrations,
// audits), not just one domain.
type workStateSnapshot struct {
	Version int              `json:"version"`
	Todos   []tools.TodoItem `json:"todos,omitempty"`
	Tasks   []tools.Task     `json:"tasks,omitempty"`
	// Mission is the agent-declared long-running goal, when one exists. It is
	// what lets an interrupted autonomous run resume and keep going.
	Mission *tools.Mission `json:"mission,omitempty"`
}

// captureWorkState serializes the current checklist/task state. It returns
// nil when there is nothing to persist so an empty session keeps no sidecar.
func (a *Agent) captureWorkState() []byte {
	snap := workStateSnapshot{
		Version: workStateVersion,
		Tasks:   tools.SnapshotTasks(),
	}
	if tw := a.todoWriteTool(); tw != nil {
		snap.Todos = tw.Store().Snapshot()
	}
	if a.mission != nil {
		if m := a.mission.Snapshot(); m.Status != "" {
			snap.Mission = &m
		}
	}
	if len(snap.Todos) == 0 && len(snap.Tasks) == 0 && snap.Mission == nil {
		return nil
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return nil
	}
	return raw
}

// applyWorkState restores a persisted snapshot into the live stores. An empty
// or corrupt blob is ignored so old sessions and fresh runs are unaffected.
func (a *Agent) applyWorkState(raw []byte) {
	if len(raw) == 0 {
		return
	}
	var snap workStateSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return
	}
	if tw := a.todoWriteTool(); tw != nil {
		tw.Store().Replace(snap.Todos)
	}
	if len(snap.Tasks) > 0 {
		tools.RestoreTasks(snap.Tasks)
	}
	if snap.Mission != nil && a.mission != nil {
		a.mission.Restore(*snap.Mission)
	}
	// The restored list must be re-injected even if an identical note was
	// already sent before compaction; forget the dedup cache.
	a.resetTodoNoteDedup()
}

// PersistWorkState snapshots the checklist/task state into the session so it
// survives compaction, process restarts, and --resume. It is safe to call on
// every turn; the session only bumps its revision when the bytes change.
func (a *Agent) PersistWorkState() {
	if a == nil || a.sess == nil {
		return
	}
	a.sess.SetWorkState(a.captureWorkState())
}

// RestoreWorkState loads a persisted blob (as returned by Session.WorkState)
// into the live stores. No-op when empty.
func (a *Agent) RestoreWorkState(raw []byte) {
	if a == nil {
		return
	}
	a.applyWorkState(raw)
}
