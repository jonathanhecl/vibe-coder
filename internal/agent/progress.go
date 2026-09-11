package agent

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// progressPromptBlock renders a compact, durable summary of the checklist and
// tasks. It lives in the system prompt (rebuilt every turn), so unlike
// transcript notes it can never be summarized away by compaction — the model
// always knows what is done and what remains on a long run.
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

// progressCacheKey identifies the durable progress state for system-prompt
// cache invalidation.
func (a *Agent) progressCacheKey() string {
	var b strings.Builder
	if tw := a.todoWriteTool(); tw != nil {
		for _, it := range tw.Store().Snapshot() {
			if !isMeaningfulTodoContent(it.Content) {
				continue
			}
			fmt.Fprintf(&b, "%s=%s;", it.ID, it.Status)
		}
	}
	for _, task := range tools.SnapshotTasks() {
		fmt.Fprintf(&b, "task:%s=%s;", task.ID, task.Status)
	}
	return b.String()
}
