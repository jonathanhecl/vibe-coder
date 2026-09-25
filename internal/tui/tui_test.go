package tui

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAskPermissionReadsLineWhenRawModeUnavailable(t *testing.T) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()
	if _, err := writeEnd.WriteString("2\n"); err != nil {
		t.Fatal(err)
	}

	ui := &PlainUI{
		in:     readEnd,
		out:    &bytes.Buffer{},
		reader: bufio.NewReader(readEnd),
		stopCh: make(chan struct{}),
	}
	if got := ui.AskPermission("Edit", nil); got != DecisionAllowSession {
		t.Fatalf("unexpected permission decision: %v", got)
	}
}

func TestInteractiveInputHidesPasteAndReturnsFullContent(t *testing.T) {
	var out bytes.Buffer
	input, err := readInteractiveInputStream(strings.NewReader("\x1b[200~first line\nsecond line\x1b[201~ + note\n"), &out)
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	if input != "first line\nsecond line + note" {
		t.Fatalf("unexpected interactive input: %q", input)
	}
	if got := out.String(); got != "[block]first line second line[/block] + note\r\n" {
		t.Fatalf("unexpected interactive display: %q", got)
	}
}

func TestFormatPastedBlockSummarizesLongContent(t *testing.T) {
	got := formatPastedBlock(strings.Repeat("x", 100))
	if !strings.HasPrefix(got, "[block]") || !strings.Contains(got, "(36 chars more)") || !strings.HasSuffix(got, "[/block]") {
		t.Fatalf("unexpected block summary: %q", got)
	}
}

func TestFormatPastedBlockDoesNotWriteCarriageReturns(t *testing.T) {
	got := formatPastedBlock("first\rsecond\nthird")
	if got != "[block]first second third[/block]" {
		t.Fatalf("unexpected safe block summary: %q", got)
	}
}

func TestInteractiveInputBackspaceRemovesPastedBlock(t *testing.T) {
	var out bytes.Buffer
	input, err := readInteractiveInputStream(strings.NewReader("\x1b[200~  first\tline  \x1b[201~\b\n"), &out)
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	if input != "" {
		t.Fatalf("expected pasted block to be removed, got %q", input)
	}
	if !strings.Contains(out.String(), "[block]first line[/block]") {
		t.Fatalf("expected compact block preview, got %q", out.String())
	}
	if !strings.Contains(out.String(), "\b") {
		t.Fatalf("expected block erase sequence, got %q", out.String())
	}
}

func TestGetInputSingleLine(t *testing.T) {
	ui := &PlainUI{
		out:    &bytes.Buffer{},
		reader: bufio.NewReader(strings.NewReader("hello\n")),
		stopCh: make(chan struct{}),
	}
	got, err := ui.GetInput("> ")
	if err != nil {
		t.Fatalf("GetInput failed: %v", err)
	}
	if got != "hello" {
		t.Fatalf("unexpected single-line input: %q", got)
	}
}

func TestGetInputMultilineWithMarker(t *testing.T) {
	ui := &PlainUI{
		out:    &bytes.Buffer{},
		reader: bufio.NewReader(strings.NewReader(";;\nline 1\nline 2\n;;\n")),
		stopCh: make(chan struct{}),
	}
	got, err := ui.GetInput("> ")
	if err != nil {
		t.Fatalf("GetInput failed: %v", err)
	}
	if got != "line 1\nline 2" {
		t.Fatalf("unexpected multiline input: %q", got)
	}
}

func TestGetInputBracketedPasteWaitsForEnter(t *testing.T) {
	ui := &PlainUI{
		out:    &bytes.Buffer{},
		reader: bufio.NewReader(strings.NewReader("\x1b[200~line 1\nline 2\x1b[201~\n\n")),
		stopCh: make(chan struct{}),
	}
	got, err := ui.GetInput("> ")
	if err != nil {
		t.Fatalf("GetInput failed: %v", err)
	}
	if got != "line 1\nline 2" {
		t.Fatalf("unexpected bracketed paste input: %q", got)
	}
}

