package tui

import (
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

	history     *inputHistory
	histEntries []string
	histIdx     int
	draft       string
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
// stripped. Wide characters (CJK/emoji) are counted as 1 column each; this is
// a known limitation consistent with the English-only convention in AGENTS.md.
func visibleWidth(s string) int {
	width := 0
	inEscape := false
	for _, r := range s {
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if r == 0x1b {
			inEscape = true
			continue
		}
		width++
	}
	return width
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
// line editor. Enter submits the line (recording it to history); Ctrl-C and
// Ctrl-D on an empty line return io.EOF.
func readInteractiveInputStreamWithStyle(reader io.Reader, out io.Writer, style Style, history *inputHistory, prompt string) (string, error) {
	e := newLineEditor(out, style, history, prompt)
	for {
		b, err := readByte(reader)
		if err != nil {
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
		case b == 0x03: // Ctrl-C
			return "", io.EOF
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
// index. Row 0 starts after the prompt; subsequent rows start at column 0.
// Block regions contribute their preview width instead of their rune count.
// idx must not fall strictly inside a block (the cursor never does).
func (e *lineEditor) computeRowCol(idx int) (row, col int) {
	row = 0
	col = 0
	pos := 0 // last position consumed in buf
	for _, b := range e.blocks {
		if b.end <= idx {
			e.advanceRowCol(&row, &col, e.buf[pos:b.start])
			col += utf8.RuneCountInString(b.display)
			pos = b.end
		} else if b.start < idx {
			e.advanceRowCol(&row, &col, e.buf[pos:b.start])
			pos = b.start
			break
		} else {
			break
		}
	}
	e.advanceRowCol(&row, &col, e.buf[pos:idx])
	return row, col
}

// advanceRowCol updates row/col for the runes in segment, handling '\n' by
// incrementing row and resetting col to 0.
func (e *lineEditor) advanceRowCol(row, col *int, segment []rune) {
	for _, r := range segment {
		if r == '\n' {
			*row++
			*col = 0
		} else {
			*col++
		}
	}
}

// moveToCursor emits the relative cursor motion needed to move the screen
// cursor from its current (screenRow, screenCol) to the position for newCursor.
func (e *lineEditor) moveToCursor(newCursor int) {
	targetRow, targetCol := e.computeRowCol(newCursor)
	// Convert to terminal columns: row 0 is offset by the prompt width.
	targetTermCol := targetCol
	if targetRow == 0 {
		targetTermCol += e.promptWidth
	}
	currentTermCol := e.screenCol
	if e.screenRow == 0 {
		currentTermCol += e.promptWidth
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
	// Move to start of editable region.
	if e.screenRow > 0 {
		fmt.Fprintf(e.out, "\x1b[%dA", e.screenRow)
	}
	currentTermCol := e.screenCol
	if e.screenRow == 0 {
		currentTermCol += e.promptWidth
	}
	if currentTermCol > 0 {
		fmt.Fprintf(e.out, "\x1b[%dD", currentTermCol)
	}
	// Erase from cursor to end of screen.
	_, _ = io.WriteString(e.out, "\x1b[J")
	// Write buffer content with block previews.
	pos := 0
	for _, b := range e.blocks {
		if b.start > pos {
			_, _ = io.WriteString(e.out, string(e.buf[pos:b.start]))
		}
		_, _ = io.WriteString(e.out, e.style.DimBlue(b.display))
		pos = b.end
	}
	if pos < len(e.buf) {
		_, _ = io.WriteString(e.out, string(e.buf[pos:]))
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
	targetTermCol := cursorCol
	if cursorRow == 0 {
		targetTermCol += e.promptWidth
	}
	if targetTermCol > 0 {
		fmt.Fprintf(e.out, "\x1b[%dC", targetTermCol)
	}
	e.screenRow = cursorRow
	e.screenCol = cursorCol
}

// insertRune inserts r at the cursor. Appending at the end is optimized to a
// single echo with no redraw; mid-line inserts trigger a full redraw.
func (e *lineEditor) insertRune(r rune) {
	at := e.cursor
	if at == len(e.buf) {
		e.buf = append(e.buf, r)
		_, _ = io.WriteString(e.out, string(r))
		e.cursor++
		if r == '\n' {
			e.screenRow++
			e.screenCol = 0
		} else {
			e.screenCol++
		}
		return
	}
	e.buf = spliceRunes(e.buf, at, []rune{r})
	e.shiftBlocks(at, 1)
	e.cursor++
	if r == '\n' {
		e.screenRow++
		e.screenCol = 0
	} else {
		e.screenCol++
	}
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
			_, _ = io.WriteString(e.out, eraseVisibleText(b.display))
			e.buf = append(e.buf[:b.start], e.buf[b.end:]...)
			e.blocks = append(e.blocks[:i], e.blocks[i+1:]...)
			e.cursor = b.start
			e.screenRow, e.screenCol = e.computeRowCol(e.cursor)
			if len(e.buf) > 0 {
				e.redraw()
			}
			return
		}
	}
	at := e.cursor - 1
	deleted := e.buf[at]
	e.buf = append(e.buf[:at], e.buf[at+1:]...)
	e.shiftBlocks(at+1, -1)
	e.cursor = at
	// Optimized path: deleting a non-newline rune at the end of the buffer.
	if e.cursor == len(e.buf) && deleted != '\n' && e.screenCol > 0 {
		_, _ = io.WriteString(e.out, "\b \b")
		e.screenCol--
	} else {
		e.redraw()
	}
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

// moveUp moves the cursor to the previous line if the buffer contains newlines.
// Returns true if it handled the movement (i.e., the buffer was multi-line).
func (e *lineEditor) moveUp() bool {
	if e.screenRow == 0 {
		return false
	}
	// Find the start of the current line.
	lineStart := e.lineStartIndex(e.cursor)
	// Find the start of the previous line.
	prevLineStart := e.lineStartIndex(lineStart - 1)
	col := e.cursor - lineStart
	target := prevLineStart + col
	if target >= lineStart-1 {
		target = lineStart - 1 // clamp to end of previous line (before the \n)
	}
	// Don't go past the end of the previous line.
	prevLineEnd := lineStart - 1
	if target > prevLineEnd {
		target = prevLineEnd
	}
	e.moveToCursor(target)
	return true
}

// moveDown moves the cursor to the next line if the buffer contains newlines.
// Returns true if it handled the movement.
func (e *lineEditor) moveDown() bool {
	if e.screenRow >= e.lineCount()-1 {
		return false
	}
	lineStart := e.lineStartIndex(e.cursor)
	col := e.cursor - lineStart
	nextLineStart := e.lineEndIndex(e.cursor) + 1
	nextLineEnd := e.lineEndIndex(nextLineStart)
	target := nextLineStart + col
	if target > nextLineEnd {
		target = nextLineEnd
	}
	e.moveToCursor(target)
	return true
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

// lineCount returns the number of lines in the buffer (1 + number of newlines).
func (e *lineEditor) lineCount() int {
	count := 1
	for _, r := range e.buf {
		if r == '\n' {
			count++
		}
	}
	return count
}

// isMultiLine returns true if the buffer contains any newlines.
func (e *lineEditor) isMultiLine() bool {
	for _, r := range e.buf {
		if r == '\n' {
			return true
		}
	}
	return false
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

// historyUp recalls the previous history entry. Only active when the buffer is
// single-line; in multi-line mode, Up navigates between lines instead.
func (e *lineEditor) historyUp() {
	if e.isMultiLine() {
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

// historyDown recalls the next history entry. Only active when the buffer is
// single-line; in multi-line mode, Down navigates between lines instead.
func (e *lineEditor) historyDown() {
	if e.isMultiLine() {
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
