package tui

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// keyKind identifies a decoded terminal key. Unknown escape sequences map to
// keyUnknown so the main loop can swallow them instead of leaking the raw ESC
// bytes into the input buffer.
type keyKind int

const (
	keyUnknown keyKind = iota
	keyEsc
	keyUp
	keyDown
	keyLeft
	keyRight
	keyHome
	keyEnd
	keyDelete
	keyInsert
	keyPageUp
	keyPageDown
	keyPasteStart
	keyAltEnter
)

// blockSpan marks an atomic bracketed-paste region inside the rune buffer. The
// raw content lives in buf[start:end], but the screen shows the compact
// `display` preview so a large paste does not flood the line. Backspace at the
// block boundary removes the whole block.
type blockSpan struct {
	start   int
	end     int
	display string
}

// lineEditor is a minimal readline-style editor that supports multi-line input
// via Alt+Enter. It tracks a rune buffer (which may contain '\n'), a logical
// cursor (rune index), and a screen position (row + column from the start of
// the editable region). Paste blocks are atomic: the cursor may sit before or
// after a block but never inside it.
type lineEditor struct {
	out   io.Writer
	style Style

	buf         []rune
	cursor      int
	screenRow   int
	screenCol   int
	blocks      []blockSpan
	prompt      string
	promptWidth int
	// promptFn, when set, supplies a prompt that changes on its own (a
	// countdown). The read loop calls refreshPrompt on every tick so the line
	// on screen keeps matching the reader's state.
	promptFn func() string

	history     *inputHistory
	histEntries []string
	histIdx     int
	draft       string

	// termWidth is the number of terminal columns (0 when unknown). It is what
	// puts soft wrapping inside the cursor model: without it a line longer than
	// the screen wraps on the terminal but not in screenRow, and every redraw
	// after that paints in the wrong place.
	termWidth int
}

func newLineEditor(out io.Writer, style Style, history *inputHistory, prompt string) *lineEditor {
	e := &lineEditor{out: out, style: style, history: history, prompt: prompt, promptWidth: visibleWidth(prompt)}
	if history != nil {
		e.histEntries = history.snapshot()
	}
	e.histIdx = len(e.histEntries)
	return e
}

// visibleWidth returns the display width of s with ANSI escape sequences
// ignored and wide runes (CJK/emoji) counted as the two cells the terminal
// actually advances by. Accounting for wide runes is essential: the prompt
// starts with the double-width 👤 emoji, and a rune-count measurement would be
// off by one, desyncing every cursor redraw.
func visibleWidth(s string) int {
	return displayWidth(s)
}

