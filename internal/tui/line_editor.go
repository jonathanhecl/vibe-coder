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

// lineEditor is a minimal readline-style editor for a single input line. It
// tracks a rune buffer, a logical cursor (rune index), and a screen cursor
// (display columns from the start of the editable region). Paste blocks are
// atomic: the cursor may sit before or after a block but never inside it.
type lineEditor struct {
	out   io.Writer
	style Style

	buf          []rune
	cursor       int
	screenCursor int
	blocks       []blockSpan

	history     *inputHistory
	histEntries []string
	histIdx     int
	draft       string
}

func newLineEditor(out io.Writer, style Style, history *inputHistory) *lineEditor {
	e := &lineEditor{out: out, style: style, history: history}
	if history != nil {
		e.histEntries = history.snapshot()
	}
	e.histIdx = len(e.histEntries)
	return e
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
func readInteractiveInputStreamWithStyle(reader io.Reader, out io.Writer, style Style, history *inputHistory) (string, error) {
	e := newLineEditor(out, style, history)
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

// displayWidth returns the screen columns from the start of the editable region
// up to buf index idx. Block regions contribute their preview width instead of
// their rune count. idx must not fall strictly inside a block (the cursor never
// does).
func (e *lineEditor) displayWidth(idx int) int {
	w := 0
	pos := 0
	for _, b := range e.blocks {
		if b.end <= idx {
			w += b.start - pos
			w += utf8.RuneCountInString(b.display)
			pos = b.end
		} else if b.start < idx {
			w += b.start - pos
			pos = b.start
			break
		} else {
			break
		}
	}
	return w + (idx - pos)
}

// moveToCursor emits the relative cursor motion needed to move the screen
// cursor from its current position to the one for newCursor.
func (e *lineEditor) moveToCursor(newCursor int) {
	target := e.displayWidth(newCursor)
	switch {
	case target > e.screenCursor:
		fmt.Fprintf(e.out, "\x1b[%dC", target-e.screenCursor)
	case target < e.screenCursor:
		fmt.Fprintf(e.out, "\x1b[%dD", e.screenCursor-target)
	}
	e.cursor = newCursor
	e.screenCursor = target
}

// redraw repaints the whole editable region: jump to its start, erase to end of
// line, render text and block previews, then reposition the cursor.
func (e *lineEditor) redraw() {
	if e.screenCursor > 0 {
		fmt.Fprintf(e.out, "\x1b[%dD", e.screenCursor)
	}
	_, _ = io.WriteString(e.out, "\x1b[K")
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
	end := e.displayWidth(len(e.buf))
	target := e.displayWidth(e.cursor)
	if back := end - target; back > 0 {
		fmt.Fprintf(e.out, "\x1b[%dD", back)
	}
	e.screenCursor = target
}

// insertRune inserts r at the cursor. Appending at the end is optimized to a
// single echo with no redraw; mid-line inserts trigger a full redraw.
func (e *lineEditor) insertRune(r rune) {
	at := e.cursor
	if at == len(e.buf) {
		e.buf = append(e.buf, r)
		_, _ = io.WriteString(e.out, string(r))
		e.cursor++
		e.screenCursor++
		return
	}
	e.buf = spliceRunes(e.buf, at, []rune{r})
	e.shiftBlocks(at, 1)
	e.cursor++
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
		e.screenCursor = e.displayWidth(e.cursor)
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
			e.screenCursor = e.displayWidth(e.cursor)
			if len(e.buf) > 0 {
				e.redraw()
			}
			return
		}
	}
	at := e.cursor - 1
	e.buf = append(e.buf[:at], e.buf[at+1:]...)
	e.shiftBlocks(at+1, -1)
	// A block whose interior was crossed (should not happen since the cursor
	// never enters a block) would need its end adjusted; shiftBlocks covers the
	// common at-or-after case.
	e.cursor = at
	if e.cursor == len(e.buf) {
		_, _ = io.WriteString(e.out, "\b \b")
		e.screenCursor--
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
	for _, b := range e.blocks {
		if b.start == e.cursor {
			next = b.end
			break
		}
	}
	e.moveToCursor(next)
}

func (e *lineEditor) moveHome() {
	e.moveToCursor(0)
}

func (e *lineEditor) moveEnd() {
	e.moveToCursor(len(e.buf))
}

func (e *lineEditor) historyUp() {
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

func (e *lineEditor) historyDown() {
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
