package tui

import (
	"strings"
	"testing"
)

// The line that spins while a tool runs must look like the card that replaces
// it: same shield, same green name, same gray command. It used to print the
// whole header in one blue, so the design changed only after the tool finished.
func TestPendingLabelMatchesTheCardDesign(t *testing.T) {
	t.Parallel()
	st := NewStyleForTest(true)

	label := pendingLabel(st, "Bash", map[string]any{"command": "ls -la"})
	want := iconShell + " " + st.BoldGreen("Shell") + " " + st.Gray("$ ls -la") + st.DimBlue(" running…")
	if label != want {
		t.Fatalf("pending label =\n%q\nwant\n%q", label, want)
	}
}

// It is the same text as the card, not a summary: a shell command keeps its
// full length while it runs too.
func TestPendingLabelShowsTheWholeCommand(t *testing.T) {
	t.Parallel()
	cmd := strings.Repeat("cd /very/long/path && ", 8) + "echo done"
	label := pendingLabel(Style{}, "Bash", map[string]any{"command": cmd})
	if !strings.Contains(label, cmd) {
		t.Fatalf("the running line must carry the whole command:\n%q", label)
	}
	if !strings.HasSuffix(label, " running…") {
		t.Fatalf("unexpected suffix: %q", label)
	}

	edit := pendingLabel(Style{}, "Edit", map[string]any{"file_path": "/home/u/proyecto/carpeta/archivo.go"})
	if !strings.HasPrefix(edit, "Edit ") || !strings.HasSuffix(edit, " running…") {
		t.Fatalf("unexpected edit label: %q", edit)
	}
}

// A long running line wraps, and the painter has to know how many rows to
// erase: clearing one row left the tail of the line on screen.
func TestPendingLineMovesByRows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		text string
		cols int
		want int
	}{
		{"short", 80, 1},
		{strings.Repeat("x", 80), 80, 1},
		{strings.Repeat("x", 81), 80, 2},
		{strings.Repeat("x", 160), 80, 2},
		{strings.Repeat("x", 512), 80, 7},
		{"\x1b[1m\x1b[32mShell\x1b[0m " + strings.Repeat("x", 70), 80, 1}, // colors take no cells
	}
	for _, tc := range cases {
		if got := wrapRows(tc.text, tc.cols); got != tc.want {
			t.Errorf("wrapRows(%q, %d) = %d, want %d", tc.text, tc.cols, got, tc.want)
		}
	}

	if got := clearPendingRows(1); got != "\r\x1b[2K" {
		t.Fatalf("clearPendingRows(1) = %q", got)
	}
	if got := clearPendingRows(3); got != "\r\x1b[2K\x1b[1A\x1b[2K\x1b[1A\x1b[2K" {
		t.Fatalf("clearPendingRows(3) = %q", got)
	}
}
