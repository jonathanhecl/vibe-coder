package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// promptCapture keeps what the user types while a turn is running. The terminal
// is already in non-canonical mode for the ESC monitor, so those bytes reach the
// UI; instead of dropping everything that is not ESC, they build a draft and a
// queue that the composer picks up when the turn ends.
//
// The draft line is painted at the end of the transcript and erased before every
// write, so streamed output never glues itself to what the user is typing. The
// capture owns its own mutex: the UI writers hold PlainUI.mu, and taking it here
// would deadlock.
type promptCapture struct {
	mu    sync.Mutex
	raw   io.Writer
	style Style

	enabled  bool
	spinning bool
	visible  bool
	// atLineStart reports that the cursor sits at column 0 of a fresh row, which
	// is the only place where a live draft line can be painted without
	// overwriting streamed content.
	atLineStart bool

	draft []rune
	queue []string

	// parser state across reads: the monitor gets bytes in arbitrary chunks, so
	// an escape sequence or a multi-byte rune can arrive split.
	state   captureState
	seq     []byte
	paste   []byte
	utf8buf []byte
	escAt   time.Time
}

type captureState int

const (
	captureNormal captureState = iota
	captureEsc
	captureCSI
	capturePaste
	captureUTF8
)

// captureESCWindow is how close two ESC presses must be to cancel the run.
const captureESCWindow = 500 * time.Millisecond

// capturePasteLimit bounds a paste captured mid-run so a huge clipboard cannot
// grow without bound.
const capturePasteLimit = 64 * 1024

// setRaw installs the terminal writer the capture paints to. Without it the
// capture still parses ESC (the cancel gesture) but stores nothing.
func (c *promptCapture) setRaw(w io.Writer, st Style) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.raw = w
	c.style = st
}

func (c *promptCapture) available() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.raw != nil
}

// setEnabled turns capture on for a run and clears the painted line when it ends.
func (c *promptCapture) setEnabled(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// A half-read escape sequence from the previous boundary must not leak into
	// the next run.
	c.resetParserLocked()
	if on {
		c.enabled = true
		c.atLineStart = true
		return
	}
	c.enabled = false
	c.clearPaintedLocked()
}

// setSpinning tells the capture the animated tool line owns the cursor row. The
// spinner renders the draft inside its own block, so the capture must not paint
// a second line while it runs.
func (c *promptCapture) setSpinning(on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spinning = on
	if on {
		c.clearPaintedLocked()
	}
}

// draftLine returns the styled line the spinner appends to its frames, or ""
// when there is nothing to show.
func (c *promptCapture) draftLine() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lineLocked()
}

// write is the terminal writer's front door: it erases the draft before the
// content and repaints it after the write leaves the cursor on a fresh row.
func (c *promptCapture) write(w io.Writer, p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.raw == nil {
		return w.Write(p)
	}
	if c.visible {
		c.clearPaintedLocked()
	}
	n, err := w.Write(p)
	if len(p) > 0 {
		c.atLineStart = p[len(p)-1] == '\n'
	}
	if c.enabled && c.atLineStart && !c.spinning {
		c.paintLocked()
	}
	return n, err
}

// handle feeds terminal bytes to the parser. It reports the double-ESC cancel
// gesture so the monitor can interrupt the run; everything else edits the draft.
func (c *promptCapture) handle(p []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	cancelled := false
	for _, b := range p {
		if c.feedLocked(b) {
			cancelled = true
		}
	}
	if c.enabled {
		c.repaintLocked()
	}
	return cancelled
}

// feedLocked advances the parser by one byte. It returns true when the byte
// completes a double-ESC.
func (c *promptCapture) feedLocked(b byte) bool {
	switch c.state {
	case captureEsc:
		if b == 0x1b {
			if time.Since(c.escAt) < captureESCWindow {
				c.resetParserLocked()
				return true
			}
			c.escAt = time.Now()
			return false
		}
		if b == '[' {
			c.state = captureCSI
			c.seq = c.seq[:0]
			return false
		}
		// A lone ESC followed by a normal key: the escape is dropped and the
		// key is handled as input.
		c.state = captureNormal
		return c.feedLocked(b)

	case captureCSI:
		c.seq = append(c.seq, b)
		if b >= 0x40 && b <= 0x7e {
			c.finishCSILocked()
		}
		return false

	case capturePaste:
		c.paste = append(c.paste, b)
		if len(c.paste) > capturePasteLimit {
			c.paste = c.paste[:capturePasteLimit]
		}
		if strings.HasSuffix(string(c.paste), "\x1b[201~") {
			body := c.paste[:len(c.paste)-len("\x1b[201~")]
			c.paste = c.paste[:0]
			c.state = captureNormal
			c.insertLocked(strings.ReplaceAll(string(body), "\r\n", "\n"))
		}
		return false

	case captureUTF8:
		c.utf8buf = append(c.utf8buf, b)
		if r, size := utf8.DecodeRune(c.utf8buf); r != utf8.RuneError || size == len(c.utf8buf) {
			c.utf8buf = c.utf8buf[:0]
			c.state = captureNormal
			if r != utf8.RuneError {
				c.insertLocked(string(r))
			}
		} else if len(c.utf8buf) >= utf8.UTFMax {
			c.utf8buf = c.utf8buf[:0]
			c.state = captureNormal
		}
		return false
	}

	// captureNormal
	switch {
	case b == 0x1b:
		c.state = captureEsc
		c.escAt = time.Now()
	case b == 0x7f || b == 0x08:
		c.backspaceLocked()
	case b == 0x15: // Ctrl+U clears the draft
		c.draft = c.draft[:0]
	case b == '\r' || b == '\n':
		c.enqueueLocked()
	case b >= 0x20 && b < 0x80:
		c.insertLocked(string(rune(b)))
	case b >= 0x80:
		c.state = captureUTF8
		c.utf8buf = append(c.utf8buf[:0], b)
	}
	return false
}

