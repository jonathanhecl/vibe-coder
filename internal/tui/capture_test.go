package tui

import (
	"bytes"
	"strings"
	"testing"
)

func newTestCapture(raw *bytes.Buffer) *promptCapture {
	c := &promptCapture{}
	c.setRaw(raw, NewStyleForTest(true))
	c.setEnabled(true)
	return c
}

// Typing during a run builds a draft; Enter moves it to the queue so the turn
// does not lose it.
func TestCaptureDraftBecomesQueuedPrompt(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	c.handle([]byte("arregla el test"))
	c.handle([]byte("\r"))

	if got := c.pendingQueue(); got != 1 {
		t.Fatalf("queued=%d, want 1", got)
	}
	draft, queue := c.take()
	if draft != "" {
		t.Fatalf("draft=%q, want empty after Enter", draft)
	}
	if len(queue) != 1 || queue[0] != "arregla el test" {
		t.Fatalf("queue=%#v", queue)
	}
	// The transcript gets a confirmation line so the user sees it was accepted.
	if !strings.Contains(raw.String(), "queued (1)") {
		t.Fatalf("missing queue confirmation: %q", raw.String())
	}
}

// A run can end with text still being typed: it stays a draft for the composer.
func TestCaptureKeepsUnfinishedDraft(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	c.handle([]byte("a medio escribir"))
	draft, queue := c.take()

	if draft != "a medio escribir" || len(queue) != 0 {
		t.Fatalf("draft=%q queue=%#v", draft, queue)
	}
}

// Backspace edits the draft, Ctrl+U clears it, and a Delete escape sequence
// behaves like backspace.
func TestCaptureEditingKeys(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	c.handle([]byte("abc"))
	c.handle([]byte{0x7f})
	c.handle([]byte("\x1b[3~"))
	draft, _ := c.take()
	if draft != "a" {
		t.Fatalf("draft=%q, want %q", draft, "a")
	}

	c.handle([]byte("borrame"))
	c.handle([]byte{0x15})
	draft, _ = c.take()
	if draft != "" {
		t.Fatalf("Ctrl+U left %q", draft)
	}
}

// A multi-byte rune can arrive split across reads and must still be inserted.
func TestCaptureSplitsUTF8AcrossReads(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	c.handle([]byte{0xc3})
	c.handle([]byte{0xb1})
	draft, _ := c.take()
	if draft != "ñ" {
		t.Fatalf("draft=%q, want ñ", draft)
	}
}

// Bracketed paste inserts its payload as-is, newlines included, instead of
// submitting on the first line.
func TestCaptureBracketedPaste(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	c.handle([]byte("\x1b[200~primera\nsegunda\x1b[201~"))
	if got := c.pendingQueue(); got != 0 {
		t.Fatalf("a paste must not submit, queued=%d", got)
	}
	draft, _ := c.take()
	if draft != "primera\nsegunda" {
		t.Fatalf("draft=%q", draft)
	}
}

// Two ESC presses still cancel the run.
func TestCaptureDoubleESC(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	if c.handle([]byte{0x1b}) {
		t.Fatal("a single ESC must not cancel")
	}
	if !c.handle([]byte{0x1b}) {
		t.Fatal("two ESC presses must cancel the run")
	}
}

// The draft line is erased before streamed content and repainted after, so it
// never glues itself to the model's output.
func TestCaptureDraftLineAroundStreamedWrite(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	c.handle([]byte("hola"))
	if !strings.Contains(raw.String(), "hola") {
		t.Fatalf("draft was not painted: %q", raw.String())
	}
	raw.Reset()

	if _, err := c.write(&raw, []byte("respuesta\n")); err != nil {
		t.Fatal(err)
	}
	out := raw.String()
	content := strings.Index(out, "respuesta")
	draft := strings.LastIndex(out, "hola")
	if content < 0 || draft < 0 {
		t.Fatalf("missing content or draft: %q", out)
	}
	if draft < content {
		t.Fatalf("the draft must be repainted after the streamed line: %q", out)
	}
	if !strings.Contains(out[:content], "\r\x1b[2K") {
		t.Fatalf("the draft row must be cleared before content: %q", out[:content])
	}
}

// A partial line (no trailing newline) must not get a draft row painted under
// it: the cursor is not at a line start and the next chunk continues the line.
func TestCaptureWaitsForLineStart(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	c.handle([]byte("hola"))
	raw.Reset()
	if _, err := c.write(&raw, []byte("parcial")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw.String(), "✎") {
		t.Fatalf("draft must not be painted mid-line: %q", raw.String())
	}
}

// While a tool runs, the spinner block carries the draft as a second row so the
// animation keeps accounting for both rows and clears them together.
func TestSpinnerFrameCarriesDraft(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer

	ui := &PlainUI{out: &raw, style: NewStyleForTest(true)}
	ui.capture.setRaw(&raw, ui.style)
	ui.capture.setEnabled(true)

	// The prompt affordance is visible from the start, so a user looking at a
	// running turn knows where to type.
	if got := ui.spinnerFrameText("Shell $ ls", 0, "1s"); !strings.Contains(got, "✎") {
		t.Fatalf("the spinner must show the prompt hint: %q", got)
	}

	ui.capture.handle([]byte("mientras corre"))
	frame := ui.spinnerFrameText("Shell $ ls", 0, "1s")
	if !strings.Contains(frame, "\n") || !strings.Contains(frame, "mientras corre") {
		t.Fatalf("draft missing from the spinner block: %q", frame)
	}
}

// The prompt line is painted the moment the run starts, not only after the first
// keystroke: an invisible composer looks exactly like a missing feature.
func TestCaptureShowsPromptLineOnEnable(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer

	c := &promptCapture{}
	c.setRaw(&raw, NewStyleForTest(true))
	c.setEnabled(true)

	if !strings.Contains(raw.String(), "✎") {
		t.Fatalf("no prompt line after enabling capture: %q", raw.String())
	}
}
