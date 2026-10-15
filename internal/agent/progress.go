package agent

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/prompt"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// progressPromptBlock renders a compact, durable summary of the checklist and
// tasks. It rides in the trailing runtime-state note (rebuilt every
// request), so unlike transcript notes it can never be summarized away by
// compaction — the model always knows what is done and what remains on a
// long run.
func (a *Agent) progressPromptBlock() string {
	tw := a.todoWriteTool()
	total, done, pending := 0, 0, 0
	var next []string
	if tw != nil {
		for _, it := range tw.Store().Snapshot() {
			if !isMeaningfulTodoContent(it.Content) {
				continue
			}
			total++
			switch it.Status {
			case tools.TodoStatusCompleted:
				done++
			case tools.TodoStatusPending, tools.TodoStatusInProgress:
				pending++
				if len(next) < 5 {
					mark := ""
					if it.Status == tools.TodoStatusInProgress {
						mark = " (in progress)"
					}
					next = append(next, fmt.Sprintf("%s: %s%s", it.ID, it.Content, mark))
				}
			}
		}
	}

	openTasks := 0
	for _, task := range tools.SnapshotTasks() {
		if task.Status != "completed" && task.Status != "cancelled" {
			openTasks++
		}
	}

	if total == 0 && openTasks == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("# Work Progress (durable)\n")
	if total > 0 {
		fmt.Fprintf(&b, "Checklist: %d total, %d done, %d pending\n", total, done, pending)
		if len(next) > 0 {
			b.WriteString("Next steps:\n")
			for _, item := range next {
				b.WriteString("- ")
				b.WriteString(item)
				b.WriteString("\n")
			}
		}
	}
	if openTasks > 0 {
		fmt.Fprintf(&b, "Open tasks: %d\n", openTasks)
	}
	b.WriteString("This summary comes from durable state and is always current; trust it over older transcript notes. " +
		"Continue with the next pending step and mark items completed as they finish.")
	return strings.TrimSpace(b.String())
}

// shouldAnchorGoal reports whether the current request should carry the active
// goal reminder at the tail of the wire window: once tools have executed this
// turn, while TODOs or a mission are active, or after compaction summarized the
// earlier transcript. Single-shot user turns omit it because the user's own
// message is already at the end of the wire window.
func (a *Agent) shouldAnchorGoal() bool {
	if a == nil {
		return false
	}
	if a.ToolCallsThisTurn() > 0 || a.hasPendingTodos() || a.MissionActive() {
		return true
	}
	if a.sess != nil {
		msgs := a.sess.MessagesReadOnly()
		if len(msgs) > 0 && strings.HasPrefix(msgs[0].Content, "[Earlier conversation summary]") {
			return true
		}
		if len(msgs) > 0 && msgs[len(msgs)-1].Role == "user" && isContinuationMessage(msgs[len(msgs)-1].Content) {
			return true
		}
	}
	return false
}

// runtimeStateNote assembles the ephemeral state appended after the
// transcript window on every request: the durable work progress, the mission
// continuation turn count, and the active goal anchor on multi-step turns.
// Keeping it out of both the system prompt and the stored transcript means it
// is always current, never accumulates, and never invalidates the prefix KV cache.
func (a *Agent) runtimeStateNote() string {
	var b strings.Builder
	if block := a.progressPromptBlock(); block != "" {
		b.WriteString(block)
	}
	if m := a.MissionSnapshot(); m.Status == tools.MissionStatusActive && m.Turns > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "Mission continuation turns so far: %d", m.Turns)
	}
	if a.shouldAnchorGoal() {
		a.mu.RLock()
		goal := strings.TrimSpace(a.currentGoal)
		a.mu.RUnlock()
		if goal != "" {
			if b.Len() > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(prompt.Render("goal", struct{ Goal string }{Goal: goal}))
		}
	}
	return strings.TrimSpace(b.String())
}
