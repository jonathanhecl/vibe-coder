package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// fitBoxWidth returns a box width that fits inside cols columns while keeping
// a 2-column margin on terminals >= 14 columns wide.
func fitBoxWidth(cols int) int {
	if cols <= 0 {
		cols = 80
	}
	w := cols - 2
	if w < 12 {
		w = cols
		if w < 6 {
			w = 6
		}
	}
	return w
}

// isZeroWidthJoinRune reports whether r attaches to the preceding rune
// (combining marks, variation selectors, and zero-width joiners).
func isZeroWidthJoinRune(r rune) bool {
	if r == '\u200D' || r == '\uFE0E' || r == '\uFE0F' {
		return true
	}
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
		return true
	}
	return runeCellWidth(r) == 0 && !unicode.IsControl(r)
}

// nextCSISequence returns the length of a CSI escape sequence (\x1b[...<final>)
// starting at s[0], plus whether it is an SGR (...m) sequence and whether it
// resets SGR attributes (\x1b[0m or \x1b[m). Returns 0 if s does not start with
// a complete CSI sequence.
func nextCSISequence(s string) (seqLen int, isSGR bool, isReset bool) {
	if len(s) < 3 || s[0] != '\x1b' || s[1] != '[' {
		return 0, false, false
	}
	for i := 2; i < len(s); i++ {
		b := s[i]
		if b >= 0x40 && b <= 0x7e {
			seq := s[:i+1]
			isSGR = b == 'm'
			isReset = seq == "\x1b[0m" || seq == "\x1b[m"
			return i + 1, isSGR, isReset
		}
	}
	return 0, false, false
}

// nextOtherEscape returns the length of a non-CSI escape sequence starting at
// s[0] so stray terminal sequences are preserved as zero-width.
func nextOtherEscape(s string) int {
	if len(s) == 0 || s[0] != '\x1b' {
		return 0
	}
	if len(s) == 1 {
		return 1
	}
	// OSC (\x1b]...\x07 or \x1b]...\x1b\\)
	if s[1] == ']' {
		for i := 2; i < len(s); i++ {
			if s[i] == '\x07' {
				return i + 1
			}
			if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	}
	_, sz := utf8.DecodeRuneInString(s[1:])
	if sz <= 0 {
		sz = 1
	}
	return 1 + sz
}

// nextGraphemeCluster returns one user-perceived grapheme cluster starting at
// s[0] (without leading ESC) so combining marks, variation selectors, and ZWJ
// emoji sequences stay together across wraps and truncations.
func nextGraphemeCluster(s string) string {
	r, sz := utf8.DecodeRuneInString(s)
	if sz <= 0 {
		return s[:1]
	}
	end := sz
	prevZWJ := r == '\u200D'
	for end < len(s) {
		if s[end] == '\x1b' {
			break
		}
		nr, nsz := utf8.DecodeRuneInString(s[end:])
		if nsz <= 0 {
			break
		}
		if prevZWJ {
			end += nsz
			prevZWJ = nr == '\u200D'
			continue
		}
		if isZeroWidthJoinRune(nr) {
			end += nsz
			prevZWJ = nr == '\u200D'
			continue
		}
		break
	}
	return s[:end]
}

type ansiToken struct {
	text    string
	isANSI  bool
	isSGR   bool
	isReset bool
	isSpace bool
}

func tokenizeANSI(s string) []ansiToken {
	var tokens []ansiToken
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			if n, isSGR, isReset := nextCSISequence(s[i:]); n > 0 {
				tokens = append(tokens, ansiToken{
					text:    s[i : i+n],
					isANSI:  true,
					isSGR:   isSGR,
					isReset: isReset,
				})
				i += n
				continue
			}
			n := nextOtherEscape(s[i:])
			tokens = append(tokens, ansiToken{
				text:   s[i : i+n],
				isANSI: true,
			})
			i += n
			continue
		}
		cluster := nextGraphemeCluster(s[i:])
		r, _ := utf8.DecodeRuneInString(cluster)
		tokens = append(tokens, ansiToken{
			text:    cluster,
			isSpace: len(cluster) == utf8.RuneLen(r) && unicode.IsSpace(r),
		})
		i += len(cluster)
	}
	return tokens
}