func TestGetInputStripsLiteralBracketedPasteMarkers(t *testing.T) {
	ui := &PlainUI{
		out:    &bytes.Buffer{},
		reader: bufio.NewReader(strings.NewReader("^[[200~python .\\auto.py^[[201~\n")),
		stopCh: make(chan struct{}),
	}
	got, err := ui.GetInput("> ")
	if err != nil {
		t.Fatalf("GetInput failed: %v", err)
	}
	if got != "python .\\auto.py" {
		t.Fatalf("unexpected literal-marker input: %q", got)
	}
}

func TestGetInputCleansMarkersFromManualMultilineInput(t *testing.T) {
	ui := &PlainUI{
		out:    &bytes.Buffer{},
		reader: bufio.NewReader(strings.NewReader(";;\n^[[200~line 1\nline 2^[[201~\n;;\n")),
		stopCh: make(chan struct{}),
	}
	got, err := ui.GetInput("> ")
	if err != nil {
		t.Fatalf("GetInput failed: %v", err)
	}
	if got != "line 1\nline 2" {
		t.Fatalf("unexpected cleaned manual input: %q", got)
	}
}

func TestGetInputBracketedPasteHandlesWindowsLineEndings(t *testing.T) {
	ui := &PlainUI{
		out:    &bytes.Buffer{},
		reader: bufio.NewReader(strings.NewReader("\x1b[200~line 1\r\nline 2\x1b[201~\r\n")),
		stopCh: make(chan struct{}),
	}
	got, err := ui.GetInput("> ")
	if err != nil {
		t.Fatalf("GetInput failed: %v", err)
	}
	if got != "line 1\nline 2" {
		t.Fatalf("unexpected Windows bracketed paste input: %q", got)
	}
}

func TestGetInputBracketedPasteAllowsContinuation(t *testing.T) {
	ui := &PlainUI{
		out:    &bytes.Buffer{},
		reader: bufio.NewReader(strings.NewReader("\x1b[200~line 1\nline 2\x1b[201~ add this\n")),
		stopCh: make(chan struct{}),
	}
	got, err := ui.GetInput("> ")
	if err != nil {
		t.Fatalf("GetInput failed: %v", err)
	}
	if got != "line 1\nline 2 add this" {
		t.Fatalf("unexpected continued bracketed paste input: %q", got)
	}
}

func TestGetInputBufferedMultilinePaste(t *testing.T) {
	ui := &PlainUI{
		out:    &bytes.Buffer{},
		reader: bufio.NewReader(strings.NewReader("line 1\nline 2\nline 3\n\n")),
		stopCh: make(chan struct{}),
	}
	got, err := ui.GetInput("> ")
	if err != nil {
		t.Fatalf("GetInput failed: %v", err)
	}
	if got != "line 1\nline 2\nline 3" {
		t.Fatalf("unexpected buffered multiline input: %q", got)
	}
}

// TestInteractiveInputSwallowsArrowKeys verifies that escape sequences for the
// arrow keys do not leak as literal ESC/letter bytes into the submitted input.
func TestInteractiveInputSwallowsArrowKeys(t *testing.T) {
	var out bytes.Buffer
	// "hi" then Up/Down/Right/Left then Enter. None of the escape sequences
	// should appear in the returned string.
	input := "hi\x1b[A\x1b[B\x1b[C\x1b[D\n"
	got, err := readInteractiveInputStream(strings.NewReader(input), &out)
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	if got != "hi" {
		t.Fatalf("expected arrow keys to be swallowed, got %q", got)
	}
	if strings.Contains(out.String(), "\x1b[A") || strings.Contains(out.String(), "\x1b[") {
		// The cursor-motion output for redraw uses \x1b[C/\x1b[D, which is fine,
		// but the raw input sequence must not be echoed verbatim.
		if !strings.Contains(out.String(), "hi") {
			t.Fatalf("expected 'hi' to be echoed, got %q", out.String())
		}
	}
}

// TestInteractiveInputCtrlUKillsToStart verifies that Ctrl-U removes everything
// before the cursor.
func TestInteractiveInputCtrlUKillsToStart(t *testing.T) {
	var out bytes.Buffer
	// Type "hello", move left twice, then Ctrl-U should clear "hel".
	input := "hello\x1b[D\x1b[D\x15\n"
	got, err := readInteractiveInputStream(strings.NewReader(input), &out)
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	if got != "lo" {
		t.Fatalf("expected Ctrl-U to clear prefix, got %q", got)
	}
}

