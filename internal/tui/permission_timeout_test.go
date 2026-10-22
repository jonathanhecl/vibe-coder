package tui

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAskBadge(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   time.Duration
		want string
	}{
		{3 * time.Minute, "⏳ 3:00"},
		{90 * time.Second, "⏳ 1:30"},
		{9 * time.Second, "⏳ 0:09"},
		{500 * time.Millisecond, "⏳ 0:01"},
		{0, ""},
		{-time.Second, ""},
	}
	for _, tc := range cases {
		if got := askBadge(tc.in); got != tc.want {
			t.Errorf("askBadge(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestPermissionDecisionMapping(t *testing.T) {
	t.Parallel()
	cases := map[string]Decision{
		"1": DecisionAllowOnce,
		"2": DecisionAllowSession,
		"3": DecisionAllowPersistent,
		"4": DecisionDenyOnce,
		"5": DecisionDenySession,
		"6": DecisionDenyPersistent,
		"7": DecisionCancel,
		"q": DecisionCancel,
		"c": DecisionCancel,
		"":  DecisionDenyOnce,
		"x": DecisionDenyOnce,
	}
	for in, want := range cases {
		if got := permissionDecision(in); got != want {
			t.Errorf("permissionDecision(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestWaitByteReturnsBufferedInputWithoutTicking(t *testing.T) {
	t.Parallel()
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()
	if _, err := writeEnd.WriteString("2\n"); err != nil {
		t.Fatal(err)
	}

	ticks := 0
	ch, err := waitByte(int(readEnd.Fd()), bufio.NewReader(readEnd), time.Now().Add(time.Minute), func(time.Duration) error {
		ticks++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != '2' {
		t.Fatalf("got %q, want '2'", ch)
	}
	if ticks != 0 {
		t.Fatalf("a buffered byte must not wait for a tick, got %d ticks", ticks)
	}
}

func TestWaitByteTimesOutWithCountdownTicks(t *testing.T) {
	t.Parallel()
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()

	start := time.Now()
	ticks := 0
	_, err = waitByte(int(readEnd.Fd()), bufio.NewReader(readEnd), start.Add(120*time.Millisecond), func(time.Duration) error {
		ticks++
		return nil
	})
	if !errors.Is(err, errPromptTimeout) {
		t.Fatalf("got %v, want errPromptTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout took %v, want about 120ms", elapsed)
	}
}

func TestWaitByteReadsArrivingInput(t *testing.T) {
	t.Parallel()
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()

	reader := bufio.NewReader(readEnd)
	go func() {
		time.Sleep(30 * time.Millisecond)
		_, _ = writeEnd.WriteString("1")
	}()

	ch, err := waitByte(int(readEnd.Fd()), reader, time.Now().Add(2*time.Second), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != '1' {
		t.Fatalf("got %q, want '1'", ch)
	}
}

func TestCountdownUnavailableWithoutTTY(t *testing.T) {
	t.Parallel()
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()

	ui := &PlainUI{
		in:       readEnd,
		out:      &bytes.Buffer{},
		reader:   bufio.NewReader(readEnd),
		stopCh:   make(chan struct{}),
		outIsTTY: false,
	}
	if ui.countdownAvailable() {
		t.Fatal("a pipe must not offer a countdown: there is nobody to see it")
	}
	ui.outIsTTY = true
	if ui.countdownAvailable() {
		t.Fatal("stdin is not a terminal either, so the countdown must stay off")
	}
}

func TestRepaintPermissionLineKeepsTheCaret(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	ui := &PlainUI{out: &out, style: NewStyle(nil)}
	ui.repaintPermissionLine(3 * time.Minute)
	want := "\r\x1b[K" + optionPromptLine(ui.style, "⏳ 3:00", permissionInstruction)
	if got := out.String(); got != want {
		t.Fatalf("repaint = %q, want %q", got, want)
	}
	got := out.String()
	if !strings.Contains(got, permissionInstruction) {
		t.Fatalf("repaint dropped the instruction: %q", got)
	}
	if strings.Index(got, "⏳ 3:00") > strings.Index(got, permissionInstruction) {
		t.Fatalf("the badge must precede the instruction: %q", got)
	}
}

// TestPermissionPromptMatchesTheQuestionCard pins the shared shape: same heading
// marker, same option rows, same input line. Two prompts that spend the same
// timeout should not look like two different programs.
func TestPermissionPromptMatchesTheQuestionCard(t *testing.T) {
	t.Parallel()
	got := buildPermissionPrompt(Style{}, permissionPayloadLines("Bash", map[string]any{
		"command": "rm -rf build",
	}))
	for _, want := range []string{"  ? PERMISSION REQUIRED", "    [1] Allow once", "    [7] Cancel", optionPromptLine(Style{}, "", permissionInstruction)} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in the permission card:\n%s", want, got)
		}
	}
	if strings.HasSuffix(got, "\n") {
		t.Fatalf("the card must end on its input line, not a newline:\n%q", got)
	}
}
