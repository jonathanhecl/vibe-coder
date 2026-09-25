package agent

import (
	"context"
	"fmt"
	"strings"

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

// isMissionEndTool reports whether a tool call ends the active mission. When
// the agent calls one of these, the turn should stop immediately: the mission
// is over and continuing to execute further (often repeated) calls only burns
// turns.
func isMissionEndTool(name string) bool {
	switch name {
	case "MissionComplete", "MissionBlocked":
		return true
	default:
		return false
	}
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
// The mission loop uses it to detect a turn that produced no work.
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

// persistMissionProgress flushes the session after each tool call while a
// mission exists. A single agent turn can execute many tool calls before Run
// returns, so saving only at turn boundaries would lose the mission, checklist,
// and transcript if the process is killed mid-turn (a multi-hour autonomous run
// being the case that matters). It is a no-op outside missions so ordinary
// runs keep their cheaper turn-boundary saves.
func (a *Agent) persistMissionProgress() {
	if a == nil || a.sess == nil {
		return
	}
	if a.MissionSnapshot().Status == "" {
		return
	}
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
// invalidation. It changes when the goal or continuation turn count does.
func (a *Agent) missionCacheKey() string {
	m := a.MissionSnapshot()
	if m.Status != tools.MissionStatusActive {
		return ""
	}
	return fmt.Sprintf("%s|%s|%d", m.Status, m.Goal, m.Turns)
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
	var b strings.Builder
	b.WriteString("# Active Mission (agent-managed)\n")
	if goal != "" {
		b.WriteString("Goal: ")
		b.WriteString(goal)
		b.WriteString("\n")
	}
	if m.Turns > 0 {
		fmt.Fprintf(&b, "Continuation turns so far: %d\n", m.Turns)
	}
	b.WriteString("The runtime keeps starting new turns for you until you end this mission. " +
		"Keep executing the checklist with tool calls. When the goal is fully accomplished and verified, " +
		"call MissionComplete with a summary. If you are truly blocked and need the user, call MissionBlocked " +
		"with the reason. Do not stop with plain text while the mission is unfinished.")
	return strings.TrimSpace(b.String())
}

// MissionContinuationPrompt records one more autonomous turn and builds the
// synthetic follow-up that keeps the model working toward the mission goal
// without restarting completed work.
func (a *Agent) MissionContinuationPrompt() string {
	a.mission.IncrementTurns()
	m := a.mission.Snapshot()
	return fmt.Sprintf(
		"%s Continue the active mission autonomously. Goal: %q. "+
			"Take the next pending checklist step with concrete tool calls, verify it, then mark it completed. "+
			"Do not repeat finished work. Call MissionComplete with a summary when the goal is fully done, "+
			"or MissionBlocked if you need the user. The runtime keeps starting turns for you until you do.",
		missionPromptPrefix, strings.TrimSpace(m.Goal),
	)
}

// CheckMissionCompletion evaluates whether the active mission goal has been achieved
// using JEV Style, if configured and enabled.
func (a *Agent) CheckMissionCompletion(ctx context.Context) (bool, error) {
	if a == nil || !a.MissionActive() {
		return false, nil
	}
	a.mu.RLock()
	jev := a.jev
	goal := a.mission.Snapshot().Goal
	a.mu.RUnlock()

	if jev == nil || !jev.Enabled() || strings.TrimSpace(goal) == "" {
		return false, nil
	}

	progress := a.recentProgressSummary()
	return jev.CheckGoalCompletion(ctx, goal, progress)
}

// CompleteMission ends the active mission successfully from the runtime.
func (a *Agent) CompleteMission(summary string) {
	if a == nil || a.mission == nil {
		return
	}
	a.mission.Complete(summary)
}

func (a *Agent) recentProgressSummary() string {
	var b strings.Builder
	if tw := a.todoWriteTool(); tw != nil {
		for _, item := range tw.Store().Snapshot() {
			if item.Status == tools.TodoStatusCompleted {
				b.WriteString("- Completed task: ")
				b.WriteString(item.Content)
				b.WriteByte('\n')
			}
		}
	}
	if a.sess != nil {
		for _, msg := range a.sess.MessagesReadOnly() {
			if msg.Role == "system" || msg.Role == "tool" {
				b.WriteString(msg.Content)
				b.WriteByte('\n')
			}
		}
	}
	return strings.TrimSpace(b.String())
}
