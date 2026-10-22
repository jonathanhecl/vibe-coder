package tui

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// ShowToolCall opens a "running" tool card. In TTY mode the card is rendered
// by the spinner goroutine so the user sees an animated indicator that
// proves the agent is alive even on slow tools (Bash, network calls).
// ShowToolResult later replaces that line with the final ✓/✗ summary.
//
// In non-TTY mode (NO_COLOR / redirected stdout / tests) we just record the
// pending state without animation; the result line is printed verbatim.
func (u *PlainUI) ShowToolCall(name string, params map[string]any) {
	u.stopSpinner()
	u.mu.Lock()

	u.endAssistantLineLocked()
	u.closeThinkingLocked(false)
	u.flushPendingToolLocked()

	compact := CompactToolHeader(name, params)
	u.pendingTool = name
	u.pendingHeader = u.style.BoldBlue(compact)
	u.pendingActive = true

	enabled := u.style.Enabled()
	u.mu.Unlock()

	if !enabled {
		return
	}
	label := fmt.Sprintf("%s %s",
		u.style.BoldBlue(compact),
		u.style.DimBlue("running…"),
	)
	u.startSpinner(label)
}

// ShowToolResult collapses the in-progress tool card into a final state,
// printing a one-line summary of the output rather than dumping everything.
// Long outputs are truncated; the user sees the same "✓ Read foo.go (1.2KB)"
// style that Cursor uses. For Write/Edit, toolParams enables Cursor-like
// +lines/−lines stats and a short diff preview (Edit only).
func (u *PlainUI) ShowToolResult(name, output string, isError bool, toolParams map[string]any) {
	u.stopSpinner()
	u.mu.Lock()
	defer u.mu.Unlock()

	icon := u.style.BrightGreen(iconTool)
	tag := u.style.DimGreen("done")
	if isError {
		icon = u.style.BoldRed(iconErr)
		tag = u.style.Red("error")
	} else if (name == "BackgroundRun" || name == "BackgroundStatus") && strings.Contains(output, "STILL RUNNING") {
		icon = u.style.Yellow("⏳")
		tag = u.style.Yellow("running in background (not finished)")
	}

	header := u.pendingHeader
	if !u.pendingActive || u.pendingTool != name {
		header = u.style.BoldBlue(name)
	}

	summary := toolResultSummary(u.style, name, output, isError, toolParams)

	if u.pendingActive {
		fmt.Fprint(u.out, u.style.ClearPendingLine())
	}
	arrow := u.style.DimGreen("→ ")
	if isError {
		fmt.Fprintf(u.out, "%s %s %s %s%s\n", icon, header, tag, arrow, u.style.Red(summary))
	} else if (name == "BackgroundRun" || name == "BackgroundStatus") && strings.Contains(output, "STILL RUNNING") {
		arrow = u.style.DimYellow("→ ")
		fmt.Fprintf(u.out, "%s %s %s %s%s\n", icon, header, tag, arrow, u.style.Yellow(summary))
	} else {
		fmt.Fprintf(u.out, "%s %s %s %s%s\n", icon, header, tag, arrow, summary)
	}

	if !isError && name == "Edit" && toolParams != nil {
		printEditDiffPreview(u.out, u.style, toolParams)
	}

	// A shell command's verdict lives on its last lines, but the card above
	// collapses the whole output to "N lines, XB". printOutputTail shows that
	// tail (dim, indented) so the transcript reads like a log snippet instead
	// of a count. Terminal only: redirected output keeps the one-line summary
	// so pipes stay parseable.
	if !isError && name == "Bash" && u.outIsTTY {
		printOutputTail(u.out, u.style, output, bashOutputTailLines)
	}

	// A failed Bash with a long command shows only "super..." in the card.
	// Print the full command so the failure is debuggable.
	if isError && name == "Bash" && toolParams != nil {
		if cmd, ok := toolParams["command"].(string); ok && bashCommandTruncated(cmd) {
			printIndented(u.out, u.style.Dim("$ "+strings.TrimSpace(cmd)))
		}
	}

	if isError && strings.TrimSpace(output) != "" {
		printIndented(u.out, u.style.Red(strings.TrimRight(output, "\n")))
	}

	u.pendingActive = false
	u.pendingTool = ""
	u.pendingHeader = ""
}

// bashCommandTruncated reports whether the one-line card header hides part
// of a Bash command (same limit as CompactToolHeader).
func bashCommandTruncated(cmd string) bool {
	oneLine := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(cmd), "\n", " "), "\r", " ")
	return len("Bash $ "+oneLine) > maxToolHeaderLen
}

// formatParams renders a compact (key=val, ...) suffix with a stable order so
// tool cards don't jitter between runs.
func formatParams(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		val := truncateInline(fmt.Sprintf("%v", params[k]), 60)
		parts = append(parts, fmt.Sprintf("%s=%s", k, val))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// summarizeOutput collapses tool output to a single short line for the card.
func summarizeOutput(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "no output"
	}
	lines := strings.Split(trimmed, "\n")
	bytes := FormatBytes(len(output))
	if len(lines) > 1 {
		return fmt.Sprintf("%d lines, %s", len(lines), bytes)
	}
	if len(trimmed) > 80 {
		return truncateInline(trimmed, 80)
	}
	return trimmed
}

func printIndented(w io.Writer, s string) {
	for _, line := range strings.Split(s, "\n") {
		fmt.Fprintln(w, "    "+line)
	}
}

// bashOutputTailLines is how many trailing lines of a successful shell command
// are shown under its card: enough to read the verdict (test result, last
// warning, the artifact that was built) without turning the transcript into a
// log dump. The card keeps the full line and byte count.
const bashOutputTailLines = 4

// outputTailMaxRunes bounds a single previewed line. Shell output is arbitrary
// text, and a 4000-column line would wreck the card layout.
const outputTailMaxRunes = 200

// printOutputTail prints the last maxLines of output, dim and indented, as a
// compact tail. Single-line output is skipped: the card summary already shows
// it verbatim.
func printOutputTail(w io.Writer, st Style, output string, maxLines int) {
	if maxLines <= 0 || strings.TrimSpace(output) == "" {
		return
	}
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) < 2 {
		return
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	for _, line := range lines {
		fmt.Fprintln(w, "    "+st.Dim(trimOutputLine(line, outputTailMaxRunes)))
	}
}

// trimOutputLine keeps the last carriage-return state of a progress line (a
// build reporting "10%\r50%\r100%" means 100%) and truncates rune-safely.
func trimOutputLine(line string, maxRunes int) string {
	if i := strings.LastIndexByte(line, '\r'); i >= 0 {
		line = line[i+1:]
	}
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(line)
	if len(runes) <= maxRunes {
		return line
	}
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}
