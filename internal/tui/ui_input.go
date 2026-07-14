package tui

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	enableBracketedPaste  = "\x1b[?2004h"
	disableBracketedPaste = "\x1b[?2004l"
	bracketedPasteStart   = "\x1b[200~"
	bracketedPasteEnd     = "\x1b[201~"
)

// GetInput reads a line from stdin, supporting a ";;...;;" multi-line marker.
func (u *PlainUI) GetInput(prompt string) (string, error) {
	u.stopSpinner()
	u.mu.Lock()
	u.flushPendingToolLocked()
	u.endAssistantLineLocked()
	if u.style.Enabled() {
		userIcon := u.style.BrightGreen(iconUser)
		userLabel := u.style.BoldGreen("user")
		promptLabel := u.style.BoldGreen(prompt)
		if u.planMode {
			userIcon = u.style.Yellow(iconUser)
			userLabel = u.style.BoldYellow("user")
			promptLabel = u.style.BoldYellow(prompt)
		}
		_, _ = io.WriteString(u.out, fmt.Sprintf("%s %s %s",
			userIcon,
			userLabel,
			promptLabel,
		))
	} else {
		_, _ = io.WriteString(u.out, prompt)
	}
	u.mu.Unlock()

	line, err := u.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	u.turnStart = time.Now()
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
		return input, nil
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

func readBracketedPaste(reader interface{ ReadString(byte) (string, error) }, first string) (string, string, error) {
	lines := make([]string, 0, 8)
	line := first
	for {
		if end := strings.Index(line, bracketedPasteEnd); end >= 0 {
			lines = append(lines, line[:end])
			pending := line[end+len(bracketedPasteEnd):]
			return strings.Join(lines, "\n"), pending, nil
		}
		lines = append(lines, line)
		next, err := reader.ReadString('\n')
		if err != nil {
			return "", "", err
		}
		line = trimLine(next)
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
		return strings.TrimRight(pasted, "\n"), nil
	}
	return pasted + continuation, nil
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
func (u *PlainUI) AskPermission(tool string, params map[string]any) Decision {
	u.stopSpinner()
	u.mu.Lock()
	u.flushPendingToolLocked()

	_, _ = io.WriteString(u.out, buildPermissionPrompt(u.style, permissionPayloadLines(tool, params)))
	u.mu.Unlock()

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
	if s == "" {
		return DecisionDenyOnce
	}

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

// readSingleChar puts stdin in raw mode for one keypress and returns it.
// It returns false if stdin is not a TTY or raw mode cannot be entered.
func (u *PlainUI) readSingleChar() (byte, bool) {
	if u.in == nil || u.reader == nil {
		return 0, false
	}
	fd := int(u.in.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return 0, false
	}
	defer term.Restore(fd, oldState)

	ch, err := u.reader.ReadByte()
	if err != nil {
		return 0, false
	}
	if next, err := u.reader.Peek(1); err == nil {
		if next[0] == '\n' {
			_, _ = u.reader.ReadByte()
		} else if next[0] == '\r' {
			_, _ = u.reader.ReadByte()
			if next, err = u.reader.Peek(1); err == nil && next[0] == '\n' {
				_, _ = u.reader.ReadByte()
			}
		}
	}
	return ch, true
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
	b.WriteString(st.DimGreen("Choose"))
	b.WriteString(st.Dim(":\n"))

	writePermissionOption := func(n string, label, desc string, color func(string) string) {
		b.WriteString(st.Dim("      "))
		b.WriteString(st.BoldBrightGreen("[" + n + "] "))
		b.WriteString(color(label))
		if desc != "" {
			b.WriteString(st.Dim("  // " + desc))
		}
		b.WriteString("\n")
	}
	writePermissionOption("1", "Allow once", "this invocation only", st.BrightGreen)
	writePermissionOption("2", "Always allow (this session)", "until you exit vibe-coder", st.Green)
	writePermissionOption("3", "Always allow (saved)", "written to permissions file", st.BrightBlue)
	b.WriteString("\n")
	writePermissionOption("4", "Not now", "deny once; you may be asked again", st.Yellow)
	writePermissionOption("5", "No — block for this session", "this tool stays off until exit", st.Red)
	writePermissionOption("6", "Never allow (saved)", "written to permissions file", st.BoldRed)
	writePermissionOption("7", "Cancel", "abort this run", st.Magenta)
	b.WriteString("\n")
	b.WriteString("\n")
	b.WriteString(st.Dim("  ;; "))
	b.WriteString(st.DimGreen("stdin"))
	b.WriteString(st.Dim(" › 1–7 "))
	b.WriteString(st.BoldBrightGreen("▸ "))
	return b.String()
}

func writePermissionPayloadLine(b *strings.Builder, st Style, line string) {
	switch {
	case strings.HasPrefix(line, "TARGET"):
		b.WriteString(st.BoldCyan(line))
	case line == "PAYLOAD":
		b.WriteString(st.DimGreen("— "))
		b.WriteString(st.BrightGreen(line))
	case strings.HasPrefix(line, "+ "):
		b.WriteString(st.Green(line))
	case strings.HasPrefix(line, "- "):
		b.WriteString(st.Red(line))
	case strings.HasPrefix(line, "…"):
		b.WriteString(st.Dim(line))
	case line == "patch:" || line == "preview:":
		b.WriteString(st.Yellow(line))
	case strings.HasPrefix(line, "file:") || strings.HasPrefix(line, "change:") || strings.HasPrefix(line, "size:"):
		b.WriteString(st.Yellow(line))
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
