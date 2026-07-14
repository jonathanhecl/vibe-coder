package tui

import (
	"bufio"
	"bytes"
	"os"
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
