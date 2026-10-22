package tui

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

// errPromptTimeout reports that a permission prompt expired before the user
// answered.
var errPromptTimeout = errors.New("permission prompt timed out")

const (
	enableBracketedPaste  = "\x1b[?2004h"
	disableBracketedPaste = "\x1b[?2004l"
	bracketedPasteStart   = "\x1b[200~"
	bracketedPasteEnd     = "\x1b[201~"
)

// GetInput reads a line from stdin, supporting a ";;...;;" multi-line marker.
func (u *PlainUI) GetInput(prompt string) (input string, err error) {
	defer func() {
		input = cleanBracketedPasteMarkers(input)
	}()
	u.stopSpinner()
	u.mu.Lock()
	u.flushPendingToolLocked()
	u.endAssistantLineLocked()
	renderedPrompt := u.renderPromptString(prompt)
	_, _ = io.WriteString(u.out, renderedPrompt)
	u.mu.Unlock()

	if u.in != nil && term.IsTerminal(int(u.in.Fd())) {
		return u.readInteractiveInput(renderedPrompt)
	}

	line, err := u.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = trimLine(line)
	if strings.HasPrefix(line, bracketedPasteStart) {
		pasted, pending, err := readBracketedPaste(u.reader, strings.TrimPrefix(line, bracketedPasteStart))
		if err != nil {
			return "", err
		}
		return readPastedInput(u.reader, pasted, pending)
	}
	if strings.TrimSpace(line) != ";;" {
		input, multiline, pending := readBufferedInput(u.reader, line)
		if multiline {
			return readPastedInput(u.reader, input, pending)
		}
		return cleanBracketedPasteMarkers(input), nil
	}

	lines := make([]string, 0, 8)
	for {
		_, _ = io.WriteString(u.out, u.style.DimGreen("... "))
		next, err := u.reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		next = trimLine(next)
		if strings.TrimSpace(next) == ";;" {
			break
		}
		lines = append(lines, next)
	}
	return strings.Join(lines, "\n"), nil
}

