package tui

import (
	"strings"
	"testing"
)

// vtScreen is a tiny terminal emulator for the escape sequences the line editor
// emits. Tests use it to check what a user would actually see, which is the only
// way to catch cursor-model desyncs.
type vtScreen struct {
	width int
	rows  [][]rune
	row   int
	col   int
	// pending is the terminal's wrap-pending state: a rune filled the last cell,
	// so the caret sits there until the next printable rune wraps.
	pending bool
}

func newVTScreen(width int) *vtScreen {
	return &vtScreen{width: width, rows: [][]rune{make([]rune, width)}}
}

func (v *vtScreen) ensureRow() {
	for len(v.rows) <= v.row {
		v.rows = append(v.rows, make([]rune, v.width))
	}
}

func (v *vtScreen) blankCells(row, from int) {
	cells := v.rows[row]
	for i := from; i < v.width; i++ {
		cells[i] = ' '
	}
}

func (v *vtScreen) writeRune(r rune) {
	w := runeCellWidth(r)
	if w < 1 {
		return
	}
	if v.pending {
		v.row++
		v.col = 0
		v.pending = false
	}
	if v.col+w > v.width {
		v.row++
		v.col = 0
	}
	v.ensureRow()
	cells := v.rows[v.row]
	cells[v.col] = r
	for i := 1; i < w && v.col+i < v.width; i++ {
		cells[v.col+i] = 0
	}
	v.col += w
	if v.col >= v.width {
		v.col = v.width - 1
		v.pending = true
	}
}

func (v *vtScreen) apply(s string) {
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case 0x1b:
			if i+1 >= len(runes) || runes[i+1] != '[' {
				continue
			}
			i += 2
			params := ""
			for i < len(runes) && ((runes[i] >= '0' && runes[i] <= '9') || runes[i] == ';') {
				params += string(runes[i])
				i++
			}
			if i >= len(runes) {
				return
			}
			n := 1
			if params != "" && params != "0" {
				n = 0
				for _, d := range params {
					n = n*10 + int(d-'0')
				}
			}
			switch runes[i] {
			case 'A':
				v.row -= n
				if v.row < 0 {
					v.row = 0
				}
				v.pending = false
			case 'B':
				v.row += n
				v.pending = false
				v.ensureRow()
			case 'C':
				v.col += n
				if v.col > v.width-1 {
					v.col = v.width - 1
				}
				v.pending = false
			case 'D':
				v.col -= n
				if v.col < 0 {
					v.col = 0
				}
				v.pending = false
			case 'J':
				v.ensureRow()
				v.blankCells(v.row, v.col)
				v.rows = v.rows[:v.row+1]
			case 'K':
				v.ensureRow()
				v.blankCells(v.row, v.col)
			case 'm', 'H', 'h', 'l':
				// SGR and mode switches carry no text position.
			}
		case '\r':
			v.col = 0
			v.pending = false
		case '\n':
			v.row++
			v.pending = false
			v.ensureRow()
		case '\b':
			if v.col > 0 {
				v.col--
			}
			v.pending = false
		default:
			if r < 0x20 {
				continue
			}
			v.writeRune(r)
		}
	}
}

