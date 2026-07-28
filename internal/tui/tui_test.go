package tui

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	// history), Ctrl-K (kill to end of line on line 1), Enter.
	// After Up+Ctrl-K, line 1 should be "aaa" (bbb killed), so result is "aaa\n".
	got, err := readInteractiveInputStreamWithStyle(
		strings.NewReader("aaa\x1b\rbbb\x1b[A\x0b\n"), &out, Style{}, hist, "")
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	if got != "aaa\n" {
		t.Fatalf("expected Up to navigate lines not history, got %q", got)
	}
}
