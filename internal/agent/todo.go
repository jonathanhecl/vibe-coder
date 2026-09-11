package agent

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func (a *Agent) hasPendingTodos() bool {
	tw := a.todoWriteTool()
	if tw == nil {
		return false
	}
	for _, it := range tw.Store().Snapshot() {
		if !isMeaningfulTodoContent(it.Content) {
			continue
		}
		if it.Status == tools.TodoStatusPending || it.Status == tools.TodoStatusInProgress {
			return true
		}
	}
	return false
}
func (a *Agent) todoProgressNote() string {
	tw := a.todoWriteTool()
	if tw == nil {
		return ""
	}
	snap := tw.Store().Snapshot()
	if len(snap) == 0 {
		return ""
	}
	var b strings.Builder
	meaningful := 0
	for _, it := range snap {
		if isMeaningfulTodoContent(it.Content) {
			meaningful++
		}
	}
	if meaningful == 0 {
		return ""
	}
	fmt.Fprintf(&b, "TODO progress (%d items):\n", meaningful)
	for _, it := range snap {
		if !isMeaningfulTodoContent(it.Content) {
			continue
		}
		status := it.Status
		if status == "" {
			status = tools.TodoStatusPending
		}
		switch status {
		case tools.TodoStatusCompleted:
			fmt.Fprintf(&b, "  [DONE] %s: %s\n", it.ID, it.Content)
		case tools.TodoStatusInProgress:
			fmt.Fprintf(&b, "  [IN PROGRESS] %s: %s — execute this now and mark DONE when finished.\n", it.ID, it.Content)
		default:
			fmt.Fprintf(&b, "  [PENDING] %s: %s\n", it.ID, it.Content)
		}
	}
	b.WriteString("Use the data from earlier tool results to complete pending steps. Do not re-investigate what you already know.")
	return b.String()
}

func (a *Agent) todoWriteTool() *tools.TodoWriteTool {
	// Tests may use partial registries, so missing or replaced TodoWrite is valid.
	tw, _ := a.reg.Get("TodoWrite").(*tools.TodoWriteTool)
	return tw
}

// addTodoProgressNoteIfChanged injects the TODO progress note only when it
// differs from the last injected one. The list is sticky across turns, so
// without this every loop iteration would append a duplicate copy to the
// transcript and burn context on all following turns.
func (a *Agent) addTodoProgressNoteIfChanged() {
	note := a.todoProgressNote()
	if note == "" {
		return
	}
	a.mu.Lock()
	changed := note != a.lastTodoNote
	if changed {
		a.lastTodoNote = note
	}
	a.mu.Unlock()
	if changed {
		// User-role so the model treats it as an actionable reminder instead of
		// its own completed output (which makes some models reply empty).
		a.sess.AddRuntimeReminder(note)
	}
}

// resetTodoNoteDedup forgets the last injected note so the next turn starts
// fresh (e.g. an older copy may have been compacted away since).
func (a *Agent) resetTodoNoteDedup() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastTodoNote = ""
}
func isMeaningfulTodoContent(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	for _, r := range content {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}