// TestInteractiveInputCtrlCReturnsEmptyNotEOF verifies that Ctrl+C clears the
// current line and returns an empty string with no error, so the REPL shows a
// fresh prompt instead of exiting.
func TestInteractiveInputCtrlCReturnsEmptyNotEOF(t *testing.T) {
	var out bytes.Buffer
	// Type "hello" then Ctrl-C: should return "", nil (not io.EOF).
	got, err := readInteractiveInputStream(strings.NewReader("hello\x03"), &out)
	if err != nil {
		t.Fatalf("Ctrl-C should not return an error, got: %v", err)
	}
	if got != "" {
		t.Fatalf("Ctrl-C should return empty string, got %q", got)
	}
	if !strings.Contains(out.String(), "^C") {
		t.Fatalf("expected ^C to be echoed, got %q", out.String())
	}
}

// TestInteractiveInputCtrlDOnEmptyReturnsEOF verifies that Ctrl+D on an empty
// line still returns io.EOF (the normal way to quit the REPL).
func TestInteractiveInputCtrlDOnEmptyReturnsEOF(t *testing.T) {
	var out bytes.Buffer
	_, err := readInteractiveInputStream(strings.NewReader("\x04"), &out)
	if err == nil {
		t.Fatal("Ctrl-D on empty line should return io.EOF, got nil")
	}
}

// TestInteractiveInputHistoryRecall verifies that Up recalls the previous
// submitted entry when a history is provided.
func TestInteractiveInputHistoryRecall(t *testing.T) {
	var out bytes.Buffer
	hist := &inputHistory{entries: []string{"previous input"}}
	// Press Up to recall the stored entry, then Enter to submit it.
	got, err := readInteractiveInputStreamWithStyle(strings.NewReader("\x1b[A\n"), &out, Style{}, hist, "")
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	if got != "previous input" {
		t.Fatalf("expected history recall, got %q", got)
	}
}

// TestInteractiveInputHistoryDownRestoresDraft verifies that pressing Down past
// the newest entry restores the in-progress draft.
func TestInteractiveInputHistoryDownRestoresDraft(t *testing.T) {
	var out bytes.Buffer
	hist := &inputHistory{entries: []string{"old"}}
	// Type "draft", Up (recall "old"), Down (restore "draft"), Enter.
	got, err := readInteractiveInputStreamWithStyle(strings.NewReader("draft\x1b[A\x1b[B\n"), &out, Style{}, hist, "")
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	if got != "draft" {
		t.Fatalf("expected draft restored after Down, got %q", got)
	}
}

// TestInputHistoryPersistsToFile verifies that submitted entries are appended to
// the history file and reloaded on the next loadHistory call.
func TestInputHistoryPersistsToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "history.txt")
	h1 := loadHistory(path)
	h1.add("first request")
	h1.add("second request")
	h1.add("second request") // consecutive duplicate is dropped

	h2 := loadHistory(path)
	got := h2.snapshot()
	if len(got) != 2 {
		t.Fatalf("expected 2 persisted entries, got %d: %v", len(got), got)
	}
	if got[0] != "first request" || got[1] != "second request" {
		t.Fatalf("expected [first request, second request], got %v", got)
	}
}

// TestInputHistoryIgnoresEmptyLines verifies that blank submissions are not
// recorded.
func TestInputHistoryIgnoresEmptyLines(t *testing.T) {
	hist := &inputHistory{}
	hist.add("")
	hist.add("   ")
	if got := hist.snapshot(); len(got) != 0 {
		t.Fatalf("expected no history entries, got %v", got)
	}
}

// TestInteractiveInputAltEnterInsertsNewline verifies that Alt+Enter (ESC+CR)
// inserts a newline into the buffer instead of submitting, and the submitted
// result contains the newline.
func TestInteractiveInputAltEnterInsertsNewline(t *testing.T) {
	var out bytes.Buffer
	// Type "line1", Alt+Enter (\x1b\r), type "line2", Enter to submit.
	got, err := readInteractiveInputStream(strings.NewReader("line1\x1b\rline2\n"), &out)
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	if got != "line1\nline2" {
		t.Fatalf("expected multi-line input 'line1\\nline2', got %q", got)
	}
}