func (u *PlainUI) readInteractiveInput(renderedPrompt string) (string, error) {
	fd := int(u.in.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	u.rawMu.Lock()
	u.rawRestore = func() { _ = term.Restore(fd, oldState) }
	u.rawMu.Unlock()
	defer func() {
		u.rawMu.Lock()
		u.rawRestore = nil
		u.rawMu.Unlock()
		_ = term.Restore(fd, oldState)
	}()
	// Re-enable bracketed paste for this read so the terminal wraps pasted
	// content with markers even if a previous read disabled it. The matching
	// disable is deferred so paste mode only covers input, not assistant output.
	if u.bracketedPaste {
		_, _ = io.WriteString(u.out, enableBracketedPaste)
		defer func() { _, _ = io.WriteString(u.out, disableBracketedPaste) }()
	}
	// Read through the buffered reader so bytes that AskPermission left in the
	// bufio buffer are not lost when we re-enter raw mode.
	return readInteractiveInputStreamWithStyle(u.reader, u.out, u.style, u.history, renderedPrompt)
}

func readInteractiveInputStream(reader io.Reader, out io.Writer) (string, error) {
	return readInteractiveInputStreamWithStyle(reader, out, Style{}, nil, "")
}

// renderPromptString formats the prompt label with the current style and plan
// mode. The returned string is both written to out (for the initial display)
// and passed to the line editor (for multi-line redraws).
func (u *PlainUI) renderPromptString(prompt string) string {
	if u.style.Enabled() {
		userIcon := u.style.BrightGreen(iconUser)
		userLabel := u.style.BoldGreen("user")
		promptLabel := u.style.BoldGreen(prompt)
		if u.planMode {
			userIcon = u.style.Yellow(iconUser)
			userLabel = u.style.BoldYellow("user")
			promptLabel = u.style.BoldYellow(prompt)
		}
		return fmt.Sprintf("%s %s %s", userIcon, userLabel, promptLabel)
	}
	return prompt
}

func pasteMarkerVariants(marker string) [][]byte {
	return [][]byte{
		[]byte(marker),
		[]byte(strings.Replace(marker, "\x1b", "^[[", 1)),
		[]byte(strings.Replace(marker, "\x1b", "^[", 1)),
	}
}

func matchingMarker(value []byte, markers [][]byte) []byte {
	for _, marker := range markers {
		if len(value) >= len(marker) && string(value[len(value)-len(marker):]) == string(marker) {
			return marker
		}
	}
	return nil
}

func hasCompleteMarker(value []byte, markers [][]byte) bool {
	return matchingMarker(value, markers) != nil
}

func hasMarkerPrefix(value []byte, markers [][]byte) bool {
	for _, marker := range markers {
		if len(value) <= len(marker) && string(marker[:len(value)]) == string(value) {
			return true
		}
	}
	return false
}

func formatPastedBlock(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = strings.Map(func(r rune) rune {
		if r < 0x20 {
			return ' '
		}
		return r
	}, content)
	content = strings.Join(strings.Fields(content), " ")
	const maxPreview = 64
	count := utf8.RuneCountInString(content)
	if count <= maxPreview {
		return "[block]" + content + "[/block]"
	}
	runes := []rune(content)
	preview := string(runes[:maxPreview])
	return fmt.Sprintf("[block]%s... (%d chars more)...[/block]", preview, count-maxPreview)
}

func eraseVisibleText(text string) string {
	count := displayWidth(text)
	if count == 0 {
		return ""
	}
	return strings.Repeat("\b", count) + strings.Repeat(" ", count) + strings.Repeat("\b", count)
}

func readBracketedPaste(reader interface{ ReadString(byte) (string, error) }, first string) (string, string, error) {
	lines := make([]string, 0, 8)
	line := first
	for {
		if end := strings.Index(line, bracketedPasteEnd); end >= 0 {
			lines = append(lines, trimLine(line[:end]))
			pending := line[end+len(bracketedPasteEnd):]
			return strings.Join(lines, "\n"), pending, nil
		}
		lines = append(lines, trimLine(line))
		next, err := reader.ReadString('\n')
		if err != nil {
			return "", "", err
		}
		line = next
	}
}

func readPastedInput(reader interface{ ReadString(byte) (string, error) }, pasted, pending string) (string, error) {
	if pending == "" {
		var err error
		pending, err = reader.ReadString('\n')
		if err != nil {
			return "", err
		}
	}
	continuation := trimLine(pending)
	if continuation == "" {
		return cleanBracketedPasteMarkers(strings.TrimRight(pasted, "\n")), nil
	}
	return cleanBracketedPasteMarkers(pasted + continuation), nil
}

func cleanBracketedPasteMarkers(input string) string {
	for _, marker := range []string{
		bracketedPasteStart,
		bracketedPasteEnd,
		"^[[200~",
		"^[[201~",
		"^[200~",
		"^[201~",
	} {
		input = strings.ReplaceAll(input, marker, "")
	}
	return input
}

func readBufferedInput(reader *bufio.Reader, first string) (string, bool, string) {
	lines := []string{first}
	for reader.Buffered() > 0 {
		next, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		lines = append(lines, trimLine(next))
	}
	if len(lines) <= 1 {
		return lines[0], false, ""
	}

	pending := ""
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
		pending = "\n"
	}
	return strings.Join(lines, "\n"), true, pending
}

// AskPermission prompts the user with a colored panel for tool consent.
// The user can press a single digit (1-7) without Enter; on non-TTY input we
// fall back to line-based reading.
//
// The wait is bounded: the prompt shows a countdown and, when it reaches zero,
// the action is denied with DecisionTimeout so a run never stalls on a prompt
// nobody is there to answer. The bound comes from the configured permission
// timeout and disappears when it is disabled or when stdin cannot be watched.
func (u *PlainUI) AskPermission(tool string, params map[string]any) Decision {
	u.stopSpinner()
	u.mu.Lock()
	u.flushPendingToolLocked()

	_, _ = io.WriteString(u.out, buildPermissionPrompt(u.style, permissionPayloadLines(tool, params)))
	u.mu.Unlock()

	timeout := u.cfg.EffectivePermissionTimeout()
	if timeout > 0 && u.countdownAvailable() {
		key, timedOut := u.readPermissionKeyCountdown(timeout)
		if timedOut {
			u.repaintPermissionLine(u.style.BoldYellow("⏳ no answer — permission denied"))
			fmt.Fprint(u.out, "\n")
			return DecisionTimeout
		}
		if key != 0 {
			u.repaintPermissionLine("")
			fmt.Fprintf(u.out, "%c\n", key)
			return permissionDecision(strings.ToLower(string(key)))
		}
	}

	var s string
	if ch, ok := u.readSingleChar(); ok {
		fmt.Fprintf(u.out, "%c\n", ch)
		s = strings.ToLower(string(ch))
	} else {
		line, err := u.reader.ReadString('\n')
		if err != nil {
			return DecisionDenyOnce
		}
		s = strings.TrimSpace(strings.ToLower(trimLine(line)))
	}
	return permissionDecision(s)
}

