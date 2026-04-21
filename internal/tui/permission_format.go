package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const permissionValueWrap = 52

// permissionDisplayMaxRunes caps a single payload line width in the permission prompt (no box).
const permissionDisplayMaxRunes = 96

const (
	permMaxPatchLines  = 16 // total − / + lines budget for Edit preview
	permMaxWritePreview = 5  // first lines of Write contents
	permMaxGenericRunes = 400
)

// fitGateLine trims a line to maxRunes without breaking UTF-8 (used for permission lines).
func fitGateLine(s string, maxRunes int) string {
	if maxRunes < 4 {
		maxRunes = 4
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	if len(r) > maxRunes-1 {
		return string(r[:maxRunes-1]) + "…"
	}
	return s
}

// permissionPayloadLines builds readable lines for the gate (no ANSI).
// Write/Edit use a compact “patch card” instead of dumping full file contents.
func permissionPayloadLines(tool string, params map[string]any) []string {
	switch strings.TrimSpace(tool) {
	case "Edit":
		return permissionPayloadEdit(params)
	case "Write", "NotebookEdit":
		return permissionPayloadWrite(params)
	default:
		return permissionPayloadGeneric(tool, params)
	}
}

func permissionPayloadEdit(params map[string]any) []string {
	out := []string{"TARGET Edit", "PAYLOAD"}
	fp, _ := params["file_path"].(string)
	if fp != "" {
		out = append(out, "file: "+compactPath(fp))
	}
	oldS, _ := params["old_string"].(string)
	newS, _ := params["new_string"].(string)
	lo, ln := lineCount(oldS), lineCount(newS)
	out = append(out, fmt.Sprintf("change: +%d −%d lines", ln, lo))
	out = append(out, "patch:")
	out = append(out, buildMiniPatch(oldS, newS, permMaxPatchLines)...)
	return out
}

func permissionPayloadWrite(params map[string]any) []string {
	out := []string{"TARGET Write", "PAYLOAD"}
	fp, _ := params["file_path"].(string)
	if fp != "" {
		out = append(out, "file: "+compactPath(fp))
	}
	c, _ := params["contents"].(string)
	if len(c) == 0 {
		out = append(out, "size: empty file")
		return out
	}
	nl := lineCount(c)
	if nl == 0 {
		nl = 1
	}
	out = append(out, fmt.Sprintf("size: %d lines · %s", nl, FormatBytes(len(c))))
	out = append(out, "preview:")
	lines := splitLinesNormalized(c)
	show := lines
	if len(show) > permMaxWritePreview {
		show = show[:permMaxWritePreview]
	}
	for _, ln := range show {
		out = append(out, "+ "+truncateForPerm(ln, permissionValueWrap))
	}
	if len(lines) > permMaxWritePreview {
		out = append(out, fmt.Sprintf("… %d more lines omitted", len(lines)-permMaxWritePreview))
	}
	return out
}

func permissionPayloadGeneric(tool string, params map[string]any) []string {
	var out []string
	out = append(out, "TARGET "+strings.TrimSpace(tool))
	if len(params) == 0 {
		return out
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out = append(out, "PAYLOAD")
	for _, k := range keys {
		raw0 := fmt.Sprintf("%v", params[k])
		raw := raw0
		if len(raw) > permMaxGenericRunes {
			raw = truncateForPerm(raw, permMaxGenericRunes) + fmt.Sprintf(" [truncated, %d bytes total]", len(raw0))
		}
		out = append(out, k+":")
		for _, ln := range wrapParagraph(strings.TrimSpace(strings.ReplaceAll(raw, "\n", " ")), permissionValueWrap) {
			out = append(out, "  "+ln)
		}
	}
	return out
}

func buildMiniPatch(oldS, newS string, maxTotal int) []string {
	if maxTotal < 4 {
		maxTotal = 4
	}
	oldLines := splitLinesNormalized(oldS)
	newLines := splitLinesNormalized(newS)
	oldBudget := maxTotal / 2
	newBudget := maxTotal - oldBudget
	var out []string
	shownOld := 0
	for _, ln := range oldLines {
		if shownOld >= oldBudget {
			break
		}
		out = append(out, "- "+truncateForPerm(ln, permissionValueWrap))
		shownOld++
	}
	if len(oldLines) > shownOld {
		out = append(out, fmt.Sprintf("… −%d lines not shown", len(oldLines)-shownOld))
	}
	shownNew := 0
	for _, ln := range newLines {
		if shownNew >= newBudget {
			break
		}
		out = append(out, "+ "+truncateForPerm(ln, permissionValueWrap))
		shownNew++
	}
	if len(newLines) > shownNew {
		out = append(out, fmt.Sprintf("… +%d lines not shown", len(newLines)-shownNew))
	}
	return out
}

func splitLinesNormalized(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

func truncateForPerm(s string, max int) string {
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

func wrapParagraph(s string, maxRunes int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return hardWrapRunes(s, maxRunes)
	}
	var lines []string
	var b strings.Builder
	countRunes := func(st string) int { return utf8.RuneCountInString(st) }
	for _, w := range words {
		trial := b.String()
		if trial != "" {
			trial += " "
		}
		trial += w
		if countRunes(trial) <= maxRunes {
			if b.Len() > 0 {
				b.WriteString(" ")
			}
			b.WriteString(w)
			continue
		}
		if b.Len() > 0 {
			lines = append(lines, b.String())
			b.Reset()
		}
		if countRunes(w) <= maxRunes {
			b.WriteString(w)
			continue
		}
		lines = append(lines, hardWrapRunes(w, maxRunes)...)
	}
	if b.Len() > 0 {
		lines = append(lines, b.String())
	}
	return lines
}

func hardWrapRunes(s string, maxRunes int) []string {
	if maxRunes < 8 {
		maxRunes = 8
	}
	r := []rune(s)
	var out []string
	for len(r) > 0 {
		if len(r) <= maxRunes {
			out = append(out, string(r))
			break
		}
		out = append(out, string(r[:maxRunes]))
		r = r[maxRunes:]
	}
	return out
}
