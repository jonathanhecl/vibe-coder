package tui

import (
	"strings"
	"testing"
	"unicode/utf8"
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

// The animated line is cleared with a single-row escape, so it must stay on one
// line: the command is cut before the color codes are added, never after.
func TestPendingLabelStaysOnOneLine(t *testing.T) {
	t.Parallel()
	st := NewStyleForTest(false)
	label := pendingLabel(st, "Bash", map[string]any{
		"command": strings.Repeat("cd /very/long/path && ", 12),
	})
	if n := utf8.RuneCountInString(label); n > maxToolHeaderLen {
		t.Fatalf("label has %d runes, want at most %d: %q", n, maxToolHeaderLen, label)
	}
	if !strings.HasSuffix(label, "… running…") {
		t.Fatalf("a clipped command must end in an ellipsis: %q", label)
	}

	edit := pendingLabel(st, "Edit", map[string]any{"file_path": "/home/u/proyecto/carpeta/archivo.go"})
	if !strings.HasPrefix(edit, "Edit ") || !strings.HasSuffix(edit, " running…") {
		t.Fatalf("unexpected edit label: %q", edit)
	}
}
