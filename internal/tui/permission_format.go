package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const permissionValueWrap = 52

// gateContentMaxRunes is the max text between "║ " and " ║" for a 60-column inner bar.
const gateContentMaxRunes = 56

// fitGateLine trims a line to fit the permission box without breaking UTF-8.
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

// permissionPayloadLines builds readable, wrapped lines for the gate (no ANSI).
func permissionPayloadLines(tool string, params map[string]any) []string {
	var out []string
	out = append(out, "TARGET  "+strings.TrimSpace(tool))
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
		raw := fmt.Sprintf("%v", params[k])
		out = append(out, k+":")
		parts := strings.Split(raw, "\n")
		for i, part := range parts {
			if i > 0 {
				out = append(out, "  ¶")
			}
			for _, ln := range wrapParagraph(strings.TrimSpace(part), permissionValueWrap) {
				out = append(out, "  "+ln)
			}
		}
	}
	return out
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