// permissionDecision maps the key or line the user typed to a Decision. An empty
// or unrecognized answer denies once, which keeps the run moving instead of
// guessing an approval.
func permissionDecision(s string) Decision {
	switch s {
	case "1":
		return DecisionAllowOnce
	case "2":
		return DecisionAllowSession
	case "3":
		return DecisionAllowPersistent
	case "4":
		return DecisionDenyOnce
	case "5":
		return DecisionDenySession
	case "6":
		return DecisionDenyPersistent
	case "7", "q", "c":
		return DecisionCancel
	default:
		return DecisionDenyOnce
	}
}

// countdownAvailable reports whether this prompt can be watched with a
// deadline. Both ends must be real terminals: stdout, because the countdown is
// repainted in place with escape sequences, and stdin, because raw mode is what
// delivers a single keypress without Enter.
func (u *PlainUI) countdownAvailable() bool {
	if u.in == nil || u.reader == nil || u.out == nil || !u.outIsTTY {
		return false
	}
	fd := int(u.in.Fd())
	return term.IsTerminal(fd) && inputWatchable(fd)
}

// readPermissionKeyCountdown waits for one keypress, repainting the time left on
// the prompt's input line once per second. It returns timedOut when the deadline
// arrives first; a zero key with timedOut false means the keypress could not be
// read in raw mode and the caller should fall back to a line read.
func (u *PlainUI) readPermissionKeyCountdown(timeout time.Duration) (byte, bool) {
	deadline := time.Now().Add(timeout)
	u.repaintPermissionLine(u.style.Dim(countdownText(timeout)))
	key, err := u.readKeyUntil(deadline, func(remaining time.Duration) {
		u.repaintPermissionLine(u.style.Dim(countdownText(remaining)))
	})
	if errors.Is(err, errPromptTimeout) {
		return 0, true
	}
	if err != nil {
		return 0, false
	}
	return key, false
}

// readKeyUntil waits for one byte from the terminal until the deadline, calling
// tick with the time left between polls. It holds escMu and raw mode for the
// whole wait, exactly like readSingleChar, so the ESC monitor cannot swallow the
// key the user pressed.
func (u *PlainUI) readKeyUntil(deadline time.Time, tick func(time.Duration)) (byte, error) {
	fd := int(u.in.Fd())
	u.escMu.Lock()
	defer u.escMu.Unlock()
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return 0, err
	}
	defer term.Restore(fd, oldState)

	ch, err := waitByte(fd, u.reader, deadline, tick)
	if err != nil {
		return 0, err
	}
	u.drainTrailingNewline()
	return ch, nil
}

// repaintPermissionLine rewrites the line the prompt left the cursor on, keeping
// the "▸ " caret so the prompt still reads as a single input line. Clearing to
// the end of the line removes whatever the previous repaint left behind.
func (u *PlainUI) repaintPermissionLine(text string) {
	caret := u.style.Dim("  ▸ ")
	if text == "" {
		fmt.Fprintf(u.out, "\r\x1b[K%s", caret)
		return
	}
	fmt.Fprintf(u.out, "\r\x1b[K%s%s", caret, text)
}

// countdownText renders the time left before the action is denied. Seconds are
// rounded up so a fresh 3-minute prompt reads 3:00 and never shows 0:00 while
// time is still left.
func countdownText(remaining time.Duration) string {
	if remaining < 0 {
		remaining = 0
	}
	secs := int((remaining + time.Second - 1) / time.Second)
	return fmt.Sprintf("⏳ auto-deny in %d:%02d", secs/60, secs%60)
}

