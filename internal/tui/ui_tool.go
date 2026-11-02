package tui

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
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

	u.pendingTool = name
	u.pendingHeader = toolHeader(u.style, name, params)
	u.pendingActive = true
	u.pendingStart = time.Now()

	enabled := u.style.Enabled()
	u.mu.Unlock()

	if !enabled {
		return
	}
	u.startSpinner(pendingLabel(u.style, name, params))
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
	if name == "Bash" {
		icon = iconShell
	}
	background := (name == "BackgroundRun" || name == "BackgroundStatus") && strings.Contains(output, "STILL RUNNING")
	if isError {
		icon = u.style.BoldRed(iconErr)
	} else if background {
		icon = u.style.Yellow("⏳")
	}

	// The final header is rendered from the params, not reused from the pending
	// card: that one is bounded so the animated line never wraps, and a shell
	// card wants the longer command.
	header := toolHeader(u.style, name, toolParams)
	if toolParams == nil && u.pendingActive && u.pendingTool == name {
		header = u.pendingHeader
	}

	summary := toolResultSummary(u.style, name, output, isError, toolParams)
	// A shell command reports how long it ran: the card opens after the
	// permission prompt, so this is the tool's own time, not the wait.
	if !isError && name == "Bash" && u.pendingActive && u.pendingTool == name && !u.pendingStart.IsZero() {
		summary = formatToolDuration(time.Since(u.pendingStart)) + ", " + summary
	}

	if u.pendingActive {
		fmt.Fprint(u.out, u.style.ClearPendingLine())
	}
	arrow := u.style.DimGreen("→ ")
	switch {
	case isError:
		fmt.Fprintf(u.out, "%s %s %s %s%s\n", icon, header, u.style.Red("error"), arrow, u.style.Red(summary))
	case background:
		arrow = u.style.DimYellow("→ ")
		fmt.Fprintf(u.out, "%s %s %s %s%s\n", icon, header,
			u.style.Yellow("running in background (not finished)"), arrow, u.style.Yellow(summary))
	default:
		fmt.Fprintf(u.out, "%s %s %s%s\n", icon, header, arrow, summary)
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
	u.pendingStart = time.Time{}
}

// bashCommandTruncated reports whether the one-line card header hides part
// of a Bash command (same limit as CompactToolHeader).
func bashCommandTruncated(cmd string) bool {
	oneLine := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(cmd), "\n", " "), "\r", " ")
	return len([]rune("Shell $ "+oneLine)) > bashHeaderRunes
}

// pendingRunningSuffix is what follows the header while the tool runs.
const pendingRunningSuffix = " running…"

// pendingLabel renders the animated line that spins while a tool works. It
// carries the same shield, green name and gray command as the card that
// replaces it, because a line that changes design half a second later reads as
// two different tools. The text is cut before the colors are applied and stays
// within one row: the animated line is erased with a single-line escape, and a
// wrapped line would leave its tail behind.
func pendingLabel(st Style, name string, params map[string]any) string {
	if name == "Bash" {
		header := iconShell + " " + st.BoldGreen("Shell")
		// The budget counts runes, and the shield is two of them (emoji plus its
		// variation selector), so measure the rendered prefix instead of guessing.
		budget := maxToolHeaderLen -
			utf8.RuneCountInString(iconShell+" Shell $ ") -
			utf8.RuneCountInString(pendingRunningSuffix)
		if cmd := bashCommand(params); cmd != "" && budget > 1 {
			header += " " + st.Gray("$ "+truncateInline(cmd, budget))
		}
		return header + st.DimBlue(pendingRunningSuffix)
	}
	// Every other card keeps the same budget rule: the parameter is cut so the
	// whole line, suffix included, still fits one row.
	if _, ok := headerParam(name, params); ok {
		budget := maxToolHeaderLen -
			utf8.RuneCountInString(name) - 1 -
			utf8.RuneCountInString(pendingRunningSuffix)
		return toolHeaderLimited(st, name, params, budget) + st.DimBlue(pendingRunningSuffix)
	}
	return st.BoldBlue(truncateInline(CompactToolHeader(name, params),
		maxToolHeaderLen-utf8.RuneCountInString(pendingRunningSuffix))) + st.DimBlue(pendingRunningSuffix)
}

// bashCommand returns the trimmed command a Bash card shows.
func bashCommand(params map[string]any) string {
	if params == nil {
		return ""
	}
	cmd, _ := params["command"].(string)
	return strings.TrimSpace(cmd)
}

// toolHeader renders the styled header of a tool card. The green name marks
// what ran; the parameter beside it says on what, and keeps its own color: the
// shell command in the thinking gray (it is context, not a result), the file
// path in blue, a URL or a search query in gray as well. The final card shows
// the whole URL/query — a truncated link is useless — while the animated line
// goes through toolHeaderLimited so it never wraps.
func toolHeader(st Style, name string, params map[string]any) string {
	return toolHeaderLimited(st, name, params, 0)
}

// toolHeaderLimited is toolHeader with a cap on the parameter text. A zero cap
// means no cap.
func toolHeaderLimited(st Style, name string, params map[string]any, cap int) string {
	clip := func(s string) string {
		if cap > 0 {
			return truncateInline(s, cap)
		}
		return s
	}
	if param, ok := headerParam(name, params); ok {
		switch name {
		case "Bash":
			limit := bashCommandRunes
			if cap > 0 && cap < limit {
				limit = cap
			}
			return st.BoldGreen("Shell") + " " + st.Gray("$ "+truncateInline(param, limit))
		case "Read", "Write", "Edit", "NotebookEdit":
			return st.BoldGreen(name) + " " + st.BoldBlue(clip(compactPath(param)))
		case "WebFetch", "WebSearch":
			return st.BoldGreen(name) + " " + st.Gray(clip(param))
		}
	}
	if name == "Bash" {
		return st.BoldGreen("Shell")
	}
	if cap > 0 {
		return st.BoldBlue(truncateInline(CompactToolHeader(name, params), cap))
	}
	return st.BoldBlue(CompactToolHeader(name, params))
}

// headerParam is the one value a card shows beside its tool name, when the tool
// has one: the command, the path, the URL or the query.
func headerParam(name string, params map[string]any) (string, bool) {
	if params == nil {
		return "", false
	}
	key := ""
	switch name {
	case "Bash":
		key = "command"
	case "Read", "Write", "Edit", "NotebookEdit":
		key = "file_path"
	case "WebFetch":
		key = "url"
	case "WebSearch":
		key = "query"
	default:
		return "", false
	}
	value, ok := params[key].(string)
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

// formatToolDuration renders a tool's own duration: a decimal under ten
// seconds ("0.3s"), whole seconds up to a minute, then the shared clock form.
func formatToolDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < 100*time.Millisecond {
		// A fast tool would otherwise print "0.0s", which reads like a bug.
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	if d < 10*time.Second {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return formatElapsed(d)
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
