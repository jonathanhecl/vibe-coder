package tui

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

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

func TestGetInputBracketedPaste(t *testing.T) {
	ui := &PlainUI{
		out:    &bytes.Buffer{},
		reader: bufio.NewReader(strings.NewReader("\x1b[200~line 1\nline 2\x1b[201~\n")),
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

func TestGetInputBufferedMultilinePaste(t *testing.T) {
	ui := &PlainUI{
		out:    &bytes.Buffer{},
		reader: bufio.NewReader(strings.NewReader("line 1\nline 2\nline 3\n")),
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
