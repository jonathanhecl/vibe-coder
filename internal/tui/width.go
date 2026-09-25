package tui

import "github.com/clipperhouse/displaywidth"

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
