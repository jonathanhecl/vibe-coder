package agent

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// maxPendingNames caps how many open TODO items a summary names. A long
// checklist would turn the notice into a second /todos listing, so the rest is
// reported as a count.
const maxPendingNames = 2

// pendingNameMaxLen trims one TODO title so the whole notice stays on a line.
const pendingNameMaxLen = 60

// PendingWorkSummary describes, in one line, what a finished turn left open:
// the TODO items still pending or in progress and whether a mission is still
// active. It returns "" when there is nothing to report, so a caller can print
// it after every turn.
//
// A turn stops for many legitimate reasons - the step cap, a repeated tool
// call, an interrupt, a paused mission - and every one of them used to return
// the user to the prompt as if the task were finished. This is what makes the
// state visible instead of silent.
func (a *Agent) PendingWorkSummary() string {
	if a == nil {
		return ""
	}
	var parts []string
	if inProgress, pending := a.openTodoItems(); len(inProgress)+len(pending) > 0 {
		parts = append(parts, todoSummary(inProgress, pending))
	}
	if m := a.MissionSnapshot(); m.Status == tools.MissionStatusActive {
		mission := "mission still active"
		if goal := strings.TrimSpace(m.Goal); goal != "" {
			mission += fmt.Sprintf(" (%q)", trimPendingName(goal))
		}
		parts = append(parts, mission)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "; ") + ` — send "continue" to resume, or /todos to review the checklist.`
}

// openTodoItems splits the meaningful checklist items that are not done.
func (a *Agent) openTodoItems() (inProgress, pending []string) {
	tw := a.todoWriteTool()
	if tw == nil {
		return nil, nil
	}
	for _, it := range tw.Store().Snapshot() {
		if !isMeaningfulTodoContent(it.Content) {
			continue
		}
		switch it.Status {
		case tools.TodoStatusInProgress:
			inProgress = append(inProgress, it.Content)
		case tools.TodoStatusCompleted:
			// Finished: nothing to report.
		default:
			pending = append(pending, it.Content)
		}
	}
	return inProgress, pending
}

// todoSummary renders "3 TODO items pending (in progress "x", pending "y" and
// 1 more)" without letting a long checklist fill the terminal.
func todoSummary(inProgress, pending []string) string {
	total := len(inProgress) + len(pending)
	head := fmt.Sprintf("%d TODO item%s pending", total, plural(total))
	var named []string
	remaining := 0
	add := func(label string, items []string) {
		for _, it := range items {
			if len(named) >= maxPendingNames {
				remaining++
				continue
			}
			named = append(named, fmt.Sprintf("%s %q", label, trimPendingName(it)))
		}
	}
	add("in progress", inProgress)
	add("pending", pending)
	if len(named) == 0 {
		return head
	}
	detail := strings.Join(named, ", ")
	if remaining > 0 {
		detail += fmt.Sprintf(" and %d more", remaining)
	}
	return fmt.Sprintf("%s (%s)", head, detail)
}

// trimPendingName keeps one checklist title short and on a single line.
func trimPendingName(content string) string {
	content = strings.Join(strings.Fields(content), " ")
	if len(content) <= pendingNameMaxLen {
		return content
	}
	cut := pendingNameMaxLen
	for cut > 0 && !isASCIIBoundary(content, cut) {
		cut--
	}
	return strings.TrimSpace(content[:cut]) + "…"
}

// isASCIIBoundary reports whether index i starts a rune, so truncation never
// splits a multi-byte character.
func isASCIIBoundary(s string, i int) bool {
	return i <= 0 || i >= len(s) || s[i]&0xC0 != 0x80
}

// plural returns "s" unless n is 1.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