// readSingleChar puts stdin in raw mode for one keypress and returns it.
// It returns false if stdin is not a TTY or raw mode cannot be entered.
// The escMu lock coordinates with the ESC monitor goroutine so only one
// reader accesses stdin at a time.
func (u *PlainUI) readSingleChar() (byte, bool) {
	if u.in == nil || u.reader == nil {
		return 0, false
	}
	fd := int(u.in.Fd())
	u.escMu.Lock()
	defer u.escMu.Unlock()
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return 0, false
	}
	defer term.Restore(fd, oldState)

	ch, err := u.reader.ReadByte()
	if err != nil {
		return 0, false
	}
	u.drainTrailingNewline()
	return ch, true
}

// drainTrailingNewline consumes the newline a terminal sends along with a
// single-key answer, so the next prompt does not start with an empty line of
// input. It only looks at bytes the bufio reader already buffered, so it never
// blocks waiting for one.
func (u *PlainUI) drainTrailingNewline() {
	if u.reader.Buffered() == 0 {
		return
	}
	next, err := u.reader.Peek(1)
	if err != nil {
		return
	}
	switch next[0] {
	case '\n':
		_, _ = u.reader.ReadByte()
	case '\r':
		_, _ = u.reader.ReadByte()
		if u.reader.Buffered() > 0 {
			if next, err = u.reader.Peek(1); err == nil && next[0] == '\n' {
				_, _ = u.reader.ReadByte()
			}
		}
	}
}

func buildPermissionPrompt(st Style, payload []string) string {
	// Rendering is separate from stdin reads so permission copy stays testable.
	var b strings.Builder
	b.WriteString("\n")
	for _, raw := range payload {
		line := fitGateLine(raw, permissionDisplayMaxRunes)
		b.WriteString(st.Dim("  "))
		writePermissionPayloadLine(&b, st, line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(st.Dim("  "))
	b.WriteString(st.BoldBrightGreen("PERMISSION REQUIRED"))
	b.WriteString(st.Dim(" — choose an option:\n"))

	writePermissionOption := func(n string, label, desc string, color func(string) string) {
		b.WriteString(st.Dim("    "))
		b.WriteString(st.BoldBrightGreen("[" + n + "] "))
		b.WriteString(color(label))
		if desc != "" {
			b.WriteString(st.Dim(" — " + desc))
		}
		b.WriteString("\n")
	}
	writePermissionOption("1", "Allow once", "this action only", st.BrightGreen)
	writePermissionOption("2", "Allow this session", "until exit", st.Green)
	writePermissionOption("3", "Always allow", "save permission", st.BrightBlue)
	b.WriteString(st.Dim("    ─────────────────────────────\n"))
	writePermissionOption("4", "Deny once", "ask again next time", st.Yellow)
	writePermissionOption("5", "Block this session", "until exit", st.Red)
	writePermissionOption("6", "Always deny", "save block", st.BoldRed)
	writePermissionOption("7", "Cancel", "abort this run", st.Magenta)
	b.WriteString("\n")
	b.WriteString(st.Dim("  Press a number 1–7; Enter is not required.\n"))
	b.WriteString(st.BoldBrightGreen("  ▸ "))
	return b.String()
}

func writePermissionPayloadLine(b *strings.Builder, st Style, line string) {
	switch {
	case strings.HasPrefix(line, "ACTION") || strings.HasPrefix(line, "TARGET"):
		b.WriteString(st.BoldCyan(line))
	case strings.HasPrefix(line, "WARNING:") || strings.HasPrefix(line, "NOTE:"):
		b.WriteString(st.BoldYellow(line))
	case strings.HasPrefix(line, "FILE:"):
		b.WriteString(st.BrightBlue(line))
	case strings.HasPrefix(line, "LINES:"):
		b.WriteString(st.Yellow(line))
	case strings.HasPrefix(line, "CHANGE:") || strings.HasPrefix(line, "SIZE:"):
		b.WriteString(st.Yellow(line))
	case line == "PREVIEW:":
		b.WriteString(st.BoldYellow(line))
	case strings.HasPrefix(line, "+ "):
		b.WriteString(st.Green(line))
	case strings.HasPrefix(line, "- "):
		b.WriteString(st.Red(line))
	case strings.HasPrefix(line, "…"):
		b.WriteString(st.Dim(line))
	case strings.HasSuffix(line, ":") && !strings.HasPrefix(line, " "):
		b.WriteString(st.Yellow(line))
	default:
		b.WriteString(st.Dim(line))
	}
}

func trimLine(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
