package tui

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
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
		u.clearPendingLocked()
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

// pendingLabel renders the animated line that spins while a tool works. It is
// the final card's header, shield included, plus the "running…" note: a line
// that changes design half a second later reads as two different tools. It is
// not shortened here — the painter measures how many rows it wraps into and
// clears them all, so the command keeps the same budget it will have when the
// card lands.
func pendingLabel(st Style, name string, params map[string]any) string {
	label := toolHeader(st, name, params)
	if name == "Bash" {
		label = iconShell + " " + label
	}
	return label + st.DimBlue(pendingRunningSuffix)
}

// toolHeader renders the styled header of a tool card. The green name marks
// what ran; the value beside it says on what and keeps its own color: a command,
// a pattern or a query in the thinking gray (it is context, not a result), a
// path or a directory in blue. Nothing is cut here except by the shell's safety
// cap, because the painter measures the wrapped rows.
func toolHeader(st Style, name string, params map[string]any) string {
	switch name {
	case "Grep", "Glob":
		if header := searchCardHeader(st, name, params); header != "" {
			return header
		}
	case "Bash":
		cmd, ok := headerParam(name, params)
		if !ok {
			return st.BoldGreen("Shell")
		}
		return st.BoldGreen("Shell") + " " + st.Gray("$ "+truncateInline(cmd, bashCommandRunes))
	case "Read", "Write", "Edit", "NotebookEdit":
		if path, ok := headerParam(name, params); ok {
			return st.BoldGreen(name) + " " + st.BoldBlue(compactPath(path))
		}
	case "WebFetch", "WebSearch":
		if target, ok := headerParam(name, params); ok {
			return st.BoldGreen(name) + " " + st.Gray(target)
		}
	}
	return st.BoldBlue(CompactToolHeader(name, params))
}

// searchCardHeader renders Grep and Glob: the pattern in gray, quoted for Grep
// (a regex reads as prose, a glob does not), and the directory in blue when the
// call named one.
func searchCardHeader(st Style, name string, params map[string]any) string {
	pattern, ok := headerParam(name, params)
	if !ok {
		return ""
	}
	shown := pattern
	if name == "Grep" {
		shown = fmt.Sprintf("%q", pattern)
	}
	header := st.BoldGreen(name) + " " + st.Gray(shown)
	key := "path"
	if name == "Glob" {
		key = "target_directory"
	}
	if params != nil {
		if dir, ok := params[key].(string); ok && strings.TrimSpace(dir) != "" {
			header += st.Dim(" in ") + st.BoldBlue(compactPath(strings.TrimSpace(dir)))
		}
	}
	return header
}

// headerParam is the one value a card shows beside its tool name, when the tool
// has one: the command, the path, the URL, the query or the pattern.
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
	case "Grep", "Glob":
		key = "pattern"
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
