package tui

import "strings"

func truncateInline(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	// Runes, not bytes: a byte slice cuts a multi-byte character in half and
	// leaves a fragment on screen.
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}
