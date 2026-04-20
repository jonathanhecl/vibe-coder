package tui

import (
	"fmt"
	"sync"
	"time"
)

// spinnerFrames is the Braille dot animation Cursor and most modern CLI tools
// use for "in progress" feedback. Ten frames at ~90ms feels alive without
// causing flicker.
var spinnerFrames = []string{
	"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏",
}

const spinnerInterval = 90 * time.Millisecond

// spinner is a single in-flight animation that owns a goroutine until it is
// asked to stop. It is paired with PlainUI through PlainUI.startSpinner /
// PlainUI.stopSpinner so the animation is always cleared from the line
// before any other content is printed.
type spinner struct {
	label   string
	started time.Time
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// startSpinner replaces any in-flight animation with a new one. Safe to call
// without holding u.mu; it acquires u.spinnerMu internally and the goroutine
// itself takes u.mu only while writing a frame.
//
// In non-TTY mode (NO_COLOR, redirected stdout, tests) it is a no-op so that
// scripts and unit tests keep producing deterministic plain output.
func (u *PlainUI) startSpinner(label string) {
	if !u.style.Enabled() {
		return
	}

	u.spinnerMu.Lock()
	if u.spinner != nil {
		u.spinnerMu.Unlock()
		u.stopSpinner()
		u.spinnerMu.Lock()
	}
	sp := &spinner{
		label:   label,
		started: time.Now(),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
	u.spinner = sp
	u.spinnerMu.Unlock()

	go u.runSpinner(sp)
}

// stopSpinner halts the current animation (if any) and clears its line so
// the next print starts on a clean column. Safe to call multiple times.
//
// IMPORTANT: never call this while holding u.mu — runSpinner takes u.mu to
// paint frames, so doing so would deadlock waiting for doneCh.
func (u *PlainUI) stopSpinner() {
	u.spinnerMu.Lock()
	sp := u.spinner
	u.spinner = nil
	u.spinnerMu.Unlock()
	if sp == nil {
		return
	}
	close(sp.stopCh)
	<-sp.doneCh

	u.mu.Lock()
	fmt.Fprint(u.out, u.style.ClearPendingLine())
	u.mu.Unlock()
}

// runSpinner is the per-spinner goroutine. It paints a single line and
// rewrites it in place every ~90ms until stopCh is closed.
//
// To avoid the visible flicker of "clear, then write" (which left the line
// blank for a moment between frames), each frame is sent in ONE write that:
//  1. moves the cursor to column 0 with \r (no clearing yet),
//  2. writes the new frame + label + elapsed counter,
//  3. clears from the cursor to the end of the line with \x1b[K so any
//     leftover characters from a longer previous frame disappear.
//
// This produces a buttery-smooth update — the terminal commits the entire
// new state atomically and never shows a half-empty row.
func (u *PlainUI) runSpinner(sp *spinner) {
	defer close(sp.doneCh)

	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()

	frame := 0
	paintFrame := func() {
		elapsed := formatElapsed(time.Since(sp.started))
		u.mu.Lock()
		fmt.Fprintf(u.out, "\r%s %s %s\x1b[K",
			u.style.BrightBlue(spinnerFrames[frame%len(spinnerFrames)]),
			sp.label,
			u.style.Dim("("+elapsed+")"),
		)
		u.mu.Unlock()
	}

	paintFrame()
	for {
		select {
		case <-sp.stopCh:
			return
		case <-ticker.C:
			frame++
			paintFrame()
		}
	}
}

// StartWaiting shows a labeled spinner while the agent is waiting for a slow
// operation (e.g. the first token from Ollama). It is a no-op in non-TTY
// modes. Calling it while a spinner is already running replaces the label.
func (u *PlainUI) StartWaiting(label string) {
	u.startSpinner(u.style.Dim(label))
}

// StopWaiting hides the spinner started by StartWaiting. Idempotent.
func (u *PlainUI) StopWaiting() {
	u.stopSpinner()
}

// formatElapsed renders a human-friendly duration meant to fit next to a
// spinner: "0s", "5s", "1m23s", "2h05m". Resolution is intentionally one
// second so the counter doesn't jitter on every frame.
func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}
	total := int(d.Seconds())
	if total < 60 {
		return fmt.Sprintf("%ds", total)
	}
	mins := total / 60
	secs := total % 60
	if mins < 60 {
		return fmt.Sprintf("%dm%02ds", mins, secs)
	}
	hours := mins / 60
	mins = mins % 60
	return fmt.Sprintf("%dh%02dm", hours, mins)
}

var _ = sync.Mutex{} // keep sync import for future extensions
