package tui

import (
	"fmt"
	"io"
	"strings"
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
	line = trimLine(line)
	if strings.TrimSpace(line) != ";;" {
		return line, nil
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

// AskPermission prompts the user with a colored panel for tool consent (English labels).
func (u *PlainUI) AskPermission(tool string, params map[string]any) Decision {
	u.stopSpinner()
	u.mu.Lock()
	u.flushPendingToolLocked()

	_, _ = io.WriteString(u.out, buildPermissionPrompt(u.style, permissionPayloadLines(tool, params)))
	u.mu.Unlock()

	line, err := u.reader.ReadString('\n')
	if err != nil {
		return DecisionDenyOnce
	}
	s := strings.TrimSpace(strings.ToLower(trimLine(line)))
	if s == "" {
		return DecisionDenyOnce
	}

	switch s {
	case "1", "y", "yes":
		return DecisionAllowOnce
	case "2":
		return DecisionAllowSession
	case "3", "a", "all":
		return DecisionAllowPersistent
	case "4", "n":
		return DecisionDenyOnce
	case "5":
		return DecisionDenySession
	case "6", "d", "deny-all", "denyall":
		return DecisionDenyPersistent
	case "7", "q", "quit", "c", "cancel":
		return DecisionCancel
	case "s", "skip", "skip-all-confirm":
		return DecisionYesMode
	default:
		return DecisionDenyOnce
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
	b.WriteString(st.Dim("      "))
	b.WriteString(st.DimGreen("[s]"))
	b.WriteString(st.Dim("  yes_mode  "))
	b.WriteString(st.Dim("(auto-approve non-dangerous tools)"))
	b.WriteString("\n\n")
	b.WriteString(st.Dim("  ;; "))
	b.WriteString(st.DimGreen("stdin"))
	b.WriteString(st.Dim(" › 1–7 | "))
	b.WriteString(st.DimGreen("y"))
	b.WriteString(st.Dim("/"))
	b.WriteString(st.DimGreen("a"))
	b.WriteString(st.Dim("/"))
	b.WriteString(st.DimGreen("d"))
	b.WriteString(st.Dim(" … "))
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
