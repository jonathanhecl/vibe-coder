package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// ErrNothingToResume reports that the agent has no durable pending work to
// continue. An empty Enter at the REPL is a no-op in that state.
var ErrNothingToResume = errors.New("nothing to resume")

// resumeCheckpoint is the compact operational state needed to continue work
// without rebuilding the conversation. It intentionally stores a cursor, not
// the transcript or hidden reasoning.
type resumeCheckpoint struct {
	Goal       string `json:"goal,omitempty"`
	NextAction string `json:"next_action,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}

// resumeCheckpointBlock is ephemeral wire context. It tells the model exactly
// where to start and how to avoid repeating completed work.
func resumeCheckpointBlock(cp resumeCheckpoint) string {
	var b strings.Builder
	b.WriteString("# Resume checkpoint\n")
	b.WriteString("Continue the active work from this checkpoint. Do not start over.\n")
	if goal := strings.TrimSpace(cp.Goal); goal != "" {
		fmt.Fprintf(&b, "Goal: %q\n", goal)
	}
	if next := strings.TrimSpace(cp.NextAction); next != "" {
		fmt.Fprintf(&b, "Next action: %s\n", next)
	}
	if reason := strings.TrimSpace(cp.StopReason); reason != "" {
		fmt.Fprintf(&b, "Previous stop: %s\n", reason)
	}
	b.WriteString("Do not restate the plan, do not repeat completed work, and do not re-read the whole conversation. " +
		"Read only the specific artifact needed for the next action, make the change or run the step, verify it, " +
		"mark finished checklist items complete, and continue until the work is done.")
	return b.String()
}

// buildResumeCheckpoint derives the current resumable work from durable stores.
// Pending checklist items are the strongest signal; an active or blocked mission
// with a goal is the fallback for work that has no checklist yet.
func (a *Agent) buildResumeCheckpoint() *resumeCheckpoint {
	if a == nil {
		return nil
	}
	next := strings.TrimSpace(a.nextPendingTodo())
	mission := a.MissionSnapshot()
	missionResumable := (mission.Status == tools.MissionStatusActive || mission.Status == tools.MissionStatusBlocked) &&
		strings.TrimSpace(mission.Goal) != ""

	a.mu.RLock()
	goal := strings.TrimSpace(a.currentGoal)
	current := a.resume
	a.mu.RUnlock()

	if next == "" && !missionResumable {
		// A turn stopped by its step budget may have no checklist at all. The
		// explicit interrupted checkpoint is the only trustworthy signal that
		// this goal still needs work; a normal completed turn cleared it.
		if current != nil && strings.TrimSpace(current.NextAction) != "" && strings.TrimSpace(current.StopReason) != "" {
			cp := *current
			if cp.Goal == "" {
				cp.Goal = goal
			}
			return &cp
		}
		return nil
	}
	if missionResumable {
		goal = strings.TrimSpace(mission.Goal)
	}
	if goal == "" && a.sess != nil {
		goal = strings.TrimSpace(extractPriorGoal(a.sess))
	}
	if next == "" {
		next = missionNextAction(mission)
	}

	cp := &resumeCheckpoint{Goal: goal, NextAction: next}
	if current != nil && current.Goal == cp.Goal && current.NextAction == cp.NextAction {
		cp.StopReason = current.StopReason
	}
	return cp
}

// applyResumeCheckpoint restores a persisted cursor over the live stores. The
// saved goal and stop reason are authoritative; the next action is re-derived
// when it no longer matches current TODO state.
func (a *Agent) applyResumeCheckpoint(saved *resumeCheckpoint) {
	cp := a.buildResumeCheckpoint()
	if cp == nil && saved != nil && strings.TrimSpace(saved.NextAction) != "" && strings.TrimSpace(saved.StopReason) != "" {
		restored := *saved
		a.mu.Lock()
		a.resume = &restored
		if strings.TrimSpace(a.currentGoal) == "" {
			a.currentGoal = restored.Goal
		}
		a.mu.Unlock()
		return
	}
	if cp == nil {
		a.mu.Lock()
		a.resume = nil
		a.mu.Unlock()
		return
	}
	if saved != nil {
		if goal := strings.TrimSpace(saved.Goal); goal != "" {
			cp.Goal = goal
			a.mu.Lock()
			if strings.TrimSpace(a.currentGoal) == "" {
				a.currentGoal = goal
			}
			a.mu.Unlock()
		}
		if next := strings.TrimSpace(saved.NextAction); next != "" && (cp.NextAction == "" || cp.NextAction == next) {
			cp.NextAction = next
		}
		cp.StopReason = saved.StopReason
	}
	a.mu.Lock()
	a.resume = cp
	a.mu.Unlock()
}

func (a *Agent) hasDurableWork() bool {
	if a == nil {
		return false
	}
	if strings.TrimSpace(a.nextPendingTodo()) != "" {
		return true
	}
	mission := a.MissionSnapshot()
	return (mission.Status == tools.MissionStatusActive || mission.Status == tools.MissionStatusBlocked) &&
		strings.TrimSpace(mission.Goal) != ""
}

func missionNextAction(mission tools.Mission) string {
	goal := strings.TrimSpace(mission.Goal)
	if goal == "" {
		return ""
	}
	if mission.Status == tools.MissionStatusBlocked {
		return "Resume the blocked mission and continue toward its goal: " + goal
	}
	return "Continue the active mission toward its goal: " + goal
}

// CanResume reports whether pressing Enter can continue durable work.
func (a *Agent) CanResume() bool {
	return a != nil && a.buildResumeCheckpoint() != nil
}

// resumeSnapshot returns the current checkpoint, or a zero value when nothing
// is resumable.
func (a *Agent) resumeSnapshot() resumeCheckpoint {
	cp := a.buildResumeCheckpoint()
	if cp == nil {
		return resumeCheckpoint{}
	}
	return *cp
}

// beginContinuation freezes the checkpoint for this run and reactivates a
// blocked mission so the existing mission loop can keep going. It never adds a
// user message to the transcript.
func (a *Agent) beginContinuation() (resumeCheckpoint, bool) {
	cp := a.buildResumeCheckpoint()
	if cp == nil || strings.TrimSpace(cp.NextAction) == "" {
		return resumeCheckpoint{}, false
	}
	if mission := a.MissionSnapshot(); mission.Status == tools.MissionStatusBlocked {
		a.mission.Resume()
	}
	a.mu.Lock()
	a.currentGoal = cp.Goal
	a.resume = cp
	a.mu.Unlock()
	return *cp, true
}

// finishRunCheckpoint records why a run stopped while work remains. A finished
// checklist with no mission clears the checkpoint, which makes a later Enter a
// no-op instead of replaying stale work.
func (a *Agent) finishRunCheckpoint(err error) {
	if err == nil && !a.hasDurableWork() {
		a.mu.Lock()
		a.resume = nil
		a.mu.Unlock()
		return
	}
	cp := a.buildResumeCheckpoint()
	if cp == nil && IsIterationCapErr(err) {
		a.mu.RLock()
		goal := strings.TrimSpace(a.currentGoal)
		a.mu.RUnlock()
		if goal == "" && a.sess != nil {
			goal = strings.TrimSpace(extractPriorGoal(a.sess))
		}
		if goal != "" {
			cp = &resumeCheckpoint{
				Goal:       goal,
				NextAction: "Continue the interrupted task; inspect the artifact already in progress before writing further.",
			}
		}
	}
	if cp == nil {
		a.mu.Lock()
		a.resume = nil
		a.mu.Unlock()
		return
	}
	cp.StopReason = resumeStopReason(err)
	a.mu.Lock()
	a.resume = cp
	a.mu.Unlock()
}

func resumeStopReason(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case IsIterationCapErr(err):
		return "the previous turn reached its step budget"
	case IsTurnCancelledErr(err):
		return "the previous turn was cancelled by the user"
	default:
		return trimResumeReason(err.Error())
	}
}

func trimResumeReason(reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	const maxRunes = 300
	runes := []rune(reason)
	if len(runes) <= maxRunes {
		return reason
	}
	return string(runes[:maxRunes]) + "…"
}

// RunContinuation starts a turn from the durable checkpoint without adding a
// user message. Typed corrections still go through Run and remain normal user
// requests.
func (a *Agent) RunContinuation(ctx context.Context) error {
	return a.run(ctx, "", true)
}