// finishCSILocked interprets a complete CSI sequence: paste markers open the
// paste state, Delete behaves like Backspace, and cursor keys are ignored (the
// composer is where a draft is edited properly).
func (c *promptCapture) finishCSILocked() {
	seq := string(c.seq)
	c.seq = c.seq[:0]
	c.state = captureNormal
	switch seq {
	case "200~":
		c.state = capturePaste
		c.paste = c.paste[:0]
	case "201~":
		// Stray end marker: nothing to close.
	case "3~":
		c.backspaceLocked()
	}
}

func (c *promptCapture) resetParserLocked() {
	c.state = captureNormal
	c.seq = c.seq[:0]
	c.utf8buf = c.utf8buf[:0]
}

func (c *promptCapture) insertLocked(s string) {
	if !c.enabled || s == "" {
		return
	}
	c.draft = append(c.draft, []rune(s)...)
}

func (c *promptCapture) backspaceLocked() {
	if !c.enabled || len(c.draft) == 0 {
		return
	}
	c.draft = c.draft[:len(c.draft)-1]
}

// enqueueLocked moves the finished draft into the queue. When the animated line
// owns the cursor row the queue count is shown there instead of printing a
// permanent line, which would desynchronize the spinner's row accounting.
func (c *promptCapture) enqueueLocked() {
	text := strings.TrimSpace(string(c.draft))
	if text == "" {
		return
	}
	c.queue = append(c.queue, text)
	c.draft = c.draft[:0]
	c.clearPaintedLocked()
	if c.spinning {
		return
	}
	if !c.atLineStart {
		fmt.Fprint(c.raw, "\n")
		c.atLineStart = true
	}
	notice := c.style.Green("✎") + " " + c.style.Dim(fmt.Sprintf("queued (%d):", len(c.queue))) + " " + text
	fmt.Fprintf(c.raw, "%s\n", truncateANSIWidth(notice, c.colsLocked()-2))
}

// take drains the draft and the queue for the composer.
func (c *promptCapture) take() (string, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	draft := strings.TrimSpace(string(c.draft))
	queue := append([]string(nil), c.queue...)
	c.draft = c.draft[:0]
	c.queue = nil
	c.clearPaintedLocked()
	return draft, queue
}

// setQueue stores the prompts the composer left pending, so the next prompt
// shows them again instead of dropping them.
func (c *promptCapture) setQueue(queue []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queue = append([]string(nil), queue...)
}

// pendingQueue reports how many prompts are queued (used by tests and status).
func (c *promptCapture) pendingQueue() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.queue)
}

func (c *promptCapture) repaintLocked() {
	c.clearPaintedLocked()
	c.paintLocked()
}

func (c *promptCapture) clearPaintedLocked() {
	if !c.visible || c.raw == nil {
		return
	}
	fmt.Fprint(c.raw, "\r\x1b[2K")
	c.visible = false
}

func (c *promptCapture) paintLocked() {
	if !c.enabled || c.visible || c.spinning || !c.atLineStart || c.raw == nil {
		return
	}
	line := c.lineLocked()
	if line == "" {
		return
	}
	fmt.Fprint(c.raw, "\r\x1b[2K", line)
	c.visible = true
}

// lineLocked renders one physical row: the draft sanitized to a single line and
// truncated to the terminal width so the cursor math stays exact.
func (c *promptCapture) lineLocked() string {
	draft := strings.Join(strings.Fields(string(c.draft)), " ")
	if draft == "" && len(c.queue) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(c.style.Dim("✎ "))
	if draft == "" {
		b.WriteString(c.style.Dim(fmt.Sprintf("%d queued prompt(s) · wait for the turn to finish", len(c.queue))))
	} else {
		b.WriteString(draft)
		if len(c.queue) > 0 {
			b.WriteString(c.style.Dim(fmt.Sprintf("  (+%d queued)", len(c.queue))))
		}
	}
	return truncateANSIWidth(b.String(), c.colsLocked()-2)
}

func (c *promptCapture) colsLocked() int {
	cols := terminalColumns(c.raw)
	if cols <= 0 {
		cols = 80
	}
	return cols
}

// liveWriter is the writer every UI print goes through in the hybrid TUI. It
// only adds the draft-line protocol; the bytes are written untouched.
type liveWriter struct {
	ui *PlainUI
	w  io.Writer
}

func (l *liveWriter) Write(p []byte) (int, error) {
	return l.ui.capture.write(l.w, p)
}

// Fd forwards the terminal descriptor so width detection keeps working with the
// writer wrapped.
func (l *liveWriter) Fd() uintptr {
	if f, ok := l.w.(interface{ Fd() uintptr }); ok {
		return f.Fd()
	}
	return 0
}