// TestInteractiveInputUpDownNavigatesLines verifies that when the buffer is
// multi-line, Up/Down move the cursor between lines instead of recalling
// history.
func TestInteractiveInputUpDownNavigatesLines(t *testing.T) {
	var out bytes.Buffer
	hist := &inputHistory{entries: []string{"old entry"}}
	// Type "aaa", Alt+Enter, "bbb", Up (should move to line 1, not recall
	// history), Ctrl-K (kill from cursor to end of buffer).
	// After Up, cursor lands on the '\n' at index 3; Ctrl-K kills "\nbbb",
	// leaving "aaa". If Up had recalled history, the buffer would be "old entry"
	// and Ctrl-K would yield "".
	got, err := readInteractiveInputStreamWithStyle(
		strings.NewReader("aaa\x1b\rbbb\x1b[A\x0b\n"), &out, Style{}, hist, "")
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	if got != "aaa" {
		t.Fatalf("expected Up to navigate lines not history, got %q", got)
	}
}

// TestRedrawPreservesPrompt verifies that history recall (Up) does not erase
// the prompt. redraw() must move to just after the prompt and erase from
// there, never from column 0.
func TestRedrawPreservesPrompt(t *testing.T) {
	var out bytes.Buffer
	hist := &inputHistory{entries: []string{"old"}}
	prompt := "user > "
	// Type "sasas" then Up to recall "old", then Enter.
	got, err := readInteractiveInputStreamWithStyle(strings.NewReader("sasas\x1b[A\n"), &out, Style{}, hist, prompt)
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	if got != "old" {
		t.Fatalf("expected history recall, got %q", got)
	}
	rendered := out.String()
	// Buffer "sasas" (5 runes) after prompt width 7 sits at term col 12.
	// Correct redraw moves left 5 to col 7 (after prompt), not 12 to col 0.
	if !strings.Contains(rendered, "\x1b[5D\x1b[J") {
		t.Fatalf("expected redraw to preserve prompt (\\x1b[5D\\x1b[J), got %q", rendered)
	}
	if strings.Contains(rendered, "\x1b[12D\x1b[J") {
		t.Fatalf("redraw erased prompt (moved to col 0), got %q", rendered)
	}
}

// TestMidLineInsertDoesNotErasePrompt is the regression test for the cursor
// desync that made text shift one cell left and eat the space after `>`. The
// bug was ordering: insertRune advanced screenCol before redraw(), so redraw
// believed the cursor was one cell further right and moved back one cell too
// far. Width-independent: it reproduces with a plain ASCII prompt.
func TestMidLineInsertDoesNotErasePrompt(t *testing.T) {
	var out bytes.Buffer
	prompt := "> "
	// Type "hola", move left twice (cursor between "ho" and "la"), insert "x".
	got, err := readInteractiveInputStreamWithStyle(strings.NewReader("hola\x1b[D\x1b[Dx\n"), &out, Style{}, nil, prompt)
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	if got != "hoxla" {
		t.Fatalf("expected mid-line insert to yield hoxla, got %q", got)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "\x1b[2D\x1b[J") {
		t.Fatalf("expected redraw to move exactly 2 cells left (to the column after the prompt), got %q", rendered)
	}
	if strings.Contains(rendered, "\x1b[3D\x1b[J") {
		t.Fatalf("redraw moved one cell too far left and erased the prompt, got %q", rendered)
	}
}

// TestVisibleWidthCountsWideRunes verifies that the double-width user icon in
// the prompt is measured as two terminal cells. Counting it as one rune was
// what desynced the editor: every redraw landed one column too far left, so
// typing overwrote the last letter and backspace could eat into the prompt.
func TestVisibleWidthCountsWideRunes(t *testing.T) {
	if got := visibleWidth("> "); got != 2 {
		t.Fatalf("expected ASCII prompt width 2, got %d", got)
	}
	if got := visibleWidth("👤 user > "); got != 10 {
		t.Fatalf("expected emoji prompt width 10, got %d", got)
	}
	styled := "\x1b[92m👤\x1b[0m \x1b[1;32muser\x1b[0m \x1b[1;32m> \x1b[0m"
	if got := visibleWidth(styled); got != 10 {
		t.Fatalf("expected styled emoji prompt width 10, got %d", got)
	}
}

