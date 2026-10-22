package tui

import (
	"bufio"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func newAskTestUI(input string, out *strings.Builder) *PlainUI {
	return &PlainUI{
		out:    out,
		reader: bufio.NewReader(strings.NewReader(input)),
		style:  Style{},
		stopCh: make(chan struct{}),
	}
}

func TestAskOptionsRendersCardAndReturnsTypedAnswer(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	ui := newAskTestUI("2\n", &out)

	got, err := ui.AskOptions(AskCard{
		Title:  "Negative prompts",
		Prompt: "Choose policy",
		Options: []AskOption{
			{Label: "Omitir por default", Description: "menos tokens"},
			{Label: "Incluir lista corta moderna"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2" {
		t.Fatalf("expected raw answer %q, got %q", "2", got)
	}

	rendered := out.String()
	for _, want := range []string{
		"Negative prompts",
		"Choose policy",
		"[1] Omitir por default",
		"menos tokens",
		"[2] Incluir lista corta moderna",
		"Select option (1-2) [default: 1]:",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered card missing %q:\n%s", want, rendered)
		}
	}
}

func TestAskOptionsRetryShowsNoteWithoutRepeatingOptions(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	ui := newAskTestUI("x\n", &out)

	_, err := ui.AskOptions(AskCard{
		Prompt:  "Choose policy",
		Options: []AskOption{{Label: "Alpha"}, {Label: "Beta"}},
		Note:    `Invalid option "x". Please enter a number (1-2):`,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rendered := out.String()
	if !strings.Contains(rendered, `Invalid option "x"`) {
		t.Fatalf("retry must show the rejection note:\n%s", rendered)
	}
	if strings.Contains(rendered, "[1] Alpha") {
		t.Fatalf("retry must not redraw the whole card:\n%s", rendered)
	}
}

func TestAskOptionsPromptHasNoTitleWhenCardHasNone(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	ui := newAskTestUI("\n", &out)

	if _, err := ui.AskOptions(AskCard{Prompt: "Pick one", Options: []AskOption{{Label: "Only"}}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "? Pick one") {
		t.Fatalf("prompt should be the heading when there is no title:\n%s", rendered)
	}
}

// askInput is exercised over a pipe: poll works there exactly as it does on a
// terminal, without needing a pty to test the two ways out of a question.
func newAskInputPipe(t *testing.T, deadline time.Time) (*askInput, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})
	return &askInput{br: bufio.NewReader(r), fd: int(r.Fd()), deadline: deadline}, w
}

func TestAskInputReturnsTypedBytes(t *testing.T) {
	t.Parallel()

	input, w := newAskInputPipe(t, time.Time{})
	if _, err := w.WriteString("3\n"); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 1)
	if _, err := input.Read(buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf[0] != '3' {
		t.Fatalf("expected '3', got %q", buf[0])
	}
}

func TestAskInputLoneEscapeMeansDefault(t *testing.T) {
	t.Parallel()

	input, w := newAskInputPipe(t, time.Time{})
	if _, err := w.Write([]byte{0x1b}); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 1)
	_, err := input.Read(buf)
	if !errors.Is(err, errAskEscape) {
		t.Fatalf("a lone ESC must ask for the default, got %v", err)
	}
}

func TestAskInputEscapeSequenceIsNotDefault(t *testing.T) {
	t.Parallel()

	input, w := newAskInputPipe(t, time.Time{})
	if _, err := w.WriteString("\x1b[A"); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 1)
	if _, err := input.Read(buf); err != nil {
		t.Fatalf("arrow key must not be read as ESC: %v", err)
	}
	if buf[0] != 0x1b {
		t.Fatalf("expected the ESC that starts the sequence, got %q", buf[0])
	}
}

func TestAskInputDeadlineTakesDefault(t *testing.T) {
	t.Parallel()

	input, _ := newAskInputPipe(t, time.Now().Add(60*time.Millisecond))

	buf := make([]byte, 1)
	start := time.Now()
	_, err := input.Read(buf)
	if !errors.Is(err, errAskTimeout) {
		t.Fatalf("expected the timeout to take the default, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout took too long: %s", elapsed)
	}
}

func TestAskInputExpiredDeadlineTakesDefaultImmediately(t *testing.T) {
	t.Parallel()

	input, _ := newAskInputPipe(t, time.Now().Add(-time.Second))

	buf := make([]byte, 1)
	if _, err := input.Read(buf); !errors.Is(err, errAskTimeout) {
		t.Fatalf("expected an expired deadline to take the default, got %v", err)
	}
}

func TestAskPromptLineShowsCountdown(t *testing.T) {
	t.Parallel()

	ui := &PlainUI{style: Style{}}
	line := ui.askPromptLine(2, 4200*time.Millisecond)
	if !strings.Contains(line, "⏳ 0:05") {
		t.Fatalf("prompt must show the countdown as m:ss: %q", line)
	}
	if !strings.Contains(line, "Select option (1-2) [default: 1]") {
		t.Fatalf("prompt lost its label: %q", line)
	}
	if strings.Index(line, "⏳ 0:05") > strings.Index(line, "Select option") {
		t.Fatalf("the countdown is a badge: it must precede the instruction: %q", line)
	}
}

func TestAskPromptLineShowsNoCountdownWithoutDeadline(t *testing.T) {
	t.Parallel()

	ui := &PlainUI{style: Style{}}
	if line := ui.askPromptLine(2, 0); strings.Contains(line, "⏳") {
		t.Fatalf("no deadline means no countdown: %q", line)
	}
}

func TestAskInputTicksBeforeDeadline(t *testing.T) {
	t.Parallel()

	input, _ := newAskInputPipe(t, time.Now().Add(5*time.Second))

	buf := make([]byte, 1)
	start := time.Now()
	_, err := input.Read(buf)
	if !errors.Is(err, errAskTick) {
		t.Fatalf("expected a countdown tick before the deadline, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("a tick must arrive within the first second, took %s", elapsed)
	}
}

func TestLineEditorRefreshPromptRepaintsCountdown(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	e := newLineEditor(&out, Style{}, nil, "  ▸ ⏳ 0:05  Select option (1-2) [default: 1]: ")
	e.promptFn = func() string { return "  ▸ ⏳ 0:04  Select option (1-2) [default: 1]: " }
	e.refreshPrompt()

	if !strings.Contains(out.String(), "⏳ 0:04") {
		t.Fatalf("prompt repaint missing the new countdown:\n%q", out.String())
	}

	out.Reset()
	e.refreshPrompt()
	if out.String() != "" {
		t.Fatalf("an unchanged prompt must not be repainted:\n%q", out.String())
	}
}

func TestLineEditorRefreshPromptWithoutFnIsNoop(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	e := newLineEditor(&out, Style{}, nil, "prompt: ")
	e.refreshPrompt()
	if out.String() != "" {
		t.Fatalf("a static prompt must never be repainted:\n%q", out.String())
	}
}
