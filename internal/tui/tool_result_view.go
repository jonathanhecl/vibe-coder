package tui

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// toolResultSummary builds the text after "→" for ShowToolResult, Cursor-style
// for Write/Edit when toolParams is non-nil.
func toolResultSummary(st Style, name, output string, isError bool, toolParams map[string]any) string {
	if isError {
		return summarizeOutput(output)
	}
	chip := cursorEditWriteChip(st, name, toolParams)
	base := strings.TrimSpace(summarizeOutput(output))
	if chip != "" {
		if boringToolSuccessMessage(base) {
			return chip
		}
		return chip + st.Dim(" · ") + base
	}
	return summarizeOutput(output)
}

func boringToolSuccessMessage(s string) bool {
	switch strings.TrimSpace(strings.ToLower(s)) {
	case "", "write successful.", "ok", "done.":
		return true
	default:
		return false
	}
}

func cursorEditWriteChip(st Style, name string, params map[string]any) string {
	if params == nil {
		return ""
	}
	switch name {
	case "Write", "NotebookEdit":
		c, ok := params["contents"].(string)
		if !ok {
			return ""
		}
		n := lineCount(c)
		if n == 0 && len(c) == 0 {
			return st.Dim("empty file")
		}
		if n == 0 {
			n = 1
		}
		return st.BrightGreen("+") + st.Green(fmt.Sprintf("%d", n)) + " " + st.Dim("lines") +
			" · " + st.Dim(FormatBytes(len(c)))
	case "Edit":
		oldS, _ := params["old_string"].(string)
		newS, _ := params["new_string"].(string)
		if oldS == "" && newS == "" {
			return ""
		}
		lo, ln := lineCount(oldS), lineCount(newS)
		delta := len(newS) - len(oldS)
		deltaStr := ""
		if delta != 0 {
			sign := "+"
			d := delta
			if delta < 0 {
				sign = "−"
				d = -delta
			}
			deltaStr = " · " + st.Dim(sign+FormatBytes(d))
		}
		return st.BrightGreen("+") + st.Green(fmt.Sprintf("%d", ln)) + " " +
			st.Red("−") + st.Red(fmt.Sprintf("%d", lo)) + " " + st.Dim("lines") + deltaStr
	default:
		return ""
	}
}

func lineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

// printEditDiffPreview prints a colored unified diff after Edit when _diff is
// present; otherwise falls back to the 2-line Cursor-like snapshot.
func printEditDiffPreview(w io.Writer, st Style, params map[string]any) {
	if params == nil {
		return
	}
	if diff, ok := params["_diff"].(string); ok && diff != "" {
		printColoredDiff(w, st, diff)
		return
	}
	oldS, ok1 := params["old_string"].(string)
	newS, ok2 := params["new_string"].(string)
	if !ok1 || !ok2 {
		return
	}
	if strings.TrimSpace(oldS) == "" && strings.TrimSpace(newS) == "" {
		return
	}
	fp, _ := params["file_path"].(string)
	if fp != "" {
		fmt.Fprintf(w, "  %s %s\n", st.Dim("∷"), st.Dim(compactPath(fp)))
	}
	ol := firstLineOrSummary(oldS, 96)
	nl := firstLineOrSummary(newS, 96)
	if ol != "" {
		fmt.Fprintf(w, "  %s %s\n", st.Red("−"), st.Dim(ol))
	}
	if nl != "" {
		fmt.Fprintf(w, "  %s %s\n", st.Green("+"), st.Dim(nl))
	}
	if strings.Count(oldS, "\n")+strings.Count(newS, "\n") > 2 {
		fmt.Fprintf(w, "  %s\n", st.Dim("… multiline change (see file)"))
	}
}

func printColoredDiff(w io.Writer, st Style, diff string) {
	lines := strings.Split(diff, "\n")
	if len(lines) > 50 {
		lines = lines[:50]
	}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "@@"):
			fmt.Fprintf(w, "  %s\n", st.Cyan(line))
		case strings.HasPrefix(line, "-"):
			fmt.Fprintf(w, "  %s\n", st.Red(line))
		case strings.HasPrefix(line, "+"):
			fmt.Fprintf(w, "  %s\n", st.Green(line))
		default:
			fmt.Fprintf(w, "  %s\n", st.Dim(line))
		}
	}
	if strings.Count(diff, "\n") >= 50 {
		fmt.Fprintf(w, "  %s\n", st.Dim("… diff truncated"))
	}
}

func compactPathDisplay(p string) string {
	return compactPath(p)
}

func firstLineOrSummary(s string, maxRunes int) string {
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return ""
	}
	line := s
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		line = s[:i]
	}
	return truncateRunes(line, maxRunes)
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) > max-1 {
		return string(r[:max-1]) + "…"
	}
	return s
}
