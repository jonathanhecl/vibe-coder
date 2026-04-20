package tui

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Style produces ANSI-colored output when the writer is a TTY and the user
// has not disabled colors via NO_COLOR.
type Style struct {
	enabled bool
}

// NewStyle decides at construction time whether colors should be emitted for
// the given writer. Colors are disabled when NO_COLOR is set or when the
// writer is not a terminal.
func NewStyle(w io.Writer) Style {
	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" {
		return Style{}
	}
	type fdHaver interface{ Fd() uintptr }
	if f, ok := w.(fdHaver); ok && term.IsTerminal(int(f.Fd())) {
		return Style{enabled: true}
	}
	return Style{}
}

// Enabled reports whether the style is producing ANSI escape codes.
func (s Style) Enabled() bool { return s.enabled }

const (
	cReset   = "\x1b[0m"
	cBold    = "\x1b[1m"
	cDim     = "\x1b[2m"
	cRed     = "\x1b[31m"
	cGreen   = "\x1b[32m"
	cYellow  = "\x1b[33m"
	cBlue    = "\x1b[34m"
	cMagenta = "\x1b[35m"
	cCyan    = "\x1b[36m"
	cGray    = "\x1b[90m"

	// Sequence to clear from cursor to end of line; used when redrawing a
	// pending tool line in place, like Cursor's collapsing tool cards.
	clearLine = "\r\x1b[2K"
)

func (s Style) wrap(code, text string) string {
	if !s.enabled {
		return text
	}
	return code + text + cReset
}

func (s Style) Bold(text string) string    { return s.wrap(cBold, text) }
func (s Style) Dim(text string) string     { return s.wrap(cDim, text) }
func (s Style) Red(text string) string     { return s.wrap(cRed, text) }
func (s Style) Green(text string) string   { return s.wrap(cGreen, text) }
func (s Style) Yellow(text string) string  { return s.wrap(cYellow, text) }
func (s Style) Blue(text string) string    { return s.wrap(cBlue, text) }
func (s Style) Magenta(text string) string { return s.wrap(cMagenta, text) }
func (s Style) Cyan(text string) string    { return s.wrap(cCyan, text) }
func (s Style) Gray(text string) string    { return s.wrap(cGray, text) }

// ClearPendingLine returns the escape sequence used to wipe a previously
// printed line so it can be redrawn with the final state.
func (s Style) ClearPendingLine() string {
	if !s.enabled {
		return "\r"
	}
	return clearLine
}

// FormatBytes returns a short human-readable byte count.
func FormatBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024.0)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024.0*1024.0))
	}
}
