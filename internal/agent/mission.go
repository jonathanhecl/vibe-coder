package agent

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/prompt"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// missionPromptPrefix marks the synthetic user turns the mission loop feeds
// back to the model. It lets goal resolution keep pointing at the user's
// original request instead of the continuation boilerplate.
const missionPromptPrefix = "[mission]"

// PendingTodoCount returns the number of meaningful TODO items that are not
// yet completed or cancelled.
func (a *Agent) PendingTodoCount() int {
	tw := a.todoWriteTool()
	if tw == nil {
		return 0
	}
	n := 0
	for _, it := range tw.Store().Snapshot() {
		if !isMeaningfulTodoContent(it.Content) {
			continue
		}
		switch it.Status {
		case tools.TodoStatusPending, tools.TodoStatusInProgress:
			n++
		}
	}
	return n
}

// IsIterationCapErr reports whether err is the agent's per-turn iteration cap
// error. The mission loop treats it as progress (the turn ran out of its own
// budget, not a failure) and starts another turn.
func IsIterationCapErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "iteration cap reached")
}

// resetTurnToolCalls starts a fresh tool-activity counter for a Run.
func (a *Agent) resetTurnToolCalls() { a.turnToolCalls.Store(0) }

// noteToolExecuted records that a tool actually ran during this turn.
func (a *Agent) noteToolExecuted() { a.turnToolCalls.Add(1) }

// ToolCallsThisTurn reports how many tools executed in the last/current Run.
// The mission loop uses it as a decision-review trigger and for run logs; the
// cross-turn progress tracker is what decides whether a turn produced new work.
func (a *Agent) ToolCallsThisTurn() int { return int(a.turnToolCalls.Load()) }

// SaveSession flushes the session to disk. The mission loop calls it after
// every turn so an interrupted autonomous run resumes with the latest
// checklist, tasks, and mission state.
func (a *Agent) SaveSession() error {
	if a == nil || a.sess == nil {
		return nil
	}
	return a.sess.Save()
}

// persistMissionProgress flushes the session after each successful tool call
// while durable work remains. A single turn can execute many tools before Run
// returns, so saving only at turn boundaries would lose the mission, checklist,
// checkpoint, and transcript if the process is killed mid-turn. Ordinary
// one-off turns with no checklist keep the cheaper turn-boundary save.
func (a *Agent) persistMissionProgress() {
	if a == nil || a.sess == nil {
		return
	}
	if a.MissionSnapshot().Status == "" && !a.hasPendingTodos() {
		return
	}
	// A successful tool means the run is still making progress; an earlier
	// pause reason must not survive into a mid-run checkpoint.
	a.mu.Lock()
	if a.resume != nil {
		a.resume.StopReason = ""
	}
	a.mu.Unlock()
	a.PersistWorkState()
	_ = a.SaveSession()
}

// MissionActive reports whether the agent declared a mission and has not yet
// completed or blocked it. Only the agent ends a mission; there is no turn cap.
func (a *Agent) MissionActive() bool {
	if a == nil {
		return false
	}
	return a.mission.Active()
}

// MissionSnapshot returns a copy of the current mission state.
func (a *Agent) MissionSnapshot() tools.Mission {
	if a == nil || a.mission == nil {
		return tools.Mission{}
	}
	return a.mission.Snapshot()
}

// BlockActiveMission pauses the active mission from outside the model (e.g.
// repeated empty responses). It is a safety valve, not a limit on turns.
func (a *Agent) BlockActiveMission(reason string) {
	if a == nil || a.mission == nil {
		return
	}
	a.mission.Block(reason)
}

// missionCacheKey identifies the active mission for system-prompt cache
// invalidation. It changes when the goal or status does; the continuation
// turn count moved to the ephemeral runtime reminder so the system prompt
// stays byte-identical across mission turns.
func (a *Agent) missionCacheKey() string {
	m := a.MissionSnapshot()
	if m.Status != tools.MissionStatusActive {
		return ""
	}
	return fmt.Sprintf("%s|%s", m.Status, m.Goal)
}

// missionPromptBlock renders the current mission for the system prompt so the
// agent always knows whether an autonomous mission is running, what its goal
// is, and how to end it. Returns "" when no mission exists.
func (a *Agent) missionPromptBlock() string {
	m := a.MissionSnapshot()
	if m.Status != tools.MissionStatusActive {
		return ""
	}
	goal := strings.TrimSpace(m.Goal)
	if goal == "" {
		a.mu.RLock()
		goal = strings.TrimSpace(a.currentGoal)
		a.mu.RUnlock()
	}
	return prompt.Render("mission", struct{ Goal string }{Goal: goal})
}

// MissionContinuationPrompt records one more autonomous turn and builds the
// synthetic follow-up that keeps the model working toward the goal
// without restarting completed work.
func (a *Agent) MissionContinuationPrompt() string {
	a.mission.IncrementTurns()
	m := a.mission.Snapshot()
	goal := strings.TrimSpace(m.Goal)
	a.mu.RLock()
	latest := strings.TrimSpace(a.currentGoal)
	a.mu.RUnlock()
	extra := ""
	if latest != "" && latest != goal && !isContinuationMessage(latest) && !strings.HasPrefix(latest, missionPromptPrefix) {
		extra = fmt.Sprintf("Latest user instruction/preference: %q. ", latest)
	}
	return fmt.Sprintf(
		"%s Continue working on the goal: %q. %s"+
			"Execute the next pending TODO step with concrete tool calls and mark it completed when done. "+
			"Do not repeat finished work or re-read unchanged files. "+
			"When the goal is finished, reply directly with a concise summary; if you need user input, call AskUserQuestion.",
		missionPromptPrefix, goal, extra,
	)
}

// ResumeMission reactivates a blocked/paused mission.
func (a *Agent) ResumeMission() {
	if a == nil || a.mission == nil {
		return
	}
	a.mission.Resume()
}

// StartMission activates an autonomous mission from the runtime or user command.
func (a *Agent) StartMission(goal string) {
	if a == nil || a.mission == nil {
		return
	}
	a.mission.Start(goal)
}

// CompleteMission ends the active mission successfully from the runtime and
// marks any open TODO items completed so no stale checklist blocks the next turn.
func (a *Agent) CompleteMission(summary string) {
	if a == nil || a.mission == nil {
		return
	}
	a.mission.Complete(summary)
	if tw := a.todoWriteTool(); tw != nil {
		items := tw.Store().Snapshot()
		changed := false
		for i := range items {
			if items[i].Status == tools.TodoStatusPending || items[i].Status == tools.TodoStatusInProgress {
				items[i].Status = tools.TodoStatusCompleted
				changed = true
			}
		}
		if changed {
			tw.Store().Replace(items)
		}
	}
}