// readByte pulls a single byte from reader.
func readByte(reader io.Reader) (byte, error) {
	var b [1]byte
	if _, err := reader.Read(b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

// readUTF8Tail completes a multi-byte rune given its leading byte.
func readUTF8Tail(reader io.Reader, first byte) (rune, error) {
	var size int
	switch {
	case first&0xE0 == 0xC0:
		size = 2
	case first&0xF0 == 0xE0:
		size = 3
	case first&0xF8 == 0xF0:
		size = 4
	default:
		return utf8.RuneError, nil
	}
	buf := make([]byte, size)
	buf[0] = first
	if _, err := io.ReadFull(reader, buf[1:]); err != nil {
		return utf8.RuneError, nil
	}
	r, _ := utf8.DecodeRune(buf)
	return r, nil
}

// readEscapeSeq consumes a CSI (\x1b[...) or SS3 (\x1bO...) sequence after the
// leading ESC has been read. A bare ESC with no following byte returns keyEsc.
func readEscapeSeq(reader io.Reader) (keyKind, error) {
	next, err := readByte(reader)
	if err != nil {
		return keyEsc, nil
	}
	switch next {
	case '\r', '\n':
		return keyAltEnter, nil
	case '[':
		var params []byte
		for {
			b, err := readByte(reader)
			if err != nil {
				return keyUnknown, nil
			}
			if (b >= '0' && b <= '9') || b == ';' || b == '?' {
				params = append(params, b)
				continue
			}
			return decodeCSI(string(params), b), nil
		}
	case 'O':
		b, err := readByte(reader)
		if err != nil {
			return keyUnknown, nil
		}
		switch b {
		case 'A':
			return keyUp, nil
		case 'B':
			return keyDown, nil
		case 'C':
			return keyRight, nil
		case 'D':
			return keyLeft, nil
		case 'H':
			return keyHome, nil
		case 'F':
			return keyEnd, nil
		}
		return keyUnknown, nil
	default:
		// Alt+key or stray escape; swallow the modifier byte.
		return keyUnknown, nil
	}
}

func decodeCSI(params string, final byte) keyKind {
	switch final {
	case 'A':
		return keyUp
	case 'B':
		return keyDown
	case 'C':
		return keyRight
	case 'D':
		return keyLeft
	case 'H':
		return keyHome
	case 'F':
		return keyEnd
	case '~':
		switch params {
		case "1", "7":
			return keyHome
		case "4", "8":
			return keyEnd
		case "3":
			return keyDelete
		case "2":
			return keyInsert
		case "5":
			return keyPageUp
		case "6":
			return keyPageDown
		case "200":
			return keyPasteStart
		}
	}
	return keyUnknown
}

// readPasteContent reads bytes until the bracketed-paste end marker, returning
// the raw pasted content (with CRLF normalized to LF).
func readPasteContent(reader io.Reader) (string, error) {
	endMarkers := pasteMarkerVariants(bracketedPasteEnd)
	var content []byte
	pending := make([]byte, 0, 8)
	for {
		b, err := readByte(reader)
		if err != nil {
			return "", err
		}
		pending = append(pending, b)
		if hasCompleteMarker(pending, endMarkers) {
			return normalizePaste(string(content)), nil
		}
		if !hasMarkerPrefix(pending, endMarkers) {
			content = append(content, pending[0])
			pending = pending[1:]
		}
	}
}

func normalizePaste(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// readInteractiveInputStreamWithStyle is the interactive input loop. It reads
// from reader byte-by-byte, decodes UTF-8 and escape sequences, and drives the
// line editor. Enter submits the line (recording it to history); Ctrl-D on an
// empty line returns io.EOF. Ctrl-C clears the current line and returns an
// empty string so the REPL shows a fresh prompt without exiting.
func readInteractiveInputStreamWithStyle(reader io.Reader, out io.Writer, style Style, history *inputHistory, prompt string) (string, error) {
	return readInteractiveInputStreamWithPromptFn(reader, out, style, history, prompt, nil)
}

// readInteractiveInputStreamWithPromptFn is readInteractiveInputStreamWithStyle
// with an optional live prompt. When promptFn is set, a reader can ask for a
// repaint by returning errAskTick; the loop repaints the prompt and the buffer
// behind it and keeps reading, so a countdown never fights the cursor.
func readInteractiveInputStreamWithPromptFn(reader io.Reader, out io.Writer, style Style, history *inputHistory, prompt string, promptFn func() string) (string, error) {
	return readInteractiveInputSized(reader, out, style, history, prompt, promptFn, terminalColumns(out))
}

// readInteractiveInputSized is the input loop with an explicit terminal width.
// A width of 0 means unknown: the editor then treats the line as never wrapping
// (the behavior tests and non-TTY writers rely on).
func readInteractiveInputSized(reader io.Reader, out io.Writer, style Style, history *inputHistory, prompt string, promptFn func() string, width int) (string, error) {
	e := newLineEditor(out, style, history, prompt)
	e.promptFn = promptFn
	e.termWidth = width
	for {
		b, err := readByte(reader)
		switch {
		case errors.Is(err, errAskTick):
			e.refreshPrompt()
			continue
		case err != nil:
			return "", err
		}
		switch {
		case b == '\r' || b == '\n':
			_, _ = io.WriteString(out, "\r\n")
			result := cleanBracketedPasteMarkers(string(e.buf))
			if history != nil {
				history.add(result)
			}
			return result, nil
		case b == 0x03: // Ctrl-C: clear line and return empty (does not exit)
			_, _ = io.WriteString(out, "^C\r\n")
			return "", nil
		case b == 0x04: // Ctrl-D: EOF on empty line, else forward delete
			if len(e.buf) == 0 {
				return "", io.EOF
			}
			e.deleteForward()
		case b == 0x7f || b == 0x08:
			e.backspace()
		case b == 0x01: // Ctrl-A
			e.moveHome()
		case b == 0x05: // Ctrl-E
			e.moveEnd()
		case b == 0x02: // Ctrl-B
			e.moveLeft()
		case b == 0x06: // Ctrl-F
			e.moveRight()
		case b == 0x0b: // Ctrl-K
			e.killToEnd()
		case b == 0x15: // Ctrl-U
			e.killToStart()
		case b == 0x1b:
			key, err := readEscapeSeq(reader)
			if err != nil {
				return "", err
			}
			switch key {
			case keyAltEnter:
				e.insertNewline()
			case keyPasteStart:
				content, err := readPasteContent(reader)
				if err != nil {
					return "", err
				}
				e.insertBlock(content)
			case keyUp:
				e.historyUp()
			case keyDown:
				e.historyDown()
			case keyLeft:
				e.moveLeft()
			case keyRight:
				e.moveRight()
			case keyHome:
				e.moveHome()
			case keyEnd:
				e.moveEnd()
			case keyDelete:
				e.deleteForward()
			default:
				// Swallow PageUp/Down/Insert/unknown sequences so they do not
				// leak as literal ESC bytes into the input.
			}
		case b >= 0x20 && b < 0x80:
			e.insertRune(rune(b))
		case b >= 0x80:
			r, _ := readUTF8Tail(reader, b)
			if r != utf8.RuneError {
				e.insertRune(r)
			}
		default:
			// Ignore other control characters.
		}
	}
}

// computeRowCol returns the (row, col) screen position for a given buffer
// index, with soft wrapping at termWidth taken into account. Row 0 starts after
// the prompt; subsequent rows start at column 0. Block regions contribute their
// preview width instead of their rune count. idx must not fall strictly inside
// a block (the cursor never does).
func (e *lineEditor) computeRowCol(idx int) (row, col int) {
	if idx < 0 {
		idx = 0
	}
	if idx > len(e.buf) {
		idx = len(e.buf)
	}
	bi := 0
	for i := 0; i < idx; {
		if bi < len(e.blocks) && e.blocks[bi].start == i {
			b := e.blocks[bi]
			if b.end > idx {
				return row, col
			}
			e.advanceCells(&row, &col, displayWidth(b.display))
			i = b.end
			bi++
			continue
		}
		if e.buf[i] == '\n' {
			row++
			col = 0
		} else {
			e.advanceCells(&row, &col, runeCellWidth(e.buf[i]))
		}
		i++
	}
	return row, col
}

// rowCapacity is how many cells the edit region has on a screen row. Row 0 is
// narrowed by the prompt; later rows use the full terminal width. 0 means the
// width is unknown and nothing wraps.
func (e *lineEditor) rowCapacity(row int) int {
	if e.termWidth <= 0 {
		return 0
	}
	if row == 0 {
		capacity := e.termWidth - e.promptWidth
		if capacity < 1 {
			capacity = 1
		}
		return capacity
	}
	return e.termWidth
}

// advanceCells moves row/col forward by w terminal cells, wrapping to the next
// row when the rune would cross the row's capacity.
func (e *lineEditor) advanceCells(row, col *int, w int) {
	if w <= 0 {
		return
	}
	if capacity := e.rowCapacity(*row); capacity > 0 && *col+w > capacity {
		*row++
		*col = 0
	}
	*col += w
}

// displayCol is the column the terminal actually shows the caret on. It differs
// from col only in the wrap-pending state: after a rune fills the last cell of a
// row, the caret waits on that last cell until the next printable rune wraps.
func (e *lineEditor) displayCol(row, col int) int {
	if col < 0 {
		return 0
	}
	if capacity := e.rowCapacity(row); capacity > 0 && col > capacity-1 {
		return capacity - 1
	}
	return col
}

// writeRendered writes input text to the terminal, turning buffer newlines into
// CRLF. Raw mode clears OPOST, so a bare LF moves down without returning to
// column 0 and would leave the next line indented.
func (e *lineEditor) writeRendered(s string) {
	if strings.Contains(s, "\n") {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	_, _ = io.WriteString(e.out, s)
}

// moveToCursor emits the relative cursor motion needed to move the screen
// cursor from its current (screenRow, screenCol) to the position for newCursor.
func (e *lineEditor) moveToCursor(newCursor int) {
	targetRow, targetCol := e.computeRowCol(newCursor)
	// Convert to terminal columns: row 0 is offset by the prompt width. The
	// movement is measured in displayed cells so a wrap-pending position (whose
	// caret sits on the last cell) is handled like the terminal handles it.
	currentTermCol := e.displayCol(e.screenRow, e.screenCol)
	if e.screenRow == 0 {
		currentTermCol += e.promptWidth
	}
	targetTermCol := e.displayCol(targetRow, targetCol)
	if targetRow == 0 {
		targetTermCol += e.promptWidth
	}
	// Vertical movement.
	switch {
	case targetRow > e.screenRow:
		fmt.Fprintf(e.out, "\x1b[%dB", targetRow-e.screenRow)
	case targetRow < e.screenRow:
		fmt.Fprintf(e.out, "\x1b[%dA", e.screenRow-targetRow)
	}
	// Horizontal movement.
	switch {
	case targetTermCol > currentTermCol:
		fmt.Fprintf(e.out, "\x1b[%dC", targetTermCol-currentTermCol)
	case targetTermCol < currentTermCol:
		fmt.Fprintf(e.out, "\x1b[%dD", currentTermCol-targetTermCol)
	}
	e.cursor = newCursor
	e.screenRow = targetRow
	e.screenCol = targetCol
}

// redraw repaints the whole editable region: jump to its start, erase to end of
// screen, render text and block previews (with newlines for multi-line), then
// reposition the cursor.
func (e *lineEditor) redraw() {
	// Move to start of editable region (just after the prompt on row 0).
	// The prompt itself must be preserved, so never move to column 0 and
	// erase from there.
	if e.screenRow > 0 {
		fmt.Fprintf(e.out, "\x1b[%dA", e.screenRow)
	}
	currentTermCol := e.displayCol(e.screenRow, e.screenCol)
	if e.screenRow == 0 {
		currentTermCol += e.promptWidth
	}
	if currentTermCol > e.promptWidth {
		fmt.Fprintf(e.out, "\x1b[%dD", currentTermCol-e.promptWidth)
	} else if currentTermCol < e.promptWidth {
		fmt.Fprintf(e.out, "\x1b[%dC", e.promptWidth-currentTermCol)
	}
	// Erase from cursor to end of screen.
	_, _ = io.WriteString(e.out, "\x1b[J")
	// Write buffer content with block previews.
	pos := 0
	for _, b := range e.blocks {
		if b.start > pos {
			e.writeRendered(string(e.buf[pos:b.start]))
		}
		_, _ = io.WriteString(e.out, e.style.DimBlue(b.display))
		pos = b.end
	}
	if pos < len(e.buf) {
		e.writeRendered(string(e.buf[pos:]))
	}
	// Reposition cursor: compute end position, then move back to cursor.
	endRow, _ := e.computeRowCol(len(e.buf))
	cursorRow, cursorCol := e.computeRowCol(e.cursor)
	// Move up from end to cursor row.
	if up := endRow - cursorRow; up > 0 {
		fmt.Fprintf(e.out, "\x1b[%dA", up)
	} else if down := cursorRow - endRow; down > 0 {
		fmt.Fprintf(e.out, "\x1b[%dB", down)
	}
	// Horizontal: go to column 0, then move right to the target column.
	// For row 0, the target terminal column includes the prompt width.
	_, _ = io.WriteString(e.out, "\r")
	targetTermCol := e.displayCol(cursorRow, cursorCol)
	if cursorRow == 0 {
		targetTermCol += e.promptWidth
	}
	if targetTermCol > 0 {
		fmt.Fprintf(e.out, "\x1b[%dC", targetTermCol)
	}
	e.screenRow = cursorRow
	e.screenCol = cursorCol
}

// refreshPrompt repaints the prompt when promptFn reports a new value. It is a
// no-op for a static prompt, so the tick path costs nothing outside a
// countdown.
func (e *lineEditor) refreshPrompt() {
	if e.promptFn == nil {
		return
	}
	next := e.promptFn()
	if next == e.prompt {
		return
	}
	e.prompt = next
	e.promptWidth = visibleWidth(next)
	e.repaintPrompt()
}

// repaintPrompt rewrites the prompt and then everything the editor owns after
// it. redraw() deliberately never touches the prompt, so a prompt that changes
// by itself needs this wrapper: go back to the first row of the editable
// region, rewrite the prompt from column 0, and let redraw repaint the buffer
// and restore the cursor from the fresh state.
func (e *lineEditor) repaintPrompt() {
	if e.screenRow > 0 {
		fmt.Fprintf(e.out, "\x1b[%dA", e.screenRow)
	}
	_, _ = io.WriteString(e.out, "\r\x1b[J")
	_, _ = io.WriteString(e.out, e.prompt)
	// The terminal cursor now sits exactly after the prompt, which is the state
	// redraw assumes when screenRow and screenCol are zero.
	e.screenRow, e.screenCol = 0, 0
	e.redraw()
}

// insertRune inserts r at the cursor. Appending at the end is optimized to a
// single echo with no redraw; mid-line inserts trigger a full redraw.
func (e *lineEditor) insertRune(r rune) {
	at := e.cursor
	if at == len(e.buf) {
		e.buf = append(e.buf, r)
		e.cursor++
		if r == '\n' {
			// CRLF, not a bare LF: raw mode has no ONLCR translation.
			_, _ = io.WriteString(e.out, "\r\n")
			e.screenRow++
			e.screenCol = 0
			return
		}
		w := runeCellWidth(r)
		capacity := e.rowCapacity(e.screenRow)
		if capacity > 0 && e.screenCol >= capacity {
			// The caret is parked on the last cell (wrap pending). The next
			// printable rune belongs on the next row; move there explicitly
			// because the terminal's pending state only wraps on its own.
			_, _ = io.WriteString(e.out, "\r\n")
			e.screenRow++
			e.screenCol = 0
			capacity = e.rowCapacity(e.screenRow)
		}
		if capacity > 0 && e.screenCol+w > capacity {
			// The terminal wraps this rune by itself.
			e.screenRow++
			e.screenCol = 0
		}
		_, _ = io.WriteString(e.out, string(r))
		e.screenCol += w
		return
	}
	e.buf = spliceRunes(e.buf, at, []rune{r})
	e.shiftBlocks(at, 1)
	e.cursor++
	// redraw() uses screenRow/screenCol as the terminal's *current* position to
	// decide how far to move back before erasing. Do not advance them here: if
	// they already reflect the newly inserted rune, redraw moves one cell too
	// far left and erases the prompt's trailing space. redraw recomputes both
	// from the new cursor index when it finishes.
	e.redraw()
}

// insertBlock inserts a bracketed-paste payload as an atomic block. Appending
// at the end echoes only the compact preview; mid-line inserts redraw.
func (e *lineEditor) insertBlock(content string) {
	runes := []rune(content)
	preview := formatPastedBlock(content)
	at := e.cursor
	appendedAtEnd := at == len(e.buf)
	e.buf = spliceRunes(e.buf, at, runes)
	e.shiftBlocks(at, len(runes))
	e.insertBlockSpan(blockSpan{start: at, end: at + len(runes), display: preview})
	e.cursor = at + len(runes)
	if appendedAtEnd {
		_, _ = io.WriteString(e.out, e.style.DimBlue(preview))
		e.screenRow, e.screenCol = e.computeRowCol(e.cursor)
	} else {
		e.redraw()
	}
}

func spliceRunes(buf []rune, at int, ins []rune) []rune {
	out := make([]rune, 0, len(buf)+len(ins))
	out = append(out, buf[:at]...)
	out = append(out, ins...)
	out = append(out, buf[at:]...)
	return out
}

// shiftBlocks moves every block at or after `at` by `delta` rune positions.
func (e *lineEditor) shiftBlocks(at, delta int) {
	for i := range e.blocks {
		if e.blocks[i].start >= at {
			e.blocks[i].start += delta
			e.blocks[i].end += delta
		}
	}
}

// insertBlockSpan keeps blocks ordered by start position.
func (e *lineEditor) insertBlockSpan(b blockSpan) {
	i := len(e.blocks)
	for j, existing := range e.blocks {
		if existing.start > b.start {
			i = j
			break
		}
	}
	e.blocks = append(e.blocks, blockSpan{})
	copy(e.blocks[i+1:], e.blocks[i:])
	e.blocks[i] = b
}

// backspace removes the rune before the cursor. If the cursor sits at the end of
// a paste block, the whole block is removed and its preview erased.
func (e *lineEditor) backspace() {
	if e.cursor == 0 {
		return
	}
	for i, b := range e.blocks {
		if b.end == e.cursor {
			startRow, _ := e.computeRowCol(b.start)
			endRow, _ := e.computeRowCol(b.end)
			sameRow := startRow == endRow && endRow == e.screenRow && e.screenCol < e.capacityOrMax(e.screenRow)
			e.buf = append(e.buf[:b.start], e.buf[b.end:]...)
			e.blocks = append(e.blocks[:i], e.blocks[i+1:]...)
			e.cursor = b.start
			if sameRow {
				_, _ = io.WriteString(e.out, eraseVisibleText(b.display))
				e.screenRow, e.screenCol = e.computeRowCol(e.cursor)
				return
			}
			e.redraw()
			return
		}
	}
	at := e.cursor - 1
	deleted := e.buf[at]
	e.buf = append(e.buf[:at], e.buf[at+1:]...)
	e.shiftBlocks(at+1, -1)
	e.cursor = at
	// Optimized path: deleting a non-newline rune at the end of the buffer.
	// Erase as many cells as the rune occupied so wide runes (emoji, CJK) do
	// not leave a phantom cell behind or overwrite the prompt on row 0. The
	// fast erase only works inside one screen row: at the start of a wrapped
	// row, or in the wrap-pending state, the caret has to be recomputed.
	if e.cursor == len(e.buf) && deleted != '\n' {
		w := runeCellWidth(deleted)
		if w > 0 && e.screenCol >= w && e.screenCol < e.capacityOrMax(e.screenRow) {
			_, _ = io.WriteString(e.out, strings.Repeat("\b \b", w))
			e.screenCol -= w
			return
		}
	}
	e.redraw()
}

// capacityOrMax returns the row capacity, or a value larger than any column when
// the width is unknown, so "col < capacity" reads as "not wrap-pending".
func (e *lineEditor) capacityOrMax(row int) int {
	if capacity := e.rowCapacity(row); capacity > 0 {
		return capacity
	}
	return int(^uint(0) >> 1)
}

// deleteForward removes the rune at the cursor.
func (e *lineEditor) deleteForward() {
	if e.cursor >= len(e.buf) {
		return
	}
	e.buf = append(e.buf[:e.cursor], e.buf[e.cursor+1:]...)
	e.shiftBlocks(e.cursor+1, -1)
	e.redraw()
}

// killToStart removes everything before the cursor (Ctrl-U).
func (e *lineEditor) killToStart() {
	if e.cursor == 0 {
		return
	}
	e.buf = e.buf[e.cursor:]
	e.blocks = trimBlocks(e.blocks, e.cursor, 0)
	e.cursor = 0
	e.redraw()
}

// killToEnd removes everything from the cursor onward (Ctrl-K).
func (e *lineEditor) killToEnd() {
	if e.cursor >= len(e.buf) {
		return
	}
	e.buf = e.buf[:e.cursor]
	e.blocks = trimBlocks(e.blocks, e.cursor, e.cursor)
	e.redraw()
}

// trimBlocks drops blocks outside [0,end) and shifts surviving blocks left by
// `removed` positions starting at `from`.
func trimBlocks(blocks []blockSpan, from, end int) []blockSpan {
	out := blocks[:0]
	for _, b := range blocks {
		if b.end <= from {
			out = append(out, b)
			continue
		}
		if b.start >= end {
			b.start -= end - from
			b.end -= end - from
			out = append(out, b)
		}
	}
	return out
}

func (e *lineEditor) moveLeft() {
	if e.cursor == 0 {
		return
	}
	next := e.cursor - 1
	// Skip over paste blocks.
	for _, b := range e.blocks {
		if b.end == e.cursor && b.start < e.cursor {
			next = b.start
			break
		}
	}
	e.moveToCursor(next)
}

func (e *lineEditor) moveRight() {
	if e.cursor >= len(e.buf) {
		return
	}
	next := e.cursor + 1
	// Skip over paste blocks.
	for _, b := range e.blocks {
		if b.start == e.cursor {
			next = b.end
			break
		}
	}
	e.moveToCursor(next)
}

// moveUp moves the cursor to the previous screen row. Returns true if it
// handled the movement (i.e., the cursor was not already on the first row).
func (e *lineEditor) moveUp() bool {
	return e.moveVertical(-1)
}

// moveDown moves the cursor to the next screen row. Returns true if it handled
// the movement.
func (e *lineEditor) moveDown() bool {
	return e.moveVertical(1)
}

// moveVertical moves the cursor one visual row up (-1) or down (+1), keeping the
// column when the target row is long enough. Visual rows include soft wrapping
// at the terminal width, so navigation matches what the user sees.
func (e *lineEditor) moveVertical(delta int) bool {
	currentRow, currentCol := e.computeRowCol(e.cursor)
	targetRow := currentRow + delta
	if targetRow < 0 {
		return false
	}
	bestIdx, bestDist := -1, 0
	row, col, bi := 0, 0, 0
	consider := func(idx int) {
		if row != targetRow {
			return
		}
		dist := col - currentCol
		if dist < 0 {
			dist = -dist
		}
		if bestIdx < 0 || dist < bestDist {
			bestIdx, bestDist = idx, dist
		}
	}
	for i := 0; i <= len(e.buf); {
		if bi < len(e.blocks) && e.blocks[bi].start == i {
			b := e.blocks[bi]
			consider(i)
			e.advanceCells(&row, &col, displayWidth(b.display))
			i = b.end
			bi++
			continue
		}
		consider(i)
		if i == len(e.buf) {
			break
		}
		if e.buf[i] == '\n' {
			row++
			col = 0
		} else {
			e.advanceCells(&row, &col, runeCellWidth(e.buf[i]))
		}
		i++
	}
	if bestIdx < 0 {
		return false
	}
	e.moveToCursor(bestIdx)
	return true
}

// visualLines is the number of screen rows the buffer occupies, including soft
// wrapping. It gates history recall: Up/Down only browse history when the draft
// fits on one row.
func (e *lineEditor) visualLines() int {
	row, _ := e.computeRowCol(len(e.buf))
	return row + 1
}

// lineStartIndex returns the buffer index of the first rune on the line
// containing idx.
func (e *lineEditor) lineStartIndex(idx int) int {
	for i := idx - 1; i >= 0; i-- {
		if e.buf[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

// lineEndIndex returns the buffer index just past the last rune on the line
// containing idx (i.e., the index of the '\n' or len(buf)).
func (e *lineEditor) lineEndIndex(idx int) int {
	for i := idx; i < len(e.buf); i++ {
		if e.buf[i] == '\n' {
			return i
		}
	}
	return len(e.buf)
}

// insertNewline inserts a '\n' at the cursor position for multi-line input.
func (e *lineEditor) insertNewline() {
	e.insertRune('\n')
}

func (e *lineEditor) moveHome() {
	// Move to start of current line.
	start := e.lineStartIndex(e.cursor)
	e.moveToCursor(start)
}

func (e *lineEditor) moveEnd() {
	// Move to end of current line.
	end := e.lineEndIndex(e.cursor)
	e.moveToCursor(end)
}

// historyUp recalls the previous history entry. Only active when the buffer fits
// on one screen row; a multi-line or soft-wrapped draft navigates between rows
// instead, so Up never replaces what the user is writing.
func (e *lineEditor) historyUp() {
	if e.visualLines() > 1 {
		e.moveUp()
		return
	}
	if e.history == nil || len(e.histEntries) == 0 {
		return
	}
	if e.histIdx == len(e.histEntries) {
		e.draft = string(e.buf)
	}
	if e.histIdx > 0 {
		e.histIdx--
		e.replaceBuf(e.histEntries[e.histIdx])
	}
}

// historyDown recalls the next history entry. Only active when the buffer fits
// on one screen row; a multi-line or soft-wrapped draft navigates between rows
// instead.
func (e *lineEditor) historyDown() {
	if e.visualLines() > 1 {
		e.moveDown()
		return
	}
	if e.history == nil {
		return
	}
	if e.histIdx >= len(e.histEntries) {
		return
	}
	e.histIdx++
	if e.histIdx == len(e.histEntries) {
		e.replaceBuf(e.draft)
	} else {
		e.replaceBuf(e.histEntries[e.histIdx])
	}
}

func (e *lineEditor) replaceBuf(s string) {
	e.buf = []rune(s)
	e.blocks = nil
	e.cursor = len(e.buf)
	e.redraw()
}