// wrapANSILine wraps a single logical line (with ANSI SGR escapes preserved)
// to at most width display cells per visual line, never emitting trailing
// right-padding spaces.
func wrapANSILine(line string, width int) []string {
	if width < 1 {
		width = 1
	}
	if line == "" {
		return []string{""}
	}

	norm := strings.ReplaceAll(line, "\r\n", "\n")
	norm = strings.ReplaceAll(norm, "\r", "")
	norm = strings.ReplaceAll(norm, "\t", "  ")

	if strings.ContainsRune(norm, '\n') {
		parts := strings.Split(norm, "\n")
		var out []string
		for _, p := range parts {
			out = append(out, wrapANSILine(p, width)...)
		}
		if len(out) == 0 {
			return []string{""}
		}
		return out
	}

	if displayWidth(norm) <= width {
		return []string{norm}
	}

	tokens := tokenizeANSI(norm)
	var out []string
	var cur strings.Builder
	curOpenSGR := false
	curHasVisible := false
	curHasNonSpace := false
	activeSGR := ""

	applySGR := func(tok ansiToken) {
		if !tok.isSGR {
			return
		}
		if tok.isReset {
			activeSGR = ""
		} else {
			activeSGR += tok.text
		}
	}

	writeTokenToCur := func(tok ansiToken) {
		cur.WriteString(tok.text)
		if tok.isSGR {
			if tok.isReset {
				curOpenSGR = false
			} else {
				curOpenSGR = true
			}
		} else if !tok.isANSI {
			curHasVisible = true
			if !tok.isSpace {
				curHasNonSpace = true
			}
		}
	}

	flushLine := func(reopenSGR string) {
		if curOpenSGR {
			cur.WriteString("\x1b[0m")
		}
		out = append(out, cur.String())
		cur.Reset()
		curOpenSGR = false
		curHasVisible = false
		curHasNonSpace = false
		if reopenSGR != "" {
			cur.WriteString(reopenSGR)
			curOpenSGR = true
		}
	}

	resetLine := func(reopenSGR string) {
		cur.Reset()
		curOpenSGR = false
		curHasVisible = false
		curHasNonSpace = false
		if reopenSGR != "" {
			cur.WriteString(reopenSGR)
			curOpenSGR = true
		}
	}

	var pending []ansiToken
	pendingHasSpace := false

	// flushPendingOnLine writes buffered separator spaces if any, or drops them
	// when starting a wrapped continuation line.
	dropPending := func() {
		for _, tok := range pending {
			if tok.isANSI {
				applySGR(tok)
			}
		}
		pending = pending[:0]
		pendingHasSpace = false
	}

	for i := 0; i < len(tokens); {
		tok := tokens[i]

		if tok.isANSI {
			if pendingHasSpace {
				pending = append(pending, tok)
			} else {
				applySGR(tok)
				writeTokenToCur(tok)
			}
			i++
			continue
		}

		if tok.isSpace {
			// Leading indentation on the very first visual line is kept as-is.
			if len(out) == 0 && !curHasVisible {
				w := displayWidth(cur.String() + tok.text)
				if w <= width {
					writeTokenToCur(tok)
					i++
					continue
				}
				flushLine(activeSGR)
				// Drop extra spaces that caused wrap on continuation line.
				for i < len(tokens) && (tokens[i].isSpace || tokens[i].isANSI) {
					if tokens[i].isANSI {
						applySGR(tokens[i])
					}
					i++
				}
				continue
			}

			// Drop spaces directly after an automatic soft-wrap.
			if len(out) > 0 && !curHasVisible {
				for i < len(tokens) && (tokens[i].isSpace || tokens[i].isANSI) {
					if tokens[i].isANSI {
						applySGR(tokens[i])
					}
					i++
				}
				continue
			}

			pending = append(pending, tok)
			pendingHasSpace = true
			i++
			continue
		}

		// Collect one word (consecutive non-space tokens, including embedded ANSI).
		wordStart := i
		var wordPlain strings.Builder
		for i < len(tokens) && !tokens[i].isSpace {
			if !tokens[i].isANSI {
				wordPlain.WriteString(tokens[i].text)
			}
			i++
		}
		wordSlice := tokens[wordStart:i]
		wordWidth := displayWidth(wordPlain.String())

		// Compute space string if pending spaces exist.
		var spaceStr strings.Builder
		if pendingHasSpace {
			for _, pt := range pending {
				if !pt.isANSI {
					spaceStr.WriteString(pt.text)
				}
			}
		}

		curWithPending := cur.String() + spaceStr.String() + wordPlain.String()
		if displayWidth(curWithPending) <= width {
			for _, pt := range pending {
				if pt.isANSI {
					applySGR(pt)
				}
				writeTokenToCur(pt)
			}
			pending = pending[:0]
			pendingHasSpace = false
			for _, wt := range wordSlice {
				if wt.isANSI {
					applySGR(wt)
				}
				writeTokenToCur(wt)
			}
			continue
		}

		// Word + pending spaces do not fit on the current line.
		if curHasNonSpace {
			// Close the current line BEFORE applying any SGR changes inside
			// pending spaces, then drop separator spaces on the wrap.
			sgrBeforeSpaces := activeSGR
			for _, pt := range pending {
				if pt.isANSI {
					applySGR(pt)
				}
			}
			pending = pending[:0]
			pendingHasSpace = false
			if curOpenSGR && sgrBeforeSpaces == "" {
				cur.WriteString("\x1b[0m")
				curOpenSGR = false
			}
			flushLine(activeSGR)
		} else {
			// Avoid emitting a spaces-only line when leading indentation cannot
			// fit before the first word on the line.
			dropPending()
			resetLine(activeSGR)
		}

		if wordWidth <= width {
			for _, wt := range wordSlice {
				if wt.isANSI {
					applySGR(wt)
				}
				writeTokenToCur(wt)
			}
			continue
		}

		// Hard-wrap a token wider than width cluster by cluster.
		for _, wt := range wordSlice {
			if wt.isANSI {
				applySGR(wt)
				writeTokenToCur(wt)
				continue
			}
			if curHasVisible && displayWidth(cur.String()+wt.text) > width {
				flushLine(activeSGR)
			}
			writeTokenToCur(wt)
		}
	}

	// Trailing spaces that were not followed by another word: keep only if they
	// fit on the current line and aren't trailing-space bloat from a wrap.
	if pendingHasSpace && curHasVisible {
		var spacePlain strings.Builder
		for _, pt := range pending {
			if !pt.isANSI {
				spacePlain.WriteString(pt.text)
			}
		}
		if displayWidth(cur.String()+spacePlain.String()) <= width {
			for _, pt := range pending {
				if pt.isANSI {
					applySGR(pt)
				}
				writeTokenToCur(pt)
			}
		} else {
			dropPending()
		}
	} else {
		dropPending()
	}

	if cur.Len() > 0 || len(out) == 0 {
		if curOpenSGR && len(out) > 0 {
			cur.WriteString("\x1b[0m")
		}
		out = append(out, cur.String())
	}
	return out
}

