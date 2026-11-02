package tui

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// answeredPermissionUI builds a prompt whose input arrives from a reader, so an
// answer can be typed without a terminal.
func answeredPermissionUI(input string, tty bool) (*PlainUI, *bytes.Buffer) {
	out := &bytes.Buffer{}
	reader := bufio.NewReader(strings.NewReader(input))
	return &PlainUI{out: out, reader: reader, style: NewStyle(nil), outIsTTY: tty}, out
}

// The answered card keeps the option the user chose and nothing else: a menu
// left on screen is noise that a long run has to scroll past.
func TestAskPermissionLeavesTheChosenOptionInTheAnswer(t *testing.T) {
	t.Parallel()
	ui, out := answeredPermissionUI("3\n", false)

	if got := ui.AskPermission("Write", map[string]any{"file_path": "a.txt"}); got != DecisionAllowPersistent {
		t.Fatalf("decision = %v, want allow persistent", got)
	}

	got := out.String()
	want := optionPromptLine(ui.style, "", permissionInstruction) + "Always allow"
	if !strings.Contains(got, want) {
		t.Fatalf("the answer line is missing from the output:\n%s", got)
	}
	if strings.HasSuffix(strings.TrimRight(got, "\n"), permissionInstruction) {
		t.Fatalf("the card must end on the answer, not the bare prompt:\n%s", got)
	}
	if strings.Contains(got, "\x1b[1A") {
		t.Fatalf("a non-terminal must not receive cursor escapes:\n%q", got)
	}
}

// On a terminal the option rows are erased, so the menu does not survive the
// answer: the cursor walks up over the seven options and their blank line.
func TestAskPermissionErasesTheMenuOnATTY(t *testing.T) {
	t.Parallel()
	ui, out := answeredPermissionUI("5\n", true)

	if got := ui.AskPermission("Bash", map[string]any{"command": "ls"}); got != DecisionDenySession {
		t.Fatalf("decision = %v, want deny session", got)
	}

	got := out.String()
	erase := "\r\x1b[K" + strings.Repeat("\x1b[1A\x1b[K", permissionCardAnswerRows)
	if !strings.Contains(got, erase) {
		t.Fatalf("the menu was not erased before the answer:\n%q", got)
	}
	if !strings.HasSuffix(got, optionPromptLine(ui.style, "", permissionInstruction)+"Block this session\n") {
		t.Fatalf("the erased card must end on the chosen option, got:\n%q", got)
	}
}

// Every key maps to the label the card showed for it, so the collapsed line can
// never name a different option than the one the user pressed.
func TestPermissionDecisionLabelsMatchTheCard(t *testing.T) {
	t.Parallel()
	keys := map[string]string{
		"1": "Allow once",
		"2": "Allow this session",
		"3": "Always allow",
		"4": "Deny once",
		"5": "Block this session",
		"6": "Always deny",
		"7": "Cancel",
	}
	for key, want := range keys {
		if got := permissionDecisionLabel(permissionDecision(key)); got != want {
			t.Errorf("key %s label = %q, want %q", key, got, want)
		}
	}
	card := buildPermissionPrompt(Style{}, nil)
	for _, want := range keys {
		if !strings.Contains(card, want) {
			t.Errorf("card is missing the option label %q", want)
		}
	}
}
