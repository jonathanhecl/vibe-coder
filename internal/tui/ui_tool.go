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
	} else {
		fmt.Fprintf(u.out, "%s %s %s %s%s\n", icon, header, tag, arrow, summary)
	}

	if !isError && name == "Edit" && toolParams != nil {
		printEditDiffPreview(u.out, u.style, toolParams)
	}

	if isError && strings.TrimSpace(output) != "" {
		printIndented(u.out, u.style.Red(strings.TrimRight(output, "\n")))
	}

	u.pendingActive = false
	u.pendingTool = ""
	u.pendingHeader = ""
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
