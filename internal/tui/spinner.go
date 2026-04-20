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
	label  string
	stopCh chan struct{}
	doneCh chan struct{}
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
		label:  label,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
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
// rewrites it in place every ~90ms until stopCh is closed. The cursor is
// kept on the same line (no \n) so stopSpinner can wipe it cleanly.
func (u *PlainUI) runSpinner(sp *spinner) {
	defer close(sp.doneCh)

	ticker := time.NewTicker(spinnerInterval)
	defer ticker.Stop()

	frame := 0
	paintFrame := func() {
		u.mu.Lock()
		fmt.Fprint(u.out, u.style.ClearPendingLine())
		fmt.Fprintf(u.out, "%s %s",
			u.style.BrightBlue(spinnerFrames[frame%len(spinnerFrames)]),
			sp.label,
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

// spinnerStateMu is exported only for clarity in tests; do not use directly.
var _ = sync.Mutex{}
