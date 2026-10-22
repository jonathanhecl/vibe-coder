package tui

import (
	"strings"
	"testing"
)

// newTailUI builds a PlainUI that writes into a buffer and reports a terminal
// stdout, which is what the interactive path looks like. ShowToolResult is
// called directly so no spinner goroutine writes into the buffer concurrently.
func newTailUI() (*PlainUI, *strings.Builder) {
	var b strings.Builder
	return &PlainUI{out: &b, style: NewStyleForTest(true), outIsTTY: true}, &b
}

func TestBashResultShowsLastFourOutputLines(t *testing.T) {
	u, b := newTailUI()
	output := "step 1\nstep 2\nstep 3\nstep 4\nstep 5\nstep 6\nok  	example.test/fixture	0.12s"
	u.ShowToolResult("Bash", output, false, map[string]any{"command": "go test ./..."})

	res := b.String()
	for _, want := range []string{"step 4", "step 5", "step 6", "ok  \texample.test/fixture"} {
		if !strings.Contains(res, want) {
			t.Fatalf("expected %q in the tail, got %q", want, res)
		}
	}
	for _, hidden := range []string{"step 1", "step 2", "step 3"} {
		if strings.Contains(res, hidden) {
			t.Fatalf("line %q is outside the tail window: %q", hidden, res)
		}
	}
}

func TestBashResultShowsShortOutputWhole(t *testing.T) {
	u, b := newTailUI()
	u.ShowToolResult("Bash", "built neodata\n60 bytes written", false, map[string]any{"command": "go build"})
	res := b.String()
	if !strings.Contains(res, "built neodata") || !strings.Contains(res, "60 bytes written") {
		t.Fatalf("expected both output lines, got %q", res)
	}
}

func TestBashResultDoesNotRepeatSingleLine(t *testing.T) {
	u, b := newTailUI()
	u.ShowToolResult("Bash", "all good", false, map[string]any{"command": "true"})
	if got := strings.Count(b.String(), "all good"); got != 1 {
		t.Fatalf("single-line output should appear once, got %d in %q", got, b.String())
	}
}

func TestBashResultTailKeepsLastProgressState(t *testing.T) {
	u, b := newTailUI()
	u.ShowToolResult("Bash", "0%\r50%\r100% done\nsecond line", false, map[string]any{"command": "make"})
	res := b.String()
	if !strings.Contains(res, "100% done") {
		t.Fatalf("expected the final progress state, got %q", res)
	}
	if strings.Contains(res, "50%") {
		t.Fatalf("stale progress state leaked into the tail: %q", res)
	}
}

func TestBashResultTailTruncatesLongLines(t *testing.T) {
	u, b := newTailUI()
	long := strings.Repeat("ñ", 400)
	u.ShowToolResult("Bash", "header\n"+long, false, map[string]any{"command": "cat huge.txt"})
	res := b.String()
	if !strings.Contains(res, "…") {
		t.Fatalf("expected a truncation marker, got %q", res)
	}
	if strings.Contains(res, long) {
		t.Fatal("long line was printed in full")
	}
	if strings.ContainsRune(res, '\uFFFD') {
		t.Fatal("truncation split a multi-byte rune")
	}
}

// Only shell cards get a tail; other tools already summarize what they did.
func TestOnlyBashGetsOutputTail(t *testing.T) {
	u, b := newTailUI()
	u.ShowToolResult("Read", "line one\nline two\nline three", false, map[string]any{"file_path": "a.txt"})
	if strings.Contains(b.String(), "line one") {
		t.Fatalf("Read output should stay summarized, got %q", b.String())
	}
}

// Redirected output keeps the compact card: the tail is a terminal nicety.
func TestBashTailSkippedWhenOutputIsNotATerminal(t *testing.T) {
	var b strings.Builder
	u := &PlainUI{out: &b, style: Style{}}
	u.ShowToolResult("Bash", "first\nsecond\nthird", false, map[string]any{"command": "ls"})
	if strings.Contains(b.String(), "third") {
		t.Fatalf("expected no tail without a terminal, got %q", b.String())
	}
}
