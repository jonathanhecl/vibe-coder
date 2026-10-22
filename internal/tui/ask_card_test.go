package tui

import (
	"bufio"
	"strings"
	"testing"
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
		Title:   "Negative prompts",
		Prompt:  "Choose policy",
		Options: []string{"Omitir por default", "Incluir lista corta moderna"},
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
		Options: []string{"Alpha", "Beta"},
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

	if _, err := ui.AskOptions(AskCard{Prompt: "Pick one", Options: []string{"Only"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "? Pick one") {
		t.Fatalf("prompt should be the heading when there is no title:\n%s", rendered)
	}
}
