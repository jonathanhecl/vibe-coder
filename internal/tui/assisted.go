package tui

import (
	"fmt"
	"strings"
	"time"
)

// AssistedOutcome describes what assisted mode did with a proposed action.
type AssistedOutcome int

const (
	// AssistedApproved: the decision model classified the action as safe, so it
	// ran without a permission prompt.
	AssistedApproved AssistedOutcome = iota
	// AssistedDangerous: the decision model classified the action as risky, so
	// the normal permission prompt is shown.
	AssistedDangerous
	// AssistedUnsupported: the decision model cannot classify this kind of
	// action, so the normal permission prompt is shown.
	AssistedUnsupported
	// AssistedUnavailable: the decision model was missing, disabled, or failed,
	// so assisted mode turned itself off and the prompt is shown.
	AssistedUnavailable
)

// AssistedNotice carries the result of an assisted-mode review to the UI.
type AssistedNotice struct {
	Tool    string
	Outcome AssistedOutcome
	Elapsed time.Duration
}

// NotifyAssisted renders the assisted-mode review outcome. It is optional: the
// permissions manager reaches it through a method assertion. Printing it makes
// it clear why an action ran without a prompt, or why the user is being asked.
func (u *PlainUI) NotifyAssisted(notice AssistedNotice) {
	tool := strings.TrimSpace(notice.Tool)
	if tool == "" {
		tool = "action"
	}
	timing := ""
	if notice.Elapsed > 0 {
		timing = " (" + formatDecisionDuration(notice.Elapsed) + ")"
	}
	var line string
	switch notice.Outcome {
	case AssistedApproved:
		line = fmt.Sprintf("%s %s %s", u.style.BrightGreen("✓"), u.style.BrightWhite(tool),
			u.style.Dim("approved by JEV Style assisted mode"+timing))
	case AssistedDangerous:
		line = fmt.Sprintf("%s %s %s", u.style.Yellow("⚠"), u.style.BrightWhite(tool),
			u.style.Dim("flagged as risky by JEV Style"+timing+" · asking for permission"))
	case AssistedUnsupported:
		line = fmt.Sprintf("%s %s %s", u.style.Yellow("⚠"), u.style.BrightWhite(tool),
			u.style.Dim("cannot be classified by JEV Style · asking for permission"))
	default: // AssistedUnavailable
		line = fmt.Sprintf("%s %s", u.style.Yellow("⚠"),
			u.style.Dim("JEV Style unavailable · assisted mode disabled · asking for permission"))
	}
	u.stopSpinner()
	u.mu.Lock()
	defer u.mu.Unlock()
	u.flushPendingToolLocked()
	fmt.Fprintln(u.out, line)
}

// formatDecisionDuration renders a decision-model latency for the assisted-mode
// notice: sub-millisecond values show as "<1ms", otherwise rounded to the
// millisecond (for example "70ms" or "1.2s").
func formatDecisionDuration(d time.Duration) string {
	if d < time.Millisecond {
		return "<1ms"
	}
	return d.Round(time.Millisecond).String()
}