// text returns the screen with trailing blanks and trailing empty rows
// trimmed, one line per row.
func (v *vtScreen) text() string {
	var lines []string
	for _, row := range v.rows {
		var b strings.Builder
		for _, r := range row {
			if r == 0 {
				continue
			}
			b.WriteRune(r)
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// feed runs the interactive editor with a fixed terminal width and returns the
// submitted buffer and the emulated screen. The prompt is drawn first, the way
// GetInput draws it before the editor starts.
func feed(t *testing.T, width int, prompt, input string) (string, *vtScreen) {
	t.Helper()
	screen := newVTScreen(width)
	_, _ = screenWriter{screen}.Write([]byte(prompt))
	got, err := readInteractiveInputSized(strings.NewReader(input), screenWriter{screen}, Style{}, nil, prompt, nil, width)
	if err != nil {
		t.Fatalf("interactive input failed: %v", err)
	}
	return got, screen
}

// screenWriter lets the editor write straight into the emulator.
type screenWriter struct{ screen *vtScreen }

func (w screenWriter) Write(p []byte) (int, error) {
	w.screen.apply(string(p))
	return len(p), nil
}

// TestAltEnterEchoReturnsToColumnZero is the regression test for the newline
// that stayed indented: raw mode disables ONLCR, so echoing a bare LF moved the
// caret down without returning it to column 0.
func TestAltEnterEchoReturnsToColumnZero(t *testing.T) {
	got, screen := feed(t, 20, "> ", "hola\x1b\rmundo\n")
	if got != "hola\nmundo" {
		t.Fatalf("expected multi-line buffer, got %q", got)
	}
	if want := "> hola\nmundo"; screen.text() != want {
		t.Fatalf("expected screen %q, got %q", want, screen.text())
	}
}

// TestBackspaceAcrossWrappedLineEditsCorrectly verifies that a line longer than
// the terminal width keeps a correct cursor model: backspace over the wrapped
// runes and the newline must leave the screen and caret where the user expects.
func TestBackspaceAcrossWrappedLineEditsCorrectly(t *testing.T) {
	// Width 20 with a 2-cell prompt leaves 18 cells on the first row. After
	// deleting "xyz" and the newline, "Z" must land at the end of the wrapped
	// line (row 1, col 12), not somewhere the desynced cursor guessed.
	input := strings.Repeat("a", 30) + "\x1b\rxyz" + strings.Repeat("\x7f", 4) + "Z\n"
	got, screen := feed(t, 20, "> ", input)
	if want := strings.Repeat("a", 30) + "Z"; got != want {
		t.Fatalf("expected the wrapped line plus the marker, got %q", got)
	}
	want := "> " + strings.Repeat("a", 18) + "\n" + strings.Repeat("a", 12) + "Z"
	if screen.text() != want {
		t.Fatalf("expected screen %q, got %q", want, screen.text())
	}
}

// TestUpDownMoveAcrossSoftWrappedRows verifies that Up/Down navigate the rows a
// long line occupies on screen instead of recalling history.
func TestUpDownMoveAcrossSoftWrappedRows(t *testing.T) {
	// 24 a's occupy row 0 (18 cells) and row 1 (6 cells).
	got, _ := feed(t, 20, "> ", strings.Repeat("a", 24)+"\x1b[A"+"X"+"\n")
	want := strings.Repeat("a", 6) + "X" + strings.Repeat("a", 18)
	if got != want {
		t.Fatalf("expected Up to move to row 0 col 6 and insert there, got %q", got)
	}
}

// TestInsertAtPendingWrapGoesToNextRow covers the terminal's wrap-pending state:
// when a rune exactly fills the last cell, the next rune must start the next
// row instead of overwriting the last cell.
func TestInsertAtPendingWrapGoesToNextRow(t *testing.T) {
	got, screen := feed(t, 20, "> ", strings.Repeat("a", 18)+"X\n")
	if want := strings.Repeat("a", 18) + "X"; got != want {
		t.Fatalf("expected the rune to be kept at the end, got %q", got)
	}
	want := "> " + strings.Repeat("a", 18) + "\nX"
	if screen.text() != want {
		t.Fatalf("expected screen %q, got %q", want, screen.text())
	}
}

// TestBackspaceAtStartOfWrappedRowRedraws verifies that backspacing the first
// cell of a wrapped row repaints instead of emitting a backspace that the
// terminal cannot carry over the row boundary.
func TestBackspaceAtStartOfWrappedRowRedraws(t *testing.T) {
	got, screen := feed(t, 20, "> ", strings.Repeat("a", 19)+strings.Repeat("\x7f", 2)+"Z\n")
	if want := strings.Repeat("a", 17) + "Z"; got != want {
		t.Fatalf("expected 17 a's plus the marker, got %q", got)
	}
	want := "> " + strings.Repeat("a", 17) + "Z"
	if screen.text() != want {
		t.Fatalf("expected screen %q, got %q", want, screen.text())
	}
}

// TestMidLineInsertOnWrappedRow verifies that editing inside a wrapped line
// repaints at the right position.
func TestMidLineInsertOnWrappedRow(t *testing.T) {
	got, screen := feed(t, 20, "> ", strings.Repeat("a", 20)+strings.Repeat("\x1b[D", 2)+"X\n")
	wantBuffer := strings.Repeat("a", 18) + "X" + strings.Repeat("a", 2)
	if got != wantBuffer {
		t.Fatalf("expected insert at index 18, got %q", got)
	}
	want := "> " + strings.Repeat("a", 18) + "\nXaa"
	if screen.text() != want {
		t.Fatalf("expected screen %q, got %q", want, screen.text())
	}
}

// TestBackspaceRemovesPasteBlockOnWrappedLine verifies the atomic paste block
// is erased cleanly when it starts on a wrapped row.
func TestBackspaceRemovesPasteBlockOnWrappedLine(t *testing.T) {
	got, screen := feed(t, 20, "> ", strings.Repeat("a", 18)+"\x1b[200~hello\x1b[201~\x7f\n")
	if want := strings.Repeat("a", 18); got != want {
		t.Fatalf("expected the block to be removed, got %q", got)
	}
	want := "> " + strings.Repeat("a", 18)
	if screen.text() != want {
		t.Fatalf("expected screen %q, got %q", want, screen.text())
	}
}