// TestInteractiveInputWidePromptAlignsCursor verifies that a mid-line insert
// with the real (emoji) prompt redraws the cursor to the correct terminal
// column, including the two cells the 👤 icon occupies.
func TestInteractiveInputWidePromptAlignsCursor(t *testing.T) {
	var out bytes.Buffer
	prompt := "👤 user > "
	// Type "ab", move left once, then insert "c" mid-line. That forces a full
	// redraw whose final cursor placement is promptWidth + cursorCol = 10 + 2.
	got, err := readInteractiveInputStreamWithStyle(strings.NewReader("ab\x1b[Dc\n"), &out, Style{}, nil, prompt)
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	if got != "acb" {
		t.Fatalf("expected mid-line insert to yield acb, got %q", got)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "\x1b[12C") {
		t.Fatalf("expected cursor placed at column 12 after redraw, got %q", rendered)
	}
	if strings.Contains(rendered, "\x1b[11C") {
		t.Fatalf("cursor placement ignored the emoji width (landed at column 11), got %q", rendered)
	}
	// The cursor was one cell into the buffer, so redraw must back up exactly
	// one cell (to the column right after the prompt), not two.
	if !strings.Contains(rendered, "\x1b[1D\x1b[J") {
		t.Fatalf("expected redraw to back up exactly one cell, got %q", rendered)
	}
	if strings.Contains(rendered, "\x1b[2D\x1b[J") {
		t.Fatalf("redraw backed up too far and erased the prompt, got %q", rendered)
	}
}

// TestBackspaceWideRuneErasesBothCells verifies that deleting a wide rune at
// the end of the line erases the two cells the terminal advanced by, instead
// of a single cell that would leave a phantom and drift the cursor.
func TestBackspaceWideRuneErasesBothCells(t *testing.T) {
	var out bytes.Buffer
	got, err := readInteractiveInputStreamWithStyle(strings.NewReader("ab👤\x7f\n"), &out, Style{}, nil, "")
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	if got != "ab" {
		t.Fatalf("expected wide rune to be removed, got %q", got)
	}
	if !strings.Contains(out.String(), "\b \b\b \b") {
		t.Fatalf("expected two erase cycles for the wide rune, got %q", out.String())
	}
}

// TestEraseVisibleTextUsesDisplayWidth verifies paste-block erasure counts
// terminal cells, not runes.
func TestEraseVisibleTextUsesDisplayWidth(t *testing.T) {
	got := eraseVisibleText("👤")
	want := "\b\b  \b\b"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

// TestNotifyAssistedOutcomes verifies the assisted-mode review notices are
// surfaced to the user: approval (with latency), rejection, unsupported, and
// unavailability.
func TestNotifyAssistedOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		notice AssistedNotice
		want   []string
	}{
		{
			name:   "approved",
			notice: AssistedNotice{Tool: "WebFetch", Outcome: AssistedApproved, Elapsed: 70 * time.Millisecond},
			want:   []string{"WebFetch", "approved by JEV Style assisted mode (70ms)"},
		},
		{
			name:   "dangerous",
			notice: AssistedNotice{Tool: "WebFetch", Outcome: AssistedDangerous, Elapsed: 70 * time.Millisecond},
			want:   []string{"WebFetch", "flagged as risky by JEV Style (70ms)", "asking for permission"},
		},
		{
			name:   "unsupported",
			notice: AssistedNotice{Tool: "WebFetch", Outcome: AssistedUnsupported},
			want:   []string{"WebFetch", "cannot be classified by JEV Style", "asking for permission"},
		},
		{
			name:   "unavailable",
			notice: AssistedNotice{Tool: "WebFetch", Outcome: AssistedUnavailable},
			want:   []string{"JEV Style unavailable", "assisted mode disabled", "asking for permission"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			ui := &PlainUI{out: &out, style: NewStyleForTest(false)}
			ui.NotifyAssisted(tc.notice)
			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("expected %q in %q", want, out.String())
				}
			}
		})
	}
}
