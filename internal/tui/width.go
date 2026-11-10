package tui

import (
	"io"
	"os"

	"github.com/clipperhouse/displaywidth"
	"golang.org/x/term"
)

// ansiWidthOptions measures display width while treating ANSI escape sequences
// as zero-width, so only the printable cells count.
var ansiWidthOptions = displaywidth.Options{
	ControlSequences:     true,
	ControlSequences8Bit: true,
}

// displayWidth returns the number of terminal cells occupied by s, ignoring
// ANSI escape sequences and accounting for wide/zero-width graphemes (CJK,
// emoji, combining marks). This is what the terminal actually advances by, so
// all cursor math must use it instead of rune counts.
func displayWidth(s string) int {
	return ansiWidthOptions.String(s)
}

// runeCellWidth returns the number of terminal cells occupied by a single rune.
// Newlines and other control characters are handled by the callers.
func runeCellWidth(r rune) int {
	w := displaywidth.Rune(r)
	if w < 0 {
		return 1
	}
	return w
}

// terminalColumns returns the column count of the terminal behind w, or 0 when
// w is not a terminal. The line editor needs it to model soft wrapping: without
// the width, the screen position of the cursor drifts as soon as a line is
// longer than the screen.
func terminalColumns(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	columns, _, err := term.GetSize(int(f.Fd()))
	if err != nil || columns <= 0 {
		return 0
	}
	return columns
}
