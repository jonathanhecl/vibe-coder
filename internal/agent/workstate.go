package agent

import (
	"encoding/json"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// workStateVersion is bumped when the persisted work-state shape changes.
const workStateVersion = 2

// workStateSnapshot is the task-agnostic durable state of an autonomous run:
// the TODO checklist the model maintains plus any explicit tasks. Keeping it
// generic means it works for any long process (image batches, migrations,
// audits), not just one domain.
type workStateSnapshot struct {
	Version int                 `json:"version"`
	Todos   []tools.TodoItem    `json:"todos,omitempty"`
	Notes   []tools.SessionNote `json:"notes,omitempty"`
	// Mission is the agent-declared long-running goal, when one exists. It is
	// what lets an interrupted autonomous run resume and keep going.
	Mission *tools.Mission `json:"mission,omitempty"`
	// Resume is the compact continuation cursor. It is present only while
	// durable work remains, so a finished task cannot be resumed by mistake.
	Resume *resumeCheckpoint `json:"resume,omitempty"`
}

// captureWorkState serializes the current checklist/task state. It returns
// nil when there is nothing to persist so an empty session keeps no sidecar.
func (a *Agent) captureWorkState() []byte {
	a.ensureNotesSession()
	snap := workStateSnapshot{
		Version: workStateVersion,
	}
	if tw := a.todoWriteTool(); tw != nil {
		snap.Todos = tw.Store().Snapshot()
	}
	if a.mission != nil {
		if m := a.mission.Snapshot(); m.Status != "" {
			snap.Mission = &m
		}
	}
	if a.notes != nil {
		snap.Notes = a.notes.Snapshot()
	}
	snap.Resume = a.buildResumeCheckpoint()
	a.mu.Lock()
	a.resume = snap.Resume
	a.mu.Unlock()
	if len(snap.Todos) == 0 && snap.Mission == nil && len(snap.Notes) == 0 && snap.Resume == nil {
		return nil
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return nil
	}
	return raw
}

// applyWorkState restores a persisted snapshot into the live stores. Notes are
// always replaced first: an empty, corrupt, or legacy blob (one written before
// notes existed) must not leave notes from a previous session behind. Other
// stores keep their old tolerance — an empty or corrupt blob leaves them
// untouched.
func (a *Agent) applyWorkState(raw []byte) {
	if a.notes != nil {
		a.notes.Restore(nil)
	}
	a.mu.Lock()
	a.resume = nil
	a.mu.Unlock()
	if len(raw) > 0 {
		var snap workStateSnapshot
		if err := json.Unmarshal(raw, &snap); err == nil {
			if tw := a.todoWriteTool(); tw != nil {
				tw.Store().Replace(snap.Todos)
			}
			if snap.Mission != nil && a.mission != nil {
				a.mission.Restore(*snap.Mission)
			}
			if a.notes != nil && len(snap.Notes) > 0 {
				a.notes.Restore(snap.Notes)
			}
			a.applyResumeCheckpoint(snap.Resume)
		}
	}
	// The restored notes (or their absence) now belong to the current session.
	if a.sess != nil {
		a.mu.Lock()
		a.notesSessionID = a.sess.ID()
		a.mu.Unlock()
	}
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
// into the live stores. An empty blob still clears session notes: notes are
// scoped to one session and must never leak across a restore boundary.
func (a *Agent) RestoreWorkState(raw []byte) {
	if a == nil {
		return
	}
	a.applyWorkState(raw)
}
