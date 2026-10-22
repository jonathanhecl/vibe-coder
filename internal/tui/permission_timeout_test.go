package tui

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"testing"
	"time"
)

func TestCountdownText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   time.Duration
		want string
	}{
		{3 * time.Minute, "⏳ auto-deny in 3:00"},
		{90 * time.Second, "⏳ auto-deny in 1:30"},
		{9 * time.Second, "⏳ auto-deny in 0:09"},
		{500 * time.Millisecond, "⏳ auto-deny in 0:01"},
		{0, "⏳ auto-deny in 0:00"},
		{-time.Second, "⏳ auto-deny in 0:00"},
	}
	for _, tc := range cases {
		if got := countdownText(tc.in); got != tc.want {
			t.Errorf("countdownText(%v) = %q, want %q", tc.in, got, tc.want)
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
	ui.repaintPermissionLine("⏳ auto-deny in 3:00")
	want := "\r\x1b[K  ▸ ⏳ auto-deny in 3:00"
	if got := out.String(); got != want {
		t.Fatalf("repaint = %q, want %q", got, want)
	}
}
