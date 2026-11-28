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

	// The prompt box is visible from the start, so a user looking at a running
	// turn knows where to type.
	if got := ui.spinnerFrameText("Shell $ ls", 0, "1s"); !strings.Contains(got, "type to queue a prompt") {
		t.Fatalf("the spinner must show the prompt box: %q", got)
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

	out := raw.String()
	if !strings.Contains(out, "╭") || !strings.Contains(out, "type to queue a prompt") {
		t.Fatalf("no prompt box after enabling capture: %q", out)
	}
}

// Queued prompts are a pipeline: the next one runs when the turn ends, without
// another Enter, in the order they were typed.
func TestCaptureDequeuesInOrder(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	c.handle([]byte("primero\r"))
	c.handle([]byte("segundo\r"))
	if got := c.pendingQueue(); got != 2 {
		t.Fatalf("queued=%d, want 2", got)
	}

	for _, want := range []string{"primero", "segundo"} {
		got, ok := c.dequeue()
		if !ok || got != want {
			t.Fatalf("dequeue = (%q, %t), want %q", got, ok, want)
		}
	}
	if _, ok := c.dequeue(); ok {
		t.Fatal("an empty queue must not dequeue")
	}
}

// The unfinished draft is handed to the composer without touching the queue.
func TestCaptureTakeDraftLeavesQueue(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	c.handle([]byte("en cola\r"))
	c.handle([]byte("a medio escribir"))

	draft := c.takeDraft()
	if draft != "a medio escribir" {
		t.Fatalf("draft=%q", draft)
	}
	if got := c.pendingQueue(); got != 1 {
		t.Fatalf("takeDraft must not consume the queue, got %d", got)
	}
}

// A cancellation holds the queue for one prompt: Ctrl+C must not run what was
// queued on the way out, but the next prompt reviews it instead of losing it.
func TestCaptureQueueHoldIsOneShot(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	if c.takeHold() {
		t.Fatal("nothing holds the queue yet")
	}
	c.holdNext()
	if !c.takeHold() {
		t.Fatal("holdNext must hold the next prompt")
	}
	if c.takeHold() {
		t.Fatal("the hold must be consumed once")
	}
}

// A multi-line draft keeps its line breaks inside the box instead of collapsing
// into one unreadable row.
func TestCaptureBoxKeepsLineBreaks(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	c.handle([]byte("\x1b[200~linea uno\nlinea dos\x1b[201~"))
	out := raw.String()

	if !strings.Contains(out, "linea uno") || !strings.Contains(out, "linea dos") {
		t.Fatalf("both lines must render: %q", out)
	}
	if got := strings.Count(out, "\n"); got < 3 {
		t.Fatalf("a two-line draft needs top border + 2 rows + bottom, got %d newlines: %q", got, out)
	}
}

// A draft longer than the box scrolls to its tail: the part being typed is what
// stays visible.
func TestCaptureBoxShowsTheTailOfALongDraft(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	head := "INICIO-DEL-TEXTO"
	tail := "FINAL-QUE-ESTOY-ESCRIBIENDO"
	c.handle([]byte(head + strings.Repeat("x", 1000) + tail))
	out := raw.String()

	// The tail may wrap across the last row, so check its closing characters.
	if !strings.Contains(out, tail[len(tail)-6:]) {
		t.Fatalf("the tail must stay visible: %q", out)
	}
	if strings.Contains(out, head) {
		t.Fatalf("a long draft must scroll, not overflow its box: %q", out)
	}
}

// The painted box is erased by its full row count before content is printed, so
// no border tails survive under a streamed line.
func TestCaptureBoxIsClearedByRows(t *testing.T) {
	t.Parallel()
	var raw bytes.Buffer
	c := newTestCapture(&raw)

	c.handle([]byte("hola"))
	raw.Reset()
	if _, err := c.write(&raw, []byte("respuesta\n")); err != nil {
		t.Fatal(err)
	}
	out := raw.String()
	if !strings.Contains(out, "\x1b[1A") {
		t.Fatalf("a multi-row box must move up to erase every row: %q", out)
	}
	if !strings.Contains(out, "respuesta") {
		t.Fatalf("content missing: %q", out)
	}
}