// truncateANSIWidth truncates a single line containing ANSI sequences to at
// most maxCols display cells, appending "…" if truncated and closing open SGR.
func truncateANSIWidth(line string, maxCols int) string {
	if maxCols <= 0 {
		return ""
	}
	norm := strings.ReplaceAll(line, "\r\n", " ")
	norm = strings.ReplaceAll(norm, "\n", " ")
	norm = strings.ReplaceAll(norm, "\r", "")
	norm = strings.ReplaceAll(norm, "\t", "  ")

	if displayWidth(norm) <= maxCols {
		return norm
	}
	if maxCols == 1 {
		return "…"
	}

	target := maxCols - 1 // reserve 1 cell for "…"
	tokens := tokenizeANSI(norm)
	var b strings.Builder
	openSGR := false
	for _, tok := range tokens {
		if tok.isANSI {
			b.WriteString(tok.text)
			if tok.isSGR {
				openSGR = !tok.isReset
			}
			continue
		}
		if displayWidth(b.String()+tok.text) > target {
			break
		}
		b.WriteString(tok.text)
	}
	b.WriteString("…")
	if openSGR {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// renderLeftRailBlock renders body with a left thick rail ("┃ ") wrapped to
// termCols without trailing right-padding spaces, preventing phantom blank
// lines when any line exceeds the terminal width.
func renderLeftRailBlock(body string, borderColor lipgloss.Color, marginLeft int, termCols int) string {
	if termCols <= 0 {
		termCols = 80
	}
	if marginLeft < 0 {
		marginLeft = 0
	}
	margin := strings.Repeat(" ", marginLeft)
	rail := lipgloss.NewStyle().Foreground(borderColor).Render("┃")
	prefix := margin + rail + " "
	prefixWidth := marginLeft + 2

	contentWidth := termCols - prefixWidth - 1 // 1-column right safety margin
	if contentWidth < 8 {
		contentWidth = termCols - prefixWidth
		if contentWidth < 4 {
			contentWidth = 4
		}
	}

	norm := strings.ReplaceAll(body, "\r\n", "\n")
	norm = strings.ReplaceAll(norm, "\r", "")
	logicalLines := strings.Split(norm, "\n")

	var out []string
	for _, ln := range logicalLines {
		wrapped := wrapANSILine(ln, contentWidth)
		for _, wl := range wrapped {
			out = append(out, prefix+wl)
		}
	}
	return strings.Join(out, "\n")
}

// renderRoundedBox draws a rounded border box of exact display width boxWidth
// around lines, wrapping any wide or ANSI-styled lines to innerWidth so the
// box never expands past the terminal width.
func renderRoundedBox(lines []string, borderStyle lipgloss.Style, boxWidth int) string {
	innerWidth := boxWidth - 4 // "│ " + content + " │"
	if innerWidth < 2 {
		innerWidth = 2
		boxWidth = 6
	}

	horiz := strings.Repeat("─", boxWidth-2)
	top := borderStyle.Render("╭" + horiz + "╮")
	bottom := borderStyle.Render("╰" + horiz + "╯")
	leftRail := borderStyle.Render("│")
	rightRail := borderStyle.Render("│")

	if len(lines) == 0 {
		lines = []string{""}
	}

	var b strings.Builder
	b.WriteString(top)
	b.WriteByte('\n')

	for _, ln := range lines {
		wrapped := wrapANSILine(ln, innerWidth)
		for _, wl := range wrapped {
			w := displayWidth(wl)
			if w > innerWidth {
				wl = truncateANSIWidth(wl, innerWidth)
				w = displayWidth(wl)
			}
			pad := ""
			if innerWidth > w {
				pad = strings.Repeat(" ", innerWidth-w)
			}
			b.WriteString(leftRail)
			b.WriteByte(' ')
			b.WriteString(wl)
			b.WriteString(pad)
			b.WriteByte(' ')
			b.WriteString(rightRail)
			b.WriteByte('\n')
		}
	}

	b.WriteString(bottom)
	return b.String()
}
