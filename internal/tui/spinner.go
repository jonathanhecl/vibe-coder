package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"
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
	u.clearPendingLocked()
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
		text := fmt.Sprintf("%s %s %s",
			u.style.BrightBlue(spinnerFrames[frame%len(spinnerFrames)]),
			sp.label,
			u.style.Dim("("+elapsed+")"),
		)
		u.mu.Lock()
		// A single row keeps the seamless in-place rewrite; a wrapped one has to
		// be cleared first, because after a wrap the cursor sits at the end of
		// the last row and a bare \r would repaint the line one row too low.
		prefix := "\r"
		if u.pendingRows > 1 {
			prefix = clearPendingRows(u.pendingRows)
		}
		fmt.Fprintf(u.out, "%s%s\x1b[K", prefix, text)
		u.pendingRows = wrapRows(text, u.terminalCols())
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

// clearPendingLocked erases the animated line, however many rows it wrapped
// into, and forgets its height. The cursor is left at the start of its first
// row so the caller can print the final card over it.
func (u *PlainUI) clearPendingLocked() {
	if u.out == nil {
		return
	}
	if u.pendingRows > 0 {
		fmt.Fprint(u.out, clearPendingRows(u.pendingRows))
		u.pendingRows = 0
		return
	}
	fmt.Fprint(u.out, u.style.ClearPendingLine())
}

// clearPendingRows returns the escape sequence that erases rows rows of an
// animated line and leaves the cursor at the start of the first one.
func clearPendingRows(rows int) string {
	if rows < 1 {
		rows = 1
	}
	var b strings.Builder
	b.WriteString("\r\x1b[2K")
	for i := 1; i < rows; i++ {
		b.WriteString("\x1b[1A\x1b[2K")
	}
	return b.String()
}

// wrapRows is how many terminal rows text occupies at cols columns. Escape
// sequences are not counted: the terminal does not reserve space for them.
func wrapRows(text string, cols int) int {
	if cols <= 0 {
		cols = 80
	}
	width := visibleWidth(text)
	if width <= 0 {
		return 1
	}
	return (width + cols - 1) / cols
}

// terminalCols is the terminal width the animated line must fit, or a plain 80
// when the writer cannot report one.
func (u *PlainUI) terminalCols() int {
	type fdHaver interface{ Fd() uintptr }
	if f, ok := u.out.(fdHaver); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 {
			return width
		}
	}
	return 80
}
